package main

import (
	"image"
	"image/color"
	"math"
	"path/filepath"
	"strconv"
	"time"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
)

// Layout sizes in dp (scaled by the window's DPI).
const (
	margin      = 8
	barHeight   = 26
	barButton   = 24
	barGap      = 6
	itemIndent  = 65  // where the item display starts, until it is dragged
	charWidth   = 250 // character box shape; its height is the character size
	charHeight  = 350
	panelWidth  = 240
	panelPad    = 10
	fieldH      = 28
	sliderH     = 24
	rowH        = 24
	btnH        = 28
	fadeTime    = 300 * time.Millisecond
	caretBlink  = 530 * time.Millisecond
	minAlphaHit = 1 // nearly invisible, but keeps icon gaps clickable
)

var (
	colChrome      = withAlpha(rgb(0x2a2b30), 0xdd)
	colChromeGlyph = rgb(0xd6d7db)
	colPanel       = withAlpha(rgb(0x1e1f22), 0xf2)
	colPanelBorder = rgb(0x3a3b40)
	colField       = rgb(0x2a2b30)
)

type hitKind int

// Kinds from hitGear on are part of the chrome or the settings panel.
const (
	hitNone hitKind = iota
	hitIcon
	hitDrag
	hitGear
	hitPanel
	hitSizeField
	hitSlider
	hitOnTop
	hitSave
	hitReset
	hitChangeGame
	hitExit
)

type hit struct {
	r    image.Rectangle
	kind hitKind
	idx  int
}

// Surface is which window an input event or hit test is for: the tracker
// itself, or the settings panel, which has a window of its own so it can be
// dragged around.
type Surface int

const (
	SurfMain Surface = iota
	SurfPanel
)

// Action tells the window what to do after a mouse press.
type Action int

const (
	ActNone Action = iota
	ActDrag
	ActCapture
	ActFocus
	ActTopmost
	ActSave
	ActConfirmReset // ask first, then reset (see ResetLayout)
	ActClose
	ActExit // close, asking first if there are unsaved changes
)

// Virtual-key codes the size fields understand.
const (
	vkBack   = 0x08
	vkReturn = 0x0D
	vkEscape = 0x1B
	vkUp     = 0x26
	vkDown   = 0x28
)

type iconState struct {
	def    IconDef
	stage  int // -1 = not collected (grayscale first stage)
	colors []*image.RGBA
	grays  []*image.RGBA

	fadeFrom  float64
	fadeStart time.Time
}

// colorAmount is 0 for fully grayscale and 1 for full colour, easing between
// the two over fadeTime after each toggle.
func (s *iconState) colorAmount(now time.Time) (float64, bool) {
	target := 0.0
	if s.stage >= 0 {
		target = 1
	}
	t := float64(now.Sub(s.fadeStart)) / float64(fadeTime)
	if s.fadeStart.IsZero() || t >= 1 {
		return target, false
	}
	return s.fadeFrom + (target-s.fadeFrom)*t, true
}

// advance moves to the next stage: off → stage 1 → stage 2 … → off.
func (s *iconState) advance(now time.Time) {
	s.fadeFrom, _ = s.colorAmount(now)
	s.fadeStart = now
	if s.stage+1 < len(s.def.Stages) {
		s.stage++
	} else {
		s.stage = -1
	}
}

// clear turns the icon back to not collected, fading to grey.
func (s *iconState) clear(now time.Time) {
	if s.stage < 0 {
		return
	}
	s.fadeFrom, _ = s.colorAmount(now)
	s.fadeStart = now
	s.stage = -1
}

func (s *iconState) shownStage() int { return max(s.stage, 0) }

// sizeControl is one "label [field] px" + slider block in the settings panel.
type sizeControl struct {
	label    string
	unit     string
	min, max int
	step     int // for arrow keys and the mouse wheel
	get      func() int
	set      func(int) // applies a new (already clamped) value
}

func (c *sizeControl) clamp(n int) int { return min(max(n, c.min), c.max) }

