package netpath

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
)

type SetState int

const (
	SetUnknown SetState = iota
	SetKnown
	SetDenied
	SetAbsent
)

type Set struct {
	Rows  []data.ResourceRow
	State SetState
}

type Graph struct {
	Services  Set
	Slices    Set
	Ingresses Set
	Classes   Set
	Policies  Set
	Pods      Set
	Nodes     Set
}

const cancelStride = 256

type objectKey struct {
	namespace, name string
}

func keyOf(r data.ResourceRow) objectKey {
	return objectKey{r.Ref.Namespace, r.Ref.Name}
}

type labelKey struct {
	namespace, key, value string
}

type indexedSet struct {
	state    SetState
	readable bool
	rows     map[objectKey]*data.ResourceRow
	order    []*data.ResourceRow
}

func compareRows(a, b *data.ResourceRow) int {
	return cmp.Or(cmp.Compare(a.Ref.Namespace, b.Ref.Namespace), cmp.Compare(a.Ref.Name, b.Ref.Name))
}

func (s indexedSet) authoritative() bool {
	return s.state == SetKnown && s.readable
}

func (s indexedSet) gap(label string) string {
	switch s.state {
	case SetDenied:
		return "sem permissão para ler " + label
	case SetAbsent:
		return label + " não é servido por este cluster"
	case SetUnknown:
		return label + ": leitura incompleta, estado desconhecido"
	}
	if !s.readable {
		return label + ": há objetos que não couberam na projeção"
	}
	return ""
}

func (s indexedSet) find(namespace, name string) *data.ResourceRow {
	return s.rows[objectKey{namespace, name}]
}

type index struct {
	ctx       context.Context
	services  indexedSet
	slices    indexedSet
	ingresses indexedSet
	classes   indexedSet
	policies  indexedSet
	pods      indexedSet
	nodes     indexedSet
	byService map[objectKey][]*data.SliceFacts
	byLabel   map[labelKey][]*data.PodFacts
	podCounts map[string]podCount
	defaults  []string
	nodeIPs   []nodeIP
	nodeTotal int
}

type nodeIP struct {
	name, ip string
}

func newIndex(ctx context.Context, g Graph) (*index, error) {
	x := &index{ctx: ctx, byService: map[objectKey][]*data.SliceFacts{}, byLabel: map[labelKey][]*data.PodFacts{}, podCounts: map[string]podCount{}}
	for _, part := range []struct {
		set  *indexedSet
		from Set
		each func(*data.ResourceRow)
	}{
		{&x.services, g.Services, nil},
		{&x.ingresses, g.Ingresses, nil},
		{&x.policies, g.Policies, nil},
		{&x.slices, g.Slices, x.addSlice},
		{&x.pods, g.Pods, x.addPod},
		{&x.classes, g.Classes, x.addClass},
		{&x.nodes, g.Nodes, x.addNode},
	} {
		*part.set = indexedSet{state: part.from.State, readable: true, rows: make(map[objectKey]*data.ResourceRow, len(part.from.Rows))}
		part.set.order = make([]*data.ResourceRow, len(part.from.Rows))
		for i := range part.from.Rows {
			part.set.order[i] = &part.from.Rows[i]
		}
		slices.SortFunc(part.set.order, compareRows)
		for i, row := range part.set.order {
			if i%cancelStride == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			part.set.rows[keyOf(*row)] = row
			if row.Network == nil || row.Network.Truncated {
				part.set.readable = false
			}
			if part.each != nil && row.Network != nil {
				part.each(row)
			}
		}
	}
	return x, ctx.Err()
}

func (x *index) addSlice(r *data.ResourceRow) {
	if s := r.Network.Slice; s != nil && s.Service != "" {
		key := objectKey{r.Ref.Namespace, s.Service}
		x.byService[key] = append(x.byService[key], s)
	}
}

func (x *index) addPod(r *data.ResourceRow) {
	if r.Network.Pod == nil {
		return
	}
	for _, l := range r.Network.Pod.Labels {
		key := labelKey{r.Ref.Namespace, l.Key, l.Value}
		x.byLabel[key] = append(x.byLabel[key], r.Network.Pod)
	}
}

