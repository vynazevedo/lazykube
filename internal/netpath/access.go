package netpath

import (
	"cmp"
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/weburl"
)

const (
	MaxAddresses = 64
	MaxRoutes    = 32
	maxNodes     = 2
	maxNotes     = 32
	maxPolicies  = 32
)

type Origin int

const (
	OriginIngress Origin = iota
	OriginLoadBalancer
	OriginExternalIP
	OriginNodePort
	OriginClusterIP
)

func (o Origin) String() string {
	return [...]string{"Ingress", "LoadBalancer", "ExternalIP", "NodePort", "ClusterIP"}[o]
}

type State int

const (
	StateReady State = iota
	StateDraining
	StatePending
	StateNoBackend
	StateUnknown
)

func (s State) String() string {
	return [...]string{"pronta", "em drenagem", "pendente", "sem backend", "desconhecida"}[s]
}

type Reach int

const (
	ReachDeclared Reach = iota
	ReachPrivate
	ReachCluster
	ReachUnknown
	ReachNodePort
)

func (r Reach) String() string {
	return [...]string{"declarada, alcance não verificado", "provável rede privada, alcance não verificado", "endereço interno do cluster", "alcance desconhecido", "porta alocada no node, alcance não verificado"}[r]
}

type Address struct {
	Origin  Origin
	Source  string
	Display string
	URL     weburl.URL
	Reach   Reach
	State   State
	Reason  string
	Scheme  string
	Refused string
}

type PolicyReport struct {
	Selecting []string
	Uncertain []string
	Note      string
	Known     bool
	Truncated bool
}

type Report struct {
	Addresses []Address
	Routes    []Route
	Policies  PolicyReport
	Notes     []string
	Truncated bool
}

type TargetKind int

const (
	TargetPod TargetKind = iota
	TargetWorkload
	TargetService
	TargetIngress
)

type Target struct {
	Kind        TargetKind
	Namespace   string
	Name        string
	UID         string
	Labels      data.Labels
	LabelsKnown bool
}

func (t Target) is(row *data.ResourceRow) bool {
	return row != nil && (t.UID == "" || row.Ref.UID == t.UID)
}

func Access(g Graph, t Target) Report {
	out, _ := AccessContext(context.Background(), g, t)
	return out
}

func AccessContext(ctx context.Context, g Graph, t Target) (Report, error) {
	x, err := newIndex(ctx, g)
	if err != nil {
		return Report{}, err
	}
	b := builder{x: x, g: g, notes: map[string]bool{}}
	switch t.Kind {
	case TargetIngress:
		b.ingressTarget(t)
	case TargetService:
		b.serviceTarget(t)
	default:
		b.workloadTarget(t)
	}
	if ctx.Err() != nil {
		return Report{}, ctx.Err()
	}
	slices.SortStableFunc(b.out.Addresses, func(x, y Address) int {
		return cmp.Or(cmp.Compare(x.Origin, y.Origin), cmp.Compare(x.State, y.State))
	})
	return b.out, nil
}

type builder struct {
	x     *index
	g     Graph
	out   Report
	notes map[string]bool
}

func (b *builder) canceled() bool {
	return b.x.ctx.Err() != nil
}

func (b *builder) full() bool {
	return len(b.out.Addresses) >= MaxAddresses
}

func (b *builder) note(text string) {
	if text == "" || b.notes[text] {
		return
	}
	if len(b.out.Notes) >= maxNotes {
		b.out.Truncated = true
		return
	}
	b.notes[text] = true
	b.out.Notes = append(b.out.Notes, text)
}

func (b *builder) address(a Address) bool {
	if b.full() {
		b.out.Truncated = true
		return false
	}
	b.out.Addresses = append(b.out.Addresses, a)
	return true
}

func (b *builder) wantsRoute() bool {
	if len(b.out.Routes) >= MaxRoutes {
		b.out.Truncated = true
		return false
	}
	return true
}

type ingressPath struct {
	row     *data.ResourceRow
	host    string
	path    string
	backend data.IngressBackend
	dead    bool
}

func (b *builder) paths(row *data.ResourceRow, only map[string]bool, limit int) []ingressPath {
	ing := row.Network.Ingress
	var out []ingressPath
	add := func(p ingressPath) bool {
		if only != nil && (p.dead || !only[p.backend.Service]) {
			return true
		}
		if len(out) == limit {
			return false
		}
		p.row = row
		out = append(out, p)
		return true
	}
	for _, rule := range ing.Rules {
		for _, p := range rule.Paths {
			if !add(ingressPath{host: rule.Host, path: cmp.Or(p.Path, "/"), backend: p.Backend}) {
				return out
			}
		}
		if len(rule.Paths) > 0 {
			continue
		}
		next := ingressPath{host: rule.Host, path: "/", dead: true}
		if ing.Default != nil {
			next.backend, next.dead = *ing.Default, false
		}
		if !add(next) {
			return out
		}
	}
	if ing.Default != nil {
		add(ingressPath{path: "/", backend: *ing.Default})
	}
	return out
}

