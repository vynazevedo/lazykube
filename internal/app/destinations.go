package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type DestinationID string

type DestKind int

const (
	DestCategory DestKind = iota
	DestLeaf
)

type Availability int

const (
	AvailReady Availability = iota
	AvailUnsupported
	AvailDisabled
	AvailPlanned
)

const reasonPlanned = "tela ainda não implementada"

type Destination struct {
	ID           DestinationID
	Parent       DestinationID
	Label        string
	Order        int
	Kind         DestKind
	Screen       ScreenID
	Implemented  bool
	Focus        Focus
	Default      DestinationID
	Shortcut     string
	Reserved     string
	Aliases      []string
	Availability Availability
	Reason       string
	BadgeKey     string
}

const (
	DestCluster        DestinationID = "cluster"
	DestDashboard      DestinationID = "cluster.dashboard"
	DestNodes          DestinationID = "nodes"
	DestWorkloads      DestinationID = "workloads"
	DestWorkloadsOver  DestinationID = "workloads.overview"
	DestPods           DestinationID = "workloads.pods"
	DestDeployments    DestinationID = "workloads.deployments"
	DestDaemonSets     DestinationID = "workloads.daemonsets"
	DestStatefulSets   DestinationID = "workloads.statefulsets"
	DestJobs           DestinationID = "workloads.jobs"
	DestCronJobs       DestinationID = "workloads.cronjobs"
	DestConfiguration  DestinationID = "configuration"
	DestNetwork        DestinationID = "network"
	DestStorage        DestinationID = "storage"
	DestNamespaces     DestinationID = "namespaces"
	DestEvents         DestinationID = "events"
	DestHelm           DestinationID = "helm"
	DestReplicaSets    DestinationID = "workloads.replicasets"
	DestAccessControl  DestinationID = "access"
	DestCustomResource DestinationID = "custom"
	DestSecurity       DestinationID = "security"
	DestRadar          DestinationID = "radar"
)

func leaf(id, parent DestinationID, label string, order int, screen ScreenID, focus Focus) Destination {
	return Destination{ID: id, Parent: parent, Label: label, Order: order, Kind: DestLeaf, Screen: screen, Implemented: true, Focus: focus}
}

func planned(id, parent DestinationID, label string, order int) Destination {
	return Destination{ID: id, Parent: parent, Label: label, Order: order, Kind: DestLeaf, Availability: AvailPlanned, Reason: reasonPlanned}
}

func category(id DestinationID, label string, order int) Destination {
	return Destination{ID: id, Label: label, Order: order, Kind: DestCategory}
}

func plannedChildren(parent DestinationID, labels ...string) []Destination {
	out := make([]Destination, len(labels))
	for i, l := range labels {
		out[i] = planned(DestinationID(string(parent)+"."+strings.ToLower(l)), parent, l, i)
	}
	return out
}

var registry = mustRegistry(buildDestinations())

func buildDestinations() []Destination {
	dashboard := leaf(DestDashboard, DestCluster, "Dashboard", 0, ScreenDashboard, FocusPods)
	dashboard.Shortcut = "2"
	nodes := leaf(DestNodes, DestCluster, "Nodes", 1, ScreenNodes, FocusPods)
	nodes.Shortcut = "4"
	events := leaf(DestEvents, DestCluster, "Events", 3, ScreenEvents, FocusPods)
	events.Shortcut = "6"
	workloads := category(DestWorkloads, "Workloads", 1)
	workloads.Shortcut, workloads.Default = "3", DestPods
	security := planned(DestSecurity, "", "Security", 8)
	security.Reserved = "5"
	radar := planned(DestRadar, "", "Radar", 9)
	radar.Reserved = "7"
	out := []Destination{
		category(DestCluster, "Cluster", 0),
		dashboard,
		nodes,
		planned(DestNamespaces, DestCluster, "Namespaces", 2),
		events,
		workloads,
		planned(DestWorkloadsOver, DestWorkloads, "Overview", 0),
		leaf(DestPods, DestWorkloads, "Pods", 1, ScreenWorkloads, FocusPods),
		leaf(DestDeployments, DestWorkloads, "Deployments", 2, ScreenWorkloads, FocusDeployments),
		planned(DestStatefulSets, DestWorkloads, "StatefulSets", 3),
		planned(DestDaemonSets, DestWorkloads, "DaemonSets", 4),
		planned(DestJobs, DestWorkloads, "Jobs", 5),
		planned(DestCronJobs, DestWorkloads, "CronJobs", 6),
		planned(DestReplicaSets, DestWorkloads, "ReplicaSets", 7),
		category(DestConfiguration, "Configuration", 2),
		category(DestNetwork, "Network", 3),
		category(DestStorage, "Storage", 4),
		category(DestAccessControl, "Access Control", 5),
		category(DestCustomResource, "Custom Resources", 6),
		category(DestHelm, "Helm", 7),
		security,
		radar,
	}
	out = append(out, plannedChildren(DestConfiguration, "ConfigMaps", "Secrets", "ResourceQuotas", "LimitRanges", "HPA", "PodDisruptionBudgets")...)
	out = append(out, plannedChildren(DestNetwork, "Services", "Endpoints", "EndpointSlices", "Ingresses", "IngressClasses", "NetworkPolicies")...)
	out = append(out, plannedChildren(DestStorage, "PersistentVolumeClaims", "PersistentVolumes", "StorageClasses")...)
	out = append(out, plannedChildren(DestAccessControl, "ServiceAccounts", "Roles", "RoleBindings", "ClusterRoles", "ClusterRoleBindings")...)
	out = append(out, plannedChildren(DestCustomResource, "Definitions")...)
	out = append(out, plannedChildren(DestHelm, "Releases")...)
	return out
}

