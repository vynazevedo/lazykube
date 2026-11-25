package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/ui"
)

func replacePod(m Model, key, uid string) Model {
	pods := make([]data.Pod, 0, len(m.snap.Pods))
	for _, p := range m.snap.Pods {
		if p.Key() == key {
			p.UID, p.Image = uid, "replacement-image"
		}
		pods = append(pods, p)
	}
	m.snap.Pods = pods
	return m
}

func TestRadarRefusesRecreatedPod(t *testing.T) {
	base := newRadarHarness(t).active(160, 48)
	base.after = instantTimer
	sig := base.selectedSignal()
	key := data.PodKey(sig.Entity.Namespace, sig.Entity.Name)

	posture := base
	posture.security = posture.security.Clone()
	p := posture.security.Pods[key]
	p.Target.UID = "replacement-uid"
	posture.security.Pods[key] = p

	absent := base
	absent.snap.Pods = nil
	for _, pod := range base.snap.Pods {
		if pod.Key() != key {
			absent.snap.Pods = append(absent.snap.Pods, pod)
		}
	}

	for name, tc := range map[string]struct {
		m      Model
		target ui.PodTarget
		notice string
	}{
		"snapshot ahead":  {replacePod(base, key, "replacement-uid"), ui.PodRecreated, "pod original não existe mais"},
		"posture ahead":   {posture, ui.PodRecreated, "pod original não existe mais"},
		"identity absent": {replacePod(base, key, ""), ui.PodUnidentified, "UID do pod não observado"},
		"pod absent":      {absent, ui.PodGone, "alvo original não existe mais"},
	} {
		if _, target := tc.m.signalPod(sig); target != tc.target {
			t.Fatalf("%s: target %d", name, target)
		}
		for _, w := range [][2]int{{160, 48}, {80, 24}} {
			m := deliver(t, tc.m, tea.WindowSizeMsg{Width: w[0], Height: w[1]})
			next, cmd := m.Update(keyPress("enter"))
			got := next.(Model)
			if got.nav != m.nav || cmd == nil || !strings.Contains(got.notice.text, tc.notice) {
				t.Fatalf("%s %v: enter must refuse and say why, notice %q", name, w, got.notice.text)
			}
			if !strings.Contains(got.Render().Plain(), tc.notice) {
				t.Fatalf("%s %v: the refusal is visible", name, w)
			}
		}
		if _, _, ok := ui.RadarPodButton(tc.m.radarViewState(), 100); ok {
			t.Fatalf("%s: no button for another pod", name)
		}
	}
}

func TestPodRouteNeverAdoptsReplacement(t *testing.T) {
	m := press(t, newRadarHarness(t).active(160, 48), "enter")
	r := m.nav.Active()
	if r.Kind != RoutePod || m.selectedPod() == nil || m.selectedPod().UID != r.Ref.UID {
		t.Fatalf("the original pod opens: %+v", r)
	}
	m = replacePod(m, r.Pod, "replacement-uid")
	m.refresh()
	if m.selectedPod() != nil || m.detailTarget() != "" || m.manifest != nil || len(m.logs) != 0 {
		t.Fatal("an open detail drops the data when the pod is replaced")
	}
	if plain := m.Render().Plain(); !strings.Contains(plain, "pod original não existe mais, recriado com outro UID") || strings.Contains(plain, "replacement-image") {
		t.Fatal("the detail says the original is gone")
	}
}

func TestSecuritySelectionNeverAdoptsHomonym(t *testing.T) {
	m := onSecurity(t, demoModel(t, 80, 24))
	m.after = instantTimer
	key := m.snap.Pods[1].Key()
	standalone := func(uid string) Model {
		m.security = m.security.Clone()
		p := m.security.Pods[key]
		p.Owner, p.Target.UID = nil, uid
		m.security.Pods[key] = p
		m.security.Revision++
		m.securitySession.Update(m.security)
		return settleSecurity(deliver(t, m, tea.FocusMsg{}))
	}
	m = standalone("first")
	for i, w := range m.sec.result.workloads {
		if w.Key.Pod == key {
			m.adoptSecurityWorkload(i)
		}
	}
	m = press(t, settleSecurity(deliver(t, m, tea.FocusMsg{})), "tab")
	first := m.sec.Selected
	if first.Pod != key || first.UID != "first" || m.layout.SecEvidence.Empty() {
		t.Fatalf("a standalone pod is selected by UID: %+v", first)
	}
	m = standalone("second")
	if m.sec.Selected == first || m.sec.Focus != ui.SecurityFocusList || m.layout.SecList.Empty() || !strings.Contains(m.notice.text, "não existe mais") {
		t.Fatalf("a recreated pod is another workload and the change is visible: %+v %q", m.sec.Selected, m.notice.text)
	}
}
