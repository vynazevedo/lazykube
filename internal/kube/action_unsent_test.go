package kube

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func TestRestartReadFailureIsNotUnknown(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   guard.Reason
	}{{http.StatusServiceUnavailable, guard.Unavailable}, {http.StatusForbidden, guard.Denied}, {http.StatusNotFound, guard.Stale}} {
		intent := false
		reads := 0
		w := &wire{}
		ok := failOnce(firstFailure{}, func(*http.Request) bool { return false })
		w.answer = func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet && intent {
				reads++
				return wireResponse(r, tc.status, "", `{}`), nil
			}
			return ok.answer(r)
		}
		exec, audit := wireExecutor(t, w)
		audit.onRecord = func(e guard.AuditEvent) { intent = intent || e.Phase == "intent" }
		p, err := exec.Prepare(context.Background(), scaleRequest(guard.Restart))
		if err != nil {
			t.Fatal(err)
		}
		before := len(w.writes())
		res, err := exec.Execute(context.Background(), p.ID, guard.Confirmation{Approved: true})
		if res.Outcome != tc.want || !errors.Is(err, tc.want) || reads != 1 {
			t.Fatal(tc.status, res, err, reads)
		}
		for _, write := range w.writes()[before:] {
			t.Fatalf("a failed read must not write: %+v", write)
		}
		if last := audit.events[len(audit.events)-1]; last.Phase != "result" || last.Outcome != tc.want {
			t.Fatal(last)
		}
	}
}

func TestRestartPatchFailureStaysUnknown(t *testing.T) {
	for _, f := range firstFailures() {
		w := failOnce(f, func(r *http.Request) bool {
			return r.Method == http.MethodPatch && r.URL.Query().Get("dryRun") == ""
		})
		exec, _ := wireExecutor(t, w)
		p, err := exec.Prepare(context.Background(), scaleRequest(guard.Restart))
		if err != nil {
			t.Fatal(err)
		}
		if res, err := exec.Execute(context.Background(), p.ID, guard.Confirmation{Approved: true}); res.Outcome != guard.Unknown || !errors.Is(err, guard.Unknown) {
			t.Fatal(f.name, res, err)
		}
	}
}
