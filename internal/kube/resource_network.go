package kube

import (
	"reflect"
	"sort"
	"strconv"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/vynazevedo/lazykube/internal/data"
)

const (
	factRowLimit       = 48 << 10
	factTextLimit      = 253
	factWordLimit      = 64
	factPathLimit      = 1024
	factLabels         = 64
	factPorts          = 64
	factAddresses      = 16
	factEndpoints      = 32
	factRules          = 64
	factPaths          = 128
	factTLS            = 32
	factTLSHosts       = 32
	factRequirements   = 32
	factRouteEntries   = 32
	defaultClassMarker = "ingressclass.kubernetes.io/is-default-class"
	legacyClassMarker  = "kubernetes.io/ingress.class"
	sliceServiceLabel  = "kubernetes.io/service-name"
)

func (e *resourceEntry) facts(object any) *data.NetworkFacts {
	if e.key.ProjectionVersion != data.ResourceNetworkProjection {
		return nil
	}
	return networkFacts(object, e.key.GVR)
}

type factBuilder struct {
	truncated bool
}

func (b *factBuilder) text(s string, limit int) (string, bool) {
	if len(s) > limit {
		b.truncated = true
		return "", false
	}
	return strings.Clone(s), true
}

func (b *factBuilder) word(s string) string {
	v, _ := b.text(s, factWordLimit)
	return v
}

func (b *factBuilder) texts(in []string, count, limit int) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(in), count))
	for _, s := range in {
		if len(out) == count {
			b.truncated = true
			break
		}
		if v, ok := b.text(s, limit); ok {
			out = append(out, v)
		}
	}
	return out
}

func (b *factBuilder) labels(in map[string]string) data.Labels {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil
	}
	out := make(data.Labels, 0, min(len(keys), factLabels))
	for _, k := range keys {
		if len(out) == factLabels {
			b.truncated = true
			break
		}
		name, okName := b.text(k, 2*factTextLimit)
		value, okValue := b.text(in[k], factTextLimit)
		if okName && okValue {
			out = append(out, data.Label{Key: name, Value: value})
		}
	}
	return out
}

func networkFacts(object any, gvr schema.GroupVersionResource) *data.NetworkFacts {
	b := &factBuilder{}
	var f data.NetworkFacts
	ok := false
	switch obj := object.(type) {
	case *corev1.Pod:
		f.Pod, ok = b.pod(obj), true
	case *corev1.Node:
		f.Node, ok = b.node(obj), true
	case *appsv1.Deployment:
		f.Template, ok = &data.TemplateFacts{Labels: b.labels(obj.Spec.Template.Labels)}, true
	case *unstructured.Unstructured:
		ok = b.unstructured(&f, obj, gvr)
	}
	f.Truncated = b.truncated
	f.Bytes = retainedBytes(reflect.ValueOf(&f))
	if !ok || f.Bytes > factRowLimit {
		marker := &data.NetworkFacts{Truncated: true}
		marker.Bytes = retainedBytes(reflect.ValueOf(marker))
		return marker
	}
	return &f
}

func decode[T any](obj *unstructured.Unstructured) (*T, bool) {
	out := new(T)
	return out, runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, out) == nil
}

func (b *factBuilder) unstructured(f *data.NetworkFacts, obj *unstructured.Unstructured, gvr schema.GroupVersionResource) bool {
	switch gvr.GroupResource() {
	case schema.GroupResource{Resource: "services"}:
		svc, ok := decode[corev1.Service](obj)
		if ok {
			f.Service = b.service(svc)
		}
		return ok
	case schema.GroupResource{Group: "discovery.k8s.io", Resource: "endpointslices"}:
		slice, ok := decode[discoveryv1.EndpointSlice](obj)
		if ok {
			f.Slice = b.slice(slice)
		}
		return ok
	case schema.GroupResource{Group: "networking.k8s.io", Resource: "ingresses"}:
		ing, ok := decode[networkingv1.Ingress](obj)
		if ok {
			f.Ingress = b.ingress(ing)
		}
		return ok
	case schema.GroupResource{Group: "networking.k8s.io", Resource: "ingressclasses"}:
		class, ok := decode[networkingv1.IngressClass](obj)
		if ok {
			controller, _ := b.text(class.Spec.Controller, factTextLimit)
			f.Class = &data.IngressClassFacts{Controller: controller, Default: class.Annotations[defaultClassMarker] == "true"}
		}
		return ok
	case schema.GroupResource{Group: "networking.k8s.io", Resource: "networkpolicies"}:
		policy, ok := decode[networkingv1.NetworkPolicy](obj)
		if ok {
			f.Policy = b.policy(policy)
		}
		return ok
	case schema.GroupResource{Group: "gateway.networking.k8s.io", Resource: "httproutes"}:
		f.Route = b.httpRoute(obj)
		return true
	case schema.GroupResource{Group: "apps", Resource: "statefulsets"}, schema.GroupResource{Group: "apps", Resource: "daemonsets"}, schema.GroupResource{Group: "apps", Resource: "replicasets"}, schema.GroupResource{Group: "apps", Resource: "deployments"}, schema.GroupResource{Group: "batch", Resource: "jobs"}:
		return b.template(f, obj, "spec", "template", "metadata", "labels")
	case schema.GroupResource{Group: "batch", Resource: "cronjobs"}:
		return b.template(f, obj, "spec", "jobTemplate", "spec", "template", "metadata", "labels")
	}
	return false
}

