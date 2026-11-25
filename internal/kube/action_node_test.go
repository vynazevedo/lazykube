package kube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
)

func nodeActionRequest(a guard.Action) guard.Request {
	return guard.Request{Action: a, Target: guard.Target{Version: "v1", Resource: "nodes", Name: "node-a", UID: "uid", ResourceVersion: "7"}}
}

func TestNodePatchTestsIdentityAndChangesOnlyScheduling(t *testing.T) {
	for _, action := range []guard.Action{guard.Cordon, guard.Uncordon} {
		var bodies, queries []string
		b := actionServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/nodes/node-a" || r.Header.Get("Content-Type") != "application/json-patch+json" {
				t.Errorf("%s %s %s", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
			}
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			bodies, queries = append(bodies, string(raw)), append(queries, r.URL.RawQuery)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Node","metadata":{"name":"node-a","uid":"uid","resourceVersion":"8"}}`))
		})
		for _, dry := range []bool{true, false} {
			if err := b.Send(context.Background(), nodeActionRequest(action), dry); err != nil {
				t.Fatal(err)
			}
		}
		if len(bodies) != 2 || bodies[0] != bodies[1] || queries[0] != "dryRun=All" || queries[1] != "" {
			t.Fatal(bodies, queries)
		}
		var patch []map[string]any
		if err := json.Unmarshal([]byte(bodies[0]), &patch); err != nil {
			t.Fatal(err)
		}
		want := []map[string]any{{"op": "test", "path": "/metadata/uid", "value": "uid"}, {"op": "test", "path": "/metadata/resourceVersion", "value": "7"}, {"op": "add", "path": "/spec/unschedulable", "value": action == guard.Cordon}}
		if len(patch) != len(want) {
			t.Fatal(patch)
		}
		for i := range want {
			for k, v := range want[i] {
				if len(patch[i]) != 3 || patch[i][k] != v {
					t.Fatal(i, patch[i])
				}
			}
		}
	}
}

func TestNodeValidateBlocksNoOpAndRecreation(t *testing.T) {
	node := func(spec string) actionBackend {
		return actionServer(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"Node","metadata":{"name":"node-a","uid":"uid","resourceVersion":"7"},"spec":` + spec + `}`))
		})
	}
	for _, tc := range []struct {
		spec   string
		action guard.Action
		want   error
	}{{`{}`, guard.Cordon, nil}, {`{}`, guard.Uncordon, guard.Unchanged}, {`{"unschedulable":true}`, guard.Cordon, guard.Unchanged}, {`{"unschedulable":true}`, guard.Uncordon, nil}} {
		if err := node(tc.spec).Validate(context.Background(), nodeActionRequest(tc.action)); !errors.Is(err, tc.want) {
			t.Fatal(tc, err)
		}
	}
	for _, change := range []func(*guard.Target){func(x *guard.Target) { x.UID = "old" }, func(x *guard.Target) { x.ResourceVersion = "6" }} {
		r := nodeActionRequest(guard.Cordon)
		change(&r.Target)
		if err := node(`{}`).Validate(context.Background(), r); !errors.Is(err, guard.Stale) {
			t.Fatal(err)
		}
	}
}

func TestNodePatchConflictIsStaleAndNeverProxy(t *testing.T) {
	for _, status := range []int{http.StatusUnprocessableEntity, http.StatusConflict, http.StatusNotFound} {
		requests := 0
		b := actionServer(t, func(w http.ResponseWriter, _ *http.Request) {
			requests++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{}`))
		})
		if err := b.Send(context.Background(), nodeActionRequest(guard.Cordon), false); !errors.Is(err, guard.Stale) || requests != 1 {
			t.Fatal(status, err, requests)
		}
	}
}
