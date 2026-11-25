//go:build integration

package kube_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"testing"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

const credentialTimeout = 15 * time.Second

func clientCredential(name string, cfg *rest.Config) (credential, error) {
	block, _ := pem.Decode(cfg.CertData)
	if block == nil {
		return credential{}, fmt.Errorf("credencial %s sem certificado de cliente", name)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return credential{}, fmt.Errorf("credencial %s com certificado ilegivel", name)
	}
	client, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return credential{}, err
	}
	return credential{name: name, notBefore: cert.NotBefore, probe: func(ctx context.Context) error {
		return client.RESTClient().Get().AbsPath("/version").Do(ctx).Error()
	}}, nil
}

func awaitCredentials(t *testing.T, configs map[string]*rest.Config) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), credentialTimeout)
	defer cancel()
	for name, cfg := range configs {
		c, err := clientCredential(name, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.await(ctx, time.Now); err != nil {
			t.Fatal(err)
		}
	}
}
