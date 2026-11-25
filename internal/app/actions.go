package app

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

const (
	actionReadTimeout = 15 * time.Second
	actionTimeout     = 30 * time.Second
	actionInputLimit  = 253
)

type actionStage int

const (
	actionClosed actionStage = iota
	actionChoosing
	actionObserving
	actionEditing
	actionPreparing
	actionPreview
	actionExecuting
	actionResult
	actionReconciling
)

func (s actionStage) beforeSend() bool {
	return s >= actionChoosing && s <= actionPreview
}

type actionInput struct {
	label   string
	value   string
	choices []string
}

type actionSpec struct {
	action  guard.Action
	label   string
	title   string
	confirm string
	success string
	focus   int
	inputs  func(guard.Observation) []actionInput
	intro   func(guard.Observation, guard.Target) []ui.ActionLine
	build   func(guard.Observation, []actionInput) (guard.Parameters, string)
	effect  func(guard.Observation, guard.Parameters, guard.Target) []ui.ActionLine
	observe func(guard.Observation) string
}

type actionDone struct {
	session uint64
	attempt uint64
	context string
	action  guard.Action
	target  guard.Target
	result  guard.Result
	err     error
	exec    *guard.Executor
}

type actionTag struct {
	session uint64
	attempt uint64
}

type actionObservedMsg struct {
	tag    actionTag
	obs    guard.Observation
	err    error
	verify bool
}

type actionPreparedMsg struct {
	tag      actionTag
	exec     *guard.Executor
	preview  guard.Preview
	err      error
	timedOut bool
}

type actionDoneMsg struct{ done actionDone }

type actionLateMsg struct{ done actionDone }

type actionTickMsg struct{ tag actionTag }

type actionAuditMsg struct{ tag actionTag }

type actionState struct {
	stage   actionStage
	tag     actionTag
	target  guard.Target
	plan    guard.Target
	context string
	items   []actionSpec
	cursor  int
	spec    actionSpec
	obs     guard.Observation
	inputs  []actionInput
	focus   int
	params  guard.Parameters
	preview guard.Preview
	expired bool
	typed   string
	err     string
	scroll  int
	waiting bool
	cancel  context.CancelFunc
	life    context.Context
	lines   []ui.ActionLine
	opID    string
	code    guard.Reason
	at      time.Time
	verify  bool
	checked []ui.ActionLine
	late    []actionDone
}

type actionSource interface {
	Actions() *guard.Executor
}

func (m Model) executor() *guard.Executor {
	src, ok := m.src.(actionSource)
	if !ok || m.snap.Synthetic {
		return nil
	}
	return src.Actions()
}

func (m Model) accessMode() data.EffectiveMode {
	if exec := m.executor(); exec != nil {
		mode := exec.Mode()
		return data.EffectiveMode{ReadOnly: mode.ReadOnly, Protected: mode.Protected, Valid: mode.ConfigValid}
	}
	if m.session.Mode == (data.EffectiveMode{}) {
		return data.ReadOnlyMode
	}
	return m.session.Mode
}

func (m Model) actionBlock() string {
	exec := m.executor()
	mode := m.accessMode()
	switch {
	case exec == nil:
		return textNoSource
	case !mode.Valid:
		return textInvalidConfig
	case mode.Protected:
		return textProtected
	case mode.ReadOnly:
		return textReadOnly
	case exec.AuditBlocked():
		return auditBlockText(exec.AuditRefusal())
	}
	return ""
}

func podTarget(p *data.Pod) (guard.Target, bool) {
	if p == nil {
		return guard.Target{}, false
	}
	return guard.Target{Version: "v1", Resource: "pods", Namespace: p.Namespace, Name: p.Name, UID: p.UID, ResourceVersion: p.ResourceVersion}, true
}