func (x *index) addClass(r *data.ResourceRow) {
	if r.Network.Class != nil && r.Network.Class.Default {
		x.defaults = append(x.defaults, r.Ref.Name)
	}
}

func (x *index) addNode(r *data.ResourceRow) {
	n := r.Network.Node
	if n == nil || n.Ready == data.TriFalse {
		return
	}
	ips := n.External
	if len(ips) == 0 {
		ips = n.Internal
	}
	if len(ips) == 0 {
		return
	}
	x.nodeTotal++
	if len(x.nodeIPs) < maxNodes {
		x.nodeIPs = append(x.nodeIPs, nodeIP{r.Ref.Name, ips[0]})
	}
}

func (x *index) service(namespace, name string) (*data.ServiceFacts, data.Tri) {
	row := x.services.find(namespace, name)
	if row == nil {
		if x.services.state == SetKnown {
			return nil, data.TriFalse
		}
		return nil, data.TriUnknown
	}
	if row.Network == nil || row.Network.Service == nil || row.Network.Truncated {
		return nil, data.TriUnknown
	}
	return row.Network.Service, data.TriTrue
}

func selects(selector, labels data.Labels) bool {
	if len(selector) == 0 {
		return false
	}
	for _, want := range selector {
		if got, ok := labels.Get(want.Key); !ok || got != want.Value {
			return false
		}
	}
	return true
}

type podCount struct {
	matched, ready, draining, unclear int
	known                             bool
}

func (c *podCount) add(p *data.PodFacts) {
	c.matched++
	switch {
	case p.Ready == data.TriTrue && p.Terminating:
		c.draining++
	case p.Ready == data.TriTrue:
		c.ready++
	case p.Ready == data.TriUnknown && p.Phase != "Pending":
		c.unclear++
	}
}

func (x *index) candidates(namespace string, selector data.Labels) []*data.PodFacts {
	var best []*data.PodFacts
	for i, l := range selector {
		list := x.byLabel[labelKey{namespace, l.Key, l.Value}]
		if i == 0 || len(list) < len(best) {
			best = list
		}
	}
	return best
}

func selectorID(namespace string, selector data.Labels) string {
	var b strings.Builder
	b.WriteString(strconv.Quote(namespace))
	for _, l := range selector {
		b.WriteString(strconv.Quote(l.Key))
		b.WriteString(strconv.Quote(l.Value))
	}
	return b.String()
}

func (x *index) countPods(namespace string, selector data.Labels) podCount {
	id := selectorID(namespace, selector)
	if cached, ok := x.podCounts[id]; ok {
		return cached
	}
	out := podCount{known: x.pods.authoritative()}
	for i, p := range x.candidates(namespace, selector) {
		if i%cancelStride == 0 && x.ctx.Err() != nil {
			return podCount{}
		}
		if p.Phase != "Succeeded" && p.Phase != "Failed" && selects(selector, p.Labels) {
			out.add(p)
		}
	}
	x.podCounts[id] = out
	return out
}

const (
	familyIPv4 = "IPv4"
	familyIPv6 = "IPv6"
)

type familyCount struct {
	name   string
	served bool
	data.EndpointTally
}

type endpointCount struct {
	families []familyCount
	slices   int
	withPort bool
	diverges bool
	known    bool
}

func (c endpointCount) any(pick func(familyCount) int) bool {
	for _, f := range c.families {
		if f.served && pick(f) > 0 {
			return true
		}
	}
	return false
}

func (c endpointCount) ready() bool    { return c.any(func(f familyCount) int { return f.Ready }) }
func (c endpointCount) draining() bool { return c.any(func(f familyCount) int { return f.Draining }) }
func (c endpointCount) unclear() bool  { return c.any(func(f familyCount) int { return f.Unclear }) }
func (c endpointCount) total() bool    { return c.any(func(f familyCount) int { return f.Total }) }

func (c endpointCount) partial() bool {
	return c.any(func(f familyCount) int {
		if f.Total > 0 && f.Ready == 0 {
			return 1
		}
		return 0
	})
}

func (c endpointCount) family(name string) data.EndpointTally {
	for _, f := range c.families {
		if f.name == name {
			return f.EndpointTally
		}
	}
	return data.EndpointTally{}
}

