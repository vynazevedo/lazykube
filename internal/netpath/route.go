package netpath

import (
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
)

type StepKind string

const (
	StepClass     StepKind = "IngressClass"
	StepTLS       StepKind = "TLS"
	StepService   StepKind = "Service"
	StepPort      StepKind = "Porta"
	StepPods      StepKind = "Pods"
	StepEndpoints StepKind = "Endpoints"
	StepAddress   StepKind = "Endereço"
)

type Status int

const (
	StatusOK Status = iota
	StatusUnknown
	StatusAttention
	StatusBroken
)

type Step struct {
	Kind     StepKind
	Status   Status
	Evidence string
}

type Route struct {
	Title string
	Steps []Step
}

func (r Route) First() (Step, bool) {
	for _, want := range []Status{StatusBroken, StatusAttention, StatusUnknown} {
		for _, s := range r.Steps {
			if s.Status == want {
				return s, true
			}
		}
	}
	return Step{}, false
}

func (r Route) Verdict() string {
	s, ok := r.First()
	switch {
	case !ok:
		return "nenhum elo quebrado nos objetos lidos; o tráfego não foi testado"
	case s.Status == StatusBroken:
		return "primeiro elo quebrado: " + string(s.Kind) + ", " + s.Evidence
	case s.Status == StatusAttention:
		return "atenção: " + string(s.Kind) + ", " + s.Evidence
	}
	return "indeterminado: " + string(s.Kind) + ", " + s.Evidence
}

func classStep(g Graph, ing *data.IngressFacts) Step {
	step := func(st Status, text string) Step { return Step{StepClass, st, text} }
	if ing.Class != "" {
		if g.Classes.find("", ing.Class) != nil {
			return step(StatusOK, ing.Class)
		}
		if g.Classes.State == SetKnown {
			return step(StatusBroken, "a IngressClass "+ing.Class+" não existe na lista lida")
		}
		return step(StatusUnknown, g.Classes.gap("IngressClasses"))
	}
	if ing.ClassAnnotation != "" {
		return step(StatusUnknown, "classe legada por anotação: "+ing.ClassAnnotation)
	}
	if !g.Classes.authoritative() {
		return step(StatusUnknown, g.Classes.gap("IngressClasses"))
	}
	var defaults []string
	for _, r := range g.Classes.Rows {
		if r.Network.Class != nil && r.Network.Class.Default {
			defaults = append(defaults, r.Ref.Name)
		}
	}
	switch len(defaults) {
	case 0:
		return step(StatusAttention, "sem ingressClassName e sem IngressClass padrão")
	case 1:
		return step(StatusOK, "padrão "+defaults[0])
	}
	return step(StatusAttention, fmt.Sprintf("%d IngressClasses marcadas como padrão", len(defaults)))
}

func tlsFor(ing *data.IngressFacts, host string) (data.IngressTLS, bool) {
	for _, t := range ing.TLS {
		if len(t.Hosts) == 0 {
			return t, true
		}
		for _, h := range t.Hosts {
			if h == host || wildcardCovers(h, host) {
				return t, true
			}
		}
	}
	return data.IngressTLS{}, false
}

func wildcardCovers(pattern, host string) bool {
	suffix, ok := strings.CutPrefix(pattern, "*.")
	if !ok {
		return false
	}
	label, rest, found := strings.Cut(host, ".")
	return found && label != "" && label != "*" && rest == suffix
}

func tlsStep(ing *data.IngressFacts, host string) Step {
	t, ok := tlsFor(ing, host)
	switch {
	case !ok:
		return Step{StepTLS, StatusOK, "sem TLS declarado para este host"}
	case t.Secret == "":
		return Step{StepTLS, StatusAttention, "TLS sem secretName; certificado não identificado"}
	}
	return Step{StepTLS, StatusOK, "secretName " + t.Secret + " (conteúdo não lido)"}
}

type backendResult struct {
	state  State
	reason string
	steps  []Step
	port   *data.ServicePort
}

func (b *backendResult) add(kind StepKind, st Status, text string) {
	b.steps = append(b.steps, Step{kind, st, text})
}

func (b *backendResult) settle(state State, reason string) {
	if b.reason == "" {
		b.state, b.reason = state, reason
	}
}

func checkIngressBackend(g Graph, namespace string, backend data.IngressBackend) backendResult {
	var out backendResult
	if backend.Service == "" {
		text := "backend é o recurso " + backend.Resource + ", fora desta análise"
		if backend.Resource == "" {
			text = "backend sem Service e sem recurso"
			out.add(StepService, StatusBroken, text)
			out.settle(StateNoBackend, text)
			return out
		}
		out.add(StepService, StatusUnknown, text)
		out.settle(StateUnknown, text)
		return out
	}
	svc, found := g.service(namespace, backend.Service)
	if !out.service(g, namespace, backend.Service, found) {
		return out
	}
	if svc.Type == "ExternalName" {
		text := "Service ExternalName: alias DNS, destino não verificado"
		out.add(StepPort, StatusUnknown, text)
		out.settle(StateUnknown, text)
		return out
	}
	out.port = findPort(svc, backend)
	if out.port == nil {
		text := "o backend não informa porta"
		if backend.Port() != "" {
			text = "a porta " + backend.Port() + " não existe no Service " + backend.Service
		}
		out.add(StepPort, StatusBroken, text)
		out.settle(StateNoBackend, text)
	} else {
		out.add(StepPort, StatusOK, portText(*out.port))
	}
	out.serve(g, namespace, backend.Service, svc, out.port)
	return out
}