func (m Model) actionTarget() (guard.Target, bool) {
	switch {
	case m.inDetail():
		return podTarget(m.selectedPod())
	case m.onScreen(ScreenResources):
		return m.tableTarget()
	case m.onScreen(ScreenNodes):
		return m.nodeTarget()
	case m.onScreen(ScreenRadar):
		return m.signalTarget()
	case m.onScreen(ScreenSecurity):
		return m.postureTarget()
	case !m.onScreen(ScreenDashboard) && !m.onScreen(ScreenWorkloads):
		return guard.Target{}, false
	}
	switch m.focus {
	case FocusPods, FocusDetails:
		return podTarget(m.selectedPod())
	case FocusDeployments:
		deps := m.visibleDeployments()
		if deploymentListed(m.snap.DeploymentCoverage) && m.depCursor >= 0 && m.depCursor < len(deps) {
			d := deps[m.depCursor]
			return guard.Target{Group: "apps", Version: "v1", Resource: "deployments", Namespace: d.Namespace, Name: d.Name, UID: d.UID, ResourceVersion: d.ResourceVersion}, true
		}
	}
	return guard.Target{}, false
}

func exactPod(p *data.Pod) (guard.Target, bool) {
	t, ok := podTarget(p)
	return t, ok && t.UID != "" && t.ResourceVersion != ""
}

func (m Model) signalTarget() (guard.Target, bool) {
	p, _ := m.signalPod(m.selectedSignal())
	return exactPod(p)
}

func (m Model) postureTarget() (guard.Target, bool) {
	if m.sec.Tab != ui.SecTabWorkloads || !m.sec.have || m.sec.Cursor < 0 || m.sec.Cursor >= len(m.sec.result.workloads) {
		return guard.Target{}, false
	}
	w := m.sec.result.workloads[m.sec.Cursor]
	if w.Key != m.sec.Selected || w.Key.Pod == "" {
		return guard.Target{}, false
	}
	for i := range m.snap.Pods {
		p := &m.snap.Pods[i]
		if p.Key() == w.Key.Pod && p.UID == w.Key.UID {
			return exactPod(p)
		}
	}
	return guard.Target{}, false
}

func (m Model) actionHelp(help []ui.KeyHelp, desc string) []ui.KeyHelp {
	if _, ok := m.actionTarget(); !ok {
		return help
	}
	return append(slices.Clone(help), ui.KeyHelp{Key: "o", Desc: desc})
}

func (m Model) tableTarget() (guard.Target, bool) {
	w, ok := m.tableLeaf()
	st := m.tableState()
	if !ok || st.Cursor < 0 || st.Cursor >= len(st.Page.Items) {
		return guard.Target{}, false
	}
	ref := st.Page.Items[st.Cursor].Ref
	return guard.Target{Group: w.gvr.Group, Version: w.gvr.Version, Resource: w.gvr.Resource, Namespace: ref.Namespace, Name: ref.Name, UID: ref.UID, ResourceVersion: ref.ResourceVersion}, true
}

func (m Model) nodeTarget() (guard.Target, bool) {
	name, _ := m.nodeScope()
	if name == "" {
		return guard.Target{}, false
	}
	t := guard.Target{Version: "v1", Resource: "nodes", Name: name}
	if n := m.snap.Node; n.Name == name {
		t.UID, t.ResourceVersion = n.UID, n.ResourceVersion
	}
	return t, true
}

func sameObject(a, b guard.Target) bool {
	a.ResourceVersion, b.ResourceVersion = "", ""
	return a == b
}

func (m *Model) anchorAction() {
	if !m.act.stage.beforeSend() || m.act.target.UID == "" {
		return
	}
	uid := m.act.target.UID
	switch {
	case m.onScreen(ScreenResources):
		st := m.tableState()
		i := slices.IndexFunc(st.Page.Items, func(r data.ResourceRow) bool { return r.Ref.UID == uid })
		if i >= 0 {
			m.res.cursor = st.Offset + i
		}
	case m.focus == FocusDeployments:
		i := slices.IndexFunc(m.visibleDeployments(), func(d data.Deployment) bool { return d.UID == uid })
		if i >= 0 {
			m.depCursor = i
		}
	}
}

func (m *Model) checkAction() tea.Cmd {
	if !m.act.stage.beforeSend() {
		return nil
	}
	if t, ok := m.actionTarget(); ok && sameObject(t, m.act.target) {
		return nil
	}
	cmd := m.abandonAction()
	m.finishAction([]ui.ActionLine{{Text: textSelection, Tone: ui.ActionWarn}}, guard.Canceled)
	return cmd
}