type Tracker struct {
	game     *Game
	charDir  string
	settings *Settings
	gs       *GameSettings // this game's sizes, always-on-top and positions
	cache    *AnimCache

	scale    float64
	iconPx   int // icon size in pixels the icon images were built for
	icons    []*iconState
	iconKeys []string // each icon's name in the settings file: "<row>/<icon>"
	rowStart []int    // index into icons where each row starts

	// The overlays as loaded, and at their current size. They are scaled
	// only when the display scale or their size setting changes.
	overlaySrc   []*image.RGBA
	overlays     []*image.RGBA
	overlayScale float64

	started   time.Time // when the tracker opened; the game's idle image plays from here
	itemPath  string
	itemStart time.Time
	charPath  string
	charStart time.Time
	resetAt   time.Time

	panelOpen bool
	pressed   int      // icon index held down, -1 if none
	hits      [2][]hit // per Surface, from the last render
	face      font.Face
	faceScale float64

	controls []*sizeControl
	// The item size before rounding, so it keeps its proportion to the
	// character however often the character is resized.
	itemExact float64
	tracks    []image.Rectangle // each control's slider track, from the last render

	// Size field being edited (-1 if none). fresh means the text is all
	// selected, so the next digit replaces it.
	editIdx    int
	editText   string
	editFresh  bool
	caretStart time.Time

	// Slider being dragged (-1 if none). slideTrack is where its track was
	// when the drag started, so the value follows the mouse even while the
	// window resizes underneath.
	slideIdx   int
	slideTrack image.Rectangle

	ChangeGame bool
}

func NewTracker(sel *Selection) *Tracker {
	t := &Tracker{
		game:     sel.Game,
		charDir:  sel.CharDir,
		settings: sel.Settings,
		gs:       sel.Settings.LoadGameSettings(sel.Game.Name),
		cache:    NewAnimCache(),
		scale:    1,
		pressed:  -1,
		editIdx:  -1,
		slideIdx: -1,
	}
	for _, r := range t.game.Rows {
		t.rowStart = append(t.rowStart, len(t.icons))
		for _, def := range r.Icons {
			t.icons = append(t.icons, &iconState{def: def, stage: -1})
			t.iconKeys = append(t.iconKeys, r.Name+"/"+def.Key)
		}
	}
	t.rowStart = append(t.rowStart, len(t.icons))

	t.controls = []*sizeControl{
		{
			label: "Icon size", unit: "px", min: MinIconSize, max: MaxIconSize, step: 1,
			get: t.iconSize,
			set: func(n int) {
				t.gs.IconSize = n
				t.prepare()
			},
		},
		{
			label: "Icon spacing", unit: "px", min: 0, max: MaxIconSpacing, step: 1,
			get: func() int { return t.gs.IconSpacing },
			set: func(n int) { t.gs.IconSpacing = n },
		},
		{
			label: "Character size", unit: "px", min: MinCharSize, max: MaxCharSize, step: 10,
			get: t.charSize,
			set: func(n int) {
				// The item above the character's head grows and shrinks with it.
				t.itemExact *= float64(n) / float64(t.gs.CharSize)
				t.itemExact = math.Max(MinItemSize, math.Min(MaxItemSize, t.itemExact))
				t.gs.ItemSize = int(math.Round(t.itemExact))
				t.gs.CharSize = n
			},
		},
		{
			label: "Item size", unit: "px", min: MinItemSize, max: MaxItemSize, step: 5,
			get: func() int { return t.gs.ItemSize },
			set: func(n int) { t.gs.ItemSize, t.itemExact = n, float64(n) },
		},
	}
	for i, ov := range t.game.Overlays {
		t.controls = append(t.controls, &sizeControl{
			label: "Overlay " + ov.Name, unit: "%", min: MinOverlaySize, max: MaxOverlaySize, step: 5,
			get: func() int { return t.overlaySize(i) },
			set: func(n int) {
				if n == 100 {
					delete(t.gs.OverlaySize, ov.Name)
				} else {
					if t.gs.OverlaySize == nil {
						t.gs.OverlaySize = map[string]int{}
					}
					t.gs.OverlaySize[ov.Name] = n
				}
				t.scaleOverlay(i)
			},
		})
	}
	t.itemExact = float64(t.gs.ItemSize)
	t.tracks = make([]image.Rectangle, len(t.controls))

	t.started = time.Now()
	t.charPath, t.charStart = filepath.Join(t.charDir, idleGif), t.started
	return t
}

func (t *Tracker) charFile(id string) string { return filepath.Join(t.charDir, id+".gif") }
func (t *Tracker) gifFile(id string) string  { return filepath.Join(t.game.GifsDir, id+".gif") }

func (t *Tracker) px(dp float64) int { return int(math.Round(dp * t.scale)) }

func (t *Tracker) iconSize() int { return t.gs.IconSize }
func (t *Tracker) charSize() int { return t.gs.CharSize }

// Game returns this game's settings, which the window keeps positions in.
func (t *Tracker) Game() *GameSettings { return t.gs }

