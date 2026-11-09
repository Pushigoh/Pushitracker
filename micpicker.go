package main

import (
	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
)

// runMicPicker shows a window listing the microphones, and blocks until one
// is picked (ok) or the window is closed. The first choice, with no ID, is no
// mic at all.
func runMicPicker(currentID string) (picked Mic, ok bool) {
	th := newDarkTheme()
	mics, err := listMics()
	choices := append([]Mic{{Name: "None (mic off)"}}, mics...)
	clicks := make([]widget.Clickable, len(choices))
	var list widget.List
	list.Axis = layout.Vertical

	w := new(app.Window)
	w.Option(app.Title("Choose microphone"), app.Size(unit.Dp(440), unit.Dp(380)), app.MinSize(unit.Dp(320), unit.Dp(220)))
	var ops op.Ops
	for {
		switch e := w.Event().(type) {
		case app.Win32ViewEvent:
			if e.HWND != 0 {
				useDarkTitleBar(e.HWND)
				// Above the tracker, which may be always on top. Asynchronous: the
				// window's thread waits for this loop, so a plain SetWindowPos
				// would never return.
				pSetWindowPos.Call(e.HWND, ^uintptr(0), 0, 0, 0, 0, swpNoMove|swpNoSize|swpAsyncWindowPos)
			}
		case app.DestroyEvent:
			return picked, ok
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			for i := range clicks {
				if clicks[i].Clicked(gtx) && !ok {
					picked, ok = choices[i], true
					w.Perform(system.ActionClose)
				}
			}
			paint.Fill(gtx.Ops, colBg)
			layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(material.H6(th, "Choose microphone").Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						l := material.Caption(th, "")
						switch {
						case err != nil:
							l.Text, l.Color = "Couldn't list microphones: "+err.Error(), colError
						case len(mics) == 0:
							l.Text, l.Color = "No microphones found.", colMuted
						}
						if l.Text == "" {
							return layout.Spacer{Height: 12}.Layout(gtx)
						}
						return layout.Inset{Top: 6, Bottom: 10}.Layout(gtx, l.Layout)
					}),
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return material.List(th, &list).Layout(gtx, len(choices), func(gtx layout.Context, i int) layout.Dimensions {
							return layout.Inset{Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min.X = gtx.Constraints.Max.X
								b := material.Button(th, &clicks[i], choices[i].Name)
								b.Background = colButton
								if choices[i].ID == currentID {
									b.Background = colAccent
								}
								b.Inset = layout.Inset{Top: 9, Bottom: 9, Left: 14, Right: 14}
								return b.Layout(gtx)
							})
						})
					}),
				)
			})
			e.Frame(gtx.Ops)
		}
	}
}

// micMissing reports whether the mic with this ID isn't plugged in or
// enabled. It is false for no mic, or if the mics can't be listed.
func micMissing(id string) bool {
	if id == "" {
		return false
	}
	mics, err := listMics()
	if err != nil {
		return false
	}
	for _, m := range mics {
		if m.ID == id {
			return false
		}
	}
	return true
}
