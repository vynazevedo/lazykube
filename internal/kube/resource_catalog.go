package kube

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
)

const catalogTTL = 5 * time.Minute
const catalogLimit = 4096

type resourceCatalog struct {
	mu       sync.Mutex
	client   discovery.DiscoveryInterface
	now      func() time.Time
	snapshot data.ResourceCatalogSnapshot
}

func (c *resourceCatalog) get(ctx context.Context, refresh bool) data.ResourceCatalogSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot.Revision != 0 && !refresh && c.now().Sub(c.snapshot.ObservedAt) < catalogTTL {
		return cloneCatalog(c.snapshot)
	}
	next := data.ResourceCatalogSnapshot{Session: c.snapshot.Session, Generation: c.snapshot.Generation, Revision: c.snapshot.Revision + 1, ObservedAt: c.now(), Coverage: data.Coverage{State: data.CoverageComplete}}
	if ctx.Err() != nil || c.client == nil {
		next.Coverage = data.Coverage{State: data.CoverageUnavailable, Reason: "discovery unavailable"}
		c.snapshot = next
		return cloneCatalog(next)
	}
	groups, err := c.client.ServerGroups()
	if err != nil {
		next.Coverage = data.Coverage{State: data.CoverageUnavailable, Reason: "discovery unavailable"}
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			next.Coverage = data.Coverage{State: data.CoverageDenied, Reason: "discovery denied"}
		}
	}
	if groups != nil {
		versions := make(map[string]bool)
		for _, g := range groups.Groups {
			for _, v := range g.Versions {
				if len(v.GroupVersion) > 320 {
					next.Coverage = data.Coverage{State: data.CoveragePartial, Reason: "invalid discovery identity"}
					continue
				}
				versions[strings.Clone(v.GroupVersion)] = v.GroupVersion == g.PreferredVersion.GroupVersion
			}
		}
		keys := make([]string, 0, len(versions))
		for v := range versions {
			keys = append(keys, v)
		}
		sort.Strings(keys)
		if len(keys) > 256 {
			keys = keys[:256]
			next.Coverage = data.Coverage{State: data.CoveragePartial, Reason: "discovery group limit"}
		}
		for _, v := range keys {
			if ctx.Err() != nil {
				next.Coverage = data.Coverage{State: data.CoveragePartial, Reason: "discovery canceled"}
				break
			}
			resources, resourceErr := c.client.ServerResourcesForGroupVersion(v)
			state := data.ResourceServed
			if resourceErr != nil {
				state = data.ResourcePartial
				if apierrors.IsForbidden(resourceErr) || apierrors.IsUnauthorized(resourceErr) {
					state = data.ResourceDenied
				}
				if apierrors.IsNotFound(resourceErr) {
					state = data.ResourceAbsent
				}
				next.Coverage = data.Coverage{State: data.CoveragePartial, Reason: "some API groups unavailable"}
			}
			next.Groups = append(next.Groups, data.ResourceGroupStatus{GroupVersion: v, Availability: state})
			if resourceErr != nil || resources == nil {
				continue
			}
			gv, parseErr := schema.ParseGroupVersion(v)
			if parseErr != nil {
				continue
			}
			for _, r := range resources.APIResources {
				if strings.Contains(r.Name, "/") || r.Name == "" {
					continue
				}
				if len(r.Name) > 253 || len(r.Kind) > 253 {
					next.Groups[len(next.Groups)-1].Availability = data.ResourcePartial
					next.Coverage = data.Coverage{State: data.CoveragePartial, Reason: "invalid discovery identity"}
					continue
				}
				if len(next.Resources) >= catalogLimit {
					next.Groups[len(next.Groups)-1].Availability = data.ResourcePartial
					next.Coverage = data.Coverage{State: data.CoveragePartial, Reason: "discovery resource limit"}
					break
				}
				next.Resources = append(next.Resources, data.ResourceDescriptor{GVR: gv.WithResource(strings.Clone(r.Name)), Kind: strings.Clone(r.Kind), Namespaced: r.Namespaced, Preferred: versions[v], List: slices.Contains(r.Verbs, "list"), Watch: slices.Contains(r.Verbs, "watch"), Get: slices.Contains(r.Verbs, "get"), Availability: data.ResourceServed})
			}
		}
	}
	sort.Slice(next.Resources, func(i, j int) bool { return next.Resources[i].GVR.String() < next.Resources[j].GVR.String() })
	c.snapshot = next
	return cloneCatalog(next)
}

func cloneCatalog(s data.ResourceCatalogSnapshot) data.ResourceCatalogSnapshot {
	s.Resources = slices.Clone(s.Resources)
	s.Groups = slices.Clone(s.Groups)
	return s
}
