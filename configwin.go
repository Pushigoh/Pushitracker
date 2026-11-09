package main

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/font"
	"gioui.org/font/gofont"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"
	"github.com/sqweek/dialog"
)

var (
	colBg      = rgb(0x1e1f22)
	colFg      = rgb(0xe6e6e6)
	colMuted   = rgb(0x9a9ca3)
	colAccent  = rgb(0x4f8cff)
	colButton  = rgb(0x3a3b40)
	colError   = rgb(0xff6b6b)
	colOnColor = rgb(0xffffff)
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

func newDarkTheme() *material.Theme {
	th := material.NewTheme()
	th.Shaper = text.NewShaper(text.WithCollection(gofont.Collection()))
	th.Palette = material.Palette{Bg: colBg, Fg: colFg, ContrastBg: colAccent, ContrastFg: colOnColor}
	return th
}

// Selection is what the setup window hands to the tracker.
type Selection struct {
	Game     *Game
	CharDir  string // the chosen character's folder
	Settings *Settings
}

type pickKind int

const (
	pickChars pickKind = iota
	pickGames
	pickSettings
)

type pick struct {
	kind pickKind
	dir  string
}

type choice struct {
	name  string
	label string
	click widget.Clickable
}

type configWindow struct {
	th       *material.Theme
	settings *Settings

	chars     []*choice
	charsErr  error
	charGifs  int
	games     []*choice
	gamesErr  error
	game      *Game
	gameErr   error
	gameIcon  int    // the selected game's saved icon size
	settingsN string // short description of the settings folder state

	browsing bool
	picked   chan pick

	charsBtn    widget.Clickable
	gamesBtn    widget.Clickable
	settingsBtn widget.Clickable
	startBtn    widget.Clickable
	micBtn      widget.Clickable
	micOpen     bool // the mic picker is open
	micPicked   chan micPick
	micMissing  bool // the chosen mic isn't plugged in
	// The chosen mic's level (dBFS bits) for its level bar, and how to stop
	// capturing it (nil when not).
	micLevel              atomic.Uint64
	micStop               func()
	micOpenDb, micCloseDb int // the chosen game's talk thresholds
	lastDraw              atomic.Int64
	noChar                bool // no character was chosen on purpose
	list                  widget.List
}

// runConfigWindow shows the setup window and blocks until it closes. It
// returns nil if the window was closed without pressing Start. s may be
// replaced when another settings folder is picked.
func runConfigWindow(th *material.Theme, s *Settings) *Selection {
	c := &configWindow{th: th, settings: s, picked: make(chan pick, 1), micPicked: make(chan micPick, 1)}
	c.micMissing = micMissing(s.MicID)
	defer c.stopMic()
	c.list.Axis = layout.Vertical
	c.rescan()

	w := new(app.Window)
	w.Option(app.Title(appName()+" – Setup"), app.Size(unit.Dp(600), unit.Dp(560)), app.MinSize(unit.Dp(460), unit.Dp(420)))

	c.startMic(w)
	var ops op.Ops
	started := false
	for {
		switch e := w.Event().(type) {
		case app.Win32ViewEvent:
			if e.HWND != 0 {
				useDarkTitleBar(e.HWND)
			}
		case app.DestroyEvent:
			if !started {
				return nil
			}
			sel := &Selection{Game: c.game, Settings: c.settings}
			if c.settings.Character != "" {
				sel.CharDir = filepath.Join(c.settings.CharsDir, c.settings.Character)
			}
			return sel
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			if c.update(gtx, w) {
				started = true
				w.Perform(system.ActionClose)
			}
			paint.Fill(gtx.Ops, colBg)
			c.layout(gtx)
			e.Frame(gtx.Ops)
		}
	}
}

func (c *configWindow) rescan() {
	s := c.settings

	names, err := ListCharacters(s.CharsDir)
	c.chars, c.charsErr = reuseChoices(c.chars, names, func(n string) string { return characterLabel(s.CharsDir, n) }), err
	if !c.hasChoice(c.chars, s.Character) {
		s.Character = ""
		// The only character is picked for you, unless you just chose none.
		if len(c.chars) == 1 && !c.noChar {
			s.Character = c.chars[0].name
		}
	}
	c.charGifs = 0
	if s.Character != "" {
		c.charGifs = countCharImages(filepath.Join(s.CharsDir, s.Character))
	}

	names, err = ListGames(s.GamesDir)
	c.games, c.gamesErr = reuseChoices(c.games, names, strings.ToUpper), err
	c.game, c.gameErr = nil, nil
	if c.hasChoice(c.games, s.Game) {
		c.game, c.gameErr = LoadGame(s.GamesDir, s.Game)
		gs := s.LoadGameSettings(s.Game)
		c.gameIcon, c.micOpenDb, c.micCloseDb = gs.IconSize, gs.TalkOpenDb, min(gs.TalkCloseDb, gs.TalkOpenDb)
	}

	c.settingsN = "Will be created on Start"
	if saved := s.SavedGames(); len(saved) > 0 {
		for i := range saved {
			saved[i] = strings.ToUpper(saved[i])
		}
		c.settingsN = "Saved settings for " + strings.Join(saved, ", ")
	} else if fileExists(filepath.Join(s.Dir(), settingsFile)) {
		c.settingsN = "No game settings saved yet"
	}
}

// reuseChoices builds buttons for names, keeping existing buttons (and so
// their keyboard focus) when the list hasn't changed.
func reuseChoices(old []*choice, names []string, label func(string) string) []*choice {
	same := len(old) == len(names)
	for i := 0; same && i < len(names); i++ {
		same = old[i].name == names[i] && old[i].label == label(names[i])
	}
	if same {
		return old
	}
	out := make([]*choice, len(names))
	for i, n := range names {
		out[i] = &choice{name: n, label: label(n)}
	}
	return out
}

func (c *configWindow) hasChoice(list []*choice, name string) bool {
	for _, ch := range list {
		if ch.name == name {
			return true
		}
	}
	return false
}

// canStart reports whether there is a game to track. A character is
// optional: without one the tracker shows just the icons, item and overlays.
func (c *configWindow) canStart() bool {
	return c.game != nil
}

func (c *configWindow) browse(w *app.Window, kind pickKind, start, title string) {
	c.browsing = true
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		dir, err := dialog.Directory().Title(title).SetStartDir(start).Browse()
		if err != nil {
			dir = ""
		}
		c.picked <- pick{kind: kind, dir: dir}
		w.Invalidate()
	}()
}

