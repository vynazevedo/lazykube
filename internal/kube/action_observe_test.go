package kube

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

func actionServer(t *testing.T, handler http.HandlerFunc) actionBackend {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	return actionBackend{client: client}
}

func TestObserveReadsOnlyTheRequestedObject(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action guard.Action
		target guard.Target
		path   string
		body   string
		check  func(guard.Observation) bool
	}{
		{"scale", guard.Scale, guard.Target{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "apps", Name: "api"}, "/apis/apps/v1/namespaces/apps/deployments/api/scale", `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"api","uid":"u","resourceVersion":"8"},"spec":{"replicas":3},"status":{"replicas":2}}`, func(o guard.Observation) bool { return o.Replicas == 3 && o.ObservedReplicas == 2 }},
		{"scale zero", guard.Scale, guard.Target{Group: "apps", Version: "v1", Resource: "statefulsets", Namespace: "apps", Name: "db"}, "/apis/apps/v1/namespaces/apps/statefulsets/db/scale", `{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"db","uid":"u","resourceVersion":"8"},"spec":{},"status":{"replicas":0}}`, func(o guard.Observation) bool { return o.Replicas == 0 }},
		{"restart", guard.Restart, guard.Target{Group: "apps", Version: "v1", Resource: "daemonsets", Namespace: "apps", Name: "agent"}, "/apis/apps/v1/namespaces/apps/daemonsets/agent", `{"apiVersion":"apps/v1","kind":"DaemonSet","metadata":{"name":"agent","uid":"u","resourceVersion":"8"},"spec":{"updateStrategy":{"type":"OnDelete"}}}`, func(o guard.Observation) bool { return o.Strategy == "OnDelete" }},
		{"pod", guard.Delete, guard.Target{Version: "v1", Resource: "pods", Namespace: "apps", Name: "api-1"}, "/api/v1/namespaces/apps/pods/api-1", `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"api-1","uid":"u","resourceVersion":"8","finalizers":["a/b"],"deletionTimestamp":"2026-01-01T00:00:00Z","ownerReferences":[{"apiVersion":"apps/v1","kind":"ReplicaSet","name":"api","uid":"o","controller":true}]},"spec":{"terminationGracePeriodSeconds":45}}`, func(o guard.Observation) bool {
			return o.GraceKnown && o.GraceSeconds == 45 && o.Owner == "ReplicaSet/api" && o.Terminating && o.Finalizers == 1
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []string
			b := actionServer(t, func(w http.ResponseWriter, r *http.Request) {
				seen = append(seen, r.Method+" "+r.URL.RequestURI())
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			})
			o, err := b.Observe(context.Background(), tc.action, tc.target)
			if err != nil || o.Target.UID != "u" || o.Target.ResourceVersion != "8" || o.Target.Name != tc.target.Name || !tc.check(o) {
				t.Fatalf("%+v %v", o, err)
			}
			if len(seen) != 1 || seen[0] != "GET "+tc.path {
				t.Fatal(seen)
			}
		})
	}
}

func TestReadFailuresStayTyped(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   guard.Reason
	}{{http.StatusForbidden, guard.Denied}, {http.StatusUnauthorized, guard.Denied}, {http.StatusNotFound, guard.Stale}, {http.StatusBadGateway, guard.Unavailable}} {
		b := actionServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte("private server response"))
		})
		if _, err := b.Observe(context.Background(), guard.Delete, scaleRequest(guard.Delete).Target); !errors.Is(err, tc.want) || err.Error() != string(tc.want) {
			t.Fatal(tc.status, err)
		}
		if err := b.Validate(context.Background(), scaleRequest(guard.Delete)); !errors.Is(err, tc.want) {
			t.Fatal(tc.status, err)
		}
	}
}

func TestScaleValidateRejectsUnchangedReplicas(t *testing.T) {
	b := actionServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"autoscaling/v1","kind":"Scale","metadata":{"name":"api","uid":"uid","resourceVersion":"7"},"spec":{"replicas":3}}`))
	})
	if err := b.Validate(context.Background(), scaleRequest(guard.Scale)); !errors.Is(err, guard.Unchanged) {
		t.Fatal(err)
	}
}

func TestWriteRejectionsStayTyped(t *testing.T) {
	for _, tc := range []struct {
		action guard.Action
		status int
		want   guard.Reason
	}{
		{guard.Delete, http.StatusForbidden, guard.Denied}, {guard.Delete, http.StatusConflict, guard.Stale}, {guard.Delete, http.StatusNotFound, guard.Stale}, {guard.Delete, http.StatusInternalServerError, guard.Unknown},
		{guard.Scale, http.StatusConflict, guard.Stale}, {guard.Scale, http.StatusUnprocessableEntity, guard.Unknown},
		{guard.Restart, http.StatusUnprocessableEntity, guard.Stale}, {guard.Restart, http.StatusForbidden, guard.Denied},
	} {
		b := actionServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.Method == http.MethodGet {
				_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"apps","uid":"uid","resourceVersion":"7"}}`))
				return
			}
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{}`))
		})
		if err := b.Send(context.Background(), scaleRequest(tc.action), false); !errors.Is(err, tc.want) {
			t.Fatal(tc.action, tc.status, err)
		}
	}
}