func (m *Model) nextActionTag() actionTag {
	m.actSeq++
	m.act.tag = actionTag{session: m.session.Generation, attempt: m.actSeq}
	return m.act.tag
}

func (m Model) openActions() (Model, tea.Cmd) {
	t, ok := m.actionTarget()
	var items []actionSpec
	if ok {
		for _, s := range actionSpecs {
			if guard.SupportsAction(s.action, t) {
				items = append(items, s)
			}
		}
	}
	if len(items) == 0 {
		if m.onScreen(ScreenRadar) || m.onScreen(ScreenSecurity) {
			return m, nil
		}
		return m, m.showNotice(textNoAction)
	}
	m.endEditing()
	m.act = actionState{stage: actionChoosing, target: t, context: m.session.Context, items: items, late: m.act.late}
	tag := m.nextActionTag()
	if exec := m.executor(); m.actionBlock() == "" {
		return m, func() tea.Msg {
			_ = exec.CheckAudit(false)
			return actionAuditMsg{tag: tag}
		}
	}
	return m, nil
}

func (m *Model) abandonAction() tea.Cmd {
	if m.act.cancel != nil {
		m.act.cancel()
		m.act.cancel = nil
	}
	var cmd tea.Cmd
	if exec := m.executor(); m.act.stage == actionPreview && m.act.preview.ID != "" && exec != nil {
		cmd = discardPreview(exec, m.act.preview.ID)
	}
	m.act.preview = guard.Preview{}
	m.nextActionTag()
	return cmd
}

func discardPreview(exec *guard.Executor, id string) tea.Cmd {
	return func() tea.Msg {
		_ = exec.Discard(id)
		return nil
	}
}

func (m *Model) closeAction() tea.Cmd {
	cmd := m.abandonAction()
	late := m.act.late
	m.act = actionState{}
	if len(late) > 0 {
		m.act.late = late[1:]
		m.showLate(late[0])
	}
	return cmd
}

func (m *Model) finishAction(lines []ui.ActionLine, code guard.Reason) {
	m.act.stage, m.act.focus, m.act.scroll = actionResult, -1, 0
	m.act.lines, m.act.code, m.act.at, m.act.err = lines, code, m.now(), ""
	m.act.checked, m.act.waiting = nil, false
}

func (m *Model) showLate(d actionDone) {
	spec, _ := specFor(d.action)
	m.act = actionState{spec: spec, target: d.target, plan: d.target, context: d.context, opID: d.result.OperationID, late: m.act.late, tag: actionTag{session: d.session}}
	lines := []ui.ActionLine{{Text: "Operacao da sessao anterior, encerrada por troca de sessao.", Tone: ui.ActionWarn}}
	m.finishAction(append(lines, doneLines(d, spec)...), d.result.Outcome)
}

func specFor(a guard.Action) (actionSpec, bool) {
	i := slices.IndexFunc(actionSpecs, func(s actionSpec) bool { return s.action == a })
	if i < 0 {
		return actionSpec{}, false
	}
	return actionSpecs[i], true
}

func (m Model) handleActionMsg(msg tea.Msg) (Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case actionLateMsg:
		if m.act.stage != actionClosed {
			m.act.late = append(slices.Clone(m.act.late), msg.done)
			return m, nil, true
		}
		m.showLate(msg.done)
		return m, nil, true
	case actionObservedMsg:
		if msg.tag != m.act.tag {
			return m, nil, true
		}
		m.stopActionWork()
		if msg.verify {
			m.verified(msg)
			return m, nil, true
		}
		cmd := m.observed(msg)
		return m, cmd, true
	case actionPreparedMsg:
		if msg.tag != m.act.tag {
			if msg.err == nil && msg.exec != nil {
				return m, discardPreview(msg.exec, msg.preview.ID), true
			}
			return m, nil, true
		}
		m.stopActionWork()
		cmd := m.prepared(msg)
		return m, cmd, true
	case actionAuditMsg:
		return m, nil, true
	case actionTickMsg:
		if msg.tag != m.act.tag || m.act.stage != actionPreview || m.act.expired {
			return m, nil, true
		}
		if !m.now().Before(m.act.preview.ExpiresAt) {
			cmd := m.expirePreview()
			return m, cmd, true
		}
		return m, m.tickAction(), true
	case actionDoneMsg:
		if msg.done.session != m.act.tag.session || msg.done.attempt != m.act.tag.attempt || m.act.stage != actionExecuting {
			return m, nil, true
		}
		m.stopActionWork()
		m.act.opID, m.act.verify = msg.done.result.OperationID, true
		m.finishAction(doneLines(msg.done, m.act.spec), doneCode(msg.done))
		return m, nil, true
	}
	return m, nil, false
}

