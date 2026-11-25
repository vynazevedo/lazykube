package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"go.uber.org/goleak"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/guard"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func (c *actionClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestBlockedPoliciesExplainAndNeverCallAPI(t *testing.T) {
	for _, tc := range []struct {
		name   string
		writes bool
		env    guard.Environment
		text   string
		header string
	}{
		{"readonly", false, guard.Environment{Valid: true}, textReadOnly, "[RO]"},
		{"context", true, guard.Environment{Valid: true, ReadOnly: true}, textReadOnly, "[RO]"},
		{"protected", true, guard.Environment{Valid: true, Protected: true}, textProtected, "[RO] [PROTECTED]"},
		{"config", true, guard.Environment{}, textInvalidConfig, "[RO] [CONFIG INVALIDA]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newActionRig(t, 130, 40, tc.env, tc.writes)
			r.onDeployment()
			r.keys("o")
			r.stage(actionChoosing)
			if plain := r.plain(); !strings.Contains(plain, tc.header) || !strings.Contains(r.dialog(), tc.text) {
				t.Fatalf("the dialog must explain the block:\n%s", plain)
			}
			for range len(r.m.act.items) {
				r.keys("enter", "enter", "down")
			}
			r.stage(actionChoosing)
			r.keys("esc")
			r.stage(actionClosed)
			if r.back.calls != 0 || len(r.back.events) != 0 {
				t.Fatalf("a blocked policy reached the API: %d calls, %d audit events", r.back.calls, len(r.back.events))
			}
		})
	}
}

func TestDemoNeverExecutes(t *testing.T) {
	m := demoModel(t, 130, 40)
	m = press(t, m, "o")
	if m.act.stage != actionChoosing || !strings.Contains(dialogText(m), textNoSource) {
		t.Fatal("the demo explains that actions are unavailable")
	}
	next, cmd := m.Update(keyPress("enter"))
	m = next.(Model)
	if m.act.stage != actionChoosing || cmd != nil && cmd() != nil {
		t.Fatal("the demo must not start work")
	}
	r := writableRig(t)
	src := r.m.src.(actionLive)
	r.m.snap = src.Demo.Snapshot()
	if r.m.executor() != nil || r.m.actionBlock() != textNoSource {
		t.Fatal("a synthetic snapshot never exposes an executor")
	}
}

func TestOnlyApprovedActionsAreOffered(t *testing.T) {
	approved := map[guard.Action]bool{guard.Scale: true, guard.Restart: true, guard.Delete: true, guard.Cordon: true, guard.Uncordon: true}
	for _, s := range actionSpecs {
		if !approved[s.action] {
			t.Fatalf("%q is out of scope", s.label)
		}
	}
	if len(actionSpecs) != len(approved) {
		t.Fatalf("%d specs, want %d", len(actionSpecs), len(approved))
	}
	r := writableRig(t)
	r.keys("6", "o")
	if r.m.act.stage != actionClosed || r.m.notice.text != textNoAction {
		t.Fatalf("a screen without target answers with a notice, got %q", r.m.notice.text)
	}
	r.keys("2")
	r.m.setFocus(FocusNamespaces)
	r.keys("o")
	if r.m.act.stage != actionClosed {
		t.Fatal("a namespace row must not redirect to the selected pod")
	}
}

func TestTargetFollowsFocusNotGlobalPod(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.keys("o")
	if got := r.m.act.target; got.Resource != "deployments" || got.UID == "" || got.ResourceVersion != "42" {
		t.Fatalf("%+v", got)
	}
	r.keys("esc", "2")
	r.m.setFocus(FocusPods)
	r.keys("o")
	if got := r.m.act.target; got.Resource != "pods" || got.Namespace+"/"+got.Name != r.m.selName {
		t.Fatalf("%+v", got)
	}
	r.keys("esc", "4", "o")
	if got := r.m.act.target; got.Resource != "nodes" || got.Namespace != "" || got.UID != "uid-node" {
		t.Fatalf("%+v", got)
	}
}