func (b *factBuilder) template(f *data.NetworkFacts, obj *unstructured.Unstructured, path ...string) bool {
	values, _, err := unstructured.NestedStringMap(obj.Object, path...)
	if err != nil {
		return false
	}
	f.Template = &data.TemplateFacts{Labels: b.labels(values)}
	return true
}

func (b *factBuilder) service(svc *corev1.Service) *data.ServiceFacts {
	out := &data.ServiceFacts{Type: b.word(string(svc.Spec.Type)), PublishNotReady: svc.Spec.PublishNotReadyAddresses}
	ips := svc.Spec.ClusterIPs
	if len(ips) == 0 && svc.Spec.ClusterIP != "" {
		ips = []string{svc.Spec.ClusterIP}
	}
	out.ClusterIPs = b.texts(ips, factAddresses, factTextLimit)
	out.ExternalIPs = b.texts(svc.Spec.ExternalIPs, factAddresses, factTextLimit)
	out.ExternalName, _ = b.text(svc.Spec.ExternalName, factTextLimit)
	out.Selector = b.labels(svc.Spec.Selector)
	out.Ports = exact[data.ServicePort](len(svc.Spec.Ports), factPorts)
	for _, p := range svc.Spec.Ports {
		if len(out.Ports) == factPorts {
			b.truncated = true
			break
		}
		name, _ := b.text(p.Name, factTextLimit)
		app := ""
		if p.AppProtocol != nil {
			app, _ = b.text(*p.AppProtocol, factTextLimit)
		}
		target, _ := b.text(p.TargetPort.String(), factTextLimit)
		out.Ports = append(out.Ports, data.ServicePort{Name: name, Port: int(p.Port), Protocol: b.word(string(p.Protocol)), Target: target, NodePort: int(p.NodePort), AppProtocol: app})
	}
	out.LoadBalancer = exact[data.LoadBalancerAddress](len(svc.Status.LoadBalancer.Ingress), factAddresses)
	for _, a := range svc.Status.LoadBalancer.Ingress {
		out.LoadBalancer = b.address(out.LoadBalancer, a.IP, a.Hostname)
	}
	return out
}

func (b *factBuilder) address(list []data.LoadBalancerAddress, ip, hostname string) []data.LoadBalancerAddress {
	if len(list) == factAddresses {
		b.truncated = true
		return list
	}
	addr, okAddr := b.text(ip, factTextLimit)
	host, okHost := b.text(hostname, factTextLimit)
	if !okAddr || !okHost || addr == "" && host == "" {
		return list
	}
	return append(list, data.LoadBalancerAddress{IP: addr, Hostname: host})
}

func exact[T any](n, limit int) []T {
	if n == 0 || limit <= 0 {
		return nil
	}
	return make([]T, 0, min(n, limit))
}

func tri(v *bool) data.Tri {
	if v == nil {
		return data.TriUnknown
	}
	return data.TriOf(*v)
}

