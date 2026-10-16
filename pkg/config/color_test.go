package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestColorUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Color
	}{
		{name: "legacy default", input: "0", want: ColorDefault},
		{name: "legacy green", input: "3", want: ColorGreen},
		{name: "legacy white", input: "8", want: ColorWhite},
		{name: "legacy out of range", input: "42", want: ColorDefault},
		{name: "negative", input: "-1", want: ColorDefault},
		{name: "name", input: "blue", want: ColorBlue},
		{name: "name mixed case", input: "MaGenTa", want: ColorMagenta},
		{name: "unknown name", input: "purple", want: ColorDefault},
		{name: "empty", input: `""`, want: ColorDefault},
		{name: "sequence", input: "[1, 2]", want: ColorDefault},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Color
			if err := yaml.Unmarshal([]byte(tt.input), &got); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLegacyGuiConfigIsReadable(t *testing.T) {
	legacy := []byte("highlight: true\nfg_color: 8\nbg_color: 0\nsel_bg_color: 0\nsel_fg_color: 3\nmouse: true\n")

	var cfg GuiConfig
	if err := yaml.Unmarshal(legacy, &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FgColor != ColorWhite || cfg.SelFgColor != ColorGreen || cfg.BgColor != ColorDefault {
		t.Fatalf("unexpected colors: %+v", cfg)
	}
}

func TestColorRoundTrip(t *testing.T) {
	out, err := yaml.Marshal(GuiConfig{FgColor: ColorWhite, SelFgColor: ColorGreen})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg GuiConfig
	if err := yaml.Unmarshal(out, &cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.FgColor != ColorWhite || cfg.SelFgColor != ColorGreen {
		t.Fatalf("round trip lost colors: %+v", cfg)
	}
}

func TestParseColor(t *testing.T) {
	if _, err := ParseColor("nope"); err == nil {
		t.Fatal("expected error for unknown color")
	}
	got, err := ParseColor(" Cyan ")
	if err != nil || got != ColorCyan {
		t.Fatalf("got %q, %v", got, err)
	}
}
