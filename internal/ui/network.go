package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

const networkNameMin = 18

const unreadable = "?"

func networkColumn(title string, width int, value func(*data.NetworkFacts) (string, bool)) resourceColumn {
	return resourceColumn{title: title, width: width, value: func(r data.ResourceRow, _ time.Time) string {
		if r.Network == nil {
			return unreadable
		}
		v, ok := value(r.Network)
		if !ok {
			return unreadable
		}
		if v == "" {
			v = "-"
		}
		return draw.Sanitize(v)
	}}
}

func networkColumns(kind data.NetworkKind, w int) []resourceColumn {
	var cols []resourceColumn
	switch kind {
	case data.NetworkService:
		cols = []resourceColumn{
			networkColumn("TYPE", 12, func(f *data.NetworkFacts) (string, bool) {
				if f.Service == nil {
					return "", false
				}
				return f.Service.Type, true
			}),
			networkColumn("PORTS", 18, func(f *data.NetworkFacts) (string, bool) {
				if f.Service == nil {
					return "", false
				}
				return servicePorts(f.Service.Ports), true
			}),
			networkColumn("CLUSTER-IP", 15, func(f *data.NetworkFacts) (string, bool) {
				if f.Service == nil {
					return "", false
				}
				return strings.Join(f.Service.ClusterIPs, ","), true
			}),
			networkColumn("EXTERNAL", 22, func(f *data.NetworkFacts) (string, bool) {
				if f.Service == nil {
					return "", false
				}
				return serviceExternal(*f.Service), true
			}),
		}
	case data.NetworkSlice:
		cols = []resourceColumn{
			networkColumn("SERVICE", 24, func(f *data.NetworkFacts) (string, bool) {
				if f.Slice == nil {
					return "", false
				}
				return f.Slice.Service, true
			}),
			networkColumn("READY", 9, func(f *data.NetworkFacts) (string, bool) {
				if f.Slice == nil {
					return "", false
				}
				return fmt.Sprintf("%d/%d", f.Slice.Ready, f.Slice.Total), true
			}),
			networkColumn("PORTS", 18, func(f *data.NetworkFacts) (string, bool) {
				if f.Slice == nil {
					return "", false
				}
				return slicePorts(f.Slice.Ports), true
			}),
			networkColumn("FAMILY", 6, func(f *data.NetworkFacts) (string, bool) {
				if f.Slice == nil {
					return "", false
				}
				return f.Slice.AddressType, true
			}),
		}
	case data.NetworkIngress:
		cols = []resourceColumn{
			networkColumn("HOSTS", 30, func(f *data.NetworkFacts) (string, bool) {
				if f.Ingress == nil {
					return "", false
				}
				return ingressHosts(*f.Ingress), true
			}),
			networkColumn("TLS", 4, func(f *data.NetworkFacts) (string, bool) {
				if f.Ingress == nil {
					return "", false
				}
				if len(f.Ingress.TLS) == 0 {
					return "", true
				}
				return "sim", true
			}),
			networkColumn("CLASS", 12, func(f *data.NetworkFacts) (string, bool) {
				if f.Ingress == nil {
					return "", false
				}
				if f.Ingress.Class == "" {
					return f.Ingress.ClassAnnotation, true
				}
				return f.Ingress.Class, true
			}),
			networkColumn("ADDRESS", 20, func(f *data.NetworkFacts) (string, bool) {
				if f.Ingress == nil {
					return "", false
				}
				return balancers(f.Ingress.Addresses), true
			}),
		}
	case data.NetworkIngressClass:
		cols = []resourceColumn{
			networkColumn("CONTROLLER", 36, func(f *data.NetworkFacts) (string, bool) {
				if f.Class == nil {
					return "", false
				}
				return f.Class.Controller, true
			}),
			networkColumn("DEFAULT", 7, func(f *data.NetworkFacts) (string, bool) {
				if f.Class == nil {
					return "", false
				}
				if f.Class.Default {
					return "sim", true
				}
				return "", true
			}),
		}
	case data.NetworkPolicy:
		cols = []resourceColumn{
			networkColumn("POD SELECTOR", 34, func(f *data.NetworkFacts) (string, bool) {
				if f.Policy == nil {
					return "", false
				}
				return policySelector(*f.Policy), true
			}),
			networkColumn("TYPES", 15, func(f *data.NetworkFacts) (string, bool) {
				if f.Policy == nil {
					return "", false
				}
				return strings.Join(f.Policy.Types, ","), true
			}),
		}
	case data.NetworkHTTPRoute:
		cols = []resourceColumn{
			networkColumn("HOSTNAMES", 32, func(f *data.NetworkFacts) (string, bool) {
				if f.Route == nil {
					return "", false
				}
				return strings.Join(f.Route.Hostnames, ","), true
			}),
			networkColumn("ACCEPTED", 8, func(f *data.NetworkFacts) (string, bool) {
				if f.Route == nil {
					return "", false
				}
				return triText(f.Route.Accepted), true
			}),
			networkColumn("PARENTS", 20, func(f *data.NetworkFacts) (string, bool) {
				if f.Route == nil {
					return "", false
				}
				return strings.Join(f.Route.Parents, ","), true
			}),
		}
	}
	if w < 100 {
		return cols[:min(2, len(cols))]
	}
	return append(cols, resourceColumn{"AGE", 5, true, func(r data.ResourceRow, ref time.Time) string { return since(r.Ref.CreatedAt, ref) }})
}

