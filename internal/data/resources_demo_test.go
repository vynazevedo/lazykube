package data

import (
	"context"
	"testing"
)

func TestDemoResourceBrowserWorkloads(t *testing.T) {
	d := NewDemoScoped("demo", "apps")
	catalog := d.Catalog(context.Background(), false)
	if !catalog.Synthetic || len(catalog.Resources) != 7 {
		t.Fatal(catalog)
	}
	for _, descriptor := range catalog.Resources {
		key := ResourceSubscriptionKey{Session: catalog.Session, Generation: catalog.Generation, GVR: descriptor.GVR, Namespace: "apps", ProjectionVersion: ResourceProjectionVersion}
		h, err := d.Subscribe(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		page := h.Page("", 1)
		if !page.Synthetic || page.Coverage.State != CoverageComplete || page.Workloads.Total != page.Total {
			t.Fatal(page)
		}
		if descriptor.Kind != "Pod" && descriptor.Kind != "Deployment" {
			if page.Total != 3 || len(page.Items) != 1 || page.Continue == "" {
				t.Fatal(page)
			}
			next := h.Page(page.Continue, 500)
			if len(next.Items) != 2 {
				t.Fatal("demo paging")
			}
		}
		h.Cancel()
		if h.Page("", 500).Coverage.State != CoverageUnavailable {
			t.Fatal("demo cancel")
		}
		key.Namespace = "other"
		if _, err = d.Subscribe(context.Background(), key); err == nil {
			t.Fatal("demo escaped scope")
		}
	}
}
