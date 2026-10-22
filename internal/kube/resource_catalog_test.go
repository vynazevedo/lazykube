package kube

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
)

type resourceDiscoveryFixture struct {
	*discoveryfake.FakeDiscovery
	groups    *metav1.APIGroupList
	resources map[string]*metav1.APIResourceList
	failures  map[string]error
	calls     int
	groupsErr error
}

func (f *resourceDiscoveryFixture) ServerGroups() (*metav1.APIGroupList, error) {
	f.calls++
	return f.groups, f.groupsErr
}
func (f *resourceDiscoveryFixture) ServerResourcesForGroupVersion(v string) (*metav1.APIResourceList, error) {
	f.calls++
	return f.resources[v], f.failures[v]
}

func TestResourceCatalogStatesAndNoInstances(t *testing.T) {
	groups := []metav1.APIGroup{}
	for _, v := range []string{"v1", "apps/v1", "denied.test/v1", "gone.test/v1", "broken.test/v1"} {
		groups = append(groups, metav1.APIGroup{Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: v}}, PreferredVersion: metav1.GroupVersionForDiscovery{GroupVersion: v}})
	}
	fake := &resourceDiscoveryFixture{FakeDiscovery: &discoveryfake.FakeDiscovery{Fake: &clienttesting.Fake{}}, groups: &metav1.APIGroupList{Groups: groups}, resources: map[string]*metav1.APIResourceList{"v1": {APIResources: []metav1.APIResource{{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}}, {Name: "pods/exec"}, {Name: "nodes/proxy"}}}, "apps/v1": {APIResources: []metav1.APIResource{{Name: "deployments", Namespaced: true, Kind: "Deployment", Verbs: []string{"list"}}}}}, failures: map[string]error{"denied.test/v1": apierrors.NewForbidden(schema.GroupResource{Resource: "discovery"}, "", errors.New("sentinel")), "gone.test/v1": apierrors.NewNotFound(schema.GroupResource{}, ""), "broken.test/v1": errors.New("aggregated unavailable")}}
	now := fixedNow
	c := &resourceCatalog{client: fake, now: func() time.Time { return now }}
	got := c.get(context.Background(), false)
	if got.Coverage.State != data.CoveragePartial || len(got.Resources) != 2 {
		t.Fatalf("unexpected discovery: %+v", got)
	}
	for gv, want := range map[string]data.ResourceAvailability{"v1": data.ResourceAbsent, "denied.test/v1": data.ResourceDenied, "gone.test/v1": data.ResourceAbsent, "broken.test/v1": data.ResourcePartial} {
		g, _ := schema.ParseGroupVersion(gv)
		if got.Lookup(g.WithResource("missing")).Availability != want {
			t.Fatalf("group %s", gv)
		}
	}
	if got.Lookup(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Availability != data.ResourceServed {
		t.Fatal("pods missing")
	}
	calls := fake.calls
	got.Resources[0].Kind = "mutated"
	if c.get(context.Background(), false).Resources[0].Kind == "mutated" || fake.calls != calls {
		t.Fatal("TTL or ownership violated")
	}
	now = now.Add(catalogTTL)
	c.get(context.Background(), false)
	if fake.calls == calls {
		t.Fatal("TTL not refreshed")
	}
	if len(fake.Actions()) != 0 {
		t.Fatal("instance request during discovery")
	}
}

func TestResourceCatalogDeniedAndCanceled(t *testing.T) {
	f := &resourceDiscoveryFixture{groupsErr: apierrors.NewForbidden(schema.GroupResource{}, "", errors.New("denied"))}
	c := &resourceCatalog{client: f, now: func() time.Time { return fixedNow }}
	got := c.get(context.Background(), true)
	if got.Lookup(schema.GroupVersionResource{Version: "v1", Resource: "pods"}).Availability != data.ResourceDenied {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := f.calls
	if c.get(ctx, true).Coverage.State != data.CoverageUnavailable || f.calls != calls {
		t.Fatal("canceled discovery issued a request")
	}
}
