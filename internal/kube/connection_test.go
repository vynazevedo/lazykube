package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestSnapshotCarriesConnectedNamespace(t *testing.T) {
	all := startTest(t, nil, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	if got := all.Snapshot().Cluster.Namespace; got != "" {
		t.Fatalf("an all namespaces connection has no connected namespace, got %q", got)
	}
	restricted := startWith(t, nil, func(s *Source) { s.opts.Namespace = "apps" }, testPod("apps", "web-1", "node-a", corev1.PodRunning))
	waitFor(t, func() bool { return len(restricted.Snapshot().Pods) == 1 })
	if got := restricted.Snapshot().Cluster.Namespace; got != "apps" {
		t.Fatalf("a restricted connection reports its namespace, got %q", got)
	}
	if !all.Snapshot().Cluster.Listed {
		t.Fatal("with list namespaces the catalog of namespaces is complete")
	}
	if restricted.Snapshot().Cluster.Listed {
		t.Fatal("a restricted connection only observes namespaces")
	}
}
