package app

import (
	"slices"
	"strings"
)

const nsSetSeparator = "\x00"

type nsSet string

func (s nsSet) names() []string {
	if s == "" {
		return nil
	}
	return strings.Split(string(s), nsSetSeparator)
}

func (s nsSet) toggle(name string) nsSet {
	names := s.names()
	if i := slices.Index(names, name); i >= 0 {
		names = slices.Delete(names, i, i+1)
	} else {
		names = append(names, name)
		slices.Sort(names)
	}
	return nsSet(strings.Join(names, nsSetSeparator))
}

func (s nsSet) single() (string, bool) {
	if s == "" || strings.Contains(string(s), nsSetSeparator) {
		return "", false
	}
	return string(s), true
}

func (s nsSet) label() string {
	return strings.Join(s.names(), ", ")
}

func (s nsSet) matcher() func(string) bool {
	if s == "" {
		return func(string) bool { return true }
	}
	if name, ok := s.single(); ok {
		return func(ns string) bool { return ns == name }
	}
	allowed := make(map[string]struct{})
	for _, name := range s.names() {
		allowed[name] = struct{}{}
	}
	return func(ns string) bool {
		_, ok := allowed[ns]
		return ok
	}
}