func (b *builder) ingressTarget(t Target) {
	row := b.x.ingresses.find(t.Namespace, t.Name)
	if !t.is(row) || row.Network == nil || row.Network.Ingress == nil {
		b.note(cmp.Or(b.x.ingresses.gap("Ingresses"), "o Ingress não está mais na lista"))
		return
	}
	b.oversized(row)
	b.evaluate(t.Namespace, b.paths(row, nil, MaxAddresses+1), nil)
	b.gaps()
}

func (b *builder) oversized(row *data.ResourceRow) {
	if row.Network.Truncated {
		b.note(source(*row) + " é grande demais: só parte das regras foi lida")
	}
}

func (b *builder) gaps() {
	b.note(b.x.services.gap("Services"))
	b.note(b.x.slices.gap("EndpointSlices"))
}

func (b *builder) serviceTarget(t Target) {
	_, found := b.x.service(t.Namespace, t.Name)
	if found != data.TriTrue || !t.is(b.x.services.find(t.Namespace, t.Name)) {
		b.note(cmp.Or(b.x.services.gap("Services"), "o Service não está mais na lista"))
		return
	}
	b.evaluate(t.Namespace, b.pathsTo(t.Namespace, map[string]bool{t.Name: true}), []string{t.Name})
	b.gaps()
	b.note(b.x.ingresses.gap("Ingresses"))
}

func (b *builder) workloadTarget(t Target) {
	if t.Kind == TargetPod && !t.LabelsKnown {
		if row := b.x.pods.find(t.Namespace, t.Name); t.is(row) && row.Network != nil && row.Network.Pod != nil && !row.Network.Truncated {
			t.Labels, t.LabelsKnown = row.Network.Pod.Labels, true
		}
	}
	b.out.Policies = b.policies(t)
	if !t.LabelsKnown {
		b.note("labels desconhecidas: não dá para saber quais Services selecionam este alvo")
		return
	}
	var names []string
	selecting := map[string]bool{}
	for i, r := range b.g.Services.Rows {
		if i%cancelStride == 0 && b.canceled() {
			return
		}
		if r.Ref.Namespace != t.Namespace || r.Network == nil || r.Network.Service == nil || r.Network.Truncated {
			continue
		}
		if selects(r.Network.Service.Selector, t.Labels) {
			selecting[r.Ref.Name] = true
			if len(names) <= MaxAddresses {
				names = append(names, r.Ref.Name)
			}
		}
	}
	if len(selecting) == 0 && b.x.services.authoritative() {
		b.note("nenhum Service da lista lida seleciona este alvo")
	}
	if len(selecting) > len(names) {
		b.out.Truncated = true
	}
	b.evaluate(t.Namespace, b.pathsTo(t.Namespace, selecting), names)
	b.gaps()
	b.note(b.x.ingresses.gap("Ingresses"))
}

func (b *builder) pathsTo(namespace string, services map[string]bool) []ingressPath {
	var out []ingressPath
	if len(services) == 0 {
		return nil
	}
	for i := range b.g.Ingresses.Rows {
		if i%cancelStride == 0 && b.canceled() {
			return nil
		}
		row := &b.g.Ingresses.Rows[i]
		if row.Ref.Namespace != namespace || row.Network == nil || row.Network.Ingress == nil {
			continue
		}
		found := b.paths(row, services, MaxAddresses+1-len(out))
		if len(found) > 0 {
			b.oversized(row)
		}
		if out = append(out, found...); len(out) > MaxAddresses {
			break
		}
	}
	return out
}

func (b *builder) evaluate(namespace string, paths []ingressPath, services []string) {
	ingresses := len(paths)
	if len(services) > 0 {
		ingresses = min(ingresses, max(MaxAddresses/2, MaxAddresses-len(services)))
	}
	for _, p := range paths[:ingresses] {
		if b.canceled() || !b.evaluateIngress(p) {
			return
		}
	}
	for _, name := range services {
		if b.canceled() || !b.evaluateService(namespace, name) {
			break
		}
	}
	if ingresses < len(paths) {
		b.out.Truncated = true
	}
}