func doneCode(d actionDone) guard.Reason {
	switch {
	case d.result.Outcome != "":
		return d.result.Outcome
	case d.err != nil:
		return reasonOf(d.err)
	}
	return guard.Unknown
}

func (m *Model) stopActionWork() {
	if m.act.cancel != nil {
		m.act.cancel()
		m.act.cancel = nil
	}
}

func (m *Model) chooseAction() tea.Cmd {
	if m.act.cursor < 0 || m.act.cursor >= len(m.act.items) {
		return nil
	}
	if m.itemBlock() != "" {
		return nil
	}
	m.act.spec, m.act.err = m.act.items[m.act.cursor], ""
	m.act.stage = actionObserving
	return m.observeAction(m.act.target, false)
}

func (m Model) itemBlock() string {
	if reason := m.actionBlock(); reason != "" {
		return reason
	}
	if m.act.target.UID == "" || m.act.target.ResourceVersion == "" {
		return textNoIdentity
	}
	return ""
}

func (m *Model) observeAction(t guard.Target, verify bool) tea.Cmd {
	exec := m.executor()
	if exec == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(m.life, actionReadTimeout)
	m.act.cancel = cancel
	tag, action := m.nextActionTag(), m.act.spec.action
	return func() tea.Msg {
		read := exec.Observe
		if verify {
			read = exec.Verify
		}
		obs, err := read(ctx, action, t)
		return actionObservedMsg{tag: tag, obs: obs, err: err, verify: verify}
	}
}

func (m *Model) observed(msg actionObservedMsg) tea.Cmd {
	if msg.err != nil {
		m.finishAction([]ui.ActionLine{{Text: m.refusalText(prepareText(msg.err, false), msg.err), Tone: ui.ActionErr}}, reasonOf(msg.err))
		return nil
	}
	if m.act.spec.action != guard.Scale && msg.obs.Target.ResourceVersion != m.act.target.ResourceVersion {
		m.finishAction([]ui.ActionLine{{Text: reasonText(guard.Stale), Tone: ui.ActionErr}}, guard.Stale)
		return nil
	}
	m.act.obs, m.act.plan = msg.obs, msg.obs.Target
	if m.act.spec.inputs == nil {
		return m.prepareAction()
	}
	m.act.inputs = m.act.spec.inputs(msg.obs)
	m.editAction()
	return nil
}

func (m *Model) editAction() {
	m.act.stage, m.act.focus, m.act.err, m.act.scroll = actionEditing, m.act.spec.focus, "", 0
}

func (m *Model) prepareAction() tea.Cmd {
	params, problem := m.act.spec.build(m.act.obs, m.act.inputs)
	if problem != "" {
		if m.act.stage == actionEditing {
			m.act.err = problem
			return nil
		}
		m.finishAction([]ui.ActionLine{{Text: problem, Tone: ui.ActionWarn}}, guard.Unchanged)
		return nil
	}
	if reason := m.actionBlock(); reason != "" {
		m.finishAction([]ui.ActionLine{{Text: reason, Tone: ui.ActionErr}}, "")
		return nil
	}
	exec := m.executor()
	m.act.params, m.act.err, m.act.typed, m.act.expired = params, "", "", false
	m.act.stage, m.act.scroll = actionPreparing, 0
	ctx, cancel := context.WithTimeout(m.life, actionTimeout)
	m.act.cancel = cancel
	tag := m.nextActionTag()
	req := m.actionRequest()
	return func() tea.Msg {
		p, err := exec.Prepare(ctx, req)
		return actionPreparedMsg{tag: tag, exec: exec, preview: p, err: err, timedOut: ctx.Err() == context.DeadlineExceeded}
	}
}

