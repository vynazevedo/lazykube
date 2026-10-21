package ui

import (
	"github.com/vynazevedo/lazykube/internal/draw"
	"github.com/vynazevedo/lazykube/internal/theme"
)

type StepState int

const (
	StepPending StepState = iota
	StepRunning
	StepDone
	StepFailed
)

type LoadingStep struct {
	Title  string
	Detail string
	State  StepState
}

type LoadingState struct {
	Steps    []LoadingStep
	Progress float64
	Error    string
	Target   string
	Previous string
	Hints    []KeyHelp
}

const (
	loadingMaxW    = 64
	loadingErrRows = 4
	logoRows       = 3
)

var logoFont = map[rune][logoRows]string{
	'l': {"█  ", "█  ", "▀▀▀"},
	'a': {"▄▀▄", "█▀█", "▀ ▀"},
	'z': {"▀▀█", "▄▀ ", "▀▀▀"},
	'y': {"█ █", "▀█▀", " ▀ "},
	'k': {"█ ▄", "█▀▄", "▀ ▀"},
	'u': {"█ █", "█ █", "▀▀▀"},
	'b': {"█▀▄", "█▀▄", "▀▀ "},
	'e': {"█▀▀", "█▀▀", "▀▀▀"},
}

func logoLines(word string) [logoRows][]rune {
	var out [logoRows][]rune
	for i, ch := range word {
		glyph := logoFont[ch]
		for row := range out {
			if i > 0 {
				out[row] = append(out[row], ' ')
			}
			out[row] = append(out[row], []rune(glyph[row])...)
		}
	}
	return out
}

func (st LoadingState) failed() (LoadingStep, bool) {
	for _, s := range st.Steps {
		if s.State == StepFailed {
			return s, true
		}
	}
	return LoadingStep{}, false
}