func (b *builder) evaluateIngress(p ingressPath) bool {
	ing := p.row.Network.Ingress
	if p.dead {
		text := "regra sem path e sem backend padrão"
		return b.address(Address{Origin: OriginIngress, Source: source(*p.row), Display: shownHost(p.host), Reach: ReachUnknown, State: StateNoBackend, Reason: text, Refused: text})
	}
	if b.full() {
		b.out.Truncated = true
		return false
	}
	result := checkIngressBackend(b.x, p.row.Ref.Namespace, p.backend)
	if b.wantsRoute() {
		b.out.Routes = append(b.out.Routes, ingressRoute(b.x, p.row.Ref.Namespace, p.row.Ref.Name, ing, p.host, p.path, p.backend, result))
	}
	return b.address(ingressAddress(*p.row, ing, p.host, p.path, result))
}

func source(row data.ResourceRow) string {
	return "Ingress " + row.Ref.Namespace + "/" + row.Ref.Name
}

func ingressAddress(row data.ResourceRow, ing *data.IngressFacts, host, path string, result backendResult) Address {
	scheme, port, why := "http", 80, "sem TLS declarado"
	if _, ok := tlsFor(ing, host); ok {
		scheme, port, why = "https", 443, "TLS declarado"
	}
	a := Address{Origin: OriginIngress, Source: source(row), Scheme: why, State: result.state, Reason: result.reason}
	a.fill(scheme, host, port, path)
	if host == "" {
		a.Display = shownHost(host) + path
	}
	if a.Reach == ReachDeclared && privateOnly(ing.Addresses) {
		a.Reach = ReachPrivate
	}
	if a.State == StateReady && len(ing.Addresses) == 0 {
		a.State, a.Reason = StatePending, "nenhum endereço publicado em status do Ingress"
	}
	return a
}

func (a *Address) fill(scheme, host string, port int, path string) {
	u, err := weburl.New(scheme, host, port, path)
	if err != nil {
		a.Refused = err.Error()
		a.Display = host + path
		a.Reach = ReachUnknown
		return
	}
	a.URL, a.Display = u, u.String()
	switch u.Reach() {
	case weburl.ReachPrivate:
		a.Reach = ReachPrivate
	case weburl.ReachCluster:
		a.Reach = ReachCluster
	default:
		a.Reach = ReachDeclared
	}
}

func privateOnly(list []data.LoadBalancerAddress) bool {
	if len(list) == 0 {
		return false
	}
	for _, l := range list {
		addr, err := netip.ParseAddr(l.IP)
		if err != nil || !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() {
			return false
		}
	}
	return true
}

func schemeOf(p data.ServicePort) (string, string) {
	if p.Protocol != "" && p.Protocol != "TCP" {
		return "", ""
	}
	switch strings.ToLower(p.AppProtocol) {
	case "http", "kubernetes.io/h2c", "kubernetes.io/ws":
		return "http", "appProtocol"
	case "https", "kubernetes.io/wss":
		return "https", "appProtocol"
	case "":
	default:
		return "", ""
	}
	for _, scheme := range []string{"https", "http"} {
		if p.Name == scheme || strings.HasPrefix(p.Name, scheme+"-") {
			return scheme, "nome da porta"
		}
	}
	switch p.Port {
	case 80:
		return "http", "presumido pela porta 80"
	case 443:
		return "https", "presumido pela porta 443"
	}
	return "", ""
}

func (b *builder) evaluateService(namespace, name string) bool {
	if b.full() {
		b.out.Truncated = true
		return false
	}
	svc, found := b.x.service(namespace, name)
	if found != data.TriTrue {
		return true
	}
	if b.wantsRoute() {
		b.out.Routes = append(b.out.Routes, serviceRoute(b.x, namespace, name))
	}
	src := "Service " + namespace + "/" + name
	if svc.Type == "ExternalName" {
		b.note(src + " declara ExternalName " + svc.ExternalName + ": alias DNS, destino não verificado")
		return true
	}
	if len(svc.Ports) == 0 {
		b.note(src + " não declara portas")
	}
	for i, p := range svc.Ports {
		if b.canceled() || !b.servicePort(namespace, name, src, svc, &svc.Ports[i], p) {
			return false
		}
	}
	return true
}

func (b *builder) servicePort(namespace, name, src string, svc *data.ServiceFacts, port *data.ServicePort, p data.ServicePort) bool {
	result := backendResult{}
	result.serve(b.x, namespace, name, svc, port)
	base := Address{Source: src, State: result.state, Reason: result.reason}
	for _, l := range svc.LoadBalancer {
		a := base
		a.Origin = OriginLoadBalancer
		if !b.port(a, cmp.Or(l.Hostname, l.IP), p.Port, p) {
			return false
		}
	}
	if svc.Type == "LoadBalancer" && len(svc.LoadBalancer) == 0 {
		if !b.address(Address{Origin: OriginLoadBalancer, Source: src, Display: "(sem endereço) " + portText(p), Reach: ReachUnknown, State: StatePending, Reason: "LoadBalancer pendente: nenhum endereço publicado em status", Refused: "sem endereço publicado"}) {
			return false
		}
	}
	for _, ip := range svc.ExternalIPs {
		a := base
		a.Origin = OriginExternalIP
		if !b.port(a, ip, p.Port, p) {
			return false
		}
	}
	if p.NodePort > 0 && (svc.Type == "NodePort" || svc.Type == "LoadBalancer" && len(svc.LoadBalancer) == 0) && !b.nodePort(base, p) {
		return false
	}
	a := base
	a.Origin, a.Reach = OriginClusterIP, ReachCluster
	if svc.Headless() {
		a.Display, a.Refused = name+"."+namespace+".svc headless "+portText(p), "Service headless não tem ClusterIP"
		return b.address(a)
	}
	return b.port(a, svc.ClusterIPs[0], p.Port, p)
}

