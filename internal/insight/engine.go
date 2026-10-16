package insight

import (
	"cmp"
	"errors"
	"slices"
	"time"
)

type signalKey struct {
	Entity    EntityKey
	Container string
	Kind      ContainerKind
	Rule      Rule
}

type restartDelta struct {
	at    time.Time
	count int64
}

type signalMachine struct {
	firstSeen    time.Time
	signal       Signal
	episode      uint64
	seen         bool
	rv           string
	priorState   SignalState
	suspended    bool
	problem      bool
	since        time.Time
	observations int
	healthy      bool
	healthySince time.Time
	counterKnown bool
	counter      int32
	deltas       []restartDelta
	lastRestart  time.Time
	termKnown    bool
	termAt       time.Time
	lastOOM      time.Time
	wasEvicted   bool
	lastEviction time.Time
	rebaseline   bool
}

type Engine struct {
	clock       Clock
	config      OperationalConfig
	configHash  string
	cursor      Cursor
	initialized bool
	coverage    Coverage
	pods        []PodObservation
	machines    map[signalKey]*signalMachine
	now         time.Time
	blocked     bool
}

func NewEngine(clock Clock, config OperationalConfig) (*Engine, error) {
	if clock == nil {
		return nil, errors.New("clock is required")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	return &Engine{clock: clock, config: config, configHash: operationalHash(config), machines: make(map[signalKey]*signalMachine)}, nil
}

func (e *Engine) Step(batch ObservationBatch) SignalSnapshot {
	now := e.clock.Now()
	if now.Before(e.now) {
		now = e.now
	}
	e.now = now
	sourceChanged := e.initialized && e.cursor.Source != batch.Cursor.Source
	if sourceChanged {
		e.machines = make(map[signalKey]*signalMachine)
		e.pods = nil
		e.initialized = false
		e.blocked = false
	}
	if e.initialized && batch.Cursor.Source == e.cursor.Source && batch.Cursor.Revisions.Inventory < e.cursor.Revisions.Inventory {
		return e.Snapshot()
	}
	same := e.initialized && batch.Cursor == e.cursor
	reset := batch.Reset && e.initialized && !same
	gap := reset || batch.SkippedInventoryRevisions > 0 || e.initialized && batch.Cursor.Revisions.Inventory > e.cursor.Revisions.Inventory+1
	if same && e.blocked {
		return e.Snapshot()
	}
	e.coverage = batch.Coverage.Inventory
	if e.coverage.State != CoverageComplete {
		e.cursor = batch.Cursor
		e.initialized = true
		e.blocked = true
		e.suspendAll(SuspensionSourceCoverage, e.coverage)
		return e.Snapshot()
	}
	if reset && batch.FullInventory {
		present := make(map[EntityKey]bool, len(batch.Pods))
		for _, p := range batch.Pods {
			present[p.Entity.EntityKey] = true
		}
		for key := range e.machines {
			if !present[key.Entity] {
				delete(e.machines, key)
			}
		}
		e.pods = nil
	}
	if gap && !same {
		e.cursor = batch.Cursor
		e.initialized = true
		e.blocked = true
		e.coverage = Coverage{State: CoveragePartial, Reason: "inventory revisions skipped"}
		e.suspendAll(SuspensionRevisionGap, e.coverage)
		return e.Snapshot()
	}
	if !same && batch.FullInventory {
		e.pods = make([]PodObservation, len(batch.Pods))
		for i, p := range batch.Pods {
			e.pods[i] = clonePod(p)
		}
		e.blocked = false
	} else if e.blocked || !e.initialized || !same {
		return e.Snapshot()
	}
	e.cursor = batch.Cursor
	e.initialized = true
	e.evaluate(!same)
	return e.Snapshot()
}

func (e *Engine) Advance() SignalSnapshot {
	now := e.clock.Now()
	if now.After(e.now) {
		e.now = now
	}
	if e.initialized && !e.blocked && e.coverage.State == CoverageComplete {
		e.evaluate(false)
	}
	return e.Snapshot()
}

func (e *Engine) Snapshot() SignalSnapshot {
	out := SignalSnapshot{Source: e.cursor.Source, ConfigHash: e.configHash, RuleVersion: OperationalRuleVersion, Coverage: e.coverage, Signals: make([]Signal, 0, len(e.machines))}
	for _, m := range e.machines {
		out.Signals = append(out.Signals, m.signal)
	}
	slices.SortFunc(out.Signals, func(a, b Signal) int {
		if n := cmp.Compare(b.Severity, a.Severity); n != 0 {
			return n
		}
		if n := b.LastObservedAt.Compare(a.LastObservedAt); n != 0 {
			return n
		}
		return cmp.Compare(a.ID, b.ID)
	})

	out.TotalSignals = len(out.Signals)
	for _, rule := range []Rule{RuleRestarts, RuleCrashLoop, RuleOOM, RuleImagePull, RulePending, RuleEvicted} {
		count := SignalCount{Rule: rule}
		for i, signal := range out.Signals {
			if signal.Rule == rule {
				count.Total++
				if i >= MaxOperationalSignals {
					count.Omitted++
				}
			}
		}
		out.Counts = append(out.Counts, count)
	}
	if len(out.Signals) > MaxOperationalSignals {
		out.OmittedSignals = len(out.Signals) - MaxOperationalSignals
		limited := make([]Signal, MaxOperationalSignals)
		copy(limited, out.Signals)
		out.Signals = limited
	}
	return out
}

func (e *Engine) suspendAll(reason SuspensionReason, coverage Coverage) {
	for _, m := range e.machines {
		m.suspend(reason, coverage)
	}
}

func (m *signalMachine) suspend(reason SuspensionReason, coverage Coverage) {
	if !m.suspended {
		m.priorState = m.signal.State
	}
	m.suspended = true
	m.signal.State = SignalSuspended
	m.signal.Coverage = coverage
	m.signal.Suspension = reason
	m.healthy = false
	m.problem = false
	m.observations = 0
	m.deltas = nil
	m.signal.Evidence.WindowRestarts = 0
	m.counterKnown = false
	m.rebaseline = true
}

func (e *Engine) machine(p PodObservation, c ContainerObservation, rule Rule, newBatch bool) (*signalMachine, bool, bool) {
	key := signalKey{Entity: p.Entity.EntityKey, Container: c.Name, Kind: c.Kind, Rule: rule}
	m := e.machines[key]
	if m == nil {
		m = &signalMachine{firstSeen: e.now, signal: Signal{Rule: rule, RuleVersion: OperationalRuleVersion, ConfigHash: e.configHash, Entity: p.Entity, Container: c.Name, ContainerKind: c.Kind, Coverage: e.coverage}}
		m.signal.ID = operationalSignalID(key, e.configHash, 0)
		e.machines[key] = m
	}
	fresh := !m.seen || m.rv != p.Entity.ResourceVersion
	baseline := !m.seen || m.rebaseline
	if m.suspended {
		if !newBatch {
			return m, false, baseline
		}
		m.suspended = false
		m.signal.Suspension = SuspensionNone
		m.signal.State = SignalNormal
		if m.priorState == SignalActive || m.priorState == SignalRecovering {
			m.signal.State = SignalActive
		}
		m.lastRestart = e.now
		m.lastOOM = e.now
		m.lastEviction = e.now
		fresh = true
	}
	m.signal.Entity = p.Entity
	m.signal.Coverage = e.coverage
	m.signal.Evidence.NodeName = p.NodeName
	m.signal.Evidence.Phase = p.Phase
	m.signal.Evidence.CreatedAt = p.CreatedAt
	if fresh {
		m.signal.LastObservedAt = e.now
		m.signal.Evidence.ReceivedAt = p.ReceivedAt
	}
	return m, fresh, baseline
}

func (m *signalMachine) consumed(p PodObservation) {
	m.rv = p.Entity.ResourceVersion
	m.seen = true
	m.rebaseline = false
}

func (e *Engine) begin(m *signalMachine, preexisting bool) {
	if m.signal.State == SignalNormal || m.signal.State == SignalResolved {
		m.episode++
		key := signalKey{Entity: m.signal.Entity.EntityKey, Container: m.signal.Container, Kind: m.signal.ContainerKind, Rule: m.signal.Rule}
		m.signal.ID = operationalSignalID(key, e.configHash, m.episode)
		m.signal.FirstObservedAt = e.now
		m.signal.ResolvedAt = time.Time{}
		m.signal.Preexisting = preexisting
		m.signal.PeakSeverity = SignalInfo
		m.signal.Severity = SignalInfo
		m.signal.State = SignalCandidate
	}
}

func (m *signalMachine) activate(severity SignalSeverity) {
	m.signal.State = SignalActive
	m.signal.Severity = max(m.signal.Severity, severity)
	m.signal.PeakSeverity = max(m.signal.PeakSeverity, severity)
}

func (e *Engine) recover(m *signalMachine, healthy bool, quiet bool) {
	if m.signal.State != SignalActive && m.signal.State != SignalRecovering {
		return
	}
	if !healthy {
		m.healthy = false
		m.signal.State = SignalActive
		return
	}
	if !m.healthy {
		m.healthy = true
		m.healthySince = e.now
	}
	m.signal.State = SignalRecovering
	if quiet && e.now.Sub(m.healthySince) >= e.config.Recovery {
		m.signal.State = SignalResolved
		m.signal.ResolvedAt = e.now
	}
}

func (e *Engine) persistent(m *signalMachine, bad, healthy, fresh, baseline bool, warning, high time.Duration, minimum int, severity SignalSeverity) {
	if bad {
		if m.signal.State == SignalRecovering {
			m.activate(m.signal.Severity)
		}
		if !m.problem {
			m.problem = true
			m.since = e.now
			m.observations = 0
			e.begin(m, baseline)
		}
		m.healthy = false
		if fresh {
			m.observations++
		}
		duration := e.now.Sub(m.since)
		m.signal.Evidence.Duration = duration
		m.signal.Evidence.Observations = m.observations
		if duration >= warning && m.observations >= minimum {
			if high > 0 && duration >= high {
				severity = SignalHigh
			}
			m.activate(severity)
		}
		return
	}
	m.problem = false
	m.observations = 0
	if m.signal.State == SignalCandidate {
		m.signal.State = SignalNormal
	}
	e.recover(m, healthy, true)
}
