//go:build gui

package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// walk descends the object tree, visiting every object. It understands the containers the
// builders use plus our own tapRow (whose content is otherwise hidden), so tests can assert on
// the rendered structure without a live controller or a real display.
func walk(o fyne.CanvasObject, visit func(fyne.CanvasObject)) {
	if o == nil {
		return
	}
	visit(o)
	switch v := o.(type) {
	case *fyne.Container:
		for _, c := range v.Objects {
			walk(c, visit)
		}
	case *container.Scroll:
		walk(v.Content, visit)
	case *tapRow:
		walk(v.content, visit)
	}
}

// texts collects the visible strings from canvas.Text, buttons, and labels in the tree.
func texts(o fyne.CanvasObject) []string {
	var out []string
	walk(o, func(c fyne.CanvasObject) {
		switch v := c.(type) {
		case *canvas.Text:
			out = append(out, v.Text)
		case *widget.Button:
			out = append(out, v.Text)
		case *widget.Label:
			out = append(out, v.Text)
		}
	})
	return out
}

func buttons(o fyne.CanvasObject) []*widget.Button {
	var out []*widget.Button
	walk(o, func(c fyne.CanvasObject) {
		if b, ok := c.(*widget.Button); ok {
			out = append(out, b)
		}
	})
	return out
}

func switches(o fyne.CanvasObject) []*Switch {
	var out []*Switch
	walk(o, func(c fyne.CanvasObject) {
		if s, ok := c.(*Switch); ok {
			out = append(out, s)
		}
	})
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