func TestTableRowTargetUsesRowIdentity(t *testing.T) {
	r := writableRig(t)
	open := func(id DestinationID) {
		r.m.activateDestination(id)
		next, cmd := r.m.Update(tea.FocusMsg{})
		r.m = drive(t, next.(Model), cmd)
	}
	t.Cleanup(func() { r.m.releaseResources() })
	open(DestStatefulSets)
	r.keys("down", "o")
	row := r.m.tableState().Page.Items[1].Ref
	want := guard.Target{Group: "apps", Version: "v1", Resource: "statefulsets", Namespace: row.Namespace, Name: row.Name, UID: row.UID, ResourceVersion: row.ResourceVersion}
	if r.m.act.target != want {
		t.Fatalf("%+v", r.m.act.target)
	}
	s := *r.m.res.subs[stsGVR]
	s.page.Items = append([]data.ResourceRow{{Ref: data.ResourceMetadataRef{Namespace: "a", Name: "new", UID: "new", ResourceVersion: "1"}}}, s.page.Items...)
	r.m.storeSub(stsGVR, s)
	r.send(tea.FocusMsg{})
	r.stage(actionChoosing)
	if r.m.res.cursor != 2 || r.m.act.target != want {
		t.Fatalf("the cursor follows the frozen row, got %d %+v", r.m.res.cursor, r.m.act.target)
	}
	r.keys("esc")
	open(DestReplicaSets)
	r.keys("o")
	if r.m.act.stage != actionClosed {
		t.Fatal("replicasets have no action")
	}
}

func TestMissingIdentityBlocksBeforeIO(t *testing.T) {
	r := writableRig(t)
	r.rows.noUID = true
	r.run(r.send(dataMsg{session: 12}))
	r.keys("o", "enter")
	r.stage(actionChoosing)
	if !strings.Contains(r.dialog(), textNoIdentity) || r.back.calls != 0 {
		t.Fatal(r.dialog(), r.back.calls)
	}
}

func TestUpdateNeverCallsBackendSynchronously(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.send(keyPress("o"))
	observe := r.send(keyPress("enter"))
	r.plain()
	if r.back.calls != 0 || observe == nil {
		t.Fatal("observe must run in a command")
	}
	r.run(observe)
	calls := r.back.calls
	r.text("5")
	r.toButton(controlPrepare)
	prepare := r.send(keyPress("enter"))
	r.plain()
	if r.back.calls != calls || prepare == nil {
		t.Fatal("prepare must run in a command")
	}
	r.run(prepare)
	calls = r.back.calls
	r.toButton(controlConfirm)
	execute := r.send(keyPress("enter"))
	r.plain()
	if r.back.calls != calls || execute == nil {
		t.Fatal("execute must run in a command")
	}
	r.run(execute)
	r.stage(actionResult)
}

func TestRepeatedInputExecutesOnce(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	r.toButton(controlConfirm)
	first := r.send(keyPress("enter"))
	for range 20 {
		if cmd := r.send(keyPress("enter")); cmd != nil && cmd() != nil {
			t.Fatal("enter during execution starts nothing")
		}
	}
	r.stage(actionExecuting)
	msg := first()
	r.run(r.send(msg))
	for range 20 {
		r.run(r.send(msg))
		r.keys("enter")
	}
	r.stage(actionResult)
	r.sent(1, 1)
}

func TestHeldEnterNeverReachesConfirmation(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.keys("o", "down")
	for range 30 {
		r.keys("enter")
	}
	r.sent(1, 0)
	r = writableRig(t)
	r.scalePreview("5")
	r.toButton(controlConfirm)
	r.run(r.send(tea.KeyPressMsg{Code: tea.KeyEnter, IsRepeat: true}))
	r.stage(actionPreview)
	r.sent(1, 0)
}

func TestMouseFocusesConfirmWithoutExecuting(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	st := r.m.actionDialog()
	g := ui.ActionLayout(r.m.w, r.m.h, st)
	clicked := false
	for y := range r.m.h {
		for x := range r.m.w {
			if kind, i := g.Hit(x, y, st); kind == ui.ActionHitButton && r.m.actionButtons()[i].control == controlConfirm {
				r.run(r.send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
				clicked = true
			}
		}
	}
	if !clicked {
		t.Fatal("the confirm button has a mouse target")
	}
	r.stage(actionPreview)
	r.sent(1, 0)
	r.keys("enter")
	r.sent(1, 1)
}

func TestDialogConsumesGlobalInput(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.choose(guard.Scale)
	before := r.m.nav.Active()
	for _, k := range []string{"c", "n", "b", "q", "?", "m", "2", "o"} {
		cmd := r.send(keyPress(k))
		if cmd != nil {
			if msg := cmd(); msg != nil {
				t.Fatalf("%s leaked %T", k, msg)
			}
		}
	}
	r.run(r.send(tea.PasteMsg{Content: "7"}))
	r.run(r.send(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft}))
	if r.m.nav.Active() != before || r.m.helpOpen || r.m.menu.open || r.m.act.inputs[0].value != "cnbq?m2o" {
		t.Fatalf("%+v %q", r.m.nav.Active(), r.m.act.inputs[0].value)
	}
}

func TestPrepareRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*actionBackendFake)
		want  guard.Reason
	}{
		{"denied", func(f *actionBackendFake) { f.denied = true }, guard.Denied},
		{"unavailable", func(f *actionBackendFake) { f.authErr = errors.New("private server response") }, guard.Unavailable},
		{"stale", func(f *actionBackendFake) { f.validate = guard.Stale }, guard.Stale},
		{"unchanged", func(f *actionBackendFake) { f.validate = guard.Unchanged }, guard.Unchanged},
		{"invalid", func(f *actionBackendFake) { f.validate = guard.Invalid }, guard.Invalid},
		{"unsupported", func(f *actionBackendFake) { f.validate = guard.Unsupported }, guard.Unsupported},
		{"dry run", func(f *actionBackendFake) { f.dryErr = errors.New("private server response") }, guard.DryRunFailed},
		{"audit", func(f *actionBackendFake) { f.failPhase = "preview" }, guard.AuditFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := writableRig(t)
			tc.setup(r.back)
			r.onDeployment()
			r.choose(guard.Scale)
			r.text("5")
			r.toButton(controlPrepare)
			r.keys("enter")
			r.stage(actionResult)
			plain := r.dialog()
			if !strings.Contains(plain, reasonText(tc.want)) || !strings.Contains(plain, string(tc.want)) || strings.Contains(plain, "private") {
				t.Fatal(plain)
			}
			if len(r.back.real) != 0 {
				t.Fatal("a refusal never sends")
			}
			r.keys("enter", "enter")
			r.stage(actionResult)
			if tc.want == guard.AuditFailed {
				r.keys("esc", "o")
				if plain := r.plain(); !strings.Contains(r.dialog(), textAuditBlocked) || !strings.Contains(plain, "[ACOES BLOQUEADAS: AUDIT]") {
					t.Fatal(plain)
				}
			}
		})
	}
}

func TestObserveRefusals(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*actionBackendFake)
		want  guard.Reason
	}{
		{"denied", func(f *actionBackendFake) { f.obsErr = guard.Denied }, guard.Denied},
		{"missing", func(f *actionBackendFake) { f.obsErr = guard.Stale }, guard.Stale},
		{"unavailable", func(f *actionBackendFake) { f.obsErr = errors.New("private") }, guard.Unavailable},
		{"recreated", func(f *actionBackendFake) { f.obsUID = "other" }, guard.Stale},
		{"changed", func(f *actionBackendFake) { f.obsVersion = "77" }, guard.Stale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := writableRig(t)
			tc.setup(r.back)
			r.onDeployment()
			r.choose(guard.Restart)
			r.stage(actionResult)
			if !strings.Contains(r.dialog(), reasonText(tc.want)) {
				t.Fatal(r.plain())
			}
			r.sent(0, 0)
		})
	}
}

func TestExecuteOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*actionRig)
		want  []string
		sent  int
	}{
		{"succeeded", func(*actionRig) {}, []string{scaleAction.success}, 1},
		{"denied before", func(r *actionRig) { r.back.denied = true }, []string{reasonText(guard.Denied)}, 0},
		{"stale before", func(r *actionRig) { r.back.validate = guard.Stale }, []string{reasonText(guard.Stale)}, 0},
		{"unavailable before", func(r *actionRig) { r.back.authErr = errors.New("private") }, []string{reasonText(guard.Unavailable)}, 0},
		{"session closed", func(r *actionRig) { r.exec.Close() }, []string{textCanceled}, 0},
		{"intent audit", func(r *actionRig) { r.back.failPhase = "intent" }, []string{textAuditBlocked}, 0},
		{"denied", func(r *actionRig) { r.back.sendErr = guard.Denied }, []string{reasonText(guard.Denied)}, 1},
		{"conflict", func(r *actionRig) { r.back.sendErr = guard.Stale }, []string{reasonText(guard.Stale)}, 1},
		{"unknown", func(r *actionRig) { r.back.sendErr = errors.New("private reset") }, []string{textUnknown}, 1},
		{"succeeded without audit", func(r *actionRig) { r.back.failPhase = "result" }, []string{textSucceededAudit}, 1},
		{"unknown without audit", func(r *actionRig) { r.back.failPhase, r.back.sendErr = "result", errors.New("private") }, []string{textUnknown, textFinalAudit}, 1},
		{"stale without audit", func(r *actionRig) { r.back.failPhase, r.back.sendErr = "result", guard.Stale }, []string{reasonText(guard.Stale), textFinalAudit}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := writableRig(t)
			r.scalePreview("5")
			tc.setup(r)
			r.toButton(controlConfirm)
			r.keys("enter")
			r.stage(actionResult)
			plain := r.dialog()
			for _, want := range tc.want {
				if !strings.Contains(plain, want) {
					t.Fatalf("missing %q in %s", want, plain)
				}
			}
			if tc.name != "succeeded" && strings.Contains(plain, scaleAction.success) || strings.Contains(plain, "private") {
				t.Fatal(plain)
			}
			r.keys("enter", "enter")
			r.sent(1, tc.sent)
			if r.m.act.opID == "" {
				t.Fatal("the result keeps the operation id")
			}
		})
	}
}

