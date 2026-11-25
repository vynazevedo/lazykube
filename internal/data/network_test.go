package data

import (
	"context"
	"testing"
)

func TestDemoNetworkResourcesFollowScopeAndProjection(t *testing.T) {
	d := NewDemoScoped("demo", "lgo")
	catalog := d.Catalog(context.Background(), false)
	totals := map[string]int{"Service": 1, "EndpointSlice": 1, "Ingress": 1, "IngressClass": 1, "NetworkPolicy": 0, "Node": 1}
	for _, descriptor := range catalog.Resources[7:] {
		key := ResourceSubscriptionKey{Session: catalog.Session, Generation: catalog.Generation, GVR: descriptor.GVR, ProjectionVersion: ResourceNetworkProjection}
		if descriptor.Namespaced {
			if _, err := d.Subscribe(context.Background(), key); err == nil {
				t.Fatalf("%s escaped the namespace scope", descriptor.Kind)
			}
			key.Namespace = "lgo"
		}
		h, err := d.Subscribe(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		page := h.Page("", 500)
		if !page.Synthetic || page.Total != totals[descriptor.Kind] {
			t.Fatalf("%s total=%d", descriptor.Kind, page.Total)
		}
		for _, row := range page.Items {
			if row.Network == nil || descriptor.Namespaced && row.Ref.Namespace != "lgo" {
				t.Fatalf("%s row = %+v", descriptor.Kind, row)
			}
		}
		h.Cancel()
	}
}

func TestDemoPodsCarryFactsOnlyInTheNetworkProjection(t *testing.T) {
	d := NewDemo()
	catalog := d.Catalog(context.Background(), false)
	for version, want := range map[int]bool{ResourceProjectionVersion: false, ResourceNetworkProjection: true} {
		for _, descriptor := range catalog.Resources[:2] {
			h, err := d.Subscribe(context.Background(), ResourceSubscriptionKey{Session: catalog.Session, Generation: catalog.Generation, GVR: descriptor.GVR, ProjectionVersion: version})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range h.Page("", 500).Items {
				if (row.Network != nil) != want {
					t.Fatalf("%s projection %d facts=%v", row.Ref.Name, version, row.Network)
				}
			}
			h.Cancel()
		}
	}
}

func TestRowMatchesNamesAndFacts(t *testing.T) {
	row := ResourceRow{Ref: ResourceMetadataRef{Namespace: "apps", Name: "web"}, Network: &NetworkFacts{Ingress: &IngressFacts{Class: "nginx", Rules: []IngressRule{{Host: "web.example.com", Paths: []IngressPath{{Path: "/api", Backend: IngressBackend{Service: "api"}}}}}}}}
	for query, want := range map[string]bool{"": true, "  ": true, "apps/web": true, "WEB": true, "nginx example": true, "/api": true, "nginx other": false, "zzz": false} {
		if row.Matches(query) != want {
			t.Errorf("%q matches=%v", query, !want)
		}
	}
}
