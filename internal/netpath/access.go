package netpath

import (
	"cmp"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/weburl"
)

const (
	maxAddresses = 64
	maxRoutes    = 32
	maxNodes     = 2
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
	StatePending
	StateNoBackend
	StateUnknown
)

func (s State) String() string {
	return [...]string{"pronta", "pendente", "sem backend", "desconhecida"}[s]
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
	Labels      data.Labels
	LabelsKnown bool
}

func Access(g Graph, t Target) Report {
	b := builder{g: g}
	switch t.Kind {
	case TargetIngress:
		b.ingressTarget(t)
	case TargetService:
		b.serviceTarget(t)
	default:
		b.workloadTarget(t)
	}
	slices.SortStableFunc(b.out.Addresses, func(x, y Address) int {
		return cmp.Or(cmp.Compare(x.Origin, y.Origin), cmp.Compare(x.State, y.State))
	})
	return b.out
}

type builder struct {
	g   Graph
	out Report
}

func (b *builder) note(text string) {
	if text != "" && !slices.Contains(b.out.Notes, text) {
		b.out.Notes = append(b.out.Notes, text)
	}
}

func (b *builder) address(a Address) {
	if len(b.out.Addresses) == maxAddresses {
		b.out.Truncated = true
		return
	}
	b.out.Addresses = append(b.out.Addresses, a)
}

func (b *builder) route(r Route) {
	if len(b.out.Routes) == maxRoutes {
		b.out.Truncated = true
		return
	}
	b.out.Routes = append(b.out.Routes, r)
}

func (b *builder) ingressTarget(t Target) {
	row := b.g.Ingresses.find(t.Namespace, t.Name)
	if row == nil || row.Network == nil || row.Network.Ingress == nil {
		b.note(cmp.Or(b.g.Ingresses.gap("Ingresses"), "o Ingress não está mais na lista"))
		return
	}
	b.ingress(*row, "")
	b.gaps()
}

func (b *builder) gaps() {
	b.note(b.g.Services.gap("Services"))
	b.note(b.g.Slices.gap("EndpointSlices"))
}

func (b *builder) serviceTarget(t Target) {
	svc, found := b.g.service(t.Namespace, t.Name)
	if found != data.TriTrue {
		b.note(cmp.Or(b.g.Services.gap("Services"), "o Service não está mais na lista"))
		return
	}
	b.service(t.Namespace, t.Name, svc)
	b.ingressesFor(t.Namespace, []string{t.Name})
	b.gaps()
	b.note(b.g.Ingresses.gap("Ingresses"))
}

func (b *builder) workloadTarget(t Target) {
	if t.Kind == TargetPod && !t.LabelsKnown {
		if row := b.g.Pods.find(t.Namespace, t.Name); row != nil && row.Network != nil && row.Network.Pod != nil && !row.Network.Truncated {
			t.Labels, t.LabelsKnown = row.Network.Pod.Labels, true
		}
	}
	b.out.Policies = b.policies(t)
	if !t.LabelsKnown {
		b.note("labels desconhecidas: não dá para saber quais Services selecionam este alvo")
		return
	}
	var names []string
	for _, r := range b.g.Services.Rows {
		if r.Ref.Namespace != t.Namespace || r.Network == nil || r.Network.Service == nil || r.Network.Truncated {
			continue
		}
		if selects(r.Network.Service.Selector, t.Labels) {
			names = append(names, r.Ref.Name)
			b.service(t.Namespace, r.Ref.Name, r.Network.Service)
		}
	}
	if len(names) == 0 && b.g.Services.authoritative() {
		b.note("nenhum Service da lista lida seleciona este alvo")
	}
	b.ingressesFor(t.Namespace, names)
	b.gaps()
	b.note(b.g.Ingresses.gap("Ingresses"))
}

func (b *builder) ingressesFor(namespace string, services []string) {
	for _, r := range b.g.Ingresses.Rows {
		if r.Ref.Namespace != namespace || r.Network == nil || r.Network.Ingress == nil {
			continue
		}
		for _, name := range services {
			b.ingress(r, name)
		}
	}
}