func TestResultNeverPromotesUncertaintyToSuccess(t *testing.T) {
	for _, tc := range []struct {
		res guard.Result
		err error
	}{
		{guard.Result{}, nil}, {guard.Result{}, errors.New("x")}, {guard.Result{Outcome: guard.Unknown}, nil},
		{guard.Result{Outcome: guard.Succeeded}, errors.New("x")}, {guard.Result{Outcome: guard.Succeeded}, guard.Unknown},
	} {
		lines := resultLines(tc.res, tc.err, "ok-sentinel")
		for _, l := range lines {
			if l.Text == "ok-sentinel" || l.Tone == ui.ActionOK {
				t.Fatalf("%+v %v: %+v", tc.res, tc.err, lines)
			}
		}
	}
}

func TestVerifyReadsWithoutChangingOutcome(t *testing.T) {
	r := writableRig(t)
	r.back.sendErr = errors.New("reset")
	r.scalePreview("5")
	r.toButton(controlConfirm)
	r.keys("enter")
	r.back.obs.Replicas = 5
	r.toButton(controlVerify)
	r.keys("enter")
	r.stage(actionResult)
	plain := r.dialog()
	if !strings.Contains(plain, textUnknown) || !strings.Contains(plain, "replicas desejadas 5") || !strings.Contains(plain, textNotAuthorship) {
		t.Fatal(plain)
	}
	r.back.obsUID = "recreated"
	r.toButton(controlVerify)
	r.keys("enter")
	if plain = r.dialog(); !strings.Contains(plain, textUIDGone) || !strings.Contains(plain, textUnknown) {
		t.Fatal(plain)
	}
	r.sent(1, 1)
}

func TestExpiredPreviewNeedsExplicitRefresh(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	id := r.m.act.preview.ID
	tick := actionTickMsg{tag: r.m.act.tag}
	r.clock.advance(119 * time.Second)
	r.run(r.send(tick))
	if r.m.act.expired || !strings.Contains(r.dialog(), "Preview valido por 1s.") {
		t.Fatal(r.plain())
	}
	r.toButton(controlConfirm)
	r.clock.advance(time.Second)
	r.keys("enter")
	if !r.m.act.expired || r.m.act.err != textExpired {
		t.Fatal("confirming at the deadline expires the preview")
	}
	r.run(r.send(tick))
	r.keys("enter")
	r.sent(1, 0)
	r = writableRig(t)
	r.scalePreview("5")
	r.clock.advance(3 * time.Minute)
	r.run(r.send(actionTickMsg{tag: r.m.act.tag}))
	if !r.m.act.expired {
		t.Fatal("the tick expires the preview")
	}
	plan := r.m.act.plan
	r.toButton(controlRefresh)
	r.keys("enter")
	r.stage(actionPreview)
	r.sent(2, 0)
	if r.m.act.expired || r.m.act.preview.ID == id || r.m.act.plan != plan {
		t.Fatal("the refresh prepares a new preview of the same frozen target")
	}
	if len(r.back.real) != 0 {
		t.Fatal("no automatic execution")
	}
}