func (c endpointCount) outside() string {
	if len(c.families) == 0 {
		return ""
	}
	parts := make([]string, len(c.families))
	for i, f := range c.families {
		parts[i] = f.text()
	}
	return "; " + strings.Join(parts, "; ")
}

func (f familyCount) text() string {
	parts := []string{fmt.Sprintf("%d de %d prontos", f.Ready, f.Total)}
	if f.Draining > 0 {
		parts = append(parts, fmt.Sprintf("%d em drenagem", f.Draining))
	}
	if f.Unclear > 0 {
		parts = append(parts, fmt.Sprintf("%d indeterminados", f.Unclear))
	}
	if f.Unavailable > 0 {
		parts = append(parts, fmt.Sprintf("%d indisponíveis", f.Unavailable))
	}
	out := f.name + ": " + strings.Join(parts, ", ")
	if !f.served {
		out += " (família fora do Service)"
	}
	return out
}

func (c endpointCount) text(single func(data.EndpointTally) string) string {
	if len(c.families) == 1 {
		return single(c.families[0].EndpointTally)
	}
	parts := make([]string, len(c.families))
	for i, f := range c.families {
		parts[i] = f.text()
	}
	return strings.Join(parts, "; ")
}

func protocolOf(p string) string {
	if p == "" {
		return "TCP"
	}
	return p
}

func targetNumber(svc *data.ServiceFacts, port *data.ServicePort) (int, bool) {
	if port == nil || len(svc.Selector) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(port.Target)
	return n, err == nil && n > 0
}

func familyOf(ip string) string {
	if strings.Contains(ip, ":") {
		return familyIPv6
	}
	return familyIPv4
}

func serves(svc *data.ServiceFacts, family string) bool {
	if family != familyIPv4 && family != familyIPv6 {
		return false
	}
	if svc.Headless() {
		return true
	}
	return slices.ContainsFunc(svc.ClusterIPs, func(ip string) bool { return familyOf(ip) == family })
}

func (x *index) endpoints(namespace, service string, svc *data.ServiceFacts, port *data.ServicePort) endpointCount {
	out := endpointCount{known: x.slices.authoritative()}
	want, numeric := targetNumber(svc, port)
	for _, s := range x.byService[objectKey{namespace, service}] {
		out.slices++
		if port != nil {
			match := matchPort(s, port)
			if match == nil {
				continue
			}
			if match.Port == 0 || numeric && match.Port != want {
				out.diverges = true
				continue
			}
		}
		out.withPort = true
		out.add(s, svc)
	}
	slices.SortFunc(out.families, func(a, b familyCount) int { return cmp.Compare(a.name, b.name) })
	served := out.total()
	for _, f := range out.families {
		if !f.served && (f.Ready+f.Draining+f.Unclear > 0 || !served && f.Total > 0) {
			out.diverges = true
		}
	}
	return out
}

func (c *endpointCount) add(s *data.SliceFacts, svc *data.ServiceFacts) {
	at := slices.IndexFunc(c.families, func(f familyCount) bool { return f.name == s.AddressType })
	if at < 0 {
		at = len(c.families)
		c.families = append(c.families, familyCount{name: s.AddressType, served: serves(svc, s.AddressType)})
	}
	c.families[at].Merge(s.Tally())
}

func matchPort(s *data.SliceFacts, port *data.ServicePort) *data.SlicePort {
	for i, p := range s.Ports {
		if p.Name == port.Name && protocolOf(p.Protocol) == protocolOf(port.Protocol) {
			return &s.Ports[i]
		}
	}
	return nil
}

func findPort(svc *data.ServiceFacts, backend data.IngressBackend) *data.ServicePort {
	var other *data.ServicePort
	for i, p := range svc.Ports {
		named := backend.PortName != "" && p.Name == backend.PortName
		numbered := backend.PortName == "" && backend.PortNumber > 0 && p.Port == backend.PortNumber
		if !named && !numbered {
			continue
		}
		if protocolOf(p.Protocol) == "TCP" {
			return &svc.Ports[i]
		}
		if other == nil {
			other = &svc.Ports[i]
		}
	}
	return other
}
