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