func TestSelectionChangeCancelsEveryStage(t *testing.T) {
	stages := map[string]func(*actionRig){
		"choosing": func(r *actionRig) { r.onDeployment(); r.keys("o") },
		"editing":  func(r *actionRig) { r.onDeployment(); r.choose(guard.Scale) },
		"preview":  func(r *actionRig) { r.scalePreview("5") },
	}
	changes := map[string]func(*actionRig){
		"recreated": func(r *actionRig) { r.rows.uid = "-new"; r.run(r.send(dataMsg{session: 12})) },
		"removed":   func(r *actionRig) { r.rows.gone = true; r.run(r.send(dataMsg{session: 12})) },
		"cursor":    func(r *actionRig) { r.m.setFocus(FocusPods); r.run(r.send(tea.FocusMsg{})) },
		"route":     func(r *actionRig) { r.m.activateScreen(ScreenEvents); r.run(r.send(tea.FocusMsg{})) },
	}
	for sn, setup := range stages {
		for cn, change := range changes {
			t.Run(sn+"/"+cn, func(t *testing.T) {
				r := writableRig(t)
				setup(r)
				change(r)
				r.stage(actionResult)
				if !strings.Contains(r.dialog(), textSelection) {
					t.Fatal(r.plain())
				}
				if len(r.back.real) != 0 {
					t.Fatal("selection change never sends")
				}
				if sn == "preview" {
					last := r.back.events[len(r.back.events)-1]
					if last.Phase != "canceled" {
						t.Fatalf("the plan is discarded: %+v", last)
					}
				}
			})
		}
	}
}

func TestReorderedRowsKeepTheFrozenTarget(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.m.depCursor = 2
	want := r.m.visibleDeployments()[2].UID
	r.keys("o")
	r.m.snap.Deployments = r.m.snap.Deployments[1:]
	r.run(r.send(tea.FocusMsg{}))
	r.stage(actionChoosing)
	if r.m.depCursor != 1 || r.m.act.target.UID != want {
		t.Fatalf("cursor %d target %+v", r.m.depCursor, r.m.act.target)
	}
}

func TestLateAsyncResultsAreDropped(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.send(keyPress("o"))
	observe := r.send(keyPress("enter"))
	r.keys("esc")
	r.run(observe)
	r.stage(actionClosed)

	r.choose(guard.Scale)
	r.text("5")
	r.toButton(controlPrepare)
	prepare := r.send(keyPress("enter"))
	msg := prepare()
	if p, ok := msg.(actionPreparedMsg); !ok || p.err != nil {
		t.Fatalf("%+v", msg)
	}
	r.keys("esc")
	r.run(r.send(msg))
	r.stage(actionClosed)
	last := r.back.events[len(r.back.events)-1]
	if last.Phase != "canceled" || len(r.back.real) != 0 {
		t.Fatalf("a late preview is discarded: %+v", last)
	}
}

func TestCancelWaitReportsUnknown(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	r.back.blockSend, r.back.sending = make(chan struct{}), make(chan struct{})
	r.toButton(controlConfirm)
	execute := r.send(keyPress("enter"))
	done := make(chan tea.Msg, 1)
	go func() { done <- execute() }()
	<-r.back.sending
	r.keys("esc")
	r.stage(actionExecuting)
	if !strings.Contains(r.dialog(), textWaitCanceled) {
		t.Fatal(r.plain())
	}
	r.run(r.send(<-done))
	r.stage(actionResult)
	if !strings.Contains(r.dialog(), textUnknown) {
		t.Fatal(r.plain())
	}
	r.sent(1, 1)
}

func TestAuditBlockedDuringPreviewStopsConfirmation(t *testing.T) {
	r := writableRig(t)
	r.scalePreview("5")
	other, err := r.exec.Prepare(t.Context(), r.m.actionRequest())
	if err != nil {
		t.Fatal(err)
	}
	r.back.failPhase = "canceled"
	if err = r.exec.Discard(other.ID); err == nil {
		t.Fatal("the fixture must break the audit")
	}
	r.toButton(controlConfirm)
	r.keys("enter")
	r.stage(actionResult)
	if !strings.Contains(r.dialog(), textAuditBlocked) || len(r.back.real) != 0 {
		t.Fatal(r.plain())
	}
}

func TestDialogTextIsSanitized(t *testing.T) {
	r := writableRig(t)
	r.onDeployment()
	r.m.act = actionState{stage: actionChoosing, target: guard.Target{Version: "v1", Resource: "pods", Namespace: "n\x1b]52;c;eA==\x07s", Name: "a\u202eb\x1b[31m", UID: "u", ResourceVersion: "1"}, context: "c\x1b[0m"}
	for _, bad := range []string{"\x1b]", "\x07", "\u202e", "\x1b[31m"} {
		if strings.Contains(r.plain(), bad) {
			t.Fatalf("%q leaked", bad)
		}
	}
}

