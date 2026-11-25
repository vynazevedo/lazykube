//go:build unix

package kube

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/guard"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, name string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

func (ca testCA) issue(t *testing.T, name string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	if ip := net.ParseIP(name); ip != nil {
		tmpl.IPAddresses, tmpl.DNSNames = []net.IP{ip}, nil
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: raw})
}

type authServer struct {
	*httptest.Server
	seen atomic.Pointer[http.Request]
}

func newAuthServer(t *testing.T, ca testCA, name string, clients *x509.CertPool) *authServer {
	t.Helper()
	certPEM, keyPEM := ca.issue(t, name)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	s := &authServer{}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.seen.Store(r)
		answerAction(w, r)
	}))
	s.EnableHTTP2 = true
	s.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	if clients != nil {
		s.TLS.ClientAuth, s.TLS.ClientCAs = tls.RequireAndVerifyClientCert, clients
	}
	s.Config.ErrorLog = nil
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func bothClients(t *testing.T, cfg *rest.Config) (error, error) {
	t.Helper()
	auth, client, err := newActionClients(cfg)
	if err != nil {
		return err, err
	}
	_, readErr := (actionBackend{client: client}).Observe(context.Background(), guard.Restart, scaleRequest(guard.Restart).Target)
	ok, authErr := (actionAuthorizer{client: auth}).Allowed(context.Background(), guard.Access{Version: "v1", Resource: "pods", Name: "api", Verb: "delete"})
	if authErr == nil && !ok {
		authErr = guard.Denied
	}
	return readErr, authErr
}

