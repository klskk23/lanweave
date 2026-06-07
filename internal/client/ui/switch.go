//go:build gui

package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
)

const (
	switchTrackW = float32(36)
	switchTrackH = float32(20)
	switchThumb  = float32(16)
)

// Switch is a self-drawn on/off toggle replacing widget.Check (UI-DESIGN §6, FR-007). Tapping
// flips On, fires OnChange, and repaints: a cyan thumb on the right over an indigo track when
// on, a gray thumb on the left over a muted track when off.
type Switch struct {
	widget.BaseWidget
	On       bool
	OnChange func(bool)
}

// NewSwitch builds a Switch wired to onChange (called with the new state on every tap).
func NewSwitch(onChange func(bool)) *Switch {
	s := &Switch{OnChange: onChange}
	s.ExtendBaseWidget(s)
	return s
}

// SetOn sets the state and repaints without firing OnChange, for seeding from a stored value.
func (s *Switch) SetOn(on bool) {
	s.On = on
	s.Refresh()
}

// Tapped flips the switch, notifies OnChange, and repaints.
func (s *Switch) Tapped(*fyne.PointEvent) {
	s.On = !s.On
	if s.OnChange != nil {
		s.OnChange(s.On)
	}
	s.Refresh()
}

func (s *Switch) CreateRenderer() fyne.WidgetRenderer {
	track := canvas.NewRectangle(color.Transparent)
	track.CornerRadius = switchTrackH / 2
	thumb := canvas.NewCircle(color.White)
	r := &switchRenderer{sw: s, track: track, thumb: thumb}
	r.applyColors()
	return r
}

type switchRenderer struct {
	sw    *Switch
	track *canvas.Rectangle
	thumb *canvas.Circle
}

func (r *switchRenderer) MinSize() fyne.Size { return fyne.NewSize(switchTrackW, switchTrackH) }

func (r *switchRenderer) Layout(size fyne.Size) {
	top := (size.Height - switchTrackH) / 2
	if top < 0 {
		top = 0
	}
	r.track.Resize(fyne.NewSize(switchTrackW, switchTrackH))
	r.track.Move(fyne.NewPos(0, top))

	inset := (switchTrackH - switchThumb) / 2
	x := inset
	if r.sw.On {
		x = switchTrackW - switchThumb - inset
	}
	r.thumb.Resize(fyne.NewSize(switchThumb, switchThumb))
	r.thumb.Move(fyne.NewPos(x, top+inset))
}

func (r *switchRenderer) applyColors() {
	if r.sw.On {
		r.track.FillColor = brandIndigo
		r.thumb.FillColor = brandCyan
	} else {
		r.track.FillColor = surfaceA
		r.thumb.FillColor = textTertiary
	}
}

func (r *switchRenderer) Refresh() {
	r.applyColors()
	r.Layout(r.sw.Size())
	canvas.Refresh(r.track)
	canvas.Refresh(r.thumb)
}

func (r *switchRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.track, r.thumb} }

func (r *switchRenderer) Destroy() {}
