package data

import "fmt"

type ContainerKind int

const (
	ContainerApp ContainerKind = iota
	ContainerInit
	ContainerSidecar
	ContainerEphemeral
)

func (k ContainerKind) String() string {
	switch k {
	case ContainerInit:
		return "init"
	case ContainerSidecar:
		return "sidecar"
	case ContainerEphemeral:
		return "ephemeral"
	default:
		return "app"
	}
}

type ContainerPhase int

const (
	PhaseWaiting ContainerPhase = iota
	PhaseRunning
	PhaseTerminated
)

func (p ContainerPhase) String() string {
	switch p {
	case PhaseRunning:
		return "running"
	case PhaseTerminated:
		return "terminated"
	default:
		return "waiting"
	}
}

type Termination struct {
	Reason   string
	Message  string
	ExitCode int
	Signal   int
	Finished string
	Age      string
}

func (t Termination) Summary() string {
	reason := t.Reason
	if reason == "" {
		reason = "Terminated"
	}
	out := fmt.Sprintf("%s · exit %d", reason, t.ExitCode)
	if t.Signal != 0 {
		out += fmt.Sprintf(" · signal %d", t.Signal)
	}
	switch {
	case t.Age != "" && t.Finished != "":
		out += " · " + t.Age + " ago (" + t.Finished + ")"
	case t.Finished != "":
		out += " · finished " + t.Finished
	}
	return out
}

type Container struct {
	Name     string
	Image    string
	Kind     ContainerKind
	Phase    ContainerPhase
	Reason   string
	Message  string
	Ready    bool
	Started  bool
	Restarts int
	Since    string
	Current  *Termination
	Last     *Termination
}

func (c Container) HasPrevious() bool {
	return c.Last != nil
}

type FeedState int

const (
	FeedReady FeedState = iota
	FeedLoading
	FeedUnavailable
	FeedForbidden
)

type LogRequest struct {
	Pod       string
	Container string
	Previous  bool
}

type LogView struct {
	Lines     []LogLine
	Container string
	Previous  bool
	State     FeedState
	Reason    string
}

type EventScope int

const (
	ScopePod EventScope = iota
	ScopeNamespace
	ScopeCluster
)

func EventScopes() []EventScope {
	return []EventScope{ScopePod, ScopeNamespace, ScopeCluster}
}

func (s EventScope) String() string {
	switch s {
	case ScopeNamespace:
		return "namespace"
	case ScopeCluster:
		return "cluster"
	default:
		return "pod"
	}
}

func (s EventScope) Next() EventScope {
	return EventScope((int(s) + 1) % len(EventScopes()))
}

type EventQuery struct {
	Scope     EventScope
	Pod       string
	Namespace string
}

type EventFeed struct {
	Scope  EventScope
	Target string
	Items  []Event
	State  FeedState
	Reason string
}

type Diagnostics interface {
	Containers(pod string) []Container
	PodLogs(req LogRequest, tick, n int) LogView
	ScopedEvents(q EventQuery) EventFeed
}

type basicDiagnostics struct {
	src Source
}

func DiagnosticsFor(src Source) Diagnostics {
	if d, ok := src.(Diagnostics); ok {
		return d
	}
	return basicDiagnostics{src: src}
}

func (b basicDiagnostics) Containers(string) []Container {
	return nil
}

func (b basicDiagnostics) PodLogs(req LogRequest, tick, n int) LogView {
	if req.Previous {
		return LogView{Previous: true, State: FeedUnavailable, Reason: "previous indisponível nesta fonte"}
	}
	return LogView{Lines: b.src.Logs(req.Pod, tick, n)}
}

func (b basicDiagnostics) ScopedEvents(q EventQuery) EventFeed {
	if q.Scope != ScopePod {
		return EventFeed{Scope: q.Scope, Target: q.Namespace, State: FeedUnavailable, Reason: "escopo indisponível nesta fonte"}
	}
	return EventFeed{Scope: ScopePod, Target: q.Pod, Items: b.src.Events(q.Pod)}
}
