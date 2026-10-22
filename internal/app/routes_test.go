package app

import (
	"testing"
)

func sessionModel(t *testing.T, gen uint64) Model {
	t.Helper()
	m := demoModel(t, 160, 48)
	m.session.Generation = gen
	m.nav.active.Generation = gen
	return m
}

func TestRoutesCarryDestinationAndGeneration(t *testing.T) {
	m := sessionModel(t, 7)
	if r := m.nav.Active(); r.Dest != DestDashboard || r.Generation != 7 {
		t.Fatalf("the initial route names its destination and session: %+v", r)
	}
	if again := press(t, m, "2"); again.nav.Depth() != 0 {
		t.Fatal("activating the initial destination again does not push")
	}
	m = press(t, m, "3")
	if r := m.nav.Active(); r.Dest != DestPods || r.Screen != ScreenWorkloads || r.Generation != 7 || m.focus != FocusPods {
		t.Fatalf("3 opens the Pods leaf of Workloads: %+v focus %d", r, m.focus)
	}
	ok, _ := m.activateDestination(DestDeployments)
	if r := m.nav.Active(); !ok || r.Dest != DestDeployments || m.focus != FocusDeployments || m.nav.Depth() != 2 {
		t.Fatalf("the Deployments leaf focuses deployments in the same screen: %+v", r)
	}
	depth := m.nav.Depth()
	if m.activateDestination(DestDeployments); m.nav.Depth() != depth {
		t.Fatal("the same target does not push twice")
	}
	m = press(t, m, "2", "3")
	if r := m.nav.Active(); r.Dest != DestDeployments || m.focus != FocusDeployments {
		t.Fatalf("3 restores the last Workloads leaf: %+v", r)
	}
	for _, id := range []DestinationID{DestJobs, DestWorkloadsOver, DestSecurity, DestRadar, DestNamespaces, DestConfiguration, "nope"} {
		before := m.nav
		if ok, reason := m.activateDestination(id); ok || reason == "" || m.nav != before {
			t.Fatalf("%s does not navigate and explains why", id)
		}
	}
	for _, k := range []string{"5", "7"} {
		if got := press(t, m, k); got.nav != m.nav {
			t.Fatalf("%s is reserved, not active", k)
		}
	}
}

func TestPodRouteCarriesTypedReference(t *testing.T) {
	m := press(t, sessionModel(t, 3), "enter")
	r := m.nav.Active()
	if r.Kind != RoutePod || r.Ref != (ResourceRef{Version: "v1", Resource: "pods", Namespace: "bilibili-raffle", Name: "bilibili-live-7f6b8d9c5-mn4tz"}) || r.Generation != 3 {
		t.Fatalf("pod route = %+v", r)
	}
	if r.Ref.UID != "" {
		t.Fatal("a UID the source does not provide stays absent")
	}
	if back := press(t, m, "esc"); back.nav.Active().Dest != DestDashboard || back.nav.Depth() != 0 {
		t.Fatal("esc returns to the dashboard frame")
	}
}

func TestNamespaceScopeSurvivesDestinations(t *testing.T) {
	m := navNamespace(t, sessionModel(t, 1))
	for _, k := range []string{"6", "4", "3"} {
		m = press(t, m, k)
		if ns := m.scopeNamespace(); ns != "kube-system" {
			t.Fatalf("after %s the namespace scope stays kube-system, got %q", k, ns)
		}
		if r := m.nav.Active(); r.Kind != RouteNamespace || r.Dest == "" {
			t.Fatalf("after %s the scoped route keeps its destination: %+v", k, r)
		}
	}
	ok, _ := m.activateDestination(DestDeployments)
	if !ok || m.scopeNamespace() != "kube-system" || m.focus != FocusDeployments {
		t.Fatal("a leaf of the current screen keeps the scope")
	}
	for range 40 {
		m.activateDestination(DestPods)
		m.activateDestination(DestDeployments)
	}
	if m.nav.Depth() != maxFrames {
		t.Fatalf("the stack stays bounded at %d, got %d", maxFrames, m.nav.Depth())
	}
}
