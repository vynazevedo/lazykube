package kube

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/vynazevedo/lazykube/internal/data"
)

func TestSecurityOwnerFromControllerReference(t *testing.T) {
	yes, no := true, false
	p := hardenedPod()
	p.OwnerReferences = []metav1.OwnerReference{
		{APIVersion: "v1", Kind: "ConfigMap", Name: "cm", UID: "cm-uid", Controller: &no},
		{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-7f", UID: "rs-uid", Controller: &yes},
	}
	got := evaluatePod(p, "ctx", 1, fixedNow).Owner
	if got == nil || *got != (data.OwnerRef{APIVersion: "apps/v1", Kind: "ReplicaSet", Name: "api-7f", UID: "rs-uid"}) {
		t.Fatalf("controller owner not taken from metadata.ownerReferences: %+v", got)
	}
	p.OwnerReferences = p.OwnerReferences[:1]
	if got := evaluatePod(p, "ctx", 1, fixedNow).Owner; got != nil {
		t.Fatalf("non-controller references must not become the owner: %+v", got)
	}
}

func TestSecurityRevisionChangesWithOwner(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{Context: "test"})
	s.podInf = cache.NewSharedIndexInformer(nil, &corev1.Pod{}, 0, nil)
	s.podCoverage.State = data.CoverageComplete
	p := hardenedPod()
	s.rebuildSecurity([]*corev1.Pod{p}, nil, fixedNow)
	before := s.Security()
	yes := true
	next := p.DeepCopy()
	next.ResourceVersion += "1"
	next.OwnerReferences = []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "db", UID: "ss", Controller: &yes}}
	s.rebuildSecurity([]*corev1.Pod{next}, nil, fixedNow)
	after := s.Security()
	if after.Revision == before.Revision {
		t.Fatal("owner change must bump the posture revision")
	}
	for _, pp := range after.Pods {
		if pp.Owner == nil || pp.Owner.Kind != "StatefulSet" {
			t.Fatalf("owner not refreshed: %+v", pp.Owner)
		}
	}
}