// SaveGame writes this game's settings file. The window calls it for the
// panel's Save button, after recording where everything is.
func (t *Tracker) SaveGame() { _ = t.gs.Save() }

// closePanel closes the settings panel. Changes stay for this session but
// are only written to disk by Save.
func (t *Tracker) closePanel() {
	if t.editIdx >= 0 {
		t.commitEdit()
	}
	t.panelOpen = false
}

// ResetLayout puts the icon, character, item and overlay sizes and the icon
// spacing back to their defaults, and the icons back in their rows. The
// window then moves the character, item and overlays back to where they
// start. Like any change, it is kept on Save.
func (t *Tracker) ResetLayout() {
	t.editIdx = -1
	t.gs.IconSize, t.gs.CharSize, t.gs.ItemSize = DefaultIconSize, DefaultCharSize, DefaultItemSize
	t.gs.IconSpacing = DefaultIconSpacing
	t.gs.IconPos = nil
	t.gs.OverlayPos = nil
	t.gs.OverlaySize = nil
	t.itemExact = DefaultItemSize
	t.overlayScale = 0 // rescale the overlays
	t.prepare()
}

// SetScale is called with the window's DPI scale (1 = 96 DPI).
func (t *Tracker) SetScale(s float64) {
	if s <= 0 {
		s = 1
	}
	t.scale = s
	t.prepare()
}

// prepare (re)builds everything that depends on pixel sizes.
func (t *Tracker) prepare() {
	if size := t.px(float64(t.iconSize())); size != t.iconPx {
		t.iconPx = size
		for _, s := range t.icons {
			s.colors, s.grays = nil, nil
			for _, st := range s.def.Stages {
				var c *image.RGBA
				// Scaled straight from the full-size image: the slider passes
				// through many sizes and caching each one would pile up memory.
				if img := t.cache.Icon(st.Path); img != nil {
					c = scaleTo(img, size, size, xdraw.CatmullRom)
				} else {
					c = image.NewRGBA(image.Rect(0, 0, size, size))
				}
				s.colors = append(s.colors, c)
				s.grays = append(s.grays, grayscale(c))
			}
		}
	}
	if t.faceScale != t.scale {
		t.face, t.faceScale = newFace(13*t.scale), t.scale
	}
	if t.overlaySrc == nil {
		for _, ov := range t.game.Overlays {
			img := loadStill(ov.Path)
			if img == nil {
				img = image.NewRGBA(image.Rectangle{})
			}
			t.overlaySrc = append(t.overlaySrc, img)
		}
		t.overlays = make([]*image.RGBA, len(t.overlaySrc))
	}
	if t.overlayScale != t.scale {
		t.overlayScale = t.scale
		for i := range t.overlays {
			t.scaleOverlay(i)
		}
	}
}

// overlaySize is overlay i's size setting, in percent of its own size.
func (t *Tracker) overlaySize(i int) int {
	if n, ok := t.gs.OverlaySize[t.game.Overlays[i].Name]; ok {
		return n
	}
	return 100
}

// scaleOverlay rebuilds overlay i at its size setting and the display scale.
func (t *Tracker) scaleOverlay(i int) {
	src := t.overlaySrc[i]
	k := t.scale * float64(t.overlaySize(i)) / 100
	if k == 1 || src.Rect.Empty() {
		t.overlays[i] = src
		return
	}
	w := max(int(math.Round(float64(src.Rect.Dx())*k)), 1)
	h := max(int(math.Round(float64(src.Rect.Dy())*k)), 1)
	t.overlays[i] = scaleTo(src, w, h, xdraw.CatmullRom)
}

// Overlays returns the game's overlays at the current scale. They don't
// change, so the window draws them once.
func (t *Tracker) Overlays() []*image.RGBA { return t.overlays }

// OverlayOffsets are where the overlays start relative to the tracker's
// top-left corner, until they are dragged: one under another, starting where
// the icons do.
func (t *Tracker) OverlayOffsets() []image.Point {
	offs := make([]image.Point, len(t.overlays))
	y := t.px(margin) + t.px(barHeight) + t.px(barGap)
	for i, img := range t.overlays {
		offs[i] = image.Pt(t.px(margin), y)
		y += img.Rect.Dy()
	}
	return offs
}

// OverlayName is what overlay i's position is saved under.
func (t *Tracker) OverlayName(i int) string { return t.game.Overlays[i].Name }

// setControl applies a value to control i; it reports whether it changed.
func (t *Tracker) setControl(i, n int) bool {
	c := t.controls[i]
	n = c.clamp(n)
	changed := n != c.get()
	if changed {
		c.set(n)
	}
	return changed
}

