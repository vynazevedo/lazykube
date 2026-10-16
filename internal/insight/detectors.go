package insight

func (e *Engine) evaluate(newBatch bool) {
	present := make(map[EntityKey]bool, len(e.pods))
	containers := make(map[signalKey]bool)
	for _, p := range e.pods {
		present[p.Entity.EntityKey] = true
		e.podRules(p, newBatch)
		for _, c := range p.Containers {
			containers[signalKey{Entity: p.Entity.EntityKey, Container: c.Name, Kind: c.Kind}] = true
			e.containerRules(p, c, newBatch)
		}
	}
	for key, m := range e.machines {
		if !present[key.Entity] {
			delete(e.machines, key)
			continue
		}
		container := key
		container.Rule = ""
		if key.Container != "" && !containers[container] {
			m.suspend(SuspensionMissingStatus, Coverage{State: CoveragePartial, Reason: "container not observed"})
		}
	}
}

func (e *Engine) podRules(p PodObservation, newBatch bool) {
	m, fresh, baseline := e.machine(p, ContainerObservation{}, RulePending, newBatch)
	if p.Phase == PodUnknown {
		m.suspend(SuspensionMissingStatus, Coverage{State: CoveragePartial, Reason: "pod phase unknown"})
	} else if !m.suspended {
		pending := p.Phase == PodPending
		for _, c := range p.Containers {
			pending = pending || c.StatusObserved && c.Phase == ContainerWaiting && c.Reason == ReasonContainerCreating && p.Phase != PodSucceeded && p.Phase != PodFailed
		}
		e.persistent(m, pending, !pending, fresh, baseline, e.config.PendingWarning, e.config.PendingHigh, 1, SignalWarning)
		m.consumed(p)
	}
	m, fresh, baseline = e.machine(p, ContainerObservation{}, RuleEvicted, newBatch)
	if p.Phase == PodUnknown {
		m.suspend(SuspensionMissingStatus, Coverage{State: CoveragePartial, Reason: "pod phase unknown"})
		return
	}
	if m.suspended {
		return
	}
	evicted := p.Reason == ReasonEvicted
	if fresh && evicted && (!m.wasEvicted || baseline) {
		if !baseline || !m.seen {
			e.begin(m, baseline)
			m.activate(SignalWarning)
		}
		m.lastEviction = e.now
		m.signal.Evidence.TransitionObservedAt = e.now
		m.signal.Evidence.Reason = ReasonEvicted
	}
	if (m.signal.State == SignalActive || m.signal.State == SignalRecovering) && e.now.Sub(m.lastEviction) >= e.config.EvictionQuiet {
		m.signal.State = SignalResolved
		m.signal.ResolvedAt = e.now
	}
	m.wasEvicted = evicted
	m.consumed(p)
}

func (e *Engine) containerRules(p PodObservation, c ContainerObservation, newBatch bool) {
	healthy := c.Phase == ContainerRunning && c.Ready == TruthTrue
	for _, rule := range []Rule{RuleRestarts, RuleCrashLoop, RuleOOM, RuleImagePull} {
		m, fresh, baseline := e.machine(p, c, rule, newBatch)
		if !c.StatusObserved || c.Phase == ContainerUnknown {
			m.suspend(SuspensionMissingStatus, Coverage{State: CoveragePartial, Reason: "container status not observed"})
			continue
		}
		if m.suspended {
			continue
		}
		switch rule {
		case RuleRestarts:
			e.restarts(m, c, fresh, baseline)
		case RuleCrashLoop:
			m.signal.Evidence.Reason = c.Reason
			e.persistent(m, c.Phase == ContainerWaiting && c.Reason == ReasonCrashLoopBackOff, healthy, fresh, baseline, e.config.CrashLoopDelay, 0, e.config.CrashLoopObservations, SignalHigh)
		case RuleImagePull:
			m.signal.Evidence.Reason = c.Reason
			bad := c.Phase == ContainerWaiting && (c.Reason == ReasonImagePullBackOff || c.Reason == ReasonErrImagePull)
			e.persistent(m, bad, healthy, fresh, baseline, e.config.ImagePullWarning, e.config.ImagePullHigh, 1, SignalWarning)
		case RuleOOM:
			e.oom(m, c, fresh, baseline, healthy)
		}
		if !m.suspended {
			m.consumed(p)
		} else {
			m.rv = p.Entity.ResourceVersion
			m.seen = true
		}
	}
}