func wantBoth(t *testing.T, cfg *rest.Config, works bool) {
	t.Helper()
	read, auth := bothClients(t, cfg)
	if works != (read == nil) || works != (auth == nil) {
		t.Fatalf("works %v, read %v, ssar %v", works, read, auth)
	}
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestActionTransportVerifiesServer(t *testing.T) {
	ca, other := newTestCA(t, "ca"), newTestCA(t, "other")
	ip := newAuthServer(t, ca, "127.0.0.1", nil)
	named := newAuthServer(t, ca, "cluster.internal", nil)
	for name, tc := range map[string]struct {
		host  string
		tls   rest.TLSClientConfig
		works bool
	}{
		"ca":                {ip.URL, rest.TLSClientConfig{CAData: ca.pem}, true},
		"wrong ca":          {ip.URL, rest.TLSClientConfig{CAData: other.pem}, false},
		"system roots":      {ip.URL, rest.TLSClientConfig{}, false},
		"insecure":          {ip.URL, rest.TLSClientConfig{Insecure: true}, true},
		"wrong server name": {named.URL, rest.TLSClientConfig{CAData: ca.pem}, false},
		"tls-server-name":   {named.URL, rest.TLSClientConfig{CAData: ca.pem, ServerName: "cluster.internal"}, true},
	} {
		t.Run(name, func(t *testing.T) { wantBoth(t, &rest.Config{Host: tc.host, TLSClientConfig: tc.tls}, tc.works) })
	}
}

func TestActionTransportFollowsCARotation(t *testing.T) {
	first, second := newTestCA(t, "first"), newTestCA(t, "second")
	s := newAuthServer(t, second, "127.0.0.1", nil)
	file := filepath.Join(t.TempDir(), "ca.pem")
	writeFile(t, file, first.pem)
	_, client, err := newActionClients(&rest.Config{Host: s.URL, TLSClientConfig: rest.TLSClientConfig{CAFile: file}})
	if err != nil {
		t.Fatal(err)
	}
	read := func() error {
		_, err := (actionBackend{client: client}).Observe(context.Background(), guard.Restart, scaleRequest(guard.Restart).Target)
		return err
	}
	if read() == nil {
		t.Fatal("the first authority does not sign this server")
	}
	writeFile(t, file, second.pem)
	if err = read(); err != nil {
		t.Fatal(err)
	}
}

func TestActionTransportSendsClientCertificate(t *testing.T) {
	ca, other := newTestCA(t, "ca"), newTestCA(t, "other")
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	s := newAuthServer(t, ca, "127.0.0.1", pool)
	cert, key := ca.issue(t, "operator")
	badCert, badKey := other.issue(t, "intruder")
	dir := t.TempDir()
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	writeFile(t, certFile, cert)
	writeFile(t, keyFile, key)
	for name, tc := range map[string]struct {
		tls   rest.TLSClientConfig
		works bool
	}{
		"none":  {rest.TLSClientConfig{CAData: ca.pem}, false},
		"data":  {rest.TLSClientConfig{CAData: ca.pem, CertData: cert, KeyData: key}, true},
		"files": {rest.TLSClientConfig{CAData: ca.pem, CertFile: certFile, KeyFile: keyFile}, true},
		"wrong": {rest.TLSClientConfig{CAData: ca.pem, CertData: badCert, KeyData: badKey}, false},
	} {
		t.Run(name, func(t *testing.T) {
			wantBoth(t, &rest.Config{Host: s.URL, TLSClientConfig: tc.tls}, tc.works)
			if tc.works && s.seen.Load().TLS.PeerCertificates[0].Subject.CommonName != "operator" {
				t.Fatal("wrong client certificate")
			}
		})
	}
	writeFile(t, certFile, badCert)
	writeFile(t, keyFile, badKey)
	_, client, err := newActionClients(&rest.Config{Host: s.URL, TLSClientConfig: rest.TLSClientConfig{CAData: ca.pem, CertFile: certFile, KeyFile: keyFile}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, certFile, cert)
	writeFile(t, keyFile, key)
	if _, err = (actionBackend{client: client}).Observe(context.Background(), guard.Restart, scaleRequest(guard.Restart).Target); err != nil {
		t.Fatal("rotated files are read again", err)
	}
}

func TestActionTransportKeepsCredentialsAndImpersonation(t *testing.T) {
	ca := newTestCA(t, "ca")
	s := newAuthServer(t, ca, "127.0.0.1", nil)
	dir := t.TempDir()
	tokenFile, plugin := filepath.Join(dir, "token"), filepath.Join(dir, "plugin.sh")
	writeFile(t, tokenFile, []byte("file-token"))
	body := "#!/bin/sh\necho '{\"apiVersion\":\"client.authentication.k8s.io/v1\",\"kind\":\"ExecCredential\",\"status\":{\"token\":\"exec-token\"}}'\n"
	if err := os.WriteFile(plugin, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	base := func() *rest.Config {
		return &rest.Config{Host: s.URL, UserAgent: "lazykube", TLSClientConfig: rest.TLSClientConfig{CAData: ca.pem}}
	}
	for name, tc := range map[string]struct {
		change func(*rest.Config)
		header string
		want   string
	}{
		"token":       {func(c *rest.Config) { c.BearerToken = "data-token" }, "Authorization", "Bearer data-token"},
		"token file":  {func(c *rest.Config) { c.BearerTokenFile = tokenFile }, "Authorization", "Bearer file-token"},
		"basic":       {func(c *rest.Config) { c.Username, c.Password = "u", "p" }, "Authorization", "Basic dTpw"},
		"impersonate": {func(c *rest.Config) { c.Impersonate = rest.ImpersonationConfig{UserName: "alice"} }, "Impersonate-User", "alice"},
		"user agent":  {func(*rest.Config) {}, "User-Agent", "lazykube"},
		"exec plugin": {func(c *rest.Config) {
			c.ExecProvider = &clientcmdapi.ExecConfig{APIVersion: "client.authentication.k8s.io/v1", Command: plugin, InteractiveMode: clientcmdapi.NeverExecInteractiveMode}
		}, "Authorization", "Bearer exec-token"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			tc.change(cfg)
			wantBoth(t, cfg, true)
			if got := s.seen.Load().Header.Get(tc.header); got != tc.want {
				t.Fatalf("%s = %q", tc.header, got)
			}
			if s.seen.Load().ProtoMajor != 1 {
				t.Fatal("http/1.1 only")
			}
		})
	}
}

func TestActionTransportUsesExecClientCertificate(t *testing.T) {
	ca := newTestCA(t, "ca")
	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)
	s := newAuthServer(t, ca, "127.0.0.1", pool)
	cert, key := ca.issue(t, "exec-operator")
	plugin := filepath.Join(t.TempDir(), "plugin.sh")
	quote := func(b []byte) string { return strings.ReplaceAll(string(b), "\n", `\n`) }
	body := "#!/bin/sh\ncat <<'CRED'\n{\"apiVersion\":\"client.authentication.k8s.io/v1\",\"kind\":\"ExecCredential\",\"status\":{\"clientCertificateData\":\"" + quote(cert) + "\",\"clientKeyData\":\"" + quote(key) + "\"}}\nCRED\n"
	if err := os.WriteFile(plugin, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	wantBoth(t, &rest.Config{Host: s.URL, TLSClientConfig: rest.TLSClientConfig{CAData: ca.pem}, ExecProvider: &clientcmdapi.ExecConfig{APIVersion: "client.authentication.k8s.io/v1", Command: plugin, InteractiveMode: clientcmdapi.NeverExecInteractiveMode}}, true)
	if got := s.seen.Load().TLS.PeerCertificates[0].Subject.CommonName; got != "exec-operator" {
		t.Fatal(got)
	}
}

func TestActionTransportUsesProxy(t *testing.T) {
	ca := newTestCA(t, "ca")
	s := newAuthServer(t, ca, "cluster.internal", nil)
	var tunnels atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "cluster.internal:443" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		tunnels.Add(1)
		up, err := net.Dial("tcp", s.Listener.Addr().String())
		if err != nil {
			t.Error(err)
			return
		}
		down, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = io.WriteString(down, "HTTP/1.1 200 OK\r\n\r\n")
		go func() { _, _ = io.Copy(up, down); _ = up.Close() }()
		_, _ = io.Copy(down, up)
		_ = down.Close()
	}))
	defer proxy.Close()
	target, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	wantBoth(t, &rest.Config{Host: "https://cluster.internal", Proxy: http.ProxyURL(target), TLSClientConfig: rest.TLSClientConfig{CAData: ca.pem}}, true)
	if tunnels.Load() != 2 {
		t.Fatalf("%d tunnels for two requests", tunnels.Load())
	}
}

func TestActionTransportRejectsInvalidConfigUpFront(t *testing.T) {
	if _, _, err := newActionClients(&rest.Config{Host: "https://cluster.internal", TLSClientConfig: rest.TLSClientConfig{CAData: []byte("invalid")}}); err == nil {
		t.Fatal("an invalid authority must fail before any action")
	}
}
