package data

import "regexp"

const AllNamespacesLabel = "Todos"

type KubeContext struct {
	Name      string
	Cluster   string
	User      string
	Namespace string
	Current   bool
	Problem   string
}

type ContextCatalog struct {
	Contexts  []KubeContext
	Current   string
	Synthetic bool
}

func (c ContextCatalog) Find(name string) (KubeContext, bool) {
	for _, k := range c.Contexts {
		if k.Name == name {
			return k, true
		}
	}
	return KubeContext{}, false
}

type Target struct {
	Context   string
	Namespace string
}

func (t Target) NamespaceLabel() string {
	if t.Namespace == "" {
		return AllNamespacesLabel
	}
	return t.Namespace
}

type EffectiveMode int

const ModeReadOnly EffectiveMode = iota

type SessionRef struct {
	Context    string
	Namespace  string
	Generation uint64
	Mode       EffectiveMode
}

var namespacePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func ValidNamespace(name string) bool {
	return namespacePattern.MatchString(name)
}