func triText(t data.Tri) string {
	switch t {
	case data.TriTrue:
		return "sim"
	case data.TriFalse:
		return "não"
	}
	return unreadable
}

func servicePort(p data.ServicePort) string {
	out := fmt.Sprint(p.Port)
	if p.NodePort > 0 {
		out += fmt.Sprintf(":%d", p.NodePort)
	}
	if p.Protocol != "" && p.Protocol != "TCP" {
		out += "/" + p.Protocol
	}
	return out
}

func servicePorts(ports []data.ServicePort) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = servicePort(p)
	}
	return strings.Join(parts, ",")
}

func slicePorts(ports []data.SlicePort) string {
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = fmt.Sprint(p.Port)
		if p.Port == 0 {
			parts[i] = unreadable
		}
	}
	return strings.Join(parts, ",")
}

func balancers(list []data.LoadBalancerAddress) string {
	var parts []string
	for _, a := range list {
		for _, v := range []string{a.Hostname, a.IP} {
			if v != "" {
				parts = append(parts, v)
				break
			}
		}
	}
	return strings.Join(parts, ",")
}

func serviceExternal(s data.ServiceFacts) string {
	if s.Type == "ExternalName" {
		return s.ExternalName
	}
	if lb := balancers(s.LoadBalancer); lb != "" {
		return lb
	}
	if len(s.ExternalIPs) > 0 {
		return strings.Join(s.ExternalIPs, ",")
	}
	if s.Type == "LoadBalancer" {
		return "pendente"
	}
	return ""
}

func ingressHosts(i data.IngressFacts) string {
	var hosts []string
	for _, r := range i.Rules {
		host := r.Host
		if host == "" {
			host = "(qualquer)"
		}
		hosts = append(hosts, host)
	}
	if len(hosts) == 0 && i.Default != nil {
		return "(padrão)"
	}
	return strings.Join(hosts, ",")
}

func policySelector(p data.NetworkPolicyFacts) string {
	parts := make([]string, 0, len(p.Expressions)+1)
	if len(p.MatchLabels) > 0 {
		parts = append(parts, p.MatchLabels.String())
	}
	for _, e := range p.Expressions {
		text := e.Key + " " + e.Operator
		if len(e.Values) > 0 {
			text += " (" + strings.Join(e.Values, ",") + ")"
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "todos os pods do namespace"
	}
	return strings.Join(parts, ", ")
}
