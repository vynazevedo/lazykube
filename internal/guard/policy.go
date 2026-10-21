package guard

import (
	"context"
	"strings"
	"time"
)

type Action uint8

const (
	ActionUnknown Action = iota
	Delete
	Scale
	Restart
	Edit
	Exec
	PortForward
)

type Reason string

const (
	Invalid              Reason = "invalid_request"
	ReadOnly             Reason = "read_only"
	InvalidConfig        Reason = "invalid_config"
	Denied               Reason = "access_denied"
	Unavailable          Reason = "unavailable"
	Unsupported          Reason = "unsupported"
	Stale                Reason = "stale_target"
	Canceled             Reason = "canceled"
	AuditFailed          Reason = "audit_failed"
	DryRunFailed         Reason = "dry_run_failed"
	ConfirmationRequired Reason = "confirmation_required"
	Unknown              Reason = "outcome_unknown"
	Succeeded            Reason = "succeeded"
)

func (r Reason) Error() string { return string(r) }

type Environment struct{ ReadOnly, Protected, Valid bool }
type Session struct {
	Context     string
	Namespace   string
	Identity    string
	Generation  uint64
	Environment Environment
}
type Mode struct{ ReadOnly, Protected, ConfigValid bool }
type Policy struct{ allowWrites bool }

func NewPolicy(allowWrites bool) Policy { return Policy{allowWrites: allowWrites} }
func (p Policy) Mode(s Session) Mode {
	if !s.Environment.Valid {
		return Mode{ReadOnly: true, Protected: true}
	}
	return Mode{ReadOnly: !p.allowWrites || s.Environment.ReadOnly || s.Environment.Protected, Protected: s.Environment.Protected, ConfigValid: true}
}
func DefaultEnvironment(name string) Environment {
	return Environment{Valid: name != ""}
}

type Target struct{ Group, Version, Resource, Namespace, Name, UID, ResourceVersion string }
type Parameters struct {
	Replicas              int32
	Container             string
	Command               []string
	LocalPort, RemotePort uint16
	Address               string
	Document              []byte
	GraceSeconds          int64
	Propagation           string
}
type Request struct {
	Action     Action
	Target     Target
	Parameters Parameters
}
type Access struct{ Group, Version, Resource, Subresource, Namespace, Name, Verb string }
type Decision struct {
	HighRisk, DryRun bool
	Mode             Mode
}

func (p Policy) Evaluate(s Session, r Request) (Decision, error) {
	d := Decision{Mode: p.Mode(s)}
	if !d.Mode.ConfigValid {
		return d, InvalidConfig
	}
	if d.Mode.ReadOnly {
		return d, ReadOnly
	}
	t := r.Target
	if s.Context == "" || s.Identity == "" || s.Generation == 0 || t.Version == "" || t.Resource == "" || t.Name == "" || t.UID == "" || t.ResourceVersion == "" || strings.Contains(t.Resource, "/") || strings.ContainsAny(t.Name, "/\\") || s.Namespace != "" && t.Namespace != s.Namespace {
		return d, Invalid
	}
	if !SupportsAction(r.Action, t) {
		return d, Unsupported
	}
	switch r.Action {
	case Delete:
		d.HighRisk, d.DryRun = true, true
		if r.Parameters.GraceSeconds < 0 || r.Parameters.Propagation != "Background" && r.Parameters.Propagation != "Foreground" && r.Parameters.Propagation != "Orphan" {
			return d, Invalid
		}
	case Scale:
		if t.Group != "apps" || t.Resource != "deployments" && t.Resource != "statefulsets" && t.Resource != "replicasets" || r.Parameters.Replicas < 0 {
			return d, Invalid
		}
		d.HighRisk = r.Parameters.Replicas == 0
		d.DryRun = true
	case Restart:
		if t.Group != "apps" || t.Resource != "deployments" && t.Resource != "statefulsets" && t.Resource != "daemonsets" {
			return d, Invalid
		}
		d.DryRun = true
	case Edit:
		if len(r.Parameters.Document) == 0 || len(r.Parameters.Document) > 1<<20 || t.Resource == "secrets" {
			return d, Unsupported
		}
		d.HighRisk, d.DryRun = true, true
	case Exec:
		if t.Group != "" || t.Resource != "pods" || r.Parameters.Container == "" || len(r.Parameters.Command) == 0 {
			return d, Invalid
		}
		d.HighRisk = true
	case PortForward:
		if t.Group != "" || t.Resource != "pods" || r.Parameters.LocalPort == 0 || r.Parameters.RemotePort == 0 || r.Parameters.Address != "127.0.0.1" && r.Parameters.Address != "::1" {
			return d, Invalid
		}
	default:
		return d, Unsupported
	}
	if d.Mode.Protected {
		d.HighRisk = true
	}
	return d, nil
}

func Requirements(r Request) []Access {
	if !SupportsAction(r.Action, r.Target) {
		return nil
	}
	t := r.Target
	a := Access{Group: t.Group, Version: t.Version, Resource: t.Resource, Namespace: t.Namespace, Name: t.Name, Verb: "get"}
	write := a
	switch r.Action {
	case Delete:
		write.Verb = "delete"
	case Scale:
		a.Subresource = "scale"
		write.Subresource = "scale"
		write.Verb = "update"
	case Restart:
		write.Verb = "patch"
	case Edit:
		write.Verb = "update"
	case Exec:
		write.Subresource = "exec"
		write.Verb = "create"
	case PortForward:
		write.Subresource = "portforward"
		write.Verb = "create"
	default:
		return nil
	}
	return []Access{a, write}
}

type Clock interface{ Now() time.Time }
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type Authorizer interface {
	Allowed(context.Context, Access) (bool, error)
}
type Backend interface {
	Supported(Action) bool
	Validate(context.Context, Request) error
	Send(context.Context, Request, bool) error
}
type Auditor interface{ Record(AuditEvent) error }
