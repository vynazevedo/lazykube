package kube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/guard"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clienttesting "k8s.io/client-go/testing"
)

func TestActionAuthorizationExactSubresource(t *testing.T) {
	client := fake.NewClientset()
	var observed *authorizationv1.ResourceAttributes
	client.PrependReactor("create", "selfsubjectaccessreviews", func(action clienttesting.Action) (bool, runtime.Object, error) {
		r := action.(clienttesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview)
		observed = r.Spec.ResourceAttributes
		return true, &authorizationv1.SelfSubjectAccessReview{Status: authorizationv1.SubjectAccessReviewStatus{Allowed: true}}, nil
	})
	a := actionAuthorizer{client}
	want := guard.Access{Group: "apps", Version: "v1", Resource: "deployments", Subresource: "scale", Namespace: "apps", Name: "api", Verb: "update"}
	ok, err := a.Allowed(context.Background(), want)
	if err != nil || !ok || observed.Subresource != "scale" || observed.Verb != "update" || observed.Name != "api" || observed.Namespace != "apps" {
		t.Fatal(observed, err)
	}
	before := len(client.Actions())
	if ok, err = a.Allowed(context.Background(), guard.Access{Resource: "nodes", Subresource: "proxy"}); ok || err == nil || len(client.Actions()) != before {
		t.Fatal("proxy authorization attempted")
	}
}
func TestActionRequestsUsePreconditionsAndServerDryRun(t *testing.T) {
	for _, action := range []guard.Action{guard.Delete, guard.Scale, guard.Restart, guard.Edit} {
		t.Run(string(rune('0'+action)), func(t *testing.T) {
			var methods, paths, dryRuns []string
			var bodies []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				methods = append(methods, r.Method)
				paths = append(paths, r.URL.Path)
				dryRuns = append(dryRuns, r.URL.Query().Get("dryRun"))
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					if r.Method == "PATCH" {
						var patch []map[string]any
						if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
							t.Error(err)
						}
						if len(patch) != 3 || patch[0]["value"] != "uid" || patch[1]["value"] != "7" {
							t.Error(patch)
						}
						bodies = append(bodies, map[string]any{})
					} else {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
						}
						bodies = append(bodies, body)
					}
				}
				_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"apps","uid":"uid","resourceVersion":"7"},"spec":{"template":{"metadata":{}}}}`))
			}))
			defer server.Close()
			dc, err := dynamic.NewForConfig(&rest.Config{Host: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			b := actionBackend{client: dc}
			r := guard.Request{Action: action, Target: guard.Target{Group: "apps", Version: "v1", Resource: "deployments", Namespace: "apps", Name: "api", UID: "uid", ResourceVersion: "7"}, Parameters: guard.Parameters{Propagation: "Background", Replicas: 3, Document: []byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"api","namespace":"apps","uid":"uid","resourceVersion":"7"}}`)}}
			if action == guard.Restart {
				r.Parameters.Document = []byte("2026-01-01T00:00:00Z")
			}
			if err = b.Validate(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			if err = b.Send(context.Background(), r, true); err != nil {
				t.Fatal(err)
			}
			last := len(methods) - 1
			if action == guard.Delete {
				pre := bodies[0]["preconditions"].(map[string]any)
				if pre["uid"] != "uid" || pre["resourceVersion"] != "7" {
					t.Fatal(pre)
				}
				if bodies[0]["dryRun"].([]any)[0] != metav1.DryRunAll {
					t.Fatal(bodies)
				}
			} else if dryRuns[last] != metav1.DryRunAll {
				t.Fatal(dryRuns)
			}
			if action == guard.Scale && !strings.HasSuffix(paths[last], "/scale") {
				t.Fatal(paths)
			}
			if action == guard.Edit && methods[last] != "PUT" {
				t.Fatal(methods)
			}
			if action == guard.Restart && methods[last] != "PATCH" {
				t.Fatal(methods)
			}
		})
	}
}
func TestActiveActionsStayUnavailableWithoutValidatedTransport(t *testing.T) {
	b := actionBackend{}
	if b.Supported(guard.Exec) || b.Supported(guard.PortForward) {
		t.Fatal("active transport enabled")
	}
}

func TestNetworkPolicyDisablesProxyBeforeReadingCaches(t *testing.T) {
	s := &Source{opts: Options{DisableNetwork: true}}
	s.pollNetwork(context.Background())
	if reason := s.netReasonLocked(nil, nil); reason != "disabled by policy" {
		t.Fatal(reason)
	}
}