// trackerLayout is where everything in the tracker is. The bar and the icons'
// rows fill (0,0)-size; icons that have been dragged out of their place can
// be anywhere, even left of or above that, and bounds covers them too.
type trackerLayout struct {
	size       image.Point
	bounds     image.Rectangle
	grip, gear image.Rectangle
	icons      []image.Rectangle
	iconsEnd   int // y just below the last icon row
}

func (t *Tracker) layout() trackerLayout {
	var l trackerLayout
	m, gap, icon := t.px(margin), t.px(float64(t.gs.IconSpacing)), t.iconPx

	contentW := t.px(panelWidth)
	y := m + t.px(barHeight) + t.px(barGap)
	l.icons = make([]image.Rectangle, len(t.icons))
	for r := 0; r+1 < len(t.rowStart); r++ {
		from, to := t.rowStart[r], t.rowStart[r+1]
		if from == to {
			continue // empty rows take no space
		}
		x := m
		for i := from; i < to; i++ {
			l.icons[i] = image.Rect(x, y, x+icon, y+icon).Add(t.iconShift(i))
			x += icon + gap
		}
		contentW = max(contentW, x-gap-m)
		y += icon
	}
	l.iconsEnd = y
	l.size = image.Pt(contentW+2*m, y+m)

	b := t.px(barButton)
	top := m + (t.px(barHeight)-b)/2
	l.gear = image.Rect(l.size.X-m-b, top, l.size.X-m, top+b)
	l.grip = l.gear.Sub(image.Pt(b+t.px(4), 0))

	l.bounds = image.Rectangle{Max: l.size}
	for _, r := range l.icons {
		l.bounds = l.bounds.Union(r)
	}
	return l
}

// iconShift is how far icon i has been dragged from its place in its row, in
// pixels. It is kept in icon sizes, so the icons keep their arrangement when
// they are resized.
func (t *Tracker) iconShift(i int) image.Point {
	off, ok := t.gs.IconPos[t.iconKeys[i]]
	if !ok {
		return image.Point{}
	}
	return image.Pt(int(math.Round(off[0]*float64(t.iconPx))), int(math.Round(off[1]*float64(t.iconPx))))
}

// IconAt returns the icon at p (in the tracker's coordinates), or -1.
func (t *Tracker) IconAt(p image.Point) int {
	if h := t.hitAt(SurfMain, p); h.kind == hitIcon {
		return h.idx
	}
	return -1
}

// IconOffset is how far icon i has been dragged from its place in its row,
// in icon sizes.
func (t *Tracker) IconOffset(i int) [2]float64 { return t.gs.IconPos[t.iconKeys[i]] }

// MoveIcon puts icon i at offset (in icon sizes) from its place in its row,
// moved by d pixels. Like any change, it is kept on Save.
func (t *Tracker) MoveIcon(i int, from [2]float64, d image.Point) {
	k := float64(max(t.iconPx, 1))
	round := func(v float64) float64 { return math.Round(v*1000) / 1000 }
	off := [2]float64{round(from[0] + float64(d.X)/k), round(from[1] + float64(d.Y)/k)}
	if t.gs.IconPos == nil {
		t.gs.IconPos = map[string][2]float64{}
	}
	if off == [2]float64{} {
		delete(t.gs.IconPos, t.iconKeys[i])
	} else {
		t.gs.IconPos[t.iconKeys[i]] = off
	}
}

func (t *Tracker) Size() image.Point { return t.layout().size }

// PanelSize is the settings panel window's size in pixels.
func (t *Tracker) PanelSize() image.Point {
	pad := t.px(panelPad)
	block := t.px(fieldH) + t.px(6) + t.px(sliderH)
	h := pad + len(t.controls)*block + (len(t.controls)-1)*t.px(12) +
		t.px(12) + t.px(rowH) + 3*(t.px(12)+t.px(btnH)) + pad
	return image.Pt(t.px(panelWidth), h)
}

// PanelOffset is where the settings panel opens relative to the tracker's
// top-left corner until it is dragged: just under the gear, right-aligned.
func (t *Tracker) PanelOffset() image.Point {
	l := t.layout()
	return image.Pt(l.gear.Max.X-t.PanelSize().X, l.gear.Max.Y+t.px(6))
}

// CharacterSize is the character window's size in pixels.
func (t *Tracker) CharacterSize() image.Point {
	h := t.px(float64(t.charSize()))
	return image.Pt(int(math.Round(float64(h)*charWidth/charHeight)), h)
}

