package utils

import (
	"fmt"
	"github.com/jesseduffield/gocui"
	"github.com/spkg/bom"
	"regexp"
	"sort"
	"strings"
)

func CleanString(s string) string {
	output := string(bom.Clean([]byte(s)))
	return NormalizeLinefeeds(output)
}

func NormalizeLinefeeds(str string) string {
	str = strings.ReplaceAll(str, "\r\n", "\n")
	str = strings.ReplaceAll(str, "\r", "")
	return str
}

func OptionsMapToString(optionsMap map[string]string) string {
	optionsArray := make([]string, 0)
	for key, description := range optionsMap {
		optionsArray = append(optionsArray, key+": "+description)
	}
	sort.Strings(optionsArray)
	return strings.Join(optionsArray, ", ")
}

func DeleteExtraSpace(s string) string {
	s1 := strings.ReplaceAll(s, "	", " ")
	regx := "\\s{2,}"
	reg, _ := regexp.Compile(regx)
	s2 := make([]byte, len(s1))
	copy(s2, s1)
	spcIndex := reg.FindStringIndex(string(s2))
	for len(spcIndex) > 0 {
		s2 = append(s2[:spcIndex[0]+1], s2[spcIndex[1]:]...)
		spcIndex = reg.FindStringIndex(string(s2))
	}
	return string(s2)
}

var keyNames = map[gocui.Key]string{
	gocui.KeyEsc:         "esc",
	gocui.KeyEnter:       "enter",
	gocui.KeySpace:       "space",
	gocui.KeyTab:         "tab",
	gocui.KeyArrowRight:  "►",
	gocui.KeyArrowLeft:   "◄",
	gocui.KeyArrowUp:     "▲",
	gocui.KeyArrowDown:   "▼",
	gocui.KeyPgup:        "PgUp",
	gocui.KeyPgdn:        "PgDn",
	gocui.KeyHome:        "Home",
	gocui.KeyEnd:         "End",
	gocui.KeyF1:          "F1",
	gocui.KeyF2:          "F2",
	gocui.KeyF3:          "F3",
	gocui.KeyF4:          "F4",
	gocui.KeyF5:          "F5",
	gocui.KeyF6:          "F6",
	gocui.KeyF7:          "F7",
	gocui.KeyF8:          "F8",
	gocui.KeyF9:          "F9",
	gocui.KeyF10:         "F10",
	gocui.KeyF11:         "F11",
	gocui.KeyF12:         "F12",
	gocui.KeyCtrlC:       "Ctrl+c",
	gocui.MouseLeft:      "MouseLeft",
	gocui.MouseWheelUp:   "WheelUp",
	gocui.MouseWheelDown: "WheelDown",
}

func GetKey(key interface{}) string {
	switch k := key.(type) {
	case rune:
		return runeKeyName(k)
	case gocui.Key:
		if name, ok := keyNames[k]; ok {
			return name
		}
		return fmt.Sprintf("%c", rune(k))
	default:
		return fmt.Sprintf("%v", key)
	}
}

func runeKeyName(r rune) string {
	if r == ' ' {
		return "space"
	}
	if r >= 'A' && r <= 'Z' {
		return fmt.Sprintf("Shift+%c", r+32)
	}
	return string(r)
}
