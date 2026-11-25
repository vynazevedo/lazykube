package app

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

type securityView struct {
	RuleVersion string
	Scope       nsSet
	NoSystem    bool
}

type securityKey struct {
	securityView
	Revision uint64
	Pods     int
}

type securityResult struct {
	key       securityKey
	all       data.SecurityAggregate
	noSystem  data.SecurityAggregate
	workloads []data.WorkloadPosture
}

type securityJob struct {
	ctx      context.Context
	key      securityKey
	snapshot data.SecuritySnapshot
	dash     data.SecurityDashboard
	selected data.WorkloadKey
	cursor   int
	known    *securityResult
}

type securityCalc struct {
	seq     uint64
	running bool
	job     securityJob
	cancel  context.CancelFunc
}

type securityCalcMsg struct {
	session  uint64
	seq      uint64
	canceled bool
	result   securityResult
	rules    []data.WorkloadRule
	rulesFor data.WorkloadKey
}

func (m Model) wantedSecurityKey() securityKey {
	scope := m.securityScope()
	return securityKey{
		securityView: securityView{RuleVersion: m.security.RuleVersion, Scope: scope, NoSystem: m.sec.NoSystem && scope == ""},
		Revision:     m.security.Revision,
		Pods:         len(m.security.Pods),
	}
}

func scopedAggregate(d data.SecurityDashboard, scope nsSet) data.SecurityAggregate {
	names := scope.names()
	switch len(names) {
	case 0:
		return d.Cluster
	case 1:
		if a, ok := d.Namespaces[names[0]]; ok {
			return a
		}
		return data.SecurityAggregate{Name: names[0], Score: data.PostureScore{Coverage: data.Coverage{State: data.CoverageUnavailable, Reason: "namespace não observado"}}}
	}
	return d.MergeNamespaces(names)
}

func selectedWorkload(workloads []data.WorkloadPosture, selected data.WorkloadKey, cursor int) int {
	if i := slices.IndexFunc(workloads, func(w data.WorkloadPosture) bool { return w.Key == selected }); i >= 0 {
		return i
	}
	return max(0, min(cursor, len(workloads)-1))
}

func (j securityJob) page() securityResult {
	allowed := j.key.Scope.matcher()
	live := func() bool { return j.ctx.Err() == nil }
	r := securityResult{key: j.key, all: scopedAggregate(j.dash, j.key.Scope)}
	r.noSystem = data.AggregateSecurity(data.SubsetSecurity(j.snapshot, func(p data.PodPosture) bool {
		return live() && !data.IsSystemNamespace(p.Target.Namespace)
	})).Cluster
	r.workloads = data.AggregateWorkloads(j.snapshot, func(p data.PodPosture) bool {
		return live() && allowed(p.Target.Namespace) && (!j.key.NoSystem || !data.IsSystemNamespace(p.Target.Namespace))
	})
	return r
}

func (j securityJob) run() securityCalcMsg {
	if j.ctx.Err() != nil {
		return securityCalcMsg{canceled: true}
	}
	msg := securityCalcMsg{}
	if j.known != nil {
		msg.result = *j.known
	} else {
		msg.result = j.page()
	}
	if j.ctx.Err() == nil && len(msg.result.workloads) > 0 {
		w := msg.result.workloads[selectedWorkload(msg.result.workloads, j.selected, j.cursor)]
		msg.rules, msg.rulesFor = data.WorkloadRules(j.snapshot, w), w.Key
	}
	if j.ctx.Err() != nil {
		return securityCalcMsg{canceled: true}
	}
	return msg
}

func (m *Model) cancelSecurityCalc() {
	if m.secCalc.running && m.secCalc.cancel != nil {
		m.secCalc.cancel()
	}
}

func (m Model) securityRulesReady() bool {
	return len(m.sec.result.workloads) == 0 || (m.sec.rulesFor == m.sec.Selected && m.sec.rulesKey == m.sec.result.key)
}

func (m *Model) followSecurity() tea.Cmd {
	if !m.onScreen(ScreenSecurity) {
		m.cancelSecurityCalc()
		if m.sec.have {
			m.sec = securityPage{securityFrame: m.sec.securityFrame}
		}
		return nil
	}
	want := m.wantedSecurityKey()
	if m.sec.have && m.sec.result.key.securityView != want.securityView {
		m.sec.have, m.sec.result = false, securityResult{}
		m.sec.rules, m.sec.rulesFor, m.sec.rulesKey = nil, data.WorkloadKey{}, securityKey{}
	}
	if m.secCalc.running {
		if m.secCalc.job.key.securityView != want.securityView {
			m.cancelSecurityCalc()
		}
		return nil
	}
	page := !m.sec.have || m.sec.result.key != want
	if !page && m.securityRulesReady() {
		return nil
	}
	ctx, cancel := context.WithCancel(m.life)
	job := securityJob{ctx: ctx, key: want, snapshot: m.security, dash: m.securitySession.Dashboard, selected: m.sec.Selected, cursor: m.sec.Cursor}
	if !page {
		known := m.sec.result
		job.known = &known
	}
	m.secCalc = securityCalc{seq: m.secCalc.seq + 1, running: true, job: job, cancel: cancel}
	session, seq := m.session.Generation, m.secCalc.seq
	return func() tea.Msg {
		msg := job.run()
		msg.session, msg.seq = session, seq
		return msg
	}
}

func (m Model) updateSecurityCalc(msg tea.Msg) (Model, tea.Cmd, bool) {
	calc, ok := msg.(securityCalcMsg)
	if !ok {
		return m, nil, false
	}
	if calc.session != m.session.Generation || calc.seq != m.secCalc.seq || !m.secCalc.running {
		return m, nil, true
	}
	m.cancelSecurityCalc()
	m.secCalc = securityCalc{seq: m.secCalc.seq}
	if calc.canceled || m.life.Err() != nil || !m.onScreen(ScreenSecurity) || calc.result.key.securityView != m.wantedSecurityKey().securityView {
		return m, nil, true
	}
	lost := m.sec.have && m.sec.Selected != (data.WorkloadKey{}) && !slices.ContainsFunc(calc.result.workloads, func(w data.WorkloadPosture) bool { return w.Key == m.sec.Selected })
	m.sec.have, m.sec.result = true, calc.result
	m.adoptSecurityWorkload(selectedWorkload(calc.result.workloads, m.sec.Selected, m.sec.Cursor))
	m.sec.rules, m.sec.rulesFor, m.sec.rulesKey = calc.rules, calc.rulesFor, calc.result.key
	if !lost {
		m.clampSecurityScroll()
		return m, nil, true
	}
	m.sec.Scroll = 0
	if m.sec.Tab != ui.SecTabWorkloads {
		return m, nil, true
	}
	focus := m.sec.Focus
	m.sec.Focus = ui.SecurityFocusList
	m.syncCompactSecurity(focus)
	cmd := m.showNotice("workload selecionado não existe mais; seleção movida")
	return m, cmd, true
}