// ItemSize is the item display window's size in pixels.
func (t *Tracker) ItemSize() image.Point {
	n := t.px(float64(t.gs.ItemSize))
	return image.Pt(n, n)
}

// ItemOffset and CharacterOffset are where the item display and the
// character start relative to the tracker's top-left corner, until they are
// dragged somewhere else: the item under the icons and the character under
// the item.
func (t *Tracker) ItemOffset() image.Point {
	return image.Pt(t.px(margin)+t.px(itemIndent), t.layout().iconsEnd)
}

func (t *Tracker) CharacterOffset() image.Point {
	return image.Pt(t.px(margin), t.layout().iconsEnd+t.ItemSize().Y)
}

// tick returns the character to idle once the display time is up.
func (t *Tracker) tick(now time.Time) {
	if !t.resetAt.IsZero() && !now.Before(t.resetAt) {
		t.resetAt = time.Time{}
		t.charPath, t.charStart = filepath.Join(t.charDir, idleGif), now
	}
}

// Render draws the tracker (the icons and its buttons) and returns when it
// next needs redrawing (zero if it is static until the next input). The
// image's bounds are layout().bounds, so they can start left of or above 0,0.
func (t *Tracker) Render(now time.Time) (*image.RGBA, time.Time) {
	t.tick(now)
	l := t.layout()
	img := image.NewRGBA(l.bounds)
	t.hits[SurfMain] = t.hits[SurfMain][:0]
	var next time.Time
	wake := func(at time.Time) {
		if next.IsZero() || at.Before(next) {
			next = at
		}
	}

	hitFill := color.RGBA{A: minAlphaHit}
	for i, s := range t.icons {
		r := l.icons[i]
		if r.Empty() {
			continue
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				img.SetRGBA(x, y, hitFill)
			}
		}
		k := s.shownStage()
		amount, fading := s.colorAmount(now)
		if fading {
			wake(now.Add(16 * time.Millisecond))
		}
		if amount < 1 {
			drawImage(img, r.Min, s.grays[k], 1)
		}
		drawImage(img, r.Min, s.colors[k], amount)
		t.hits[SurfMain] = append(t.hits[SurfMain], hit{r: r, kind: hitIcon, idx: i})
	}

	fillRoundRect(img, l.grip, float64(t.px(5)), colChrome)
	drawGrip(img, l.grip.Inset(t.px(4)), colChromeGlyph)
	gearBg := colChrome
	if t.panelOpen {
		gearBg = colAccent
	}
	fillRoundRect(img, l.gear, float64(t.px(5)), gearBg)
	drawGear(img, l.gear.Inset(t.px(4)), colChromeGlyph)
	t.hits[SurfMain] = append(t.hits[SurfMain], hit{r: l.grip, kind: hitDrag}, hit{r: l.gear, kind: hitGear})
	return img, next
}

// RenderPanel draws the settings panel for its own window.
func (t *Tracker) RenderPanel(now time.Time) (*image.RGBA, time.Time) {
	img := image.NewRGBA(image.Rectangle{Max: t.PanelSize()})
	t.hits[SurfPanel] = t.hits[SurfPanel][:0]
	var next time.Time
	t.renderPanel(img, img.Rect, now, func(at time.Time) {
		if next.IsZero() || at.Before(next) {
			next = at
		}
	})
	return img, next
}

// RenderItem draws the item display (the clicked item's gif, or the game's
// idle image) on its own; the window places it.
// The returned image may be a cached frame, so it must not be changed.
func (t *Tracker) RenderItem(now time.Time) (*image.RGBA, time.Time) {
	t.tick(now)
	// No fill: transparent parts click through to whatever is behind, such
	// as the icons.
	path, start, until := t.game.IdlePath, t.started, time.Time{}
	if !t.resetAt.IsZero() {
		path, start, until = t.itemPath, t.itemStart, t.resetAt
	}
	return t.animFrame(path, t.ItemSize(), start, until, now)
}

// RenderCharacter draws the character on its own; the window places it.
// The returned image may be a cached frame, so it must not be changed.
func (t *Tracker) RenderCharacter(now time.Time) (*image.RGBA, time.Time) {
	t.tick(now)
	return t.animFrame(t.charPath, t.CharacterSize(), t.charStart, t.resetAt, now)
}

