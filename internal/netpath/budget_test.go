package netpath

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
)

type countingContext struct {
	context.Context
	calls int
	limit int
}

func (c *countingContext) Err() error {
	c.calls++
	if c.limit > 0 && c.calls >= c.limit {
		return context.Canceled
	}
	return nil
}

func largeGraph(n int) Graph {
	g := healthy()
	g.Services.Rows, g.Ingresses.Rows, g.Pods.Rows, g.Slices.Rows = nil, nil, nil, nil
	for i := range n {
		name := fmt.Sprintf("web-%05d", i)
		s := webService(nil)
		s.Ref.Name = name
		ing := webIngress(nil)
		ing.Ref.Name = name
		ing.Network.Ingress.Rules[0].Paths[0].Backend.Service = name
		e := webSlice(1, 1)
		e.Ref.Name, e.Network.Slice.Service = name, name
		g.Services.Rows = append(g.Services.Rows, s)
		g.Ingresses.Rows = append(g.Ingresses.Rows, ing)
		g.Pods.Rows = append(g.Pods.Rows, webPod(name, data.TriTrue))
		g.Slices.Rows = append(g.Slices.Rows, e)
	}
	return g
}

var largeTarget = Target{Kind: TargetWorkload, Namespace: "apps", Name: "all", Labels: webLabels, LabelsKnown: true}

func TestCancellationStopsAtEveryPoint(t *testing.T) {
	g := largeGraph(3000)
	total := &countingContext{Context: context.Background()}
	if _, err := AccessContext(total, g, largeTarget); err != nil {
		t.Fatal(err)
	}
	for limit := 1; limit <= total.calls; limit++ {
		ctx := &countingContext{Context: context.Background(), limit: limit}
		r, err := AccessContext(ctx, g, largeTarget)
		if !errors.Is(err, context.Canceled) || len(r.Addresses)+len(r.Routes)+len(r.Notes) != 0 {
			t.Fatalf("canceled at check %d of %d: err=%v addresses=%d", limit, total.calls, err, len(r.Addresses))
		}
		if ctx.calls > limit+8 {
			t.Fatalf("canceled at check %d, kept going for %d more checks", limit, ctx.calls-limit)
		}
	}
}

func TestOutputLimitsStopTheWork(t *testing.T) {
	for _, n := range []int{200, 2000, 5000} {
		g := largeGraph(n)
		ctx := &countingContext{Context: context.Background()}
		r, err := AccessContext(ctx, g, largeTarget)
		if err != nil || len(r.Addresses) != MaxAddresses || len(r.Routes) != MaxRoutes || !r.Truncated {
			t.Fatalf("n=%d addresses=%d routes=%d truncated=%v err=%v", n, len(r.Addresses), len(r.Routes), r.Truncated, err)
		}
		scans := 8 * (n/cancelStride + 1)
		podScan := n/cancelStride + 1
		if budget := scans + podScan + 4*MaxAddresses; ctx.calls > budget {
			t.Fatalf("n=%d did %d checks, budget %d", n, ctx.calls, budget)
		}
	}
}

func TestLimitsKeepBothIngressesAndServices(t *testing.T) {
	r := Access(largeGraph(500), largeTarget)
	seen := map[Origin]int{}
	for _, a := range r.Addresses {
		seen[a.Origin]++
	}
	if seen[OriginIngress] < MaxAddresses/4 || seen[OriginClusterIP] < MaxAddresses/4 {
		t.Fatalf("one kind starves the other: %v", seen)
	}
}

func TestServicesWithoutAddressesAreAlsoLimited(t *testing.T) {
	g := largeGraph(500)
	g.Ingresses.Rows = nil
	for i := range g.Services.Rows {
		g.Services.Rows[i].Network.Service.Ports = nil
	}
	r := Access(g, largeTarget)
	if !r.Truncated || len(r.Notes) > maxNotes || len(r.Routes) != MaxRoutes {
		t.Fatalf("truncated=%v notes=%d routes=%d", r.Truncated, len(r.Notes), len(r.Routes))
	}
}

func TestSmallReportsAreNotMarkedAsLimited(t *testing.T) {
	for _, target := range []Target{podTarget, serviceTarget, ingressTarget} {
		if r := Access(healthy(), target); r.Truncated || r.Policies.Truncated {
			t.Fatalf("%+v", target)
		}
	}
	g := largeGraph(1)
	for i := range 64 {
		g.Policies.Rows = append(g.Policies.Rows, row("apps", fmt.Sprintf("p%d", i), data.NetworkFacts{Policy: &data.NetworkPolicyFacts{}}))
	}
	if r := Access(g, largeTarget); len(r.Policies.Selecting) != maxPolicies || !r.Policies.Truncated {
		t.Fatalf("%d %v", len(r.Policies.Selecting), r.Policies.Truncated)
	}
}

func TestSelectorCacheNeverMixesSelectors(t *testing.T) {
	g := healthy()
	x, err := newIndex(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	one := data.Labels{{Key: "a", Value: "1,b=2"}}
	two := data.Labels{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	x.countPods("apps", one)
	x.countPods("apps", two)
	if len(x.podCounts) != 2 {
		t.Fatal("selectors that print alike are different selectors")
	}
}

func BenchmarkAccess5000(b *testing.B) {
	g := largeGraph(5000)
	b.ResetTimer()
	for range b.N {
		Access(g, largeTarget)
	}
}
