package data

import "strings"

type Stage int

const (
	StageKubeconfig Stage = iota
	StageConnect
	StagePermissions
	StageSync
)

func Stages() []Stage {
	return []Stage{StageKubeconfig, StageConnect, StagePermissions, StageSync}
}

type Progress struct {
	Stage   Stage
	Done    bool
	Context string
	Detail  string
}

type escState int

const (
	escNone escState = iota
	escStart
	escCSI
	escString
	escStringEnd
)

func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	state := escNone
	for _, r := range s {
		switch state {
		case escStart:
			switch r {
			case '[':
				state = escCSI
			case ']', 'P', '_', '^', 'X':
				state = escString
			default:
				state = escNone
			}
			continue
		case escCSI:
			if r >= 0x40 && r <= 0x7e {
				state = escNone
			}
			continue
		case escString:
			switch r {
			case 0x07:
				state = escNone
			case 0x1b:
				state = escStringEnd
			}
			continue
		case escStringEnd:
			state = escNone
			continue
		}
		switch {
		case r == 0x1b:
			state = escStart
		case r == '\t':
			b.WriteString("    ")
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