// animFrame returns the frame of the file at path that is due at now, played
// from start, and when to draw again: the next frame change or until,
// whichever comes first (zero for neither).
func (t *Tracker) animFrame(path string, size image.Point, start, until, now time.Time) (*image.RGBA, time.Time) {
	a := t.cache.Sized(path, size.X, size.Y)
	if a == nil {
		return image.NewRGBA(image.Rectangle{Max: size}), until
	}
	frame, d := a.Frame(now.Sub(start))
	if d > 0 && (until.IsZero() || now.Add(d).Before(until)) {
		until = now.Add(d)
	}
	return frame, until
}

func (t *Tracker) renderPanel(img *image.RGBA, panel image.Rectangle, now time.Time, wake func(time.Time)) {
	fillRoundRect(img, panel, float64(t.px(8)), colPanel)
	strokeRoundRect(img, panel, float64(t.px(8)), t.scale, colPanelBorder)
	t.hits[SurfPanel] = append(t.hits[SurfPanel], hit{r: panel, kind: hitPanel})

	inner := panel.Inset(t.px(panelPad))
	y := inner.Min.Y
	for i := range t.controls {
		y = t.renderControl(img, i, inner, y, now, wake) + t.px(12)
	}

	box := t.px(16)
	row := image.Rect(inner.Min.X, y, inner.Max.X, y+t.px(rowH))
	cb := image.Rect(row.Min.X, y+(row.Dy()-box)/2, row.Min.X+box, y+(row.Dy()-box)/2+box)
	if t.gs.AlwaysOnTop {
		fillRoundRect(img, cb, float64(t.px(3)), colAccent)
		drawCheck(img, cb, colOnColor)
	} else {
		strokeRoundRect(img, cb, float64(t.px(3)), 1.5*t.scale, colMuted)
	}
	drawText(img, t.face, "Always on top", cb.Max.X+t.px(8), y+row.Dy()/2, colFg)
	t.hits[SurfPanel] = append(t.hits[SurfPanel], hit{r: row, kind: hitOnTop})
	y = row.Max.Y + t.px(12)

	// Save on its own row, then Reset layout and Change game, then Exit.
	half := (inner.Dx() - t.px(6)) / 2
	save := image.Rect(inner.Min.X, y, inner.Max.X, y+t.px(btnH))
	fillRoundRect(img, save, float64(t.px(5)), colAccent)
	drawTextCentered(img, t.face, "Save", save, colOnColor)
	y = save.Max.Y + t.px(12)

	reset := image.Rect(inner.Min.X, y, inner.Min.X+half, y+t.px(btnH))
	change := image.Rect(inner.Max.X-half, y, inner.Max.X, y+t.px(btnH))
	fillRoundRect(img, reset, float64(t.px(5)), colButton)
	drawTextCentered(img, t.face, "Reset layout", reset, colFg)
	fillRoundRect(img, change, float64(t.px(5)), colButton)
	drawTextCentered(img, t.face, "Change game", change, colFg)
	y = reset.Max.Y + t.px(12)

	exit := image.Rect(inner.Min.X, y, inner.Max.X, y+t.px(btnH))
	fillRoundRect(img, exit, float64(t.px(5)), rgb(0x8a2e2e))
	drawTextCentered(img, t.face, "Exit", exit, colFg)
	t.hits[SurfPanel] = append(t.hits[SurfPanel],
		hit{r: save, kind: hitSave}, hit{r: reset, kind: hitReset},
		hit{r: change, kind: hitChangeGame}, hit{r: exit, kind: hitExit})
}

// renderControl draws "label [field] px" and a slider for control i starting
// at y, and returns the y just below it.
func (t *Tracker) renderControl(img *image.RGBA, i int, inner image.Rectangle, y int, now time.Time, wake func(time.Time)) int {
	c := t.controls[i]
	fh := t.px(fieldH)
	unitW := font.MeasureString(t.face, "px").Ceil() // the units share a column
	fx1 := inner.Max.X - unitW - t.px(6)
	field := image.Rect(fx1-t.px(58), y, fx1, y+fh)
	drawText(img, t.face, fitText(t.face, c.label, field.Min.X-t.px(8)-inner.Min.X), inner.Min.X, y+fh/2, colFg)
	t.renderField(img, i, field, now, wake)
	drawText(img, t.face, c.unit, inner.Max.X-unitW, y+fh/2, colMuted)
	t.hits[SurfPanel] = append(t.hits[SurfPanel], hit{r: field, kind: hitSizeField, idx: i})
	y += fh + t.px(6)

	knob := t.px(8)
	sh := t.px(sliderH)
	row := image.Rect(inner.Min.X, y, inner.Max.X, y+sh)
	mid := y + sh/2
	track := image.Rect(row.Min.X+knob, mid, row.Max.X-knob, mid+1)
	t.tracks[i] = track
	frac := float64(c.get()-c.min) / float64(c.max-c.min)
	kx := float64(track.Min.X) + frac*float64(track.Dx())
	th := t.px(2)
	fillRoundRect(img, image.Rect(track.Min.X, mid-th, track.Max.X, mid+th), float64(th), colButton)
	fillRoundRect(img, image.Rect(track.Min.X, mid-th, int(kx), mid+th), float64(th), colAccent)
	kr := float64(knob)
	fillShape(img, image.Rect(int(kx-kr)-1, int(float64(mid)-kr)-1, int(kx+kr)+2, int(float64(mid)+kr)+2), colOnColor,
		func(x, y float64) bool { return math.Hypot(x-kx, y-float64(mid)) <= kr })
	t.hits[SurfPanel] = append(t.hits[SurfPanel], hit{r: row, kind: hitSlider, idx: i})
	return row.Max.Y
}

