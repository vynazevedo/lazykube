package kube

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
	"k8s.io/client-go/rest"
)

type firstFailure struct {
	name string
	fail func(*http.Request) (*http.Response, error)
}

func firstFailures() []firstFailure {
	var out []firstFailure
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		out = append(out, firstFailure{fmt.Sprint(status), func(r *http.Request) (*http.Response, error) {
			resp := wireResponse(r, status, "", `{}`)
			resp.Header.Set("Retry-After", "0")
			return resp, nil
		}})
	}
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, syscall.ECONNRESET, errors.New("http2: client connection lost"), context.DeadlineExceeded} {
		out = append(out, firstFailure{err.Error(), func(*http.Request) (*http.Response, error) { return nil, err }})
	}
	return out
}

func failOnce(f firstFailure, at func(*http.Request) bool) *wire {
	failed := false
	return &wire{answer: func(r *http.Request) (*http.Response, error) {
		if !failed && at(r) {
			failed = true
			return f.fail(r)
		}
		body := wireObject
		if strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews") {
			body = wireAllowed
		}
		return wireResponse(r, http.StatusOK, "", body), nil
	}}
}

func anyRequest(*http.Request) bool { return true }

func TestActionReadsNeverRetry(t *testing.T) {
	for _, f := range firstFailures() {
		for _, r := range wireActions() {
			t.Run(fmt.Sprintf("%s/%d", f.name, r.Action), func(t *testing.T) {
				for name, read := range map[string]func(actionBackend) error{
					"observe":  func(b actionBackend) error { _, err := b.Observe(context.Background(), r.Action, r.Target); return err },
					"validate": func(b actionBackend) error { return b.Validate(context.Background(), r) },
				} {
					w := failOnce(f, anyRequest)
					client, err := newActionClient(&rest.Config{Host: wireHost, Transport: w})
					if err != nil {
						t.Fatal(err)
					}
					if err = read(actionBackend{client: client}); !errors.Is(err, guard.Unavailable) || len(w.requests) != 1 {
						t.Fatalf("%s: %v after %d requests", name, err, len(w.requests))
					}
				}
			})
		}
	}
}

func TestActionAuthorizationNeverRetries(t *testing.T) {
	for _, f := range firstFailures() {
		w := failOnce(f, anyRequest)
		auth, _, err := newActionClients(&rest.Config{Host: wireHost, Transport: w})
		if err != nil {
			t.Fatal(err)
		}
		ok, err := (actionAuthorizer{client: auth}).Allowed(context.Background(), guard.Access{Version: "v1", Resource: "pods", Name: "api", Verb: "delete"})
		if ok || !errors.Is(err, guard.Unavailable) || len(w.requests) != 1 {
			t.Fatal(f.name, ok, err, len(w.requests))
		}
	}
}

func TestActionWritesNeverRetryAnyFailure(t *testing.T) {
	isWrite := func(r *http.Request) bool { return r.Method != http.MethodGet }
	for _, f := range firstFailures() {
		for _, r := range wireActions() {
			for _, dry := range []bool{true, false} {
				w := failOnce(f, isWrite)
				client, err := newActionClient(&rest.Config{Host: wireHost, Transport: w})
				if err != nil {
					t.Fatal(err)
				}
				if err = (actionBackend{client: client}).Send(context.Background(), r, dry); !errors.Is(err, guard.Unknown) || len(w.writes()) != 1 {
					t.Fatal(f.name, r.Action, dry, err, len(w.writes()))
				}
			}
		}
	}
}

func TestPrepareTurnsFirstReadFailureIntoRefusal(t *testing.T) {
	for _, f := range firstFailures() {
		for stage, at := range map[string]func(*http.Request) bool{
			"authorization": anyRequest,
			"validate":      func(r *http.Request) bool { return r.Method == http.MethodGet },
		} {
			w := failOnce(f, at)
			exec, audit := wireExecutor(t, w)
			if p, err := exec.Prepare(context.Background(), scaleRequest(guard.Scale)); !errors.Is(err, guard.Unavailable) || p.ID != "" || len(w.writes()) != 0 {
				t.Fatal(f.name, stage, p, err, w.writes())
			}
			if last := audit.events[len(audit.events)-1]; last.Phase != "unavailable" {
				t.Fatal(last)
			}
		}
	}
}

func TestGeneralClientsKeepTheirRetries(t *testing.T) {
	w := failOnce(firstFailures()[0], anyRequest)
	cfg := &rest.Config{Host: wireHost, Transport: w}
	if _, _, err := newActionClients(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.WrapTransport != nil {
		t.Fatal("the shared config must not be changed")
	}
	client, err := newResourceClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b := actionBackend{client: client}
	if _, err = b.Observe(context.Background(), guard.Restart, scaleRequest(guard.Restart).Target); err != nil || len(w.requests) != 2 {
		t.Fatal(err, len(w.requests))
	}
}