func TestDialogFitsMinimumTerminal(t *testing.T) {
	for _, action := range []guard.Action{guard.Scale, guard.Delete} {
		r := newActionRig(t, 80, 24, guard.Environment{Valid: true}, true)
		r.back.obs.Owner, r.back.obs.Terminating = "ReplicaSet/"+strings.Repeat("x", 60), true
		r.m.selName = r.m.snap.Pods[1].Key()
		if action == guard.Scale {
			r.keys("3")
			r.m.setFocus(FocusDeployments)
		}
		r.choose(action)
		if action == guard.Scale {
			r.text("0")
		}
		r.toButton(controlPrepare)
		r.keys("enter")
		r.stage(actionPreview)
		plain := r.plain()
		for _, want := range []string{"[Cancelar]", "[Voltar aos parametros]", "[" + r.m.act.spec.confirm + "]", "Digite exatamente"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("missing %q at 80x24:\n%s", want, plain)
			}
		}
		for range 10 {
			r.keys("pgdown")
		}
		if !strings.Contains(r.plain(), "Nenhuma alteracao real foi enviada.") {
			t.Fatalf("scroll reaches the last line:\n%s", r.plain())
		}
	}
}

func TestActionsReleaseEverythingOnClose(t *testing.T) {
	baseline := goleak.IgnoreCurrent()
	r := writableRig(t)
	r.back.block = make(chan struct{})
	r.onDeployment()
	r.choose(guard.Scale)
	r.text("5")
	r.toButton(controlPrepare)
	prepare := r.send(keyPress("enter"))
	done := make(chan tea.Msg, 1)
	go func() { done <- prepare() }()
	r.keys("esc")
	r.run(r.send(<-done))
	r.stage(actionClosed)
	r.exec.Close()
	goleak.VerifyNone(t, baseline)
}

func TestActionGoldens(t *testing.T) {
	fix := func(r *actionRig) string {
		if r.m.act.preview.ID != "" {
			r.m.act.preview.ID = "fixture-scale-01"
		}
		if r.m.act.opID != "" {
			r.m.act.opID = "fixture-operation-01"
		}
		return r.plain()
	}
	for _, size := range [][2]int{{80, 24}, {130, 40}, {160, 48}} {
		suffix := fmt.Sprintf("_%dx%d.txt", size[0], size[1])
		open := func(env guard.Environment, writes bool) *actionRig {
			return newActionRig(t, size[0], size[1], env, writes)
		}
		deployment := func(r *actionRig) { r.keys("3"); r.m.setFocus(FocusDeployments); r.m.depCursor = 0 }

		r := open(guard.Environment{Valid: true}, false)
		deployment(r)
		r.keys("o", "enter")
		golden(t, "action_readonly"+suffix, fix(r))

		r = open(guard.Environment{Valid: true, Protected: true}, true)
		deployment(r)
		r.keys("o")
		golden(t, "action_protected"+suffix, fix(r))

		r = open(guard.Environment{Valid: true}, true)
		deployment(r)
		r.scalePreviewHere("5")
		golden(t, "action_scale_preview"+suffix, fix(r))
		r.back.sendErr = errors.New("reset")
		r.toButton(controlConfirm)
		r.keys("enter")
		golden(t, "action_unknown"+suffix, fix(r))

		r = open(guard.Environment{Valid: true}, true)
		deployment(r)
		r.choose(guard.Delete)
		r.toButton(controlPrepare)
		r.keys("enter")
		golden(t, "action_delete_preview"+suffix, fix(r))

		r = open(guard.Environment{Valid: true}, true)
		r.keys("4")
		r.choose(guard.Cordon)
		golden(t, "action_cordon_preview"+suffix, fix(r))

		r = open(guard.Environment{Valid: true}, true)
		deployment(r)
		r.scalePreviewHere("5")
		r.back.failPhase = "result"
		r.toButton(controlConfirm)
		r.keys("enter")
		golden(t, "action_audit_failed"+suffix, fix(r))
	}
	c := newActionRig(t, 130, 40, guard.Environment{Valid: true}, true)
	if c.m.snap.Synthetic || c.m.accessMode() != (data.EffectiveMode{Valid: true}) {
		t.Fatal("the fixture is an explicit synthetic RW session")
	}
}

func (r *actionRig) scalePreviewHere(n string) {
	r.t.Helper()
	r.choose(guard.Scale)
	r.text(n)
	r.toButton(controlPrepare)
	r.keys("enter")
	r.stage(actionPreview)
}
