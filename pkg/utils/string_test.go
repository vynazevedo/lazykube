package utils

import (
	"testing"

	"github.com/jesseduffield/gocui"
)

func TestGetKey(t *testing.T) {
	tests := []struct {
		name string
		key  interface{}
		want string
	}{
		{name: "lower rune", key: 'f', want: "f"},
		{name: "upper rune", key: 'C', want: "Shift+c"},
		{name: "space rune", key: ' ', want: "space"},
		{name: "esc", key: gocui.KeyEsc, want: "esc"},
		{name: "enter", key: gocui.KeyEnter, want: "enter"},
		{name: "tab", key: gocui.KeyTab, want: "tab"},
		{name: "arrow up", key: gocui.KeyArrowUp, want: "▲"},
		{name: "arrow down", key: gocui.KeyArrowDown, want: "▼"},
		{name: "arrow left", key: gocui.KeyArrowLeft, want: "◄"},
		{name: "arrow right", key: gocui.KeyArrowRight, want: "►"},
		{name: "page up", key: gocui.KeyPgup, want: "PgUp"},
		{name: "page down", key: gocui.KeyPgdn, want: "PgDn"},
		{name: "f3", key: gocui.KeyF3, want: "F3"},
		{name: "f4", key: gocui.KeyF4, want: "F4"},
		{name: "unsupported type", key: 42, want: "42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetKey(tt.key); got != tt.want {
				t.Fatalf("GetKey(%v) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}