func (m Model) actionRequest() guard.Request {
	return guard.Request{Action: m.act.spec.action, Target: m.act.plan, Parameters: m.act.params}
}

func (m *Model) prepared(msg actionPreparedMsg) tea.Cmd {
	if msg.err != nil {
		m.finishAction([]ui.ActionLine{{Text: m.refusalText(prepareText(msg.err, msg.timedOut), msg.err), Tone: ui.ActionErr}}, reasonOf(msg.err))
		return nil
	}
	m.act.preview, m.act.stage, m.act.focus = msg.preview, actionPreview, 0
	m.act.life, m.act.cancel = context.WithCancel(m.life)
	return m.tickAction()
}

func (m Model) tickAction() tea.Cmd {
	return m.actTick(m.act.life, time.Second, actionTickMsg{tag: m.act.tag})
}

func tickWithin(ctx context.Context, d time.Duration, msg tea.Msg) tea.Cmd {
	return func() tea.Msg {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return msg
		}
	}
}

func (m *Model) expirePreview() tea.Cmd {
	cmd := m.abandonAction()
	m.act.expired, m.act.err, m.act.typed, m.act.focus = true, textExpired, "", 0
	return cmd
}

func (m *Model) confirmAction() tea.Cmd {
	switch {
	case m.act.stage != actionPreview || m.act.expired:
		return nil
	case !m.now().Before(m.act.preview.ExpiresAt):
		return m.expirePreview()
	case m.act.preview.Decision.HighRisk && m.act.typed != m.act.plan.Name:
		m.act.err = textNameMismatch
		return nil
	}
	if reason := m.actionBlock(); reason != "" {
		cmd := m.abandonAction()
		m.finishAction([]ui.ActionLine{{Text: reason, Tone: ui.ActionErr}}, "")
		return cmd
	}
	m.stopActionWork()
	exec, id := m.executor(), m.act.preview.ID
	confirmation := guard.Confirmation{Approved: true, Context: m.session.Context, Resource: m.act.typed}
	ctx, cancel := context.WithTimeout(m.life, actionTimeout)
	m.act.cancel = cancel
	tag := m.nextActionTag()
	m.act.stage, m.act.focus, m.act.err, m.act.scroll = actionExecuting, -1, "", 0
	m.act.preview.ID, m.act.opID = "", id
	done := actionDone{session: tag.session, attempt: tag.attempt, context: m.act.context, action: m.act.spec.action, target: m.act.plan, exec: exec}
	return func() tea.Msg {
		done.result, done.err = exec.Execute(ctx, id, confirmation)
		return actionDoneMsg{done: done}
	}
}

func (m Model) refusalText(text string, err error) string {
	if exec := m.executor(); exec != nil && reasonOf(err) == guard.AuditFailed {
		return auditBlockText(exec.AuditRefusal())
	}
	return text
}

func doneLines(d actionDone, spec actionSpec) []ui.ActionLine {
	lines := resultLines(d.result, d.err, spec.success)
	if d.err == nil || d.exec == nil || reasonOf(d.err) != guard.AuditFailed {
		return lines
	}
	if refusal := d.exec.AuditRefusal(); refusal != "" {
		lines = append(lines, ui.ActionLine{Text: auditBlockText(refusal), Tone: ui.ActionErr})
	}
	return lines
}

func (m *Model) verifyAction() tea.Cmd {
	if !m.act.verify || m.executor() == nil {
		return nil
	}
	m.act.stage, m.act.checked = actionReconciling, nil
	return m.observeAction(m.act.plan, true)
}

func (m *Model) verified(msg actionObservedMsg) {
	m.act.stage = actionResult
	switch {
	case msg.err != nil && reasonOf(msg.err) == guard.Stale:
		m.act.checked = []ui.ActionLine{{Text: textUIDGone, Tone: ui.ActionWarn}}
	case msg.err != nil:
		m.act.checked = []ui.ActionLine{{Text: prepareText(msg.err, false), Tone: ui.ActionErr}}
		return
	default:
		m.act.checked = []ui.ActionLine{{Text: "Observado: " + m.act.spec.observe(msg.obs)}}
	}
	m.act.checked = append(m.act.checked, ui.ActionLine{Text: textNotAuthorship, Tone: ui.ActionMuted})
}

