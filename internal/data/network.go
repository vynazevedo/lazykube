package data

import (
	"strconv"
	"strings"
)

const ResourceNetworkProjection = 2

type NetworkKind string

const (
	NetworkService      NetworkKind = "Service"
	NetworkSlice        NetworkKind = "EndpointSlice"
	NetworkIngress      NetworkKind = "Ingress"
	NetworkIngressClass NetworkKind = "IngressClass"
	NetworkPolicy       NetworkKind = "NetworkPolicy"
	NetworkHTTPRoute    NetworkKind = "HTTPRoute"
)

type Tri int8

const (
	TriUnknown Tri = iota
	TriTrue
	TriFalse
)

func TriOf(v bool) Tri {
	if v {
		return TriTrue
	}
	return TriFalse
}

type Label struct {
	Key, Value string
}

type Labels []Label

func (l Labels) Get(key string) (string, bool) {
	for _, p := range l {
		if p.Key == key {
			return p.Value, true
		}
	}
	return "", false
}

func (l Labels) String() string {
	parts := make([]string, len(l))
	for i, p := range l {
		parts[i] = p.Key + "=" + p.Value
	}
	return strings.Join(parts, ",")
}

type ServicePort struct {
	Name        string
	Port        int
	Protocol    string
	Target      string
	NodePort    int
	AppProtocol string
}

type LoadBalancerAddress struct {
	IP       string
	Hostname string
}

type ServiceFacts struct {
	Type            string
	ClusterIPs      []string
	ExternalIPs     []string
	ExternalName    string
	Selector        Labels
	Ports           []ServicePort
	LoadBalancer    []LoadBalancerAddress
	PublishNotReady bool
}

func (s ServiceFacts) Headless() bool {
	return len(s.ClusterIPs) == 0 || s.ClusterIPs[0] == "None"
}

type SliceEndpoint struct {
	Addresses   []string
	Ready       Tri
	Serving     Tri
	Terminating Tri
	TargetKind  string
	TargetName  string
	Node        string
}

type SlicePort struct {
	Name     string
	Port     int
	Protocol string
}

type SliceFacts struct {
	Service     string
	AddressType string
	Ports       []SlicePort
	Endpoints   []SliceEndpoint
	Total       int
	Ready       int
}

type IngressBackend struct {
	Service    string
	PortName   string
	PortNumber int
	Resource   string
}

func (b IngressBackend) Port() string {
	if b.PortName != "" {
		return b.PortName
	}
	if b.PortNumber > 0 {
		return strconv.Itoa(b.PortNumber)
	}
	return ""
}

func (b IngressBackend) String() string {
	if b.Resource != "" {
		return b.Resource
	}
	if port := b.Port(); port != "" {
		return b.Service + ":" + port
	}
	return b.Service
}

type IngressPath struct {
	Path    string
	Type    string
	Backend IngressBackend
}

type IngressRule struct {
	Host  string
	Paths []IngressPath
}

type IngressTLS struct {
	Hosts  []string
	Secret string
}

type IngressFacts struct {
	Class           string
	ClassAnnotation string
	Default         *IngressBackend
	Rules           []IngressRule
	TLS             []IngressTLS
	Addresses       []LoadBalancerAddress
}

type IngressClassFacts struct {
	Controller string
	Default    bool
}

type SelectorRequirement struct {
	Key      string
	Operator string
	Values   []string
}

type NetworkPolicyFacts struct {
	MatchLabels  Labels
	Expressions  []SelectorRequirement
	Types        []string
	IngressRules int
	EgressRules  int
}

type HTTPRouteFacts struct {
	Hostnames []string
	Parents   []string
	Backends  []string
	Rules     int
	Accepted  Tri
}

type PodFacts struct {
	Labels      Labels
	Ready       Tri
	Phase       string
	Terminating bool
}

type NodeFacts struct {
	Ready    Tri
	Internal []string
	External []string
}

type TemplateFacts struct {
	Labels Labels
}

type NetworkFacts struct {
	Service   *ServiceFacts
	Slice     *SliceFacts
	Ingress   *IngressFacts
	Class     *IngressClassFacts
	Policy    *NetworkPolicyFacts
	Route     *HTTPRouteFacts
	Pod       *PodFacts
	Node      *NodeFacts
	Template  *TemplateFacts
	Truncated bool
	Bytes     int
}

func (f *NetworkFacts) searchText() []string {
	if f == nil {
		return nil
	}
	var out []string
	if s := f.Service; s != nil {
		out = append(out, s.Type, s.ExternalName)
		out = append(out, s.ClusterIPs...)
		for _, a := range s.LoadBalancer {
			out = append(out, a.IP, a.Hostname)
		}
		for _, p := range s.Ports {
			out = append(out, p.Name, strconv.Itoa(p.Port))
		}
	}
	if s := f.Slice; s != nil {
		out = append(out, s.Service)
	}
	if i := f.Ingress; i != nil {
		out = append(out, i.Class, i.ClassAnnotation)
		for _, r := range i.Rules {
			out = append(out, r.Host)
			for _, p := range r.Paths {
				out = append(out, p.Path, p.Backend.Service)
			}
		}
	}
	if c := f.Class; c != nil {
		out = append(out, c.Controller)
	}
	if p := f.Policy; p != nil {
		out = append(out, p.Types...)
	}
	if r := f.Route; r != nil {
		out = append(out, r.Hostnames...)
		out = append(out, r.Parents...)
	}
	return out
}

func (r ResourceRow) Matches(query string) bool {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return true
	}
	fields := append([]string{r.Ref.Namespace + "/" + r.Ref.Name, string(r.Workload.State)}, r.Network.searchText()...)
	hay := strings.ToLower(strings.Join(fields, "\n"))
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func SameResourceRow(a, b ResourceRow) bool {
	return a.Ref == b.Ref && a.Workload == b.Workload && a.Truncated == b.Truncated && (a.Network == nil) == (b.Network == nil)
}
