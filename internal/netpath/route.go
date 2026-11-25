package netpath

import (
	"fmt"
	"strings"

	"github.com/vynazevedo/lazykube/internal/data"
)

const maxRoutePorts = 8

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
	StatusDraining
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
	for _, want := range []Status{StatusBroken, StatusDraining, StatusAttention, StatusUnknown} {
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
	case s.Status == StatusDraining:
		return "em drenagem: " + string(s.Kind) + ", " + s.Evidence + "; o tráfego não foi testado"
	case s.Status == StatusAttention:
		return "atenção: " + string(s.Kind) + ", " + s.Evidence
	}
	return "indeterminado: " + string(s.Kind) + ", " + s.Evidence
}

func classStep(x *index, ing *data.IngressFacts) Step {
	step := func(st Status, text string) Step { return Step{StepClass, st, text} }
	if ing.Class != "" {
		if x.classes.find("", ing.Class) != nil {
			return step(StatusOK, ing.Class)
		}
		if x.classes.state == SetKnown {
			return step(StatusBroken, "a IngressClass "+ing.Class+" não existe na lista lida")
		}
		return step(StatusUnknown, x.classes.gap("IngressClasses"))
	}
	if ing.ClassAnnotation != "" {
		return step(StatusUnknown, "classe legada por anotação: "+ing.ClassAnnotation)
	}
	if !x.classes.authoritative() {
		return step(StatusUnknown, x.classes.gap("IngressClasses"))
	}
	switch len(x.defaults) {
	case 0:
		return step(StatusAttention, "sem ingressClassName e sem IngressClass padrão")
	case 1:
		return step(StatusOK, "padrão "+x.defaults[0])
	}
	return step(StatusAttention, fmt.Sprintf("%d IngressClasses marcadas como padrão", len(x.defaults)))
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

func checkIngressBackend(x *index, namespace string, backend data.IngressBackend) backendResult {
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
	svc, found := x.service(namespace, backend.Service)
	if !out.service(x, namespace, backend.Service, found) {
		return out
	}
	if svc.Type == "ExternalName" {
		text := "Service ExternalName: alias DNS, destino não verificado"
		out.add(StepPort, StatusUnknown, text)
		out.settle(StateUnknown, text)
		return out
	}
	out.port = findPort(svc, backend)
	switch {
	case out.port == nil:
		text := "o backend não informa porta"
		if backend.Port() != "" {
			text = "a porta " + backend.Port() + " não existe no Service " + backend.Service
		}
		out.add(StepPort, StatusBroken, text)
		out.settle(StateNoBackend, text)
		return out
	case protocolOf(out.port.Protocol) != "TCP":
		text := "a porta " + portText(*out.port) + " não é TCP; atendimento pelo Ingress não verificado"
		out.add(StepPort, StatusUnknown, text)
		out.settle(StateUnknown, text)
	default:
		out.add(StepPort, StatusOK, portText(*out.port))
	}
	out.serve(x, namespace, backend.Service, svc, out.port)
	return out
}

func (b *backendResult) service(x *index, namespace, name string, found data.Tri) bool {
	switch found {
	case data.TriTrue:
		b.add(StepService, StatusOK, namespace+"/"+name)
		return true
	case data.TriFalse:
		text := "o Service " + namespace + "/" + name + " não existe na lista lida"
		b.add(StepService, StatusBroken, text)
		b.settle(StateNoBackend, text)
	default:
		text := x.services.gap("Services")
		b.add(StepService, StatusUnknown, text)
		b.settle(StateUnknown, text)
	}
	return false
}

func (b *backendResult) serve(x *index, namespace, name string, svc *data.ServiceFacts, port *data.ServicePort) {
	pods := podCount{}
	if len(svc.Selector) > 0 {
		pods = x.countPods(namespace, svc.Selector)
	}
	eps := x.endpoints(namespace, name, svc, port)
	b.podStep(x, svc, pods, eps)
	b.endpointStep(x, svc, pods, eps, port)
}

func (b *backendResult) podStep(x *index, svc *data.ServiceFacts, pods podCount, eps endpointCount) {
	if len(svc.Selector) == 0 {
		b.add(StepPods, StatusOK, "Service sem selector; nenhum pod a conferir")
		return
	}
	elsewhere := eps.Ready > 0 || eps.Draining > 0 || eps.Unclear > 0 || eps.diverges
	switch {
	case pods.ready > 0:
		text := fmt.Sprintf("%d de %d prontos", pods.ready, pods.matched)
		if pods.draining > 0 {
			text += fmt.Sprintf(", %d em drenagem", pods.draining)
		}
		b.add(StepPods, StatusOK, text)
	case pods.draining > 0 && eps.Draining > 0 && eps.Ready == 0:
		b.add(StepPods, StatusDraining, fmt.Sprintf("%d de %d pods encerrando e ainda Ready, nenhum outro pronto", pods.draining, pods.matched))
	case pods.draining > 0:
		b.add(StepPods, StatusAttention, fmt.Sprintf("%d de %d pods encerrando e ainda Ready; os EndpointSlices lidos não confirmam drenagem", pods.draining, pods.matched))
	case pods.unclear > 0:
		b.add(StepPods, StatusUnknown, fmt.Sprintf("%d de %d pods sem condição Ready informada", pods.unclear, pods.matched))
	case !pods.known:
		b.add(StepPods, StatusUnknown, x.pods.gap("Pods"))
	case pods.matched > 0 && svc.PublishNotReady:
		b.add(StepPods, StatusAttention, fmt.Sprintf("%d pods, nenhum pronto; o Service declara publishNotReadyAddresses", pods.matched))
	case elsewhere:
		b.add(StepPods, StatusAttention, fmt.Sprintf("%d pods selecionados, nenhum pronto, mas há endpoints que não estão indisponíveis", pods.matched))
	case pods.matched == 0:
		b.add(StepPods, StatusBroken, "o selector "+svc.Selector.String()+" não encontra pods na lista lida")
	default:
		b.add(StepPods, StatusBroken, fmt.Sprintf("%d pods selecionados, nenhum pronto", pods.matched))
	}
}

func (b *backendResult) last() Step {
	return b.steps[len(b.steps)-1]
}

func (b *backendResult) endpointStep(x *index, svc *data.ServiceFacts, pods podCount, eps endpointCount, port *data.ServicePort) {
	podStep := b.last()
	label := ""
	if port != nil {
		label = portText(*port) + ": "
	}
	end := func(st Status, state State, text string) {
		b.add(StepEndpoints, st, label+text)
		reason := text
		if podStep.Status == StatusBroken && st != StatusUnknown {
			reason = podStep.Evidence
		}
		if state == StateReady {
			reason = ""
		}
		b.settle(state, reason)
	}
	switch {
	case eps.Ready > 0 && svc.PublishNotReady && len(svc.Selector) > 0 && pods.ready == 0:
		end(StatusUnknown, StateUnknown, fmt.Sprintf("%d de %d publicados, mas o Service declara publishNotReadyAddresses e nenhum pod pronto foi lido", eps.Ready, eps.Total))
	case eps.Ready > 0:
		text := fmt.Sprintf("%d de %d prontos", eps.Ready, eps.Total)
		if eps.Draining > 0 {
			text += fmt.Sprintf(", %d em drenagem", eps.Draining)
		}
		end(StatusOK, StateReady, text)
	case eps.Draining > 0:
		end(StatusDraining, StateDraining, fmt.Sprintf("%d de %d em drenagem: encerrando e ainda atendendo, nenhum pronto", eps.Draining, eps.Total))
	case eps.Unclear > 0:
		end(StatusUnknown, StateUnknown, fmt.Sprintf("%d de %d com condições que não permitem concluir", eps.Unclear, eps.Total))
	case eps.diverges:
		end(StatusUnknown, StateUnknown, "há EndpointSlices com número de porta ou família de IP diferente do Service")
	case !eps.known:
		text := x.slices.gap("EndpointSlices")
		b.add(StepEndpoints, StatusUnknown, label+text)
		if podStep.Status == StatusBroken {
			state := StatePending
			if pods.matched == 0 {
				state = StateNoBackend
			}
			b.settle(state, podStep.Evidence)
		}
		b.settle(StateUnknown, text)
	case eps.slices > 0 && !eps.withPort:
		end(StatusBroken, StateNoBackend, "nenhum EndpointSlice lido declara esta porta e protocolo")
	case eps.Total == 0 && pods.matched > 0:
		end(StatusBroken, StatePending, "nenhum endpoint do Service na lista lida")
	case eps.Total == 0:
		end(StatusBroken, StateNoBackend, "nenhum endpoint do Service na lista lida")
	default:
		end(StatusBroken, StatePending, fmt.Sprintf("%d endpoints, nenhum pronto", eps.Total))
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

func serviceRoute(x *index, namespace, name string) Route {
	var b backendResult
	route := Route{Title: "Service " + namespace + "/" + name}
	svc, found := x.service(namespace, name)
	if !b.service(x, namespace, name, found) {
		route.Steps = b.steps
		return route
	}
	switch {
	case svc.Type == "ExternalName":
		b.add(StepEndpoints, StatusUnknown, "ExternalName sem endpoints a conferir: alias DNS, destino não verificado")
	case len(svc.Ports) == 0:
		b.serve(x, namespace, name, svc, nil)
	default:
		for i := range svc.Ports {
			if i == maxRoutePorts {
				b.add(StepEndpoints, StatusUnknown, fmt.Sprintf("mais %d portas não conferidas nesta rota", len(svc.Ports)-i))
				break
			}
			one := backendResult{}
			one.serve(x, namespace, name, svc, &svc.Ports[i])
			if i > 0 {
				one.steps = one.steps[1:]
			}
			b.steps = append(b.steps, one.steps...)
		}
	}
	if svc.Type == "LoadBalancer" {
		if len(svc.LoadBalancer) == 0 {
			b.add(StepAddress, StatusBroken, "LoadBalancer pendente: nenhum endereço publicado em status")
		} else {
			b.add(StepAddress, StatusOK, "LoadBalancer com endereço publicado")
		}
	}
	route.Steps = b.steps
	return route
}

func ingressRoute(x *index, namespace, name string, ing *data.IngressFacts, host, path string, backend data.IngressBackend, result backendResult) Route {
	route := Route{Title: "Ingress " + namespace + "/" + name + " " + shownHost(host) + path + " para " + backend.String()}
	route.Steps = append(route.Steps, classStep(x, ing), tlsStep(ing, host))
	route.Steps = append(route.Steps, result.steps...)
	if len(ing.Addresses) == 0 {
		route.Steps = append(route.Steps, Step{StepAddress, StatusAttention, "nenhum endereço publicado em status do Ingress"})
	} else {
		route.Steps = append(route.Steps, Step{StepAddress, StatusOK, "endereço publicado no Ingress"})
	}
	return route
}

func shownHost(host string) string {
	if host == "" {
		return "(qualquer host)"
	}
	return host
}