type actionControl int

const (
	controlClose actionControl = iota
	controlPrepare
	controlBack
	controlConfirm
	controlRefresh
	controlVerify
)

type actionButton struct {
	label    string
	control  actionControl
	disabled bool
}

func (m Model) actionButtons() []actionButton {
	a := m.act
	switch a.stage {
	case actionEditing:
		return []actionButton{{label: "Cancelar"}, {label: "Gerar preview validado", control: controlPrepare}}
	case actionPreview:
		out := []actionButton{{label: "Cancelar"}}
		if len(a.inputs) > 0 {
			out = append(out, actionButton{label: "Voltar aos parametros", control: controlBack})
		}
		if a.expired {
			return append(out, actionButton{label: "Atualizar preview", control: controlRefresh})
		}
		blocked := a.preview.Decision.HighRisk && a.typed != a.plan.Name
		return append(out, actionButton{label: a.spec.confirm, control: controlConfirm, disabled: blocked})
	case actionResult:
		out := []actionButton{{label: "Fechar"}}
		if a.verify {
			out = append(out, actionButton{label: "Verificar por leitura", control: controlVerify})
		}
		return out
	}
	return nil
}

func (m Model) actionFields() []actionInput {
	switch {
	case m.act.stage == actionEditing:
		return m.act.inputs
	case m.act.stage == actionPreview && !m.act.expired && m.act.preview.Decision.HighRisk:
		return []actionInput{{label: "Digite exatamente " + m.act.plan.Name + " para confirmar", value: m.act.typed}}
	}
	return nil
}

func (m *Model) moveActionFocus(delta int) {
	n := len(m.actionFields()) + len(m.actionButtons())
	if n == 0 {
		return
	}
	if m.act.focus < 0 {
		m.act.focus = 0
		if delta < 0 {
			m.act.focus = n - 1
		}
		return
	}
	m.act.focus = ((m.act.focus+delta)%n + n) % n
}

func (m *Model) setActionField(i int, value string) {
	if m.act.stage == actionPreview {
		m.act.typed = value
	} else {
		m.act.inputs = slices.Clone(m.act.inputs)
		m.act.inputs[i].value = value
	}
	m.act.err = ""
}

func (m *Model) cycleActionField(i, delta int) {
	f := m.actionFields()[i]
	at := slices.Index(f.choices, f.value)
	n := len(f.choices)
	m.setActionField(i, f.choices[((at+delta)%n+n)%n])
}

func (m *Model) activate(c actionControl) tea.Cmd {
	switch c {
	case controlClose:
		return m.closeAction()
	case controlPrepare:
		return m.prepareAction()
	case controlBack:
		cmd := m.abandonAction()
		m.act.expired, m.act.typed = false, ""
		m.editAction()
		return cmd
	case controlConfirm:
		return m.confirmAction()
	case controlRefresh:
		return m.prepareAction()
	case controlVerify:
		return m.verifyAction()
	}
	return nil
}

func (m *Model) escapeAction() tea.Cmd {
	switch m.act.stage {
	case actionExecuting:
		m.act.waiting = true
		if m.act.cancel != nil {
			m.act.cancel()
		}
		return nil
	case actionReconciling:
		m.stopActionWork()
		m.nextActionTag()
		m.act.stage = actionResult
		return nil
	case actionObserving, actionPreparing:
		cmd := m.closeAction()
		return tea.Batch(cmd, m.showNotice(textCanceled))
	}
	return m.closeAction()
}

