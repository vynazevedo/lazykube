package kube

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/guard"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type actionCall struct {
	name string
	run  func(kubernetes.Interface, dynamic.Interface) error
}

func actionCalls() []actionCall {
	calls := []actionCall{
		{"ssar", func(auth kubernetes.Interface, _ dynamic.Interface) error {
			_, err := (actionAuthorizer{client: auth}).Allowed(context.Background(), guard.Access{Version: "v1", Resource: "pods", Name: "api", Verb: "delete"})
			return err
		}},
		{"observe", func(_ kubernetes.Interface, c dynamic.Interface) error {
			_, err := (actionBackend{client: c}).Observe(context.Background(), guard.Restart, scaleRequest(guard.Restart).Target)
			return err
		}},
		{"validate", func(_ kubernetes.Interface, c dynamic.Interface) error {
			return (actionBackend{client: c}).Validate(context.Background(), scaleRequest(guard.Delete))
		}},
	}
	for _, r := range wireActions() {
		if r.Action == guard.Restart {
			continue
		}
		for _, dry := range []bool{true, false} {
			calls = append(calls, actionCall{fmt.Sprintf("send %d %v", r.Action, dry), func(_ kubernetes.Interface, c dynamic.Interface) error {
				return (actionBackend{client: c}).Send(context.Background(), r, dry)
			}})
		}
	}
	return calls
}

func answerAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	body := wireObject
	if strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
		body = wireAllowed
	}
	_, _ = io.WriteString(w, body)
}

func TestClosedConnectionNeverRepeatsActionRequests(t *testing.T) {
	for _, call := range actionCalls() {
		t.Run(call.name, func(t *testing.T) {
			var calls atomic.Int32
			var mu sync.Mutex
			peers := map[string]int{}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				peers[r.RemoteAddr]++
				mu.Unlock()
				if calls.Add(1) == 2 {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					_ = conn.Close()
					return
				}
				if !r.Close {
					t.Errorf("%s keeps the connection open", r.Method)
				}
				answerAction(w, r)
			}))
			defer srv.Close()
			auth, client, err := newActionClients(&rest.Config{Host: srv.URL, QPS: 100, Burst: 100, Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if err = call.run(auth, client); err != nil {
				t.Fatal("warm up", err)
			}
			if err = call.run(auth, client); err == nil {
				t.Fatal("a dropped connection is a refusal")
			}
			mu.Lock()
			defer mu.Unlock()
			if n := calls.Load(); n != 2 || len(peers) != 2 {
				t.Fatalf("%d requests over %d connections", n, len(peers))
			}
		})
	}
}

type goAwayServer struct {
	*httptest.Server
	streams atomic.Int32
	plain   atomic.Int32
	mu      sync.Mutex
	offered [][]string
}

func newGoAwayServer(t *testing.T) *goAwayServer {
	t.Helper()
	s := &goAwayServer{}
	s.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.plain.Add(1)
		answerAction(w, r)
	}))
	s.EnableHTTP2 = true
	s.TLS = &tls.Config{GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		s.mu.Lock()
		s.offered = append(s.offered, slices.Clone(h.SupportedProtos))
		s.mu.Unlock()
		return nil, nil
	}}
	s.Config.TLSNextProto = map[string]func(*http.Server, *tls.Conn, http.Handler){"h2": func(_ *http.Server, c *tls.Conn, _ http.Handler) { s.goAway(c) }}
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

func (s *goAwayServer) goAway(c net.Conn) {
	defer func() { _ = c.Close() }()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.ReadFull(c, make([]byte, len("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"))); err != nil {
		return
	}
	if _, err := c.Write([]byte{0, 0, 0, 4, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	for {
		head := make([]byte, 9)
		if _, err := io.ReadFull(c, head); err != nil {
			return
		}
		size := int(head[0])<<16 | int(binary.BigEndian.Uint16(head[1:3]))
		if _, err := io.CopyN(io.Discard, c, int64(size)); err != nil {
			return
		}
		kind, flags := head[3], head[4]
		if (kind == 0 || kind == 1) && flags&1 != 0 {
			s.streams.Add(1)
			_, _ = c.Write([]byte{0, 0, 8, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
			return
		}
	}
}

func TestActionClientsNeverSpeakHTTP2(t *testing.T) {
	for _, call := range actionCalls() {
		t.Run(call.name, func(t *testing.T) {
			s := newGoAwayServer(t)
			auth, client, err := newActionClients(&rest.Config{Host: s.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}, Timeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			if err = call.run(auth, client); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.streams.Load() != 0 || s.plain.Load() != 1 || len(s.offered) != 1 || len(s.offered[0]) != 1 || s.offered[0][0] != "http/1.1" {
				t.Fatalf("h2 streams %d, http/1.1 requests %d, offered %v", s.streams.Load(), s.plain.Load(), s.offered)
			}
		})
	}
}

func TestActionTransportDoesNotLeakIntoOtherClients(t *testing.T) {
	var mu sync.Mutex
	protos := map[int]int{}
	peers := map[string]int{}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		protos[r.ProtoMajor]++
		peers[r.RemoteAddr]++
		mu.Unlock()
		answerAction(w, r)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	cfg := &rest.Config{Host: srv.URL, TLSClientConfig: rest.TLSClientConfig{Insecure: true}, QPS: 100, Burst: 100}
	general := func() dynamic.Interface {
		c, err := dynamic.NewForConfig(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	read := func(c dynamic.Interface, times int) {
		for range times {
			if _, err := (actionBackend{client: c}).Observe(context.Background(), guard.Restart, scaleRequest(guard.Restart).Target); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := general()
	read(before, 3)
	auth, actions, err := newActionClients(cfg)
	if err != nil {
		t.Fatal(err)
	}
	read(actions, 3)
	if err = actionCalls()[0].run(auth, actions); err != nil {
		t.Fatal(err)
	}
	after := general()
	read(before, 3)
	read(after, 3)
	mu.Lock()
	defer mu.Unlock()
	if protos[2] != 9 || protos[1] != 4 || len(peers) != 5 {
		t.Fatalf("protocols %v over %d connections: general clients keep h2 on one shared connection, actions use one http/1.1 connection per request", protos, len(peers))
	}
	if len(cfg.NextProtos) != 0 || cfg.Transport != nil || cfg.WrapTransport != nil || cfg.Proxy != nil || cfg.Dial != nil {
		t.Fatal("the shared config must not change")
	}
}

func TestCanceledDialIsNeverReused(t *testing.T) {
	var accepted atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { answerAction(w, r) }))
	defer srv.Close()
	release := make(chan struct{})
	cfg := &rest.Config{Host: srv.URL, QPS: 100, Burst: 100, Dial: func(_ context.Context, network, addr string) (net.Conn, error) {
		if accepted.Add(1) == 1 {
			<-release
		}
		return net.Dial(network, addr)
	}}
	client, err := newActionClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b := actionBackend{client: client}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = b.Observe(ctx, guard.Restart, scaleRequest(guard.Restart).Target); !errors.Is(err, guard.Unavailable) {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	if _, err = b.Observe(context.Background(), guard.Restart, scaleRequest(guard.Restart).Target); err != nil || accepted.Load() != 2 {
		t.Fatal(err, accepted.Load())
	}
}
