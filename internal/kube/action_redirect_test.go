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
	"time"

	"github.com/vynazevedo/lazykube/internal/guard"
	"k8s.io/client-go/rest"
)

type wireRequest struct {
	method, host, path, query, body string
}

type wire struct {
	requests []wireRequest
	answer   func(*http.Request) (*http.Response, error)
}

func (w *wire) RoundTrip(r *http.Request) (*http.Response, error) {
	req := wireRequest{method: r.Method, host: r.URL.Host, path: r.URL.Path, query: r.URL.RawQuery}
	if r.Body != nil {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		req.body = string(raw)
	}
	w.requests = append(w.requests, req)
	return w.answer(r)
}

func (w *wire) writes() []wireRequest {
	var out []wireRequest
	for _, r := range w.requests {
		if r.method != http.MethodGet && !strings.HasSuffix(r.path, "/selfsubjectaccessreviews") {
			out = append(out, r)
		}
	}
	return out
}

func wireResponse(r *http.Request, status int, location, body string) *http.Response {
	h := http.Header{"Content-Type": []string{"application/json"}}
	if location != "" {
		h.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

const (
	wireHost    = "http://synthetic.invalid"
	wireObject  = `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"apps","uid":"uid","resourceVersion":"7"},"spec":{"replicas":1,"template":{"metadata":{}}}}`
	wireAllowed = `{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":true}}`
)

var redirectStatuses = []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect, http.StatusMultipleChoices, http.StatusNotModified}

func redirectLocations(r *http.Request) []string {
	return []string{"", r.URL.Path, r.URL.RequestURI(), wireHost + r.URL.Path, "http://other.invalid" + r.URL.RequestURI(), "//other.invalid" + r.URL.Path, "?dryRun=", "relative"}
}

func redirectWrites(status, location int) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/selfsubjectaccessreviews"):
			return wireResponse(r, http.StatusCreated, "", wireAllowed), nil
		case r.Method == http.MethodGet:
			return wireResponse(r, http.StatusOK, "", wireObject), nil
		}
		return wireResponse(r, status, redirectLocations(r)[location], wireObject), nil
	}
}

func wireActions() []guard.Request {
	restart := scaleRequest(guard.Restart)
	restart.Parameters.Document = []byte("2026-01-01T00:00:00Z")
	return []guard.Request{scaleRequest(guard.Scale), restart, scaleRequest(guard.Delete), nodeActionRequest(guard.Cordon), nodeActionRequest(guard.Uncordon)}
}

func TestActionWritesNeverFollowRedirects(t *testing.T) {
	methods := map[guard.Action]string{guard.Scale: http.MethodPut, guard.Restart: http.MethodPatch, guard.Delete: http.MethodDelete, guard.Cordon: http.MethodPatch, guard.Uncordon: http.MethodPatch}
	paths := map[guard.Action]string{guard.Scale: "/apis/apps/v1/namespaces/apps/deployments/api/scale", guard.Restart: "/apis/apps/v1/namespaces/apps/deployments/api", guard.Delete: "/apis/apps/v1/namespaces/apps/deployments/api", guard.Cordon: "/api/v1/nodes/node-a", guard.Uncordon: "/api/v1/nodes/node-a"}
	for _, r := range wireActions() {
		for _, dry := range []bool{true, false} {
			for _, status := range redirectStatuses {
				for location := range 8 {
					t.Run(fmt.Sprintf("%d/%v/%d/%d", r.Action, dry, status, location), func(t *testing.T) {
						w := &wire{answer: redirectWrites(status, location)}
						client, err := newActionClient(&rest.Config{Host: wireHost, Transport: w})
						if err != nil {
							t.Fatal(err)
						}
						if err = (actionBackend{client: client}).Send(context.Background(), r, dry); !errors.Is(err, guard.Unknown) {
							t.Fatal(err)
						}
						writes := w.writes()
						if len(writes) != 1 {
							t.Fatalf("%d writes: %+v", len(writes), writes)
						}
						got := writes[0]
						if got.method != methods[r.Action] || got.host != "synthetic.invalid" || got.path != paths[r.Action] || got.body == "" {
							t.Fatalf("%+v", got)
						}
						inQuery, inBody := got.query == "dryRun=All", strings.Contains(got.body, `"dryRun":["All"]`)
						if dry != (inQuery || inBody) || !dry && got.query != "" {
							t.Fatalf("dry %v: %+v", dry, got)
						}
					})
				}
			}
		}
	}
}