func (b *factBuilder) slice(s *discoveryv1.EndpointSlice) *data.SliceFacts {
	service, _ := b.text(s.Labels[sliceServiceLabel], factTextLimit)
	out := &data.SliceFacts{Service: service, AddressType: b.word(string(s.AddressType)), Total: len(s.Endpoints)}
	out.Ports = exact[data.SlicePort](len(s.Ports), factPorts)
	for _, p := range s.Ports {
		if len(out.Ports) == factPorts {
			b.truncated = true
			break
		}
		port := data.SlicePort{}
		if p.Name != nil {
			port.Name, _ = b.text(*p.Name, factTextLimit)
		}
		if p.Port != nil {
			port.Port = int(*p.Port)
		}
		if p.Protocol != nil {
			port.Protocol = b.word(string(*p.Protocol))
		}
		out.Ports = append(out.Ports, port)
	}
	out.Endpoints = exact[data.SliceEndpoint](len(s.Endpoints), factEndpoints)
	for _, ep := range s.Endpoints {
		if ep.Conditions.Ready == nil || *ep.Conditions.Ready {
			out.Ready++
		}
		if len(out.Endpoints) == factEndpoints {
			continue
		}
		kind, name, node := "", "", ""
		if ep.TargetRef != nil {
			kind, _ = b.text(ep.TargetRef.Kind, factTextLimit)
			name, _ = b.text(ep.TargetRef.Name, factTextLimit)
		}
		if ep.NodeName != nil {
			node, _ = b.text(*ep.NodeName, factTextLimit)
		}
		out.Endpoints = append(out.Endpoints, data.SliceEndpoint{Addresses: b.texts(ep.Addresses, factAddresses, factTextLimit), Ready: tri(ep.Conditions.Ready), Serving: tri(ep.Conditions.Serving), Terminating: tri(ep.Conditions.Terminating), TargetKind: kind, TargetName: name, Node: node})
	}
	return out
}

func (b *factBuilder) backend(in networkingv1.IngressBackend) (data.IngressBackend, bool) {
	out := data.IngressBackend{}
	ok := true
	if in.Service != nil {
		var okName, okPort bool
		out.Service, okName = b.text(in.Service.Name, factTextLimit)
		out.PortName, okPort = b.text(in.Service.Port.Name, factTextLimit)
		out.PortNumber = int(in.Service.Port.Number)
		ok = okName && okPort
	}
	if in.Resource != nil {
		var okKind, okName bool
		kind, okKind := b.text(in.Resource.Kind, factTextLimit)
		name, okName := b.text(in.Resource.Name, factTextLimit)
		out.Resource = kind + "/" + name
		ok = ok && okKind && okName
	}
	return out, ok
}

func (b *factBuilder) ingress(ing *networkingv1.Ingress) *data.IngressFacts {
	out := &data.IngressFacts{}
	if ing.Spec.IngressClassName != nil {
		out.Class, _ = b.text(*ing.Spec.IngressClassName, factTextLimit)
	}
	out.ClassAnnotation, _ = b.text(ing.Annotations[legacyClassMarker], factTextLimit)
	if ing.Spec.DefaultBackend != nil {
		if backend, ok := b.backend(*ing.Spec.DefaultBackend); ok {
			out.Default = &backend
		}
	}
	paths := 0
	out.Rules = exact[data.IngressRule](len(ing.Spec.Rules), factRules)
	for _, r := range ing.Spec.Rules {
		if len(out.Rules) == factRules {
			b.truncated = true
			break
		}
		host, ok := b.text(r.Host, factTextLimit)
		if !ok {
			continue
		}
		rule := data.IngressRule{Host: host}
		if r.HTTP != nil {
			rule.Paths = exact[data.IngressPath](len(r.HTTP.Paths), factPaths-paths)
			for _, p := range r.HTTP.Paths {
				if paths == factPaths {
					b.truncated = true
					break
				}
				path, okPath := b.text(p.Path, factPathLimit)
				backend, okBackend := b.backend(p.Backend)
				if !okPath || !okBackend {
					continue
				}
				kind := ""
				if p.PathType != nil {
					kind = b.word(string(*p.PathType))
				}
				rule.Paths = append(rule.Paths, data.IngressPath{Path: path, Type: kind, Backend: backend})
				paths++
			}
		}
		out.Rules = append(out.Rules, rule)
	}
	out.TLS = exact[data.IngressTLS](len(ing.Spec.TLS), factTLS)
	for _, t := range ing.Spec.TLS {
		if len(out.TLS) == factTLS {
			b.truncated = true
			break
		}
		secret, _ := b.text(t.SecretName, factTextLimit)
		out.TLS = append(out.TLS, data.IngressTLS{Hosts: b.texts(t.Hosts, factTLSHosts, factTextLimit), Secret: secret})
	}
	out.Addresses = exact[data.LoadBalancerAddress](len(ing.Status.LoadBalancer.Ingress), factAddresses)
	for _, a := range ing.Status.LoadBalancer.Ingress {
		out.Addresses = b.address(out.Addresses, a.IP, a.Hostname)
	}
	return out
}

