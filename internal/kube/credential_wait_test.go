package kube_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	credentialMargin = 2 * time.Second
	credentialPoll   = 50 * time.Millisecond
)

type credential struct {
	name      string
	notBefore time.Time
	probe     func(context.Context) error
}

func (c credential) await(ctx context.Context, now func() time.Time) error {
	ticker := time.NewTicker(credentialPoll)
	defer ticker.Stop()
	last := errors.New("nenhuma tentativa antes do prazo")
	for {
		if now().Sub(c.notBefore) >= credentialMargin {
			if last = c.probe(ctx); last == nil {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("credencial %s nao aceita no prazo; relogio esta %s apos o NotBefore (folga exigida %s): %w", c.name, now().Sub(c.notBefore).Round(time.Millisecond), credentialMargin, last)
		case <-ticker.C:
		}
	}
}

func TestCredentialWait(t *testing.T) {
	issued := time.Date(2026, 11, 25, 20, 0, 0, 0, time.UTC)
	denied := errors.New("Unauthorized")
	clock := func(steps ...time.Duration) func() time.Time {
		i := -1
		return func() time.Time {
			i = min(i+1, len(steps)-1)
			return issued.Add(steps[i])
		}
	}
	for name, tc := range map[string]struct {
		now     func() time.Time
		results []error
		probes  int
		want    string
	}{
		"accepted after the margin":                 {clock(0, time.Second, 2*time.Second), []error{nil}, 1, ""},
		"backward step keeps waiting":               {clock(1900*time.Millisecond, 1150*time.Millisecond, 2100*time.Millisecond), []error{nil}, 1, ""},
		"certificate from the future keeps waiting": {clock(-time.Second, 3*time.Second), []error{nil}, 1, ""},
		"rejection is retried":                      {clock(5 * time.Second), []error{denied, denied, nil}, 3, ""},
		"rejection until the deadline":              {clock(5 * time.Second), []error{denied}, -1, "Unauthorized"},
		"accepted but still inside the margin":      {clock(time.Second), []error{nil}, 0, "1s apos o NotBefore"},
		"clock stuck before the margin":             {clock(-700 * time.Millisecond), nil, 0, "-700ms apos o NotBefore"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
			defer cancel()
			probes := 0
			err := credential{name: "admin", notBefore: issued, probe: func(context.Context) error {
				probes++
				return tc.results[min(probes, len(tc.results))-1]
			}}.await(ctx, tc.now)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "credencial admin")) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if tc.probes >= 0 && probes != tc.probes {
				t.Fatalf("probes = %d, want %d", probes, tc.probes)
			}
		})
	}
}
