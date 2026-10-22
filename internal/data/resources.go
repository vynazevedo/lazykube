package data

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

type ResourceAvailability string

const (
	ResourceServed            ResourceAvailability = "served"
	ResourceDenied            ResourceAvailability = "denied"
	ResourceAbsent            ResourceAvailability = "absent"
	ResourcePartial           ResourceAvailability = "partial discovery"
	ResourceUnknown           ResourceAvailability = "unknown"
	ResourceProjectionVersion                      = 1
	CoverageListOnly          CoverageState        = "list-only"
)

type ResourceDescriptor struct {
	GVR          schema.GroupVersionResource
	Kind         string
	Namespaced   bool
	Preferred    bool
	List         bool
	Watch        bool
	Get          bool
	Availability ResourceAvailability
}

type ResourceGroupStatus struct {
	GroupVersion string
	Availability ResourceAvailability
}

type ResourceCatalogSnapshot struct {
	Session    string
	Generation uint64
	Revision   uint64
	ObservedAt time.Time
	Coverage   Coverage
	Resources  []ResourceDescriptor
	Groups     []ResourceGroupStatus
}

func (s ResourceCatalogSnapshot) Lookup(gvr schema.GroupVersionResource) ResourceDescriptor {
	for _, r := range s.Resources {
		if r.GVR == gvr {
			return r
		}
	}
	state := ResourceUnknown
	if s.Coverage.State == CoverageDenied {
		state = ResourceDenied
	}
	if s.Coverage.State == CoveragePartial {
		state = ResourcePartial
	}
	if s.Coverage.State == CoverageComplete {
		state = ResourceAbsent
	}
	for _, group := range s.Groups {
		if group.GroupVersion == gvr.GroupVersion().String() {
			state = group.Availability
			if state == ResourceServed {
				state = ResourceAbsent
			}
			break
		}
	}
	return ResourceDescriptor{GVR: gvr, Availability: state}
}

type ResourceSubscriptionKey struct {
	Session           string
	Generation        uint64
	GVR               schema.GroupVersionResource
	Namespace         string
	LabelSelector     string
	FieldSelector     string
	ProjectionVersion int
}

type ResourceMetadataRef struct {
	Namespace       string
	Name            string
	UID             string
	ResourceVersion string
	CreatedAt       time.Time
}

type ResourceRow struct {
	Ref       ResourceMetadataRef
	Truncated bool
}

type ResourcePage struct {
	Items      []ResourceRow
	Continue   string
	Revision   uint64
	ObservedAt time.Time
	Coverage   Coverage
	Total      int
	Truncated  bool
	Borrowed   bool
}

type ResourceSubscription interface {
	Page(cursor string, limit int) ResourcePage
	Changes() <-chan struct{}
	Refresh()
	Cancel()
}

type ResourceBrowser interface {
	Catalog(context.Context, bool) ResourceCatalogSnapshot
	Subscribe(context.Context, ResourceSubscriptionKey) (ResourceSubscription, error)
}
