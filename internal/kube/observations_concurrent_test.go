package kube

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/insight"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestObservationReadersConcurrentWithRebuild(t *testing.T) {
	s := newSource(fake.NewClientset(), nil, Options{Now: func() time.Time { return fixedNow }})
	defer s.cancel()
	s.podInf = cache.NewSharedIndexInformer(&cache.ListWatch{}, &corev1.Pod{}, 0, cache.Indexers{})
	s.podCoverage = data.Coverage{State: data.CoverageComplete}
	p := hardenedPod()
	if err := s.podInf.GetStore().Add(p); err != nil {
		t.Fatal(err)
	}
	s.rebuild()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			var cursor insight.Cursor
			for range 100 {
				batch := s.Observations(cursor)
				if !batch.Unchanged {
					if len(batch.Pods) != 1 || batch.Pods[0].Entity.UID != string(p.UID) {
						t.Error("incoherent snapshot")
						return
					}
					batch.Pods[0].Containers[0].Name = "reader mutation"
				}
				cursor = batch.Cursor
			}
		})
	}
	for i := range 30 {
		next := p.DeepCopy()
		next.ResourceVersion = strconv.Itoa(i + 2)
		if err := s.podInf.GetStore().Update(next); err != nil {
			t.Fatal(err)
		}
		s.rebuild()
	}
	wg.Wait()
	if s.Observations(insight.Cursor{}).Pods[0].Containers[0].Name == "reader mutation" {
		t.Fatal("reader mutated cached evidence")
	}
}
