package config

type GuiConfig struct {
	Highlight  bool  `yaml:"highlight"`
	Cursor     bool  `yaml:"cursor"`
	FgColor    Color `yaml:"fg_color"`
	BgColor    Color `yaml:"bg_color"`
	SelBgColor Color `yaml:"sel_bg_color"`
	SelFgColor Color `yaml:"sel_fg_color"`
	Mouse      bool  `yaml:"mouse"`
	InputEsc   bool  `yaml:"input_esc"`
}