func (b *factBuilder) policy(p *networkingv1.NetworkPolicy) *data.NetworkPolicyFacts {
	out := &data.NetworkPolicyFacts{MatchLabels: b.labels(p.Spec.PodSelector.MatchLabels), IngressRules: len(p.Spec.Ingress), EgressRules: len(p.Spec.Egress)}
	out.Expressions = exact[data.SelectorRequirement](len(p.Spec.PodSelector.MatchExpressions), factRequirements)
	for _, r := range p.Spec.PodSelector.MatchExpressions {
		if len(out.Expressions) == factRequirements {
			b.truncated = true
			break
		}
		out.Expressions = append(out.Expressions, b.requirement(r))
	}
	out.Types = exact[string](len(p.Spec.PolicyTypes), 4)
	for _, t := range p.Spec.PolicyTypes {
		if len(out.Types) < 4 {
			v, _ := b.text(string(t), factTextLimit)
			out.Types = append(out.Types, v)
		}
	}
	return out
}

func (b *factBuilder) requirement(r metav1.LabelSelectorRequirement) data.SelectorRequirement {
	key, _ := b.text(r.Key, 2*factTextLimit)
	op, _ := b.text(string(r.Operator), factTextLimit)
	return data.SelectorRequirement{Key: key, Operator: op, Values: b.texts(r.Values, factLabels, factTextLimit)}
}

func (b *factBuilder) httpRoute(obj *unstructured.Unstructured) *data.HTTPRouteFacts {
	out := &data.HTTPRouteFacts{}
	hostnames, _, _ := unstructured.NestedStringSlice(obj.Object, "spec", "hostnames")
	out.Hostnames = b.texts(hostnames, factRouteEntries, factTextLimit)
	parents := nestedMaps(obj.Object, "spec", "parentRefs")
	for _, p := range parents {
		name, _, _ := unstructured.NestedString(p, "name")
		out.Parents = append(out.Parents, b.texts([]string{name}, 1, factTextLimit)...)
		if len(out.Parents) >= factRouteEntries {
			b.truncated = true
			break
		}
	}
	rules := nestedMaps(obj.Object, "spec", "rules")
	out.Rules = len(rules)
	for _, r := range rules {
		for _, ref := range nestedMaps(r, "backendRefs") {
			if len(out.Backends) == factRouteEntries {
				b.truncated = true
				break
			}
			name, _, _ := unstructured.NestedString(ref, "name")
			text := name
			if port, ok, _ := unstructured.NestedInt64(ref, "port"); ok {
				text += ":" + strconv.FormatInt(port, 10)
			}
			out.Backends = append(out.Backends, b.texts([]string{text}, 1, factTextLimit)...)
		}
	}
	accepted, refused := 0, 0
	statuses := nestedMaps(obj.Object, "status", "parents")
	for _, s := range statuses {
		for _, c := range nestedMaps(s, "conditions") {
			if c["type"] != "Accepted" {
				continue
			}
			switch c["status"] {
			case "True":
				accepted++
			case "False":
				refused++
			}
		}
	}
	switch {
	case refused > 0:
		out.Accepted = data.TriFalse
	case len(parents) > 0 && accepted >= len(parents):
		out.Accepted = data.TriTrue
	}
	return out
}

func nestedMaps(obj map[string]any, path ...string) []map[string]any {
	raw, _, _ := unstructured.NestedFieldNoCopy(obj, path...)
	list, _ := raw.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func (b *factBuilder) pod(p *corev1.Pod) *data.PodFacts {
	out := &data.PodFacts{Labels: b.labels(p.Labels), Phase: b.word(string(p.Status.Phase)), Terminating: p.DeletionTimestamp != nil}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			switch c.Status {
			case corev1.ConditionTrue:
				out.Ready = data.TriTrue
			case corev1.ConditionFalse:
				out.Ready = data.TriFalse
			}
		}
	}
	return out
}

func (b *factBuilder) node(n *corev1.Node) *data.NodeFacts {
	out := &data.NodeFacts{}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			switch c.Status {
			case corev1.ConditionTrue:
				out.Ready = data.TriTrue
			case corev1.ConditionFalse:
				out.Ready = data.TriFalse
			}
		}
	}
	for _, a := range n.Status.Addresses {
		switch a.Type {
		case corev1.NodeInternalIP:
			if len(out.Internal) < factAddresses {
				out.Internal = append(out.Internal, b.texts([]string{a.Address}, 1, factTextLimit)...)
			}
		case corev1.NodeExternalIP:
			if len(out.External) < factAddresses {
				out.External = append(out.External, b.texts([]string{a.Address}, 1, factTextLimit)...)
			}
		}
	}
	return out
}