func (m Model) handleActionKey(msg tea.KeyPressMsg) (Model, tea.Cmd) {
	k := m.keys
	pressed := msg.String()
	switch {
	case pressed == "esc":
		cmd := m.escapeAction()
		return m, cmd
	case pressed == "pgup" || pressed == "pgdown":
		m.scrollAction(map[string]int{"pgup": -5, "pgdown": 5}[pressed])
		return m, nil
	case pressed == "enter" && msg.IsRepeat:
		return m, nil
	}
	if m.act.stage == actionChoosing {
		switch {
		case key.Matches(msg, k.Up):
			m.act.cursor, m.act.err = max(0, m.act.cursor-1), ""
		case key.Matches(msg, k.Down):
			m.act.cursor, m.act.err = min(len(m.act.items)-1, m.act.cursor+1), ""
		case pressed == "enter":
			cmd := m.chooseAction()
			return m, cmd
		}
		return m, nil
	}
	fields, buttons := m.actionFields(), m.actionButtons()
	if len(buttons) == 0 {
		return m, nil
	}
	switch pressed {
	case "tab":
		m.moveActionFocus(1)
		return m, nil
	case "shift+tab":
		m.moveActionFocus(-1)
		return m, nil
	}
	f := m.act.focus
	switch {
	case f < 0:
		return m, nil
	case f >= len(fields):
		switch pressed {
		case "left":
			m.act.focus = max(len(fields), f-1)
		case "right":
			m.act.focus = min(len(fields)+len(buttons)-1, f+1)
		case "enter":
			cmd := m.pressButton(f - len(fields))
			return m, cmd
		}
		return m, nil
	case len(fields[f].choices) > 0:
		switch pressed {
		case "left":
			m.cycleActionField(f, -1)
		case "right":
			m.cycleActionField(f, 1)
		}
		return m, nil
	}
	m.typeAction(f, msg)
	return m, nil
}

func (m *Model) pressButton(i int) tea.Cmd {
	buttons := m.actionButtons()
	if i < 0 || i >= len(buttons) {
		return nil
	}
	return m.activate(buttons[i].control)
}

func (m *Model) typeAction(f int, msg tea.KeyPressMsg) {
	value := m.actionFields()[f].value
	switch msg.String() {
	case "enter":
		m.act.err = m.fieldProblem(f)
	case "backspace":
		if r := []rune(value); len(r) > 0 {
			m.setActionField(f, string(r[:len(r)-1]))
		}
	case "ctrl+u":
		m.setActionField(f, "")
	default:
		if msg.Text == "" || msg.Mod&(tea.ModCtrl|tea.ModAlt) != 0 || len(value)+len(msg.Text) > actionInputLimit || strings.IndexFunc(msg.Text, unicode.IsControl) >= 0 {
			return
		}
		m.setActionField(f, value+msg.Text)
	}
}

func (m Model) fieldProblem(f int) string {
	if m.act.stage == actionPreview {
		if m.act.typed != m.act.plan.Name {
			return textNameMismatch
		}
		return ""
	}
	_, problem := m.act.spec.build(m.act.obs, m.act.inputs)
	return problem
}

func (m *Model) scrollAction(delta int) {
	g := ui.ActionLayout(m.w, m.h, m.actionDialog())
	m.act.scroll = max(0, min(m.act.scroll+delta, g.MaxScroll))
}

func (m Model) actionMouse(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		idle := !m.busy()
		next, cmd := m.handleActionClick(tea.Mouse(msg))
		return wake(idle, next, cmd)
	case tea.MouseWheelMsg:
		m.scrollAction(map[tea.MouseButton]int{tea.MouseWheelUp: -1, tea.MouseWheelDown: 1}[msg.Button])
	}
	return m, nil
}

func (m Model) handleActionClick(ms tea.Mouse) (Model, tea.Cmd) {
	if ms.Button != tea.MouseLeft {
		return m, nil
	}
	st := m.actionDialog()
	kind, i := ui.ActionLayout(m.w, m.h, st).Hit(ms.X, ms.Y, st)
	switch kind {
	case ui.ActionHitItem:
		m.act.cursor, m.act.err = i, ""
		cmd := m.chooseAction()
		return m, cmd
	case ui.ActionHitField:
		m.act.focus = i
	case ui.ActionHitButton:
		m.act.focus = len(m.actionFields()) + i
		if m.actionButtons()[i].control == controlConfirm {
			return m, nil
		}
		cmd := m.pressButton(i)
		return m, cmd
	}
	return m, nil
}