func Loading(r draw.Region, st LoadingState, ctx Ctx) {
	r.Fill(theme.BgApp)
	if r.Empty() {
		return
	}
	w := min(loadingMaxW, max(0, r.W-4))
	logo := logoLines("lazykube")
	bigLogo := len(logo[0]) <= r.W-2
	failedStep, failed := st.failed()
	var errLines []string
	if failed {
		errLines = draw.Wrap(st.Error, max(1, w-2))
		if len(errLines) > loadingErrRows {
			errLines = errLines[:loadingErrRows]
			errLines[loadingErrRows-1] = draw.Truncate(errLines[loadingErrRows-1]+" …", w-2)
		}
	}

	logoH := 1
	if bigLogo {
		logoH = logoRows
	}
	body := logoH + 2 + 2 + len(st.Steps) + 2
	if st.Target != "" {
		body++
	}
	if failed {
		body += 2 + len(errLines)
	}
	spriteRows := SpriteH / 2
	showSprite := r.H >= body+spriteRows+3 && r.W >= SpriteW+4
	total := body
	if showSprite {
		total += spriteRows + 1
	}
	y := max(0, (r.H-total)/2)

	if showSprite {
		glow := theme.Mix(theme.BgApp, ctx.Pal.C, GlowFactor(ctx.Clock.Phase)+0.06)
		if failed {
			glow = theme.Mix(theme.BgApp, theme.Err, 0.18)
		}
		px := Sprite(ctx.Pal, ctx.Clock.FX(), func(py int) theme.RGB {
			if py >= 22 {
				return glow
			}
			return theme.BgApp
		})
		r.HalfBlock((r.W-SpriteW)/2, y, px)
		y += spriteRows + 1
	}

	if bigLogo {
		width := len(logo[0])
		colors := theme.TextGradient(ctx.Pal, width, ctx.Clock.Phase)
		x := (r.W - width) / 2
		for row, runes := range logo {
			for i, ch := range runes {
				if ch != ' ' {
					r.Put(x+i, y+row, ch, draw.Fg(colors[i]).WithBold())
				}
			}
		}
	} else {
		word := "lazykube"
		colors := theme.TextGradient(ctx.Pal, len(word), ctx.Clock.Phase)
		x := max(0, (r.W-len(word))/2)
		for i, ch := range word {
			r.Put(x+i, y, ch, draw.Fg(colors[i]).WithBold())
		}
	}
	y += logoH + 1

	subtitle := "conectando ao cluster"
	switch {
	case failed:
		subtitle = "falha na conexão"
	case st.Progress >= 1:
		subtitle = "pronto"
	}
	sub := draw.Line{muted(Version + "  ·  "), text(subtitle)}.Truncate(r.W)
	r.Line(max(0, (r.W-sub.Width())/2), y, sub)
	y += 2
	if st.Target != "" {
		dest := draw.Line{muted("destino "), bold(draw.Sanitize(st.Target), ctx.Pal.C)}
		if st.Previous != "" {
			dest = append(dest, muted("  ·  anterior "), text(draw.Sanitize(st.Previous)))
		}
		dest = dest.Truncate(r.W)
		r.Line(max(0, (r.W-dest.Width())/2), y-1, dest)
		y++
	}

	x := (r.W - w) / 2
	loadingBar(r.Sub(x, y, w, 1), st.Progress, failed, ctx)
	y += 2

	for _, s := range st.Steps {
		stepRow(r.Sub(x, y, w, 1), s, ctx)
		y++
	}
	y++

	if failed {
		head := draw.Line{bold("✗ falhou: ", theme.Err), colored(failedStep.Title, theme.Err)}.Truncate(w)
		r.Line(x, y, head)
		y++
		for _, l := range errLines {
			r.Text(x+2, y, l, draw.Fg(theme.TextSoft))
			y++
		}
		y++
	}

	hint := draw.Line{bold("ctrl+c", ctx.Pal.A), muted(" cancelar")}
	if failed {
		hint = draw.Line{bold("q", ctx.Pal.A), muted(" sair")}
	}
	if len(st.Hints) > 0 {
		hint = nil
		for i, h := range st.Hints {
			if i > 0 {
				hint = append(hint, faint("  ·  "))
			}
			hint = append(hint, bold(h.Key, ctx.Pal.A), muted(" "+h.Desc))
		}
	}
	hint = hint.Truncate(r.W)
	r.Line(max(0, (r.W-hint.Width())/2), y, hint)
}

func loadingBar(r draw.Region, progress float64, failed bool, ctx Ctx) {
	filled := draw.Filled(progress, 1, r.W)
	for i := 0; i < r.W; i++ {
		c := theme.BorderSoft
		if i < filled {
			c = theme.Cycle(ctx.Pal, float64(i)/float64(max(1, r.W))-ctx.Clock.Phase/3)
			if failed {
				c = theme.Mix(theme.Err, theme.BgApp, 0.35)
			}
		}
		r.Put(i, 0, '━', draw.Fg(c))
	}
}

func stepRow(r draw.Region, s LoadingStep, ctx Ctx) {
	glyph, glyphColor, titleColor := "·", theme.Faint, theme.Faint
	switch s.State {
	case StepRunning:
		glyph, glyphColor, titleColor = string(ctx.Clock.Spinner()), ctx.Pal.A, theme.Text
	case StepDone:
		glyph, glyphColor, titleColor = "✓", theme.OK, theme.TextMid
	case StepFailed:
		glyph, glyphColor, titleColor = "✗", theme.Err, theme.Err
	}
	left := draw.Line{colored(glyph, glyphColor), colored("  "+s.Title, titleColor)}
	if s.State == StepRunning {
		left[1] = bold("  "+s.Title, titleColor)
	}
	detail := ""
	if s.State != StepPending && s.State != StepFailed {
		detail = s.Detail
	}
	room := r.W - left.Width() - 2
	if detail != "" && room >= 4 {
		d := draw.Truncate(detail, room)
		r.Text(r.W-draw.Width(d), 0, d, draw.Fg(theme.Muted))
	}
	r.Line(0, 0, left.Truncate(r.W))
}