func (b *builder) port(a Address, host string, port int, p data.ServicePort) bool {
	cluster := a.Reach == ReachCluster
	scheme, why := schemeOf(p)
	if scheme == "" {
		a.Refused = "porta sem esquema http ou https conhecido"
		a.Display = fmt.Sprintf("%s %d/%s", host, port, protocolOf(p.Protocol))
		a.Reach = hostReach(host)
	} else {
		a.Scheme = why
		a.fill(scheme, host, port, "/")
		if a.URL.IsZero() {
			a.Display = fmt.Sprintf("%s %d/TCP", host, port)
		}
	}
	if a.Origin == OriginNodePort {
		a.Reach = ReachNodePort
	}
	if cluster {
		a.Reach, a.URL = ReachCluster, a.URL.WithinCluster()
	}
	return b.address(a)
}

func hostReach(host string) Reach {
	u, err := weburl.New("http", host, 80, "/")
	if err != nil {
		return ReachUnknown
	}
	return [...]Reach{ReachDeclared, ReachPrivate, ReachCluster}[u.Reach()]
}

func (b *builder) nodePort(base Address, p data.ServicePort) bool {
	base.Origin = OriginNodePort
	for _, n := range b.x.nodeIPs {
		a := base
		a.Source += ", node " + n.name
		if !b.port(a, n.ip, p.NodePort, p) {
			return false
		}
	}
	if hidden := b.x.nodeTotal - len(b.x.nodeIPs); hidden > 0 {
		b.note(fmt.Sprintf("NodePort %d, outros nodes não listados: %d; porta alocada no node, alcance não verificado", p.NodePort, hidden))
	}
	if b.x.nodeTotal > 0 {
		return true
	}
	a := base
	a.Display, a.Reach = fmt.Sprintf("(node não identificado):%d", p.NodePort), ReachNodePort
	a.Refused = cmp.Or(b.x.nodes.gap("Nodes"), "nenhum node pronto com IP na lista lida")
	return b.address(a)
}

const policyCaveat = "o efeito real depende do CNI e não foi avaliado"

func (b *builder) policies(t Target) PolicyReport {
	out := PolicyReport{Note: policyCaveat}
	if gap := b.x.policies.gap("NetworkPolicies"); gap != "" {
		out.Note = gap
	}
	if !t.LabelsKnown {
		out.Note = "labels desconhecidas"
		return out
	}
	out.Known = b.x.policies.authoritative()
	for i, r := range b.g.Policies.Rows {
		if i%cancelStride == 0 && b.canceled() {
			return PolicyReport{}
		}
		if r.Ref.Namespace != t.Namespace {
			continue
		}
		switch policySelects(r.Network, t.Labels) {
		case data.TriTrue:
			if len(out.Selecting) == maxPolicies {
				out.Truncated = true
				continue
			}
			out.Selecting = append(out.Selecting, r.Ref.Name)
		case data.TriUnknown:
			if len(out.Uncertain) == maxPolicies {
				out.Truncated = true
				continue
			}
			out.Uncertain = append(out.Uncertain, r.Ref.Name)
		}
	}
	return out
}

func policySelects(f *data.NetworkFacts, labels data.Labels) data.Tri {
	if f == nil || f.Policy == nil || f.Truncated {
		return data.TriUnknown
	}
	for _, want := range f.Policy.MatchLabels {
		if got, ok := labels.Get(want.Key); !ok || got != want.Value {
			return data.TriFalse
		}
	}
	out := data.TriTrue
	for _, e := range f.Policy.Expressions {
		got, ok := labels.Get(e.Key)
		switch e.Operator {
		case "In":
			if !ok || !slices.Contains(e.Values, got) {
				return data.TriFalse
			}
		case "NotIn":
			if ok && slices.Contains(e.Values, got) {
				return data.TriFalse
			}
		case "Exists":
			if !ok {
				return data.TriFalse
			}
		case "DoesNotExist":
			if ok {
				return data.TriFalse
			}
		default:
			out = data.TriUnknown
		}
	}
	return out
}