func (b *backendResult) service(g Graph, namespace, name string, found data.Tri) bool {
	switch found {
	case data.TriTrue:
		b.add(StepService, StatusOK, namespace+"/"+name)
		return true
	case data.TriFalse:
		text := "o Service " + namespace + "/" + name + " não existe na lista lida"
		b.add(StepService, StatusBroken, text)
		b.settle(StateNoBackend, text)
	default:
		text := g.Services.gap("Services")
		b.add(StepService, StatusUnknown, text)
		b.settle(StateUnknown, text)
	}
	return false
}

func (b *backendResult) serve(g Graph, namespace, name string, svc *data.ServiceFacts, port *data.ServicePort) {
	eps := g.endpoints(namespace, name, port)
	pods := podCount{}
	if len(svc.Selector) == 0 {
		b.add(StepPods, StatusOK, "Service sem selector; nenhum pod a conferir")
	} else {
		pods = g.pods(namespace, svc.Selector)
		b.podStep(pods, svc, g, eps.ready > 0)
	}
	switch {
	case eps.ready > 0:
		b.add(StepEndpoints, StatusOK, fmt.Sprintf("%d de %d prontos", eps.ready, eps.total))
		b.settle(StateReady, "")
	case !eps.known:
		text := g.Slices.gap("EndpointSlices")
		b.add(StepEndpoints, StatusUnknown, text)
		b.settle(StateUnknown, text)
	case eps.slices > 0 && !eps.withPort:
		text := "nenhum EndpointSlice lido declara a porta " + portText(*port)
		b.add(StepEndpoints, StatusBroken, text)
		b.settle(StateNoBackend, text)
	case eps.total == 0:
		text := "nenhum endpoint do Service na lista lida"
		b.add(StepEndpoints, StatusBroken, text)
		if pods.matched > 0 {
			b.settle(StatePending, text)
		}
		b.settle(StateNoBackend, text)
	default:
		text := fmt.Sprintf("%d endpoints, nenhum pronto", eps.total)
		b.add(StepEndpoints, StatusBroken, text)
		b.settle(StatePending, text)
	}
}

func (b *backendResult) podStep(pods podCount, svc *data.ServiceFacts, g Graph, serving bool) {
	switch {
	case serving && pods.ready == 0 && pods.known:
		b.add(StepPods, StatusAttention, fmt.Sprintf("%d pods selecionados, nenhum pronto, mas há endpoints prontos", pods.matched))
	case pods.matched == 0 && !pods.known:
		b.add(StepPods, StatusUnknown, g.Pods.gap("Pods"))
	case pods.matched == 0:
		text := "o selector " + svc.Selector.String() + " não encontra pods na lista lida"
		b.add(StepPods, StatusBroken, text)
		b.settle(StateNoBackend, text)
	case pods.ready == 0 && !pods.known:
		b.add(StepPods, StatusUnknown, g.Pods.gap("Pods"))
	case pods.ready == 0 && svc.PublishNotReady:
		b.add(StepPods, StatusAttention, fmt.Sprintf("%d pods, nenhum pronto; o Service declara publishNotReadyAddresses", pods.matched))
	case pods.ready == 0:
		text := fmt.Sprintf("%d pods selecionados, nenhum pronto", pods.matched)
		b.add(StepPods, StatusBroken, text)
		b.settle(StatePending, text)
	default:
		b.add(StepPods, StatusOK, fmt.Sprintf("%d de %d prontos", pods.ready, pods.matched))
	}
}

func portText(p data.ServicePort) string {
	proto := p.Protocol
	if proto == "" {
		proto = "TCP"
	}
	text := fmt.Sprintf("%d/%s", p.Port, proto)
	if p.Name != "" {
		text = p.Name + " " + text
	}
	return text
}

func serviceRoute(g Graph, namespace, name string) (Route, backendResult) {
	var b backendResult
	route := Route{Title: "Service " + namespace + "/" + name}
	svc, found := g.service(namespace, name)
	if b.service(g, namespace, name, found) {
		if svc.Type == "ExternalName" {
			text := "ExternalName sem endpoints a conferir: alias DNS, destino não verificado"
			b.add(StepEndpoints, StatusUnknown, text)
			b.settle(StateUnknown, text)
		} else {
			b.serve(g, namespace, name, svc, nil)
			if svc.Type == "LoadBalancer" {
				if len(svc.LoadBalancer) == 0 {
					b.add(StepAddress, StatusBroken, "LoadBalancer pendente: nenhum endereço publicado em status")
				} else {
					b.add(StepAddress, StatusOK, "LoadBalancer com endereço publicado")
				}
			}
		}
	}
	route.Steps = b.steps
	return route, b
}

func ingressRoute(g Graph, namespace, name string, ing *data.IngressFacts, host, path string, backend data.IngressBackend) (Route, backendResult) {
	route := Route{Title: "Ingress " + namespace + "/" + name + " " + shownHost(host) + path + " para " + backend.String()}
	route.Steps = append(route.Steps, classStep(g, ing), tlsStep(ing, host))
	b := checkIngressBackend(g, namespace, backend)
	route.Steps = append(route.Steps, b.steps...)
	if len(ing.Addresses) == 0 {
		route.Steps = append(route.Steps, Step{StepAddress, StatusAttention, "nenhum endereço publicado em status do Ingress"})
	} else {
		route.Steps = append(route.Steps, Step{StepAddress, StatusOK, "endereço publicado no Ingress"})
	}
	return route, b
}

func shownHost(host string) string {
	if host == "" {
		return "(qualquer host)"
	}
	return host
}