func (c *configWindow) applyPick(p pick) {
	s := c.settings
	switch p.kind {
	case pickChars:
		s.CharsDir = p.dir
	case pickGames:
		s.GamesDir = p.dir
	case pickSettings:
		// Take everything saved in the new folder, but keep folders already
		// chosen here.
		loaded := LoadSettings(p.dir)
		if s.CharsDir != "" {
			loaded.CharsDir = s.CharsDir
		}
		if s.GamesDir != "" {
			loaded.GamesDir = s.GamesDir
		}
		if loaded.Character == "" {
			loaded.Character = s.Character
		}
		if loaded.Game == "" {
			loaded.Game = s.Game
		}
		*s = *loaded
	}
	c.rescan()
}

// update handles input; it reports whether Start was pressed.
func (c *configWindow) update(gtx layout.Context, w *app.Window) bool {
	s := c.settings
	select {
	case p := <-c.picked:
		c.browsing = false
		if p.dir != "" {
			c.applyPick(p)
		}
	default:
	}
	select {
	case p := <-c.micPicked:
		c.micOpen = false
		if p.ok {
			s.MicID, s.MicName, c.micMissing = p.mic.ID, p.mic.Name, false
			if p.mic.ID == "" {
				s.MicName = ""
			}
			c.startMic(w)
		}
	default:
	}
	if c.micBtn.Clicked(gtx) && !c.micOpen {
		c.micOpen = true
		id := s.MicID
		go func() {
			m, ok := runMicPicker(id)
			c.micPicked <- micPick{m, ok}
			w.Invalidate()
		}()
	}

	if !c.browsing {
		if c.charsBtn.Clicked(gtx) {
			c.browse(w, pickChars, s.CharsDir, "Choose the characters folder (contains a folder per character)")
		}
		if c.gamesBtn.Clicked(gtx) {
			c.browse(w, pickGames, s.GamesDir, "Choose the games folder (contains a folder per game)")
		}
		if c.settingsBtn.Clicked(gtx) {
			c.browse(w, pickSettings, s.Dir(), "Choose the settings folder")
		}
	}
	for _, ch := range c.chars {
		if ch.click.Clicked(gtx) {
			// Clicking the chosen character again chooses none.
			if s.Character == ch.name {
				s.Character, c.noChar = "", true
			} else {
				s.Character, c.noChar = ch.name, false
			}
			c.rescan()
		}
	}
	for _, g := range c.games {
		if g.click.Clicked(gtx) {
			s.Game = g.name
			c.rescan()
		}
	}
	if c.startBtn.Clicked(gtx) && c.canStart() {
		_ = s.Save()
		rememberSettingsDir(s.Dir())
		return true
	}
	return false
}

