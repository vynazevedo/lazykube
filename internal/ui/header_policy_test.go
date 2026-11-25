package ui

import (
	"strings"
	"testing"

	"github.com/vynazevedo/lazykube/internal/data"
	"github.com/vynazevedo/lazykube/internal/draw"
)

func TestHeaderPolicyRemainsVisible(t *testing.T) {
	for _, width := range []int{130, 160} {
		c := canvas(width, 1, func(r draw.Region) {
			Header(r, HeaderState{Cluster: data.Cluster{Context: strings.Repeat("production", 40)}, ReadOnly: true, Protected: true, SecurityAlerts: 20}, fixedCtx())
		})
		text := c.Plain()
		if !strings.Contains(text, "[RO]") || !strings.Contains(text, "[PROTECTED]") {
			t.Fatal(text)
		}
	}
}

func TestHeaderModesKeepContextAtEveryWidth(t *testing.T) {
	cluster := data.Cluster{Context: "laboratorio", Namespace: "apps"}
	for _, tc := range []struct {
		st    HeaderState
		wide  string
		small string
	}{
		{HeaderState{ReadOnly: true}, "[RO]", "[RO]"},
		{HeaderState{Writable: true}, "[RW: --read-write]", "[RW]"},
		{HeaderState{ReadOnly: true, Protected: true}, "[RO] [PROTECTED]", "[RO] [PROTECTED]"},
		{HeaderState{ReadOnly: true, ConfigInvalid: true, Protected: true}, "[RO] [CONFIG INVALIDA]", "[RO] [CONFIG]"},
		{HeaderState{Writable: true, AuditBlocked: true}, "[RW: --read-write] [ACOES BLOQUEADAS: AUDIT]", "[RW] [AUDIT]"},
	} {
		for _, width := range []int{80, 130, 160} {
			st := tc.st
			st.Cluster = cluster
			want := tc.wide
			if width < 100 {
				want = tc.small
			}
			text := canvas(width, 1, func(r draw.Region) { Header(r, st, fixedCtx()) }).Plain()
			if !strings.Contains(text, want) || !strings.Contains(text, "laboratorio") || !strings.Contains(text, "namespace apps") {
				t.Fatalf("%d cols: want %q in %q", width, want, text)
			}
		}
	}
}