func (t *Tracker) renderField(img *image.RGBA, i int, r image.Rectangle, now time.Time, wake func(time.Time)) {
	radius := float64(t.px(5))
	fillRoundRect(img, r, radius, colField)
	border := colPanelBorder
	editing := t.editIdx == i
	txt := strconv.Itoa(t.controls[i].get())
	if editing {
		border, txt = colAccent, t.editText
	}
	strokeRoundRect(img, r, radius, math.Max(1, t.scale), border)

	tw := font.MeasureString(t.face, txt).Ceil()
	tx := r.Min.X + (r.Dx()-tw)/2
	if editing && t.editFresh && txt != "" {
		sel := image.Rect(tx-t.px(2), r.Min.Y+t.px(6), tx+tw+t.px(2), r.Max.Y-t.px(6))
		fillRoundRect(img, sel, float64(t.px(2)), withAlpha(colAccent, 0x99))
	}
	drawText(img, t.face, txt, tx, (r.Min.Y+r.Max.Y)/2, colFg)

	if editing && !t.editFresh {
		phase := now.Sub(t.caretStart) / caretBlink
		if phase%2 == 0 {
			cx := tx + tw + t.px(1)
			caret := image.Rect(cx, r.Min.Y+t.px(7), cx+max(1, t.px(1.5)), r.Max.Y-t.px(7))
			fillRoundRect(img, caret, 0, colFg)
		}
		wake(t.caretStart.Add((phase + 1) * caretBlink))
	}
}

func (t *Tracker) hitAt(s Surface, p image.Point) hit {
	hits := t.hits[s]
	for i := len(hits) - 1; i >= 0; i-- {
		if p.In(hits[i].r) {
			return hits[i]
		}
	}
	return hit{}
}

// Cursor reports what is under p so the window can pick a mouse cursor.
func (t *Tracker) Cursor(s Surface, p image.Point) hitKind {
	return t.hitAt(s, p).kind
}

func (t *Tracker) sliderValue(i, x int, track image.Rectangle) int {
	c := t.controls[i]
	frac := float64(x-track.Min.X) / float64(max(track.Dx(), 1))
	frac = math.Max(0, math.Min(1, frac))
	return c.min + int(math.Round(frac*float64(c.max-c.min)))
}

// MouseDown handles a press on surface s. On the panel, the background
// (anywhere that isn't a control) drags the panel.
func (t *Tracker) MouseDown(s Surface, p image.Point, now time.Time) Action {
	h := t.hitAt(s, p)
	if t.editIdx >= 0 && !(h.kind == hitSizeField && h.idx == t.editIdx) {
		t.commitEdit()
	}
	if s == SurfMain && t.panelOpen && h.kind != hitGear {
		t.closePanel() // a click on the tracker just closes the panel
		if h.kind != hitDrag {
			return ActNone
		}
	}
	switch h.kind {
	case hitDrag, hitPanel:
		return ActDrag
	case hitGear:
		if t.panelOpen {
			t.closePanel()
		} else {
			t.panelOpen = true
		}
	case hitIcon:
		t.pressed = h.idx
		return ActCapture
	case hitSizeField:
		if t.editIdx != h.idx {
			t.editIdx, t.editFresh = h.idx, true
			t.editText = strconv.Itoa(t.controls[h.idx].get())
		}
		t.caretStart = now
		return ActFocus
	case hitSlider:
		t.slideIdx, t.slideTrack = h.idx, t.tracks[h.idx]
		t.setControl(h.idx, t.sliderValue(h.idx, p.X, t.slideTrack))
		return ActCapture
	case hitOnTop:
		t.gs.AlwaysOnTop = !t.gs.AlwaysOnTop
		return ActTopmost
	case hitSave:
		t.closePanel()
		return ActSave
	case hitReset:
		return ActConfirmReset
	case hitChangeGame:
		t.ChangeGame = true
		t.closePanel()
		return ActClose
	case hitExit:
		return ActExit
	}
	return ActNone
}