func (c *configWindow) layout(gtx layout.Context) layout.Dimensions {
	th, s := c.th, c.settings
	heading := func(label string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Body1(th, label)
			l.Font.Weight = font.Bold
			return layout.Inset{Top: 16, Bottom: 6}.Layout(gtx, l.Layout)
		})
	}
	status := func(err error, ok string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			l := material.Caption(th, ok)
			l.Color = colMuted
			if err != nil {
				l.Text, l.Color = err.Error(), colError
			}
			if l.Text == "" {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 6}.Layout(gtx, l.Layout)
		})
	}
	buttons := func(list []*choice, selected string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(list) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return flowWrap(gtx, gtx.Dp(8), len(list), func(gtx layout.Context, i int) layout.Dimensions {
					ch := list[i]
					b := material.Button(th, &ch.click, ch.label)
					b.Background = colButton
					if ch.name == selected {
						b.Background = colAccent
					}
					b.Inset = layout.Inset{Top: 9, Bottom: 9, Left: 16, Right: 16}
					return b.Layout(gtx)
				})
			})
		})
	}

	charStatus := "No character (optional)"
	if s.Character != "" {
		charStatus = fmt.Sprintf("%s: %d images", characterLabel(s.CharsDir, s.Character), c.charGifs)
		dir := filepath.Join(s.CharsDir, s.Character)
		for _, f := range []string{idleName, rewardName} {
			if characterImage(dir, f) == "" {
				charStatus += ", no " + f + " image"
			}
		}
	}
	gameStatus := "Pick a game"
	if c.game != nil {
		if n := c.game.IconCount(); n > 0 {
			gameStatus = fmt.Sprintf("%s: %d rows, %d icons, icon size %d px", strings.ToUpper(c.game.Name), len(c.game.Rows), n, c.gameIcon)
			if !isDir(c.game.GifsDir) {
				gameStatus += " (no gifs folder)"
			}
		} else {
			gameStatus = strings.ToUpper(c.game.Name) + ": no icons, "
			switch n := len(c.game.Overlays); n {
			case 0:
				gameStatus += "no overlays"
			case 1:
				gameStatus += "1 overlay"
			default:
				gameStatus += fmt.Sprintf("%d overlays", n)
			}
		}
	}

	rows := []layout.FlexChild{
		layout.Rigid(material.H6(th, "Pushitracker setup").Layout),

		heading("Characters"),
		layout.Rigid(c.pathRow(s.CharsDir, &c.charsBtn)),
		buttons(c.chars, s.Character),
		status(c.charsErr, charStatus),

		heading("Microphone"),
		layout.Rigid(c.micRow()),
		layout.Rigid(c.micMeter),
		status(nil, c.micStatus()),

		heading("Games"),
		layout.Rigid(c.pathRow(s.GamesDir, &c.gamesBtn)),
		buttons(c.games, s.Game),
		status(firstErr(c.gamesErr, c.gameErr), gameStatus),

		heading("Settings"),
		layout.Rigid(c.pathRow(s.Dir(), &c.settingsBtn)),
		status(nil, c.settingsN),
	}

	return layout.UniformInset(18).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return material.List(th, &c.list).Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows[i])
				})
			}),
			layout.Rigid(layout.Spacer{Height: 12}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					if !c.canStart() {
						gtx = gtx.Disabled()
					}
					b := material.Button(th, &c.startBtn, "Start")
					b.Inset = layout.Inset{Top: 10, Bottom: 10, Left: 28, Right: 28}
					return b.Layout(gtx)
				})
			}),
		)
	})
}

// micRow shows the chosen mic and the Choose microphone button.
func (c *configWindow) micRow() layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		s := c.settings
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(c.th, s.MicName)
				if s.MicID == "" {
					l.Text, l.Color = "None (mic off)", colMuted
				} else if l.Text == "" {
					l.Text = "Microphone"
				}
				l.MaxLines = 2
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: 10}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if c.micOpen {
					gtx = gtx.Disabled()
				}
				b := material.Button(c.th, &c.micBtn, "Choose microphone")
				b.Background = colButton
				return b.Layout(gtx)
			}),
		)
	}
}