func wireExecutor(t *testing.T, w *wire) (*guard.Executor, *wireAudit) {
	t.Helper()
	auth, client, err := newActionClients(&rest.Config{Host: wireHost, Transport: w})
	if err != nil {
		t.Fatal(err)
	}
	audit := &wireAudit{}
	session := guard.Session{Context: "synthetic", Identity: "session-1", Generation: 1, Environment: guard.Environment{Valid: true}}
	exec, err := NewActionExecutor(context.Background(), session, true, auth, client, audit, guard.ClockFunc(time.Now), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(exec.Close)
	return exec, audit
}

type wireAudit struct{ events []guard.AuditEvent }

func (a *wireAudit) Record(e guard.AuditEvent) error {
	a.events = append(a.events, e)
	return nil
}

func (a *wireAudit) has(phase string) bool {
	for _, e := range a.events {
		if e.Phase == phase {
			return true
		}
	}
	return false
}

func TestPrepareRejectsRedirectedDryRun(t *testing.T) {
	for _, r := range wireActions() {
		for _, status := range redirectStatuses {
			for location := range 8 {
				w := &wire{}
				redirected := redirectWrites(status, location)
				w.answer = func(req *http.Request) (*http.Response, error) {
					if req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/v1/nodes") {
						return wireResponse(req, http.StatusOK, "", fmt.Sprintf(`{"apiVersion":"v1","kind":"Node","metadata":{"name":"node-a","uid":"uid","resourceVersion":"7"},"spec":{"unschedulable":%v}}`, r.Action == guard.Uncordon)), nil
					}
					return redirected(req)
				}
				exec, audit := wireExecutor(t, w)
				p, err := exec.Prepare(context.Background(), r)
				if !errors.Is(err, guard.DryRunFailed) || p.ID != "" {
					t.Fatalf("action %d status %d: %v %+v", r.Action, status, err, p)
				}
				writes := w.writes()
				if len(writes) != 1 || writes[0].query != "dryRun=All" && !strings.Contains(writes[0].body, `"dryRun":["All"]`) {
					t.Fatalf("prepare emitted a real write: %+v", writes)
				}
				if audit.has("intent") || audit.has("result") {
					t.Fatalf("%+v", audit.events)
				}
				for _, req := range w.requests {
					if req.host != "synthetic.invalid" {
						t.Fatalf("request left the session host: %+v", req)
					}
				}
			}
		}
	}
}

func TestExecuteRedirectIsUnknownAndSentOnce(t *testing.T) {
	for _, status := range redirectStatuses {
		for location := range 8 {
			w := &wire{}
			redirected := redirectWrites(status, location)
			w.answer = func(req *http.Request) (*http.Response, error) {
				if req.URL.Query().Get("dryRun") != "" {
					return wireResponse(req, http.StatusOK, "", wireObject), nil
				}
				return redirected(req)
			}
			exec, audit := wireExecutor(t, w)
			p, err := exec.Prepare(context.Background(), scaleRequest(guard.Scale))
			if err != nil {
				t.Fatal(err)
			}
			r, err := exec.Execute(context.Background(), p.ID, guard.Confirmation{Approved: true})
			if !errors.Is(err, guard.Unknown) || r.Outcome != guard.Unknown || len(w.writes()) != 2 || !audit.has("intent") {
				t.Fatal(status, r, err, w.writes())
			}
			if last := audit.events[len(audit.events)-1]; last.Phase != "result" || last.Outcome != guard.Unknown {
				t.Fatal(last)
			}
		}
	}
}

func TestRedirectedReadsAndAuthorizationFailClosed(t *testing.T) {
	for _, status := range redirectStatuses {
		for location := range 8 {
			for _, stage := range []string{"authorization", "read"} {
				w := &wire{}
				w.answer = func(req *http.Request) (*http.Response, error) {
					review := strings.HasSuffix(req.URL.Path, "/selfsubjectaccessreviews")
					if review == (stage == "authorization") {
						return wireResponse(req, status, redirectLocations(req)[location], wireAllowed), nil
					}
					return wireResponse(req, http.StatusCreated, "", wireAllowed), nil
				}
				exec, audit := wireExecutor(t, w)
				if _, err := exec.Prepare(context.Background(), scaleRequest(guard.Scale)); !errors.Is(err, guard.Unavailable) {
					t.Fatal(stage, status, err)
				}
				for _, req := range w.requests {
					if req.host != "synthetic.invalid" {
						t.Fatalf("%+v", req)
					}
				}
				if len(w.writes()) != 0 || audit.has("preview") || audit.has("intent") {
					t.Fatal(stage, w.writes(), audit.events)
				}
				if stage == "authorization" && len(w.requests) != 1 {
					t.Fatal(w.requests)
				}
			}
		}
	}
}

func TestActionWritesNeverRepeatOnTransportFailure(t *testing.T) {
	for _, failure := range []error{syscall.ECONNRESET, io.EOF, io.ErrUnexpectedEOF, context.DeadlineExceeded} {
		for _, r := range wireActions() {
			w := &wire{}
			w.answer = func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodGet {
					return wireResponse(req, http.StatusOK, "", wireObject), nil
				}
				return nil, failure
			}
			client, err := newActionClient(&rest.Config{Host: wireHost, Transport: w})
			if err != nil {
				t.Fatal(err)
			}
			if err = (actionBackend{client: client}).Send(context.Background(), r, false); !errors.Is(err, guard.Unknown) || len(w.writes()) != 1 {
				t.Fatal(r.Action, failure, err, len(w.writes()))
			}
		}
	}
}
