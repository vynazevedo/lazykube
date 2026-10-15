package config

import (
	"fmt"
	"strings"

	"github.com/jesseduffield/gocui"
	"gopkg.in/yaml.v3"
)

type Color string

const (
	ColorDefault Color = "default"
	ColorBlack   Color = "black"
	ColorRed     Color = "red"
	ColorGreen   Color = "green"
	ColorYellow  Color = "yellow"
	ColorBlue    Color = "blue"
	ColorMagenta Color = "magenta"
	ColorCyan    Color = "cyan"
	ColorWhite   Color = "white"
)

var legacyTermboxColors = []Color{
	ColorDefault,
	ColorBlack,
	ColorRed,
	ColorGreen,
	ColorYellow,
	ColorBlue,
	ColorMagenta,
	ColorCyan,
	ColorWhite,
}

var colorAttributes = map[Color]gocui.Attribute{
	ColorDefault: gocui.ColorDefault,
	ColorBlack:   gocui.ColorBlack,
	ColorRed:     gocui.ColorRed,
	ColorGreen:   gocui.ColorGreen,
	ColorYellow:  gocui.ColorYellow,
	ColorBlue:    gocui.ColorBlue,
	ColorMagenta: gocui.ColorMagenta,
	ColorCyan:    gocui.ColorCyan,
	ColorWhite:   gocui.ColorWhite,
}

func ParseColor(value string) (Color, error) {
	normalized := Color(strings.ToLower(strings.TrimSpace(value)))
	if normalized == "" {
		return ColorDefault, nil
	}
	if _, ok := colorAttributes[normalized]; !ok {
		return ColorDefault, fmt.Errorf("unknown color %q", value)
	}
	return normalized, nil
}

func colorFromLegacyIndex(index int) (Color, error) {
	if index < 0 || index >= len(legacyTermboxColors) {
		return ColorDefault, fmt.Errorf("unknown legacy color index %d", index)
	}
	return legacyTermboxColors[index], nil
}

func (c Color) Attribute() gocui.Attribute {
	if attr, ok := colorAttributes[c]; ok {
		return attr
	}
	return gocui.ColorDefault
}

func (c *Color) UnmarshalYAML(node *yaml.Node) error {
	*c = decodeColorNode(node)
	return nil
}

func decodeColorNode(node *yaml.Node) Color {
	if node.Kind != yaml.ScalarNode {
		return ColorDefault
	}

	var index int
	if node.Tag == "!!int" && node.Decode(&index) == nil {
		color, _ := colorFromLegacyIndex(index)
		return color
	}

	color, _ := ParseColor(node.Value)
	return color
}
