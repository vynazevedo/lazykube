package app

import (
	"fmt"
	"strings"
	"testing"
)

func TestSecuritySelectedPodClarity(t *testing.T) {
	for _, size := range [][2]int{{130, 40}, {160, 48}} {
		m := press(t, demoModel(t, size[0], size[1]), "S")
		check := func() {
			t.Helper()
			score := m.securitySession.Dashboard.Pods[m.activeName()]
			want := fmt.Sprintf("Pod %d %s", score.Value, score.Grade)
			out := m.Render().Plain()
			if !strings.Contains(out, want) || !strings.Contains(out, "Namespace") || !strings.Contains(out, "Cluster") {
				t.Fatalf("selected score hidden: %s", out)
			}
			rows := strings.Join(m.securityView.Rows, "\n")
			if strings.Index(rows, "Field:") > strings.Index(rows, "ALERTS") {
				t.Fatal("pod evidence below alerts")
			}
		}
		check()
		m.selectPod(2)
		m.refresh()
		check()
		m = press(t, m, "enter")
		m = press(t, m, "S")
		check()
		golden(t, fmt.Sprintf("pod_security_clarity_%d.txt", size[0]), m.Render().Plain())
	}
}
