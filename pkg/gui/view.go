package gui

import (
	"fmt"
	"github.com/TNK-Studio/lazykube/pkg/log"
	"github.com/TNK-Studio/lazykube/pkg/utils"
	"github.com/jesseduffield/gocui"
	"strings"
)

var (
	NotEnoughSpace *View
)

func init() {
	NotEnoughSpace = &View{
		Name:  "notEnoughSpace",
		Title: "Not enough space to render.",
		DimensionFunc: func(gui *Gui, view *View) (int, int, int, int) {
			maxWidth, maxHeight := gui.Size()
			return 0, 0, maxWidth - 1, maxHeight - 1
		},
		OnRender: func(gui *Gui, view *View) error {
			gui.Config.Cursor = false
			gui.Configure()
			view.ReRender()
			return nil
		},
	}
}

type (
	View struct {
		Actions               []ActionInterface
		Name                  string
		Title                 string
		SelectedLine          string
		OnClick               ViewHandler
		OnLineClick           func(gui *Gui, view *View, cy int, lineString string) error
		OnRender              ViewHandler
		OnRenderOptions       ViewHandler
		OnFocus               ViewHandler
		OnFocusLost           ViewHandler
		OnEditedChange        func(gui *Gui, view *View, key gocui.Key, ch rune, mod gocui.Modifier)
		OnSelectedLineChange  func(gui *Gui, view *View, selectedLine string) error
		DimensionFunc         DimensionFunc
		UpperLeftPointXFunc   ViewPointFunc
		UpperLeftPointYFunc   ViewPointFunc
		LowerRightPointXFunc  ViewPointFunc
		LowerRightPointYFunc  ViewPointFunc
		ZIndex                int
		x0                    int
		y0                    int
		x1                    int
		y1                    int
		gui                   *Gui
		v                     *gocui.View
		state                 State
		FgColor               gocui.Attribute
		BgColor               gocui.Attribute
		SelBgColor            gocui.Attribute
		SelFgColor            gocui.Attribute
		Clickable             bool
		Editable              bool
		Wrap                  bool
		Autoscroll            bool
		IgnoreCarriageReturns bool
		Highlight             bool
		NoFrame               bool
		CanNotReturn          bool
		renderTimes           int
		AlwaysOnTop           bool
		content               renderedContent
	}

	ViewHandler func(gui *Gui, view *View) error
)

func (view *View) InitView() {
	if view.state == nil {
		view.state = NewStateMap()
	}
	if view.v != nil {
		view.v.Title = view.Title
		view.v.Wrap = view.Wrap
		view.v.Editable = view.Editable
		view.v.Autoscroll = view.Autoscroll
		view.v.Highlight = view.Highlight
		view.v.Frame = !view.NoFrame
		view.v.FgColor = view.FgColor
		view.v.BgColor = view.BgColor
		view.v.SelBgColor = view.SelBgColor
		view.v.SelFgColor = view.SelFgColor
		view.v.IgnoreCarriageReturns = view.IgnoreCarriageReturns
		view.v.Editor = NewViewEditor(view.gui, view)
	}
}

func (view *View) BindGui(gui *Gui) {
	view.gui = gui
}

func (view *View) InitDimension() {
	if !view.IsBindingGui() {
		log.Logger.Warningf("Please run 'InitDimension' after binding Gui.")
		return
	}

	if view.DimensionFunc == nil {
		return
	}

	view.x0, view.y0, view.x1, view.y1 = view.DimensionFunc(view.gui, view)
}

func (view *View) UpperLeftPointX() int {
	if view.IsBindingGui() && view.UpperLeftPointXFunc != nil {
		return view.UpperLeftPointXFunc(view.gui, view)
	}
	return view.x0
}

func (view *View) UpperLeftPointY() int {
	if view.IsBindingGui() && view.UpperLeftPointYFunc != nil {
		return view.UpperLeftPointYFunc(view.gui, view)
	}
	return view.y0
}

func (view *View) LowerRightPointX() int {
	if view.IsBindingGui() && view.LowerRightPointXFunc != nil {
		return view.LowerRightPointXFunc(view.gui, view)
	}
	return view.x1
}

func (view *View) LowerRightPointY() int {
	if view.IsBindingGui() && view.LowerRightPointYFunc != nil {
		return view.LowerRightPointYFunc(view.gui, view)
	}
	return view.y1
}