func (e *Engine) restarts(m *signalMachine, c ContainerObservation, fresh, baseline bool) {
	kept := m.deltas[:0]
	for _, d := range m.deltas {
		if e.now.Sub(d.at) < e.config.RestartWindow {
			kept = append(kept, d)
		}
	}
	m.deltas = kept
	if c.RestartCount < 0 {
		m.suspend(SuspensionInvalidCounter, Coverage{State: CoveragePartial, Reason: "negative restart counter"})
		return
	}
	if fresh {
		if baseline || !m.counterKnown {
			m.counter = c.RestartCount
			m.signal.Evidence.CounterBefore = c.RestartCount
			m.signal.Evidence.CounterAfter = c.RestartCount
			m.counterKnown = true
		} else if c.RestartCount < m.counter {
			m.signal.Evidence.CounterBefore = m.counter
			m.signal.Evidence.CounterAfter = c.RestartCount
			m.suspend(SuspensionCounterReset, Coverage{State: CoveragePartial, Reason: "restart counter decreased; new baseline required"})
			m.counter = c.RestartCount
			return
		} else if c.RestartCount > m.counter {
			if len(m.deltas) >= e.config.MaxRestartDeltas {
				m.suspend(SuspensionBufferLimit, Coverage{State: CoveragePartial, Reason: "restart delta limit exceeded"})
				return
			}
			m.signal.Evidence.CounterBefore = m.counter
			m.signal.Evidence.CounterAfter = c.RestartCount
			m.deltas = append(m.deltas, restartDelta{at: e.now, count: int64(c.RestartCount) - int64(m.counter)})
			m.lastRestart = e.now
			m.counter = c.RestartCount
		}
	}
	var total int64
	for _, d := range m.deltas {
		total += d.count
	}
	m.signal.Evidence.WindowRestarts = total
	if total >= int64(e.config.RestartWarning) {
		e.begin(m, false)
		severity := SignalWarning
		if total >= int64(e.config.RestartHigh) {
			severity = SignalHigh
		}
		m.activate(severity)
	} else if (m.signal.State == SignalActive || m.signal.State == SignalRecovering) && e.now.Sub(m.lastRestart) >= e.config.RestartWindow {
		m.signal.State = SignalResolved
		m.signal.ResolvedAt = e.now
	}
}

func (e *Engine) oom(m *signalMachine, c ContainerObservation, fresh, baseline, healthy bool) {
	if c.RestartCount < 0 {
		m.suspend(SuspensionInvalidCounter, Coverage{State: CoveragePartial, Reason: "negative restart counter"})
		return
	}
	if fresh && m.counterKnown && c.RestartCount < m.counter {
		m.suspend(SuspensionCounterReset, Coverage{State: CoveragePartial, Reason: "restart counter decreased; OOM baseline uncertain"})
		return
	}
	m.counterKnown = true
	m.counter = c.RestartCount
	var termination *Termination
	for _, t := range []*Termination{c.CurrentTermination, c.LastTermination} {
		if t == nil || t.Reason != ReasonOOMKilled {
			continue
		}
		if t.FinishedAt.IsZero() || t.FinishedAt.After(e.now.Add(e.config.FutureTolerance)) {
			m.suspend(SuspensionInvalidTimestamp, Coverage{State: CoveragePartial, Reason: "OOM timestamp absent or in the future"})
			return
		}
		if termination == nil || t.FinishedAt.After(termination.FinishedAt) {
			termination = t
		}
	}
	if termination != nil && fresh && (!m.termKnown || termination.FinishedAt.After(m.termAt)) {
		preexisting := baseline || termination.FinishedAt.Before(m.firstSeen) && !m.termKnown
		e.begin(m, preexisting)
		m.activate(SignalHigh)
		m.termAt = termination.FinishedAt
		m.termKnown = true
		m.lastOOM = e.now
		m.healthy = false
		m.signal.Evidence.EventAt = termination.FinishedAt
		m.signal.Evidence.ExitCode = termination.ExitCode
		m.signal.Evidence.Reason = ReasonOOMKilled
	}
	if baseline && termination != nil {
		m.termAt = termination.FinishedAt
		m.termKnown = true
	}
	e.recover(m, healthy, e.now.Sub(m.lastOOM) >= e.config.OOMQuiet)
}