// micStatus is a problem with the chosen mic, if there is one.
func (c *configWindow) micStatus() string {
	s := c.settings
	switch {
	case s.MicID == "":
		return ""
	case c.micMissing:
		return "This microphone isn't plugged in or is turned off"
	case s.Character != "" && characterImage(filepath.Join(s.CharsDir, s.Character), talkName) == "":
		return "This character has no talk image"
	}
	return ""
}

// startMic captures from the chosen mic for its level bar, redrawing the
// window at most every meterInterval.
func (c *configWindow) startMic(w *app.Window) {
	c.stopMic()
	c.micLevel.Store(math.Float64bits(micSilence))
	if c.settings.MicID == "" || c.micMissing {
		return
	}
	stop, err := startMic(c.settings.MicID, func(db float64) {
		c.micLevel.Store(math.Float64bits(max(db, micSilence)))
		now := time.Now().UnixMilli()
		if last := c.lastDraw.Load(); now-last >= meterInterval.Milliseconds() && c.lastDraw.CompareAndSwap(last, now) {
			w.Invalidate()
		}
	})
	if err != nil {
		c.micMissing = true
		return
	}
	c.micStop = stop
}

func (c *configWindow) stopMic() {
	if c.micStop != nil {
		c.micStop()
		c.micStop = nil
	}
}

// micMeter is the chosen mic's level bar, with lines at the game's open
// (white) and close (grey) thresholds.
func (c *configWindow) micMeter(gtx layout.Context) layout.Dimensions {
	if c.micStop == nil {
		return layout.Dimensions{}
	}
	return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		w, h := gtx.Constraints.Max.X, gtx.Dp(meterH)
		bar := image.Rect(0, 0, w, h)
		paint.FillShape(gtx.Ops, colButton, clip.UniformRRect(bar, h/2).Op(gtx.Ops))
		db := math.Float64frombits(c.micLevel.Load())
		lc := colMuted
		if db >= float64(c.micOpenDb) {
			lc = colTalk
		}
		if lx := int(levelFrac(db) * float64(w)); lx > 0 {
			paint.FillShape(gtx.Ops, lc, clip.UniformRRect(image.Rect(0, 0, lx, h), h/2).Op(gtx.Ops))
		}
		if c.game != nil {
			k := max(gtx.Dp(1), 1)
			for _, l := range []struct {
				db  int
				col color.NRGBA
			}{{c.micCloseDb, colMuted}, {c.micOpenDb, colFg}} {
				tx := min(max(int(levelFrac(float64(l.db))*float64(w)), k), w-k)
				paint.FillShape(gtx.Ops, l.col, clip.Rect(image.Rect(tx-k, -gtx.Dp(2), tx+k, h+gtx.Dp(2))).Op())
			}
		}
		return layout.Dimensions{Size: bar.Size()}
	})
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *configWindow) pathRow(path string, btn *widget.Clickable) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				l := material.Body2(c.th, displayPath(path))
				if path == "" {
					l.Text, l.Color = "(none selected)", colMuted
				}
				l.MaxLines = 2
				return l.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: 10}.Layout),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if c.browsing {
					gtx = gtx.Disabled()
				}
				b := material.Button(c.th, btn, "Browse…")
				b.Background = colButton
				return b.Layout(gtx)
			}),
		)
	}
}

// displayPath shortens folders that sit next to the exe to `.\name`, since
// long paths get cut off before the part that matters.
func displayPath(p string) string {
	exe, err := os.Executable()
	if err != nil || p == "" {
		return p
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	if strings.EqualFold(filepath.Dir(filepath.Clean(p)), filepath.Dir(exe)) {
		return "." + string(filepath.Separator) + filepath.Base(p)
	}
	return p
}

// flowWrap lays out n widgets left to right, wrapping onto new lines.
func flowWrap(gtx layout.Context, gap, n int, item func(gtx layout.Context, i int) layout.Dimensions) layout.Dimensions {
	maxW := gtx.Constraints.Max.X
	x, y, lineH, width := 0, 0, 0, 0
	for i := 0; i < n; i++ {
		m := op.Record(gtx.Ops)
		cgtx := gtx
		cgtx.Constraints.Min = image.Point{}
		d := item(cgtx, i)
		call := m.Stop()
		if x > 0 && x+d.Size.X > maxW {
			x, y, lineH = 0, y+lineH+gap, 0
		}
		st := op.Offset(image.Pt(x, y)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		st.Pop()
		x += d.Size.X + gap
		lineH = max(lineH, d.Size.Y)
		width = max(width, x-gap)
	}
	return layout.Dimensions{Size: image.Pt(width, y+lineH)}
}