type destinationRegistry struct {
	all      []Destination
	byID     map[DestinationID]int
	children map[DestinationID][]DestinationID
}

func mustRegistry(ds []Destination) destinationRegistry {
	r, err := newRegistry(ds)
	if err != nil {
		panic(err)
	}
	return r
}

func newRegistry(ds []Destination) (destinationRegistry, error) {
	r := destinationRegistry{all: ds, byID: map[DestinationID]int{}, children: map[DestinationID][]DestinationID{}}
	shortcuts := map[string]DestinationID{}
	for i, d := range ds {
		if d.ID == "" {
			return destinationRegistry{}, errors.New("destino sem ID")
		}
		if _, dup := r.byID[d.ID]; dup {
			return destinationRegistry{}, fmt.Errorf("destino %s duplicado", d.ID)
		}
		r.byID[d.ID] = i
		for _, k := range []string{d.Shortcut, d.Reserved} {
			if k == "" {
				continue
			}
			if other, dup := shortcuts[k]; dup {
				return destinationRegistry{}, fmt.Errorf("atalho %s repetido em %s e %s", k, other, d.ID)
			}
			shortcuts[k] = d.ID
		}
		if d.Reserved != "" && d.Availability != AvailPlanned {
			return destinationRegistry{}, fmt.Errorf("atalho reservado em %s que ja existe", d.ID)
		}
	}
	for _, d := range ds {
		if d.Parent != "" {
			p, ok := r.byID[d.Parent]
			if !ok {
				return destinationRegistry{}, fmt.Errorf("destino %s sem pai %s", d.ID, d.Parent)
			}
			if ds[p].Kind != DestCategory {
				return destinationRegistry{}, fmt.Errorf("pai %s de %s não é categoria", d.Parent, d.ID)
			}
		}
		switch {
		case d.Kind == DestCategory && d.Implemented:
			return destinationRegistry{}, fmt.Errorf("categoria %s com tela", d.ID)
		case d.Kind == DestLeaf && d.Availability == AvailReady && !d.Implemented:
			return destinationRegistry{}, fmt.Errorf("folha %s pronta sem tela", d.ID)
		case d.Kind == DestLeaf && d.Implemented && d.Availability == AvailPlanned:
			return destinationRegistry{}, fmt.Errorf("folha %s implementada marcada como futura", d.ID)
		case d.Availability == AvailPlanned && d.Shortcut != "":
			return destinationRegistry{}, fmt.Errorf("destino futuro %s com atalho", d.ID)
		}
		r.children[d.Parent] = append(r.children[d.Parent], d.ID)
	}
	for _, d := range ds {
		seen := map[DestinationID]bool{}
		for cur := d.ID; cur != ""; cur = ds[r.byID[cur]].Parent {
			if seen[cur] {
				return destinationRegistry{}, fmt.Errorf("ciclo em %s", d.ID)
			}
			seen[cur] = true
		}
		if d.Default != "" {
			def, ok := r.get(d.Default)
			if !ok || def.Parent != d.ID || !def.ready() {
				return destinationRegistry{}, fmt.Errorf("padrão inválido em %s", d.ID)
			}
		}
		if d.Kind == DestCategory && d.Shortcut != "" && d.Default == "" {
			return destinationRegistry{}, fmt.Errorf("categoria %s com atalho sem padrão", d.ID)
		}
	}
	for parent, ids := range r.children {
		slices.SortStableFunc(ids, func(a, b DestinationID) int {
			return ds[r.byID[a]].Order - ds[r.byID[b]].Order
		})
		r.children[parent] = ids
	}
	return r, nil
}

func (d Destination) ready() bool {
	return d.Kind == DestLeaf && d.Implemented && d.Availability == AvailReady
}

func (r destinationRegistry) get(id DestinationID) (Destination, bool) {
	i, ok := r.byID[id]
	if !ok {
		return Destination{}, false
	}
	return r.all[i], true
}

func (r destinationRegistry) Children(id DestinationID) []Destination {
	ids := r.children[id]
	out := make([]Destination, len(ids))
	for i, c := range ids {
		out[i], _ = r.get(c)
	}
	return out
}

func (r destinationRegistry) main() []Destination {
	var out []Destination
	for _, d := range r.all {
		if d.Shortcut != "" && (d.ready() || d.Default != "") {
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(a, b Destination) int {
		if a.Shortcut < b.Shortcut {
			return -1
		}
		if a.Shortcut > b.Shortcut {
			return 1
		}
		return 0
	})
	return out
}

func (r destinationRegistry) target(d Destination) (Destination, bool) {
	if d.ready() {
		return d, true
	}
	if d.Default != "" {
		return r.get(d.Default)
	}
	return Destination{}, false
}

func (r destinationRegistry) forScreen(id ScreenID) (Destination, bool) {
	for _, d := range r.main() {
		if t, ok := r.target(d); ok && t.Screen == id {
			return d, true
		}
	}
	return Destination{}, false
}

func (r destinationRegistry) shortcut(key string) (Destination, bool) {
	for _, d := range r.main() {
		if d.Shortcut == key {
			return d, true
		}
	}
	return Destination{}, false
}

func registeredScreens() []ScreenID {
	var out []ScreenID
	for _, d := range registry.main() {
		if t, ok := registry.target(d); ok {
			out = append(out, t.Screen)
		}
	}
	return out
}

func menuKey(id ScreenID) string {
	d, _ := registry.forScreen(id)
	return d.Shortcut
}
