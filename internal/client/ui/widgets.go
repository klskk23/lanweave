//go:build gui

package ui

import (
	"image/color"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// avatarInitial picks the uppercase leading rune of a name for the avatar circle, "?" when empty.
func avatarInitial(name string) string {
	for _, r := range name {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// avatarBase builds the 36px circle + centered initial shared by makeAvatar/makeAvatarPlain.
func avatarBase(name string) (*canvas.Circle, *fyne.Container) {
	bg := canvas.NewCircle(avatarBg)
	bg.Resize(fyne.NewSize(36, 36))
	bg.Move(fyne.NewPos(0, 0))

	letter := canvas.NewText(avatarInitial(name), textPrimary)
	letter.TextSize = 14
	letter.Alignment = fyne.TextAlignCenter
	center := container.NewCenter(letter)
	center.Resize(fyne.NewSize(36, 36))
	center.Move(fyne.NewPos(0, 0))
	return bg, center
}

// makeAvatar builds a 36px circular avatar with an uppercase initial and an 11px status dot in
// the bottom-right corner (green online / gray offline), per UI-DESIGN §4.
func makeAvatar(name string, online bool) fyne.CanvasObject {
	bg, center := avatarBase(name)
	dot := canvas.NewCircle(statusColor(online))
	dot.StrokeColor = surfaceA
	dot.StrokeWidth = 2
	dot.Resize(fyne.NewSize(11, 11))
	dot.Move(fyne.NewPos(25, 25))

	box := container.NewWithoutLayout(bg, center, dot)
	return container.NewGridWrap(fyne.NewSize(36, 36), box)
}

// makeAvatarPlain is makeAvatar without a status dot, for entities (zones) that have no
// online/offline state.
func makeAvatarPlain(name string) fyne.CanvasObject {
	bg, center := avatarBase(name)
	box := container.NewWithoutLayout(bg, center)
	return container.NewGridWrap(fyne.NewSize(36, 36), box)
}

// card wraps content in a rounded surfaceB panel with inner padding (Hero/wizard look).
func card(content fyne.CanvasObject) fyne.CanvasObject {
	bg := canvas.NewRectangle(surfaceB)
	bg.CornerRadius = 12
	return container.NewStack(bg, container.NewPadded(content))
}

// coloredText is a 13px canvas.Text in a single brand color (status lines, menu rows).
func coloredText(s string, c color.Color) *canvas.Text {
	t := canvas.NewText(s, c)
	t.TextSize = 13
	return t
}

// tapRow wraps content with an optional rounded highlight background and an optional whole-row
// tap handler (zone rows open a detail sheet; the this-machine node row only highlights; the
// overflow logout row taps). A nil onTap makes the row inert.
type tapRow struct {
	widget.BaseWidget
	content fyne.CanvasObject
	bg      color.Color
	onTap   func()
}

func newTapRow(content fyne.CanvasObject, bg color.Color, onTap func()) *tapRow {
	r := &tapRow{content: content, bg: bg, onTap: onTap}
	r.ExtendBaseWidget(r)
	return r
}

func (r *tapRow) Tapped(*fyne.PointEvent) {
	if r.onTap != nil {
		r.onTap()
	}
}

func (r *tapRow) CreateRenderer() fyne.WidgetRenderer {
	rect := canvas.NewRectangle(color.Transparent)
	if r.bg != nil {
		rect.FillColor = r.bg
	}
	rect.CornerRadius = 8
	return widget.NewSimpleRenderer(container.NewStack(rect, r.content))
}

// makeChip is a pill label (CornerRadius 999) used for small status tags like "本机"/"owner".
func makeChip(text string, bg, fg color.Color) fyne.CanvasObject {
	rect := canvas.NewRectangle(bg)
	rect.CornerRadius = 999
	label := canvas.NewText(text, fg)
	label.TextSize = 11
	label.Alignment = fyne.TextAlignCenter
	return container.NewStack(rect, container.NewPadded(label))
}

// statusIndicator renders a colored dot + short colored text (e.g. "● 已连接"), replacing the
// old "[offline]" bracket style (UI-DESIGN §4, FR-005). The dot and text share one color.
func statusIndicator(text string, c color.Color) fyne.CanvasObject {
	dot := canvas.NewCircle(c)
	label := canvas.NewText(text, c)
	label.TextSize = 13
	return container.NewHBox(
		container.NewCenter(container.NewGridWrap(fyne.NewSize(9, 9), dot)),
		container.NewCenter(label),
	)
}