func (b *builder) ingress(row data.ResourceRow, only string) {
	ing := row.Network.Ingress
	if row.Network.Truncated {
		b.note(source(row) + " é grande demais: só parte das regras foi lida")
	}
	emit := func(host, path string, backend data.IngressBackend) {
		if only != "" && backend.Service != only {
			return
		}
		route, result := ingressRoute(b.g, row.Ref.Namespace, row.Ref.Name, ing, host, path, backend)
		b.route(route)
		b.address(ingressAddress(row, ing, host, path, result))
	}
	for _, rule := range ing.Rules {
		for _, p := range rule.Paths {
			emit(rule.Host, cmp.Or(p.Path, "/"), p.Backend)
		}
		if len(rule.Paths) > 0 {
			continue
		}
		switch {
		case ing.Default != nil:
			emit(rule.Host, "/", *ing.Default)
		case only == "":
			text := "regra sem path e sem backend padrão"
			b.address(Address{Origin: OriginIngress, Source: source(row), Display: shownHost(rule.Host), Reach: ReachUnknown, State: StateNoBackend, Reason: text, Refused: text})
		}
	}
	if ing.Default != nil {
		emit("", "/", *ing.Default)
	}
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

func (b *builder) service(namespace, name string, svc *data.ServiceFacts) {
	route, _ := serviceRoute(b.g, namespace, name)
	b.route(route)
	src := "Service " + namespace + "/" + name
	if svc.Type == "ExternalName" {
		b.note(src + " declara ExternalName " + svc.ExternalName + ": alias DNS, destino não verificado")
		return
	}
	for i, p := range svc.Ports {
		result := backendResult{}
		result.serve(b.g, namespace, name, svc, &svc.Ports[i])
		base := Address{Source: src, State: result.state, Reason: result.reason}
		for _, l := range svc.LoadBalancer {
			a := base
			a.Origin = OriginLoadBalancer
			b.port(a, cmp.Or(l.Hostname, l.IP), p.Port, p)
		}
		if svc.Type == "LoadBalancer" && len(svc.LoadBalancer) == 0 {
			b.address(Address{Origin: OriginLoadBalancer, Source: src, Display: "(sem endereço) " + portText(p), Reach: ReachUnknown, State: StatePending, Reason: "LoadBalancer pendente: nenhum endereço publicado em status", Refused: "sem endereço publicado"})
		}
		for _, ip := range svc.ExternalIPs {
			a := base
			a.Origin = OriginExternalIP
			b.port(a, ip, p.Port, p)
		}
		if p.NodePort > 0 && (svc.Type == "NodePort" || svc.Type == "LoadBalancer" && len(svc.LoadBalancer) == 0) {
			b.nodePort(base, p)
		}
		a := base
		a.Origin, a.Reach = OriginClusterIP, ReachCluster
		if svc.Headless() {
			a.Display, a.Refused = name+"."+namespace+".svc headless "+portText(p), "Service headless não tem ClusterIP"
			b.address(a)
			continue
		}
		b.port(a, svc.ClusterIPs[0], p.Port, p)
	}
	if len(svc.Ports) == 0 {
		b.note(src + " não declara portas")
	}
}

func (b *builder) port(a Address, host string, port int, p data.ServicePort) {
	cluster := a.Reach == ReachCluster
	scheme, why := schemeOf(p)
	if scheme == "" {
		a.Refused = "porta sem esquema http ou https conhecido"
		a.Display = fmt.Sprintf("%s %d/%s", host, port, cmp.Or(p.Protocol, "TCP"))
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
	b.address(a)
}

func hostReach(host string) Reach {
	u, err := weburl.New("http", host, 80, "/")
	if err != nil {
		return ReachUnknown
	}
	return [...]Reach{ReachDeclared, ReachPrivate, ReachCluster}[u.Reach()]
}

func (b *builder) nodePort(base Address, p data.ServicePort) {
	base.Origin = OriginNodePort
	shown, total := 0, 0
	for _, r := range b.g.Nodes.Rows {
		if r.Network == nil || r.Network.Node == nil || r.Network.Node.Ready == data.TriFalse {
			continue
		}
		n := r.Network.Node
		ips := n.External
		if len(ips) == 0 {
			ips = n.Internal
		}
		if len(ips) == 0 {
			continue
		}
		total++
		if shown == maxNodes {
			continue
		}
		shown++
		a := base
		a.Source += ", node " + r.Ref.Name
		b.port(a, ips[0], p.NodePort, p)
	}
	if total > shown {
		b.note(fmt.Sprintf("NodePort %d, outros nodes não listados: %d; porta alocada no node, alcance não verificado", p.NodePort, total-shown))
	}
	if total == 0 {
		a := base
		a.Display, a.Reach = fmt.Sprintf("(node não identificado):%d", p.NodePort), ReachNodePort
		a.Refused = cmp.Or(b.g.Nodes.gap("Nodes"), "nenhum node pronto com IP na lista lida")
		b.address(a)
	}
}

const policyCaveat = "o efeito real depende do CNI e não foi avaliado"

func (b *builder) policies(t Target) PolicyReport {
	out := PolicyReport{Note: policyCaveat}
	if gap := b.g.Policies.gap("NetworkPolicies"); gap != "" {
		out.Note = gap
	}
	if !t.LabelsKnown {
		out.Note = "labels desconhecidas"
		return out
	}
	out.Known = b.g.Policies.authoritative()
	for _, r := range b.g.Policies.Rows {
		if r.Ref.Namespace != t.Namespace {
			continue
		}
		switch policySelects(r.Network, t.Labels) {
		case data.TriTrue:
			out.Selecting = append(out.Selecting, r.Ref.Name)
		case data.TriUnknown:
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