func (view *View) GetDimensions() (int, int, int, int) {
	view.InitDimension()
	x0, y0, x1, y1 := view.UpperLeftPointX(), view.UpperLeftPointY(), view.LowerRightPointX(), view.LowerRightPointY()
	return x0, y0, x1, y1
}

func (view *View) IsBindingGui() bool {
	if view.gui != nil && view.gui.g != nil {
		return true
	}

	return false
}

func (view *View) Rendered() bool {
	return view.v != nil
}

func (view *View) SetViewContent(s string) error {
	view.v.Clear()
	view.content.reset()
	if _, err := fmt.Fprint(view.v, utils.CleanString(s)); err != nil {
		return err
	}
	view.content.set(s)
	return nil
}

func (view *View) SetOrigin(x, y int) {
	if view.Rendered() {
		view.v.SetOrigin(x, y)
	}
}

func (view *View) Origin() (int, int) {
	return view.v.Origin()
}

func (view *View) SetCursor(x, y int) {
	if view.Rendered() {
		view.v.SetCursor(x, y)
	}
}

func (view *View) Write(p []byte) (n int, err error) {
	view.content.reset()
	return view.v.Write(p)
}

func (view *View) Clear() {
	view.content.reset()
	if view.Rendered() {
		view.v.Clear()
	}
}

func (view *View) Cursor() (int, int) {
	return view.v.Cursor()
}

func (view *View) ViewBufferLines() []string {
	return view.v.ViewBufferLines()
}

func (view *View) ViewBuffer() string {
	return view.v.ViewBuffer()
}

func (view *View) Line(y int) (string, error) {
	line, ok := view.v.Line(y)
	if !ok {
		return "", ErrInvalidLine
	}
	return strings.ReplaceAll(line, "\x00", ""), nil
}

func (view *View) WhichLine(s string) int {
	y := -1
	for index, line := range view.v.ViewBufferLines() {
		if line == s {
			return index
		}
	}
	return y
}

func (view *View) MoveCursorVertically(dy int) {
	_, height := view.v.InnerSize()
	cx, cy := view.v.Cursor()
	ox, oy := view.v.Origin()
	position := NextVerticalPosition(VerticalPosition{Cursor: cy, Origin: oy}, dy, height, view.v.ViewLinesHeight())
	view.v.SetOrigin(ox, position.Origin)
	view.v.SetCursor(cx, position.Cursor)
}

func (view *View) SetEditableContent(s string) {
	view.v.TextArea.Clear()
	view.v.TextArea.TypeString(s)
	view.v.RenderTextArea()
}

func (view *View) ReRender() {
	view.renderTimes++
}

func (view *View) ReRenderTimes(times int) {
	view.renderTimes += times
}

func (view *View) render() error {
	if view.renderTimes < 0 {
		return nil
	}
	view.renderTimes--

	if view.OnRender != nil {
		if err := view.OnRender(view.gui, view); err != nil {
			return err
		}
	}
	return nil
}

func (view *View) renderOptions() error {
	if view.OnRenderOptions != nil {
		if err := view.OnRenderOptions(view.gui, view); err != nil {
			return nil
		}
	}
	return nil
}

func (view *View) focus() error {
	log.Logger.Debugf("view.focus - view name :%s", view.Name)
	if view.OnFocus != nil {
		log.Logger.Debugf("view.OnFocus - view name :%s", view.Name)
		if err := view.OnFocus(view.gui, view); err != nil {
			return nil
		}
	}
	return nil
}

func (view *View) focusLost() error {
	log.Logger.Debugf("view.focusLost - view name :%s", view.Name)
	if view.OnFocusLost != nil {
		log.Logger.Debugf("view.OnFocusLost - view name :%s", view.Name)
		if err := view.OnFocusLost(view.gui, view); err != nil {
			return nil
		}
	}
	return nil
}

func (view *View) Size() (int, int) {
	return view.v.InnerSize()
}

func (view *View) ResetCursorOrigin() {
	view.v.SetCursor(0, 0)
	view.v.SetOrigin(0, 0)
}

func (view *View) SetState(key string, value interface{}, reRender bool) error {
	err := view.state.Set(key, value)
	if err != nil {
		return err
	}

	if reRender {
		view.ReRender()
	}
	return nil
}

func (view *View) GetState(key string) (interface{}, error) {
	return view.state.Get(key)
}

type DimensionFunc func(gui *Gui, view *View) (int, int, int, int)

type ViewPointFunc func(gui *Gui, view *View) int
