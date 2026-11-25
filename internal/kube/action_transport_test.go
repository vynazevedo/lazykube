package kube

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
	"k8s.io/client-go/rest"
)

func TestActionClientNeverRetriesWrites(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		for _, action := range []guard.Action{guard.Delete, guard.Scale} {
			var writes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes.Add(1)
				}
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
			}))
			client, err := newActionClient(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			err = actionBackend{client: client}.Send(context.Background(), scaleRequest(action), false)
			server.Close()
			if !errors.Is(err, guard.Unknown) || writes.Load() != 1 {
				t.Fatalf("status %d action %d: %v after %d writes", status, action, err, writes.Load())
			}
		}
	}
}

func scaleRequest(action guard.Action) guard.Request {
	return guard.Request{Action: action, Target: guard.Target{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "apps", Name: "api", UID: "uid", ResourceVersion: "7"}, Parameters: guard.Parameters{Propagation: "Background", GraceSeconds: 30, Replicas: 3}}
}
