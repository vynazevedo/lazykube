package insight

import "time"

type machineSignal struct {
	ID              string
	State           SignalState
	Severity        SignalSeverity
	PeakSeverity    SignalSeverity
	Preexisting     bool
	FirstObservedAt time.Time
	LastObservedAt  time.Time
	ResolvedAt      time.Time
	Coverage        Coverage
	Suspension      SuspensionReason
	Evidence        SignalEvidence
}

func (m *signalMachine) export(key signalKey, config string) Signal {
	return Signal{
		ID: m.signal.ID, Rule: key.Rule, RuleVersion: OperationalRuleVersion, ConfigHash: config,
		Entity: EntityRef{EntityKey: *key.Entity, ResourceVersion: m.rv}, Container: key.Container, ContainerKind: key.Kind,
		State: m.signal.State, Severity: m.signal.Severity, PeakSeverity: m.signal.PeakSeverity, Preexisting: m.signal.Preexisting,
		FirstObservedAt: m.signal.FirstObservedAt, LastObservedAt: m.signal.LastObservedAt, ResolvedAt: m.signal.ResolvedAt,
		Coverage: m.signal.Coverage, Suspension: m.signal.Suspension, Evidence: m.signal.Evidence,
	}
}