// MouseMove reports whether the overlay needs redrawing.
func (t *Tracker) MouseMove(p image.Point) bool {
	if t.slideIdx < 0 {
		return false
	}
	return t.setControl(t.slideIdx, t.sliderValue(t.slideIdx, p.X, t.slideTrack))
}

func (t *Tracker) MouseUp(surf Surface, p image.Point, now time.Time) {
	if t.slideIdx >= 0 {
		t.slideIdx = -1
		return
	}
	i := t.pressed
	t.pressed = -1
	if h := t.hitAt(surf, p); i < 0 || h.kind != hitIcon || h.idx != i {
		return
	}
	s := t.icons[i]
	s.advance(now)
	id := s.def.Stages[s.shownStage()].ID
	t.itemPath, t.itemStart = t.gifFile(id), now
	// The character reacts with <id>.gif if it has one, otherwise reward.gif.
	t.charPath, t.charStart = t.charFile(id), now
	if !fileExists(t.charPath) {
		t.charPath = filepath.Join(t.charDir, rewardGif)
	}
	// One shared deadline: a new click restarts the display timer.
	t.resetAt = now.Add(time.Duration(t.settings.DisplaySeconds) * time.Second)
}

// RightClick turns the icon at p back to grey (not collected), without
// changing what the character and item display show. It reports whether the
// overlay needs redrawing.
func (t *Tracker) RightClick(s Surface, p image.Point, now time.Time) bool {
	h := t.hitAt(s, p)
	if s != SurfMain || h.kind != hitIcon || t.icons[h.idx].stage < 0 {
		return false
	}
	t.icons[h.idx].clear(now)
	return true
}

// CaptureLost is called when the window loses the mouse mid-press.
func (t *Tracker) CaptureLost() {
	t.pressed = -1
	t.slideIdx = -1
}

// Wheel nudges a size when scrolling over its field or slider. It reports
// whether the overlay needs redrawing.
func (t *Tracker) Wheel(s Surface, p image.Point, notches int) bool {
	h := t.hitAt(s, p)
	if h.kind != hitSizeField && h.kind != hitSlider {
		return false
	}
	c := t.controls[h.idx]
	changed := t.setControl(h.idx, c.get()+notches*c.step)
	if t.editIdx == h.idx {
		t.editText, t.editFresh = strconv.Itoa(c.get()), true
	}
	return changed
}

// Key handles a key press while a size field is focused. It reports whether
// the key was used.
func (t *Tracker) Key(vk int, now time.Time) bool {
	if t.editIdx < 0 {
		return false
	}
	c := t.controls[t.editIdx]
	t.caretStart = now
	switch vk {
	case vkReturn:
		t.commitEdit()
	case vkEscape:
		t.editIdx = -1
	case vkUp, vkDown:
		d := c.step
		if vk == vkDown {
			d = -d
		}
		t.setControl(t.editIdx, c.get()+d)
		t.editText, t.editFresh = strconv.Itoa(c.get()), true
	case vkBack:
		if t.editFresh {
			t.editText = ""
		} else if t.editText != "" {
			t.editText = t.editText[:len(t.editText)-1]
		}
		t.editFresh = false
	default:
		return false
	}
	return true
}

// Char handles a typed character while a size field is focused.
func (t *Tracker) Char(r rune, now time.Time) bool {
	if t.editIdx < 0 || r < '0' || r > '9' {
		return false
	}
	if t.editFresh {
		t.editText, t.editFresh = "", false
	}
	if len(t.editText) < len(strconv.Itoa(t.controls[t.editIdx].max)) {
		t.editText += string(r)
	}
	t.caretStart = now
	return true
}

// FocusLost commits a half-typed size when the overlay loses focus.
func (t *Tracker) FocusLost() {
	if t.editIdx >= 0 {
		t.commitEdit()
	}
}

func (t *Tracker) commitEdit() {
	i := t.editIdx
	t.editIdx = -1
	if n, err := strconv.Atoi(t.editText); err == nil {
		t.setControl(i, n)
	}
}
