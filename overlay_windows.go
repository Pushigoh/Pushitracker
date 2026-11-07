package main

import (
	"bytes"
	"encoding/json"
	"image"
	"math"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The tracker is a borderless layered window updated with
// UpdateLayeredWindow, which gives true per-pixel transparency: the desktop
// shows through wherever it is transparent, and clicks there pass through to
// what's behind. The icons, the character and the item display above its
// head are all drawn in this one window, so capturing it (in OBS, say) shows
// all of them, along with the game's overlays behind them. The character,
// item and overlays can each be dragged with Shift held, and each icon with
// Ctrl held: the window grows to cover wherever they are. The item follows the character when the character
// moves or is resized; everything follows the grip. The settings panel has a
// window of its own, owned by the tracker window.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi = windows.NewLazySystemDLL("dwmapi.dll")

	pRegisterClassExW             = user32.NewProc("RegisterClassExW")
	pCreateWindowExW              = user32.NewProc("CreateWindowExW")
	pDefWindowProcW               = user32.NewProc("DefWindowProcW")
	pDestroyWindow                = user32.NewProc("DestroyWindow")
	pShowWindow                   = user32.NewProc("ShowWindow")
	pGetMessageW                  = user32.NewProc("GetMessageW")
	pTranslateMessage             = user32.NewProc("TranslateMessage")
	pDispatchMessageW             = user32.NewProc("DispatchMessageW")
	pSendMessageW                 = user32.NewProc("SendMessageW")
	pPostQuitMessage              = user32.NewProc("PostQuitMessage")
	pSetCapture                   = user32.NewProc("SetCapture")
	pMessageBoxW                  = user32.NewProc("MessageBoxW")
	pGetKeyState                  = user32.NewProc("GetKeyState")
	pSetFocus                     = user32.NewProc("SetFocus")
	pSetForegroundWindow          = user32.NewProc("SetForegroundWindow")
	pReleaseCapture               = user32.NewProc("ReleaseCapture")
	pLoadCursorW                  = user32.NewProc("LoadCursorW")
	pLoadImageW                   = user32.NewProc("LoadImageW")
	pSetCursor                    = user32.NewProc("SetCursor")
	pGetCursorPos                 = user32.NewProc("GetCursorPos")
	pScreenToClient               = user32.NewProc("ScreenToClient")
	pSetWindowPos                 = user32.NewProc("SetWindowPos")
	pGetWindowRect                = user32.NewProc("GetWindowRect")
	pUpdateLayeredWindow          = user32.NewProc("UpdateLayeredWindow")
	pUpdateLayeredWindowIndirect  = user32.NewProc("UpdateLayeredWindowIndirect")
	pGetDpiForWindow              = user32.NewProc("GetDpiForWindow")
	pSetThreadDpiAwarenessContext = user32.NewProc("SetThreadDpiAwarenessContext")
	pMonitorFromPoint             = user32.NewProc("MonitorFromPoint")
	pGetSystemMetrics             = user32.NewProc("GetSystemMetrics")
	pSetTimer                     = user32.NewProc("SetTimer")
	pKillTimer                    = user32.NewProc("KillTimer")
	pCreateCompatibleDC           = gdi32.NewProc("CreateCompatibleDC")
	pCreateDIBSection             = gdi32.NewProc("CreateDIBSection")
	pSelectObject                 = gdi32.NewProc("SelectObject")
	pDeleteObject                 = gdi32.NewProc("DeleteObject")
	pDeleteDC                     = gdi32.NewProc("DeleteDC")
	pDwmSetWindowAttribute        = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	wsPopup        = 0x80000000
	wsExLayered    = 0x00080000
	wsExAppWindow  = 0x00040000
	wsExToolWindow = 0x00000080
	swShow         = 5
	swShowNA       = 8
	swHide         = 0
	wmDestroy      = 0x0002
	wmMove         = 0x0003
	wmKillFocus    = 0x0008
	wmClose        = 0x0010
	wmSetCursor    = 0x0020
	wmNCLButtonDn  = 0x00A1
	wmKeyDown      = 0x0100
	wmChar         = 0x0102
	wmMouseMove    = 0x0200
	wmLButtonDown  = 0x0201
	wmLButtonUp    = 0x0202
	wmRButtonDown  = 0x0204
	wmMouseWheel   = 0x020A
	wmCaptureChg   = 0x0215
	wmExitSizeMove = 0x0232
	wmDpiChanged   = 0x02E0
	wmTimer        = 0x0113
	mkShift        = 0x0004
	mkControl      = 0x0008
	vkShift        = 0x10
	vkControl      = 0x11
	htCaption      = 2
	htClient       = 1
	idcArrow       = 32512
	idcIBeam       = 32513
	idcSizeAll     = 32646
	idcHand        = 32649
	ulwAlpha       = 2
	acSrcAlpha     = 1
	imageIcon      = 1
	lrDefaultSize  = 0x0040
	lrShared       = 0x8000
	swpNoSize      = 0x0001
	swpNoMove      = 0x0002
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	smCxScreen     = 0
	smCyScreen     = 1
	// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
	dpiPerMonitorV2 = ^uintptr(3)
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       uintptr
	cursor     uintptr
	background uintptr
	menuName   *uint16
	className  *uint16
	iconSm     uintptr
}

type point struct{ x, y int32 }
type size struct{ cx, cy int32 }
type rect struct{ left, top, right, bottom int32 }

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

type blendFunction struct {
	op, flags, constantAlpha, alphaFormat byte
}

type updateLayeredWindowInfo struct {
	size     uint32
	dst      uintptr
	pptDst   *point
	psize    *size
	src      uintptr
	pptSrc   *point
	key      uint32
	pblend   *blendFunction
	flags    uint32
	prcDirty *rect
}

// layered is one per-pixel-alpha window and the bitmap it is drawn from.
type layered struct {
	hwnd    uintptr
	memDC   uintptr
	bitmap  uintptr
	bits    unsafe.Pointer
	bmpSize image.Point
	shown   *image.RGBA // the image last pushed
}

// The parts drawn in the tracker window. The overlays come after these, as
// parts numParts on, and are drawn below them; see order.
const (
	partTracker = iota // the icons and the bar above them
	partChar
	partItem
	numParts
)

type overlay struct {
	t     *Tracker
	main  layered // the tracker, character and item
	panel layered
	// When each of the tracker, character, item and settings panel next needs
	// redrawing; zero when it is still until the next input.
	due [4]time.Time

	// Each part's latest picture, and where it is on screen; then the parts
	// in drawing order, bottom to top.
	parts []*image.RGBA
	at    []point
	order []int
	// Where the tracker window is on screen and its size, as last set, and
	// the area (on screen) that needs drawing again.
	origin  point
	winSize image.Point
	dirty   image.Rectangle

	// Where the item sits relative to the character's top-left corner. It is
	// kept unrounded so it scales exactly as the character is resized.
	itemOff [2]float64
	charH   int // character and item heights last drawn, to spot resizes
	itemPx  int

	// The character or item being dragged (-1 for none), where the cursor
	// was when the drag started and where the part was. partTracker means an
	// icon is being dragged: dragIcon, which started dragIconFrom away from
	// its place in its row.
	dragPart     int
	dragFrom     point
	dragStart    point
	dragIcon     int
	dragIconFrom [2]float64

	// This game's settings as last saved (or loaded), to tell whether
	// anything has changed since.
	saved []byte

	// Where the settings panel sits relative to the tracker, once it has been
	// opened or loaded from the settings file.
	panelOff    point
	panelOffSet bool
	panelMoved  bool // dragged, or placed from the settings file
	panelShown  bool
}

var (
	current       *overlay // the one open tracker
	registerClass sync.Once
	className     = windows.StringToUTF16Ptr("PushitrackerOverlay")
)

// runTracker opens the tracker and blocks until it is closed.
func runTracker(t *Tracker) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pSetThreadDpiAwarenessContext.Call(dpiPerMonitorV2)

	var instance windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &instance)
	registerClass.Do(func() {
		// The exe's icon (resource 1), for the taskbar and Alt+Tab.
		icon, _, _ := pLoadImageW.Call(uintptr(instance), 1, imageIcon, 0, 0, lrDefaultSize|lrShared)
		wc := wndClassEx{
			wndProc:   syscall.NewCallback(wndProc),
			instance:  instance,
			icon:      icon,
			className: className,
		}
		wc.size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	o := &overlay{t: t, dragPart: -1}
	current = o
	defer func() { current = nil }()

	title := windows.StringToUTF16Ptr(appName())
	x, y := screenPosition(t.gs.TrackerPos, func() (int32, int32) {
		w, _, _ := pGetSystemMetrics.Call(smCxScreen)
		h, _, _ := pGetSystemMetrics.Call(smCyScreen)
		return int32(w) / 3, int32(h) / 8
	})
	o.main.hwnd, _, _ = pCreateWindowExW.Call(
		wsExLayered|wsExAppWindow,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsPopup, uintptr(x), uintptr(y), 100, 100, 0, 0, uintptr(instance), 0)
	if o.main.hwnd == 0 {
		return
	}
	o.origin = point{x, y}
	dpi, _, _ := pGetDpiForWindow.Call(o.main.hwnd)
	if dpi == 0 {
		dpi = 96
	}
	t.SetScale(float64(dpi) / 96)

	n := numParts + len(t.Overlays())
	o.parts, o.at = make([]*image.RGBA, n), make([]point, n)
	for i := numParts; i < n; i++ {
		o.order = append(o.order, i)
	}
	o.order = append(o.order, partTracker, partChar, partItem)

	// The overlays start one under another until they have been dragged.
	for i, off := range t.OverlayOffsets() {
		if p, ok := t.gs.OverlayPos[t.OverlayName(i)]; ok {
			off = image.Pt(p[0], p[1])
		}
		o.at[numParts+i] = point{x + int32(off.X), y + int32(off.Y)}
	}

	// The character starts under the item display, and the item under the
	// icons, until they have been dragged.
	o.at[partTracker] = point{x, y}
	o.at[partChar].x, o.at[partChar].y = screenPosition(t.gs.CharPos, func() (int32, int32) {
		off := t.CharacterOffset()
		return x + int32(off.X), y + int32(off.Y)
	})
	ix, iy := screenPosition(t.gs.ItemPos, func() (int32, int32) {
		off := t.ItemOffset()
		return x + int32(off.X), y + int32(off.Y)
	})
	o.itemOff = [2]float64{float64(ix - o.at[partChar].x), float64(iy - o.at[partChar].y)}
	o.at[partItem] = point{ix, iy}

	// The settings panel starts hidden; draw shows it while it is open. It is
	// owned by the tracker window: it stays above it, has no taskbar button of
	// its own, and closes with it.
	if px, py, ok := savedPosition(t.gs.PanelPos); ok {
		o.panelOff, o.panelOffSet, o.panelMoved = point{px - x, py - y}, true, true
	}
	panelTitle := windows.StringToUTF16Ptr("Pushitracker settings")
	o.panel.hwnd, _, _ = pCreateWindowExW.Call(
		wsExLayered|wsExToolWindow,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(panelTitle)),
		wsPopup, uintptr(x), uintptr(y), 100, 100, o.main.hwnd, 0, uintptr(instance), 0)

	o.render()
	o.saved = o.snapshot()
	o.applyTopmost()
	pShowWindow.Call(o.main.hwnd, swShow)

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
	o.main.free()
	o.panel.free()
}

// screenPosition returns a saved position if it is still on a monitor,
// otherwise the fallback.
func screenPosition(saved *[2]int, fallback func() (int32, int32)) (int32, int32) {
	if x, y, ok := savedPosition(saved); ok {
		return x, y
	}
	return fallback()
}

func savedPosition(p *[2]int) (int32, int32, bool) {
	if p == nil {
		return 0, 0, false
	}
	pt := uintptr(uint32(int32(p[0]))) | uintptr(uint32(int32(p[1])))<<32
	if mon, _, _ := pMonitorFromPoint.Call(pt, 0); mon == 0 {
		return 0, 0, false
	}
	return int32(p[0]), int32(p[1]), true
}

func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	if o := current; o != nil {
		switch hwnd {
		case o.main.hwnd:
			if r, ok := o.mainProc(message, wParam, lParam); ok {
				return r
			}
		case o.panel.hwnd:
			if r, ok := o.panelProc(message, wParam, lParam); ok {
				return r
			}
		}
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return r
}

// mainProc handles the tracker window. ok is false to fall through to
// DefWindowProc.
func (o *overlay) mainProc(message, wParam, lParam uintptr) (uintptr, bool) {
	hwnd := o.main.hwnd
	switch message {
	case wmTimer:
		o.renderDue()
	case wmDpiChanged:
		o.t.SetScale(float64(wParam&0xffff) / 96)
		o.render()
	case wmMove:
		o.followMain()
	case wmClose:
		o.exit()
	case wmDestroy:
		pPostQuitMessage.Call(0)
	case wmLButtonDown:
		pt := lParamPoint(lParam)
		switch p := o.partAt(pt); {
		case wParam&mkShift != 0 && p != partTracker && p >= 0:
			// Shift+drag moves the character (bringing the item along), just
			// the item, or an overlay.
			o.dragPart, o.dragFrom, o.dragStart = p, cursorPos(), o.at[p]
		case wParam&mkShift != 0 && p == partTracker && !o.onChrome(pt):
			// On an icon, it moves the overlay behind it, if there is one.
			q := o.overlayAt(pt)
			if q < 0 {
				return 0, true
			}
			o.dragPart, o.dragFrom, o.dragStart = q, cursorPos(), o.at[q]
		case wParam&mkControl != 0 && p == partTracker && o.iconAt(pt) >= 0:
			// Ctrl+drag moves one icon.
			i := o.iconAt(pt)
			o.dragPart, o.dragFrom, o.dragIcon, o.dragIconFrom = partTracker, cursorPos(), i, o.t.IconOffset(i)
		case p != partTracker && p >= 0:
			// Without Shift the character, item and overlays do nothing.
			return 0, true
		default:
			return o.inputProc(SurfMain, hwnd, message, wParam, lParam)
		}
		pSetCapture.Call(hwnd)
	case wmMouseMove:
		if o.dragPart < 0 {
			return o.inputProc(SurfMain, hwnd, message, wParam, lParam)
		}
		c := cursorPos()
		d := point{c.x - o.dragFrom.x, c.y - o.dragFrom.y}
		to := point{o.dragStart.x + d.x, o.dragStart.y + d.y}
		switch o.dragPart {
		case partTracker:
			o.t.MoveIcon(o.dragIcon, o.dragIconFrom, image.Pt(int(d.x), int(d.y)))
			o.draw([4]bool{true})
		case partChar:
			o.moveChar(to)
			o.compose()
		default:
			o.movePart(o.dragPart, to)
			o.compose()
		}
	case wmLButtonUp, wmCaptureChg:
		if o.dragPart < 0 {
			return o.inputProc(SurfMain, hwnd, message, wParam, lParam)
		}
		if o.dragPart == partItem {
			o.measureItemOffset()
		}
		o.dragPart = -1
		if message == wmLButtonUp {
			pReleaseCapture.Call()
		}
	case wmSetCursor:
		if lParam&0xffff != htClient {
			return 0, false
		}
		c := clientPoint(hwnd, cursorPos())
		p := o.partAt(c)
		move := p == partTracker && keyDown(vkControl) && o.iconAt(c) >= 0
		if keyDown(vkShift) {
			move = move || p >= 0 && p != partTracker ||
				p == partTracker && !o.onChrome(c) && o.overlayAt(c) >= 0
		}
		switch {
		case move:
			setCursor(idcSizeAll)
			return 1, true
		case p != partTracker:
			setCursor(idcArrow)
			return 1, true
		}
		return o.inputProc(SurfMain, hwnd, message, wParam, lParam)
	default:
		return o.inputProc(SurfMain, hwnd, message, wParam, lParam)
	}
	return 0, true
}

// panelProc handles the settings panel window.
func (o *overlay) panelProc(message, wParam, lParam uintptr) (uintptr, bool) {
	switch message {
	case wmExitSizeMove:
		o.measurePanelOffset()
	case wmClose:
		o.exit()
	default:
		return o.inputProc(SurfPanel, o.panel.hwnd, message, wParam, lParam)
	}
	return 0, true
}

// inputProc passes mouse and keyboard input on the tracker or the panel to
// the tracker, and carries out what it asks for.
func (o *overlay) inputProc(s Surface, hwnd, message, wParam, lParam uintptr) (uintptr, bool) {
	now := time.Now()
	// Points in the tracker window are moved to the tracker part's own
	// coordinates.
	var off image.Point
	if s == SurfMain {
		off = o.trackerOff()
	}
	switch message {
	case wmLButtonDown:
		switch o.t.MouseDown(s, lParamPoint(lParam).Sub(off), now) {
		case ActDrag:
			// Windows runs the move. Moving the tracker window brings
			// everything along (see followMain); moving the panel moves just
			// the panel.
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLButtonDn, htCaption, 0)
		case ActSave:
			o.save()
		case ActExit:
			o.exit()
			return 0, true
		case ActConfirmReset:
			if confirmReset(o.panel.hwnd) {
				o.t.ResetLayout()
				o.resetPositions()
			}
		case ActCapture:
			pSetCapture.Call(hwnd)
		case ActFocus:
			pSetForegroundWindow.Call(hwnd)
			pSetFocus.Call(hwnd)
		case ActTopmost:
			o.applyTopmost()
		case ActClose:
			pDestroyWindow.Call(o.main.hwnd)
			return 0, true
		}
		o.render()
	case wmRButtonDown:
		// Only on the tracker itself, not the character or item over it.
		if s == SurfMain && o.partAt(lParamPoint(lParam)) != partTracker {
			break
		}
		if o.t.RightClick(s, lParamPoint(lParam).Sub(off), now) {
			o.render()
		}
	case wmLButtonUp:
		o.t.MouseUp(s, lParamPoint(lParam).Sub(off), now)
		pReleaseCapture.Call()
		o.render()
	case wmCaptureChg:
		o.t.CaptureLost()
		o.render()
	case wmMouseMove:
		if o.t.MouseMove(lParamPoint(lParam).Sub(off)) {
			o.render()
		}
	case wmMouseWheel:
		// Wheel positions are in screen coordinates.
		p := point{int32(int16(lParam & 0xffff)), int32(int16((lParam >> 16) & 0xffff))}
		notches := int(int16((wParam>>16)&0xffff)) / 120
		if o.t.Wheel(s, clientPoint(hwnd, p).Sub(off), notches) {
			o.render()
		}
	case wmKeyDown:
		if !o.t.Key(int(wParam), now) {
			return 0, false
		}
		o.render()
	case wmChar:
		if !o.t.Char(rune(wParam), now) {
			return 0, false
		}
		o.render()
	case wmKillFocus:
		o.t.FocusLost()
		o.render()
	case wmSetCursor:
		if lParam&0xffff != htClient {
			return 0, false
		}
		id := uintptr(idcArrow)
		switch o.t.Cursor(s, clientPoint(hwnd, cursorPos()).Sub(off)) {
		case hitDrag, hitPanel:
			id = idcSizeAll
		case hitIcon, hitGear, hitSlider, hitOnTop, hitSave, hitReset, hitChangeGame, hitExit:
			id = idcHand
		case hitSizeField:
			id = idcIBeam
		}
		setCursor(id)
		return 1, true
	default:
		return 0, false
	}
	return 0, true
}

// trackerOff is where the tracker's own 0,0 is in the tracker window.
func (o *overlay) trackerOff() image.Point {
	return image.Pt(int(o.at[partTracker].x-o.origin.x), int(o.at[partTracker].y-o.origin.y))
}

// iconAt returns the icon at p, in tracker window coordinates, or -1.
func (o *overlay) iconAt(p image.Point) int { return o.t.IconAt(p.Sub(o.trackerOff())) }

// onChrome reports whether p, in tracker window coordinates, is on the
// tracker's grip or gear.
func (o *overlay) onChrome(p image.Point) bool {
	k := o.t.Cursor(SurfMain, p.Sub(o.trackerOff()))
	return k == hitDrag || k == hitGear
}

func keyDown(vk uintptr) bool {
	s, _, _ := pGetKeyState.Call(vk)
	return int16(s) < 0
}

// overlayAt returns the topmost overlay with a visible pixel at p, in tracker
// window coordinates, whatever is drawn over it; -1 if there is none.
func (o *overlay) overlayAt(p image.Point) int {
	for i := len(o.parts) - 1; i >= numParts; i-- {
		if o.visibleAt(i, p) {
			return i
		}
	}
	return -1
}

// visibleAt reports whether part i has a visible pixel at p, in tracker
// window coordinates.
func (o *overlay) visibleAt(i int, p image.Point) bool {
	img := o.parts[i]
	if img == nil {
		return false
	}
	q := p.Add(image.Pt(int(o.origin.x-o.at[i].x), int(o.origin.y-o.at[i].y)))
	return q.In(img.Rect) && img.Pix[img.PixOffset(q.X, q.Y)+3] != 0
}

// partAt returns the topmost part with a visible pixel at p, in tracker
// window coordinates, or -1 if there is none.
func (o *overlay) partAt(p image.Point) int {
	for k := len(o.order) - 1; k >= 0; k-- {
		if i := o.order[k]; o.visibleAt(i, p) {
			return i
		}
	}
	return -1
}

// partRect is where part i is on screen. at is where the part's 0,0 is; its
// picture can start left of or above that (the tracker's can, when icons have
// been dragged there).
func (o *overlay) partRect(i int) image.Rectangle {
	if o.parts[i] == nil {
		return image.Rectangle{}
	}
	return o.parts[i].Rect.Add(image.Pt(int(o.at[i].x), int(o.at[i].y)))
}

// setPart gives part i a new picture.
func (o *overlay) setPart(i int, img *image.RGBA) {
	if img == o.parts[i] {
		return
	}
	o.dirty = o.dirty.Union(o.partRect(i))
	o.parts[i] = img
	o.dirty = o.dirty.Union(o.partRect(i))
}

// movePart moves part i to p on screen.
func (o *overlay) movePart(i int, p point) {
	if p == o.at[i] {
		return
	}
	o.dirty = o.dirty.Union(o.partRect(i))
	o.at[i] = p
	o.dirty = o.dirty.Union(o.partRect(i))
}

// moveChar moves the character to p, and the item with it.
func (o *overlay) moveChar(p point) {
	o.movePart(partChar, p)
	o.placeItem()
}

// placeItem puts the item at its offset from the character.
func (o *overlay) placeItem() {
	c := o.at[partChar]
	o.movePart(partItem, point{c.x + int32(math.Round(o.itemOff[0])), c.y + int32(math.Round(o.itemOff[1]))})
}

// measureItemOffset records where the item now is relative to the character.
func (o *overlay) measureItemOffset() {
	c, i := o.at[partChar], o.at[partItem]
	o.itemOff = [2]float64{float64(i.x - c.x), float64(i.y - c.y)}
}

// resetPositions moves the character, item and overlays back to where they
// start, relative to the tracker, at their (just reset) sizes.
func (o *overlay) resetPositions() {
	m := o.at[partTracker]
	for i, off := range o.t.OverlayOffsets() {
		o.movePart(numParts+i, point{m.x + int32(off.X), m.y + int32(off.Y)})
	}
	co, io := o.t.CharacterOffset(), o.t.ItemOffset()
	o.itemOff = [2]float64{float64(io.X - co.X), float64(io.Y - co.Y)}
	// Already at the new sizes, so followResize leaves the offset alone.
	o.charH, o.itemPx = o.t.CharacterSize().Y, o.t.ItemSize().Y
	o.moveChar(point{m.x + int32(co.X), m.y + int32(co.Y)})
	o.panelOffSet, o.panelMoved = false, false
	if o.panelShown {
		o.placePanel()
	}
}

// placePanel puts the settings panel at its offset from the tracker, or
// under the gear if it hasn't been moved.
func (o *overlay) placePanel() {
	if !o.panelOffSet {
		off := o.t.PanelOffset()
		o.panelOff, o.panelOffSet = point{int32(off.X), int32(off.Y)}, true
	}
	m := o.at[partTracker]
	pSetWindowPos.Call(o.panel.hwnd, 0, uintptr(m.x+o.panelOff.x), uintptr(m.y+o.panelOff.y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

// measurePanelOffset records where the panel was dragged to, relative to the
// tracker, so it opens there again and follows the grip.
func (o *overlay) measurePanelOffset() {
	var p rect
	if ok, _, _ := pGetWindowRect.Call(o.panel.hwnd, uintptr(unsafe.Pointer(&p))); ok != 0 {
		m := o.at[partTracker]
		o.panelOff, o.panelOffSet, o.panelMoved = point{p.left - m.x, p.top - m.y}, true, true
	}
}

// followMain keeps track of the tracker window being dragged by the grip:
// everything in it moves with it, and so does the settings panel.
func (o *overlay) followMain() {
	var r rect
	if ok, _, _ := pGetWindowRect.Call(o.main.hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	dx, dy := r.left-o.origin.x, r.top-o.origin.y
	if dx == 0 && dy == 0 {
		return
	}
	o.origin = point{r.left, r.top}
	for i := range o.at {
		o.at[i].x += dx
		o.at[i].y += dy
	}
	o.dirty = o.dirty.Add(image.Pt(int(dx), int(dy)))
	if o.panelShown {
		o.placePanel()
	}
}

// current is this game's settings as Save would write them now, with where
// everything is.
func (o *overlay) current() GameSettings {
	g := *o.t.gs
	m := o.at[partTracker]
	g.TrackerPos = savePoint(m)
	g.CharPos = savePoint(o.at[partChar])
	g.ItemPos = savePoint(o.at[partItem])
	g.OverlayPos = nil
	for i := numParts; i < len(o.at); i++ {
		if g.OverlayPos == nil {
			g.OverlayPos = map[string][2]int{}
		}
		g.OverlayPos[o.t.OverlayName(i-numParts)] = [2]int{int(o.at[i].x - m.x), int(o.at[i].y - m.y)}
	}
	g.PanelPos = nil
	if o.panelMoved {
		g.PanelPos = savePoint(point{m.x + o.panelOff.x, m.y + o.panelOff.y})
	}
	return g
}

func (o *overlay) snapshot() []byte {
	b, _ := json.Marshal(o.current())
	return b
}

// save writes this game's settings file.
func (o *overlay) save() {
	*o.t.gs = o.current()
	o.t.SaveGame()
	o.saved = o.snapshot()
}

// exit closes the tracker, first asking whether to save if anything has
// changed since the last Save.
func (o *overlay) exit() {
	o.t.FocusLost() // keep a half-typed size
	if !bytes.Equal(o.snapshot(), o.saved) {
		switch askSave(o.main.hwnd) {
		case idYes:
			o.save()
		case idNo:
		default:
			o.render()
			return
		}
	}
	pDestroyWindow.Call(o.main.hwnd)
}

const (
	mbOKCancel     = 0x1
	mbYesNoCancel  = 0x3
	mbIconQuestion = 0x20
	mbDefButton2   = 0x100
	mbTopmost      = 0x40000
	idOK           = 1
	idYes          = 6
	idNo           = 7
)

// askSave asks whether to save before exiting: idYes, idNo or idCancel.
func askSave(owner uintptr) uintptr {
	text := windows.StringToUTF16Ptr("Save your changes to this game's layout and settings before exiting?")
	title := windows.StringToUTF16Ptr("Unsaved changes")
	r, _, _ := pMessageBoxW.Call(owner, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)),
		mbYesNoCancel|mbIconQuestion|mbTopmost)
	return r
}

// confirmReset asks before Reset layout puts everything back.
func confirmReset(owner uintptr) bool {
	text := windows.StringToUTF16Ptr("Put all sizes and positions back to their defaults?\n\nNothing is written to the settings file until you press Save.")
	title := windows.StringToUTF16Ptr("Reset layout")
	r, _, _ := pMessageBoxW.Call(owner, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)),
		mbOKCancel|mbIconQuestion|mbDefButton2|mbTopmost)
	return r == idOK
}

func cursorPos() point {
	var p point
	pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	return p
}

// clientPoint converts a screen point to hwnd's client coordinates.
func clientPoint(hwnd uintptr, p point) image.Point {
	pScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&p)))
	return image.Pt(int(p.x), int(p.y))
}

func savePoint(p point) *[2]int { return &[2]int{int(p.x), int(p.y)} }

func setCursor(id uintptr) {
	c, _, _ := pLoadCursorW.Call(0, id)
	pSetCursor.Call(c)
}

func lParamPoint(l uintptr) image.Point {
	return image.Pt(int(int16(l&0xffff)), int(int16((l>>16)&0xffff)))
}

func (o *overlay) applyTopmost() {
	after := ^uintptr(1) // HWND_NOTOPMOST
	if o.t.gs.AlwaysOnTop {
		after = ^uintptr(0) // HWND_TOPMOST
	}
	for _, h := range []uintptr{o.main.hwnd, o.panel.hwnd} {
		if h != 0 {
			pSetWindowPos.Call(h, after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		}
	}
}

// render redraws everything, after input or anything else that may have
// changed it.
func (o *overlay) render() {
	o.draw([4]bool{true, true, true, true})
}

// renderDue redraws just what has moved on, so a character gif doesn't keep
// redrawing the icons and the settings panel.
func (o *overlay) renderDue() {
	now := time.Now().Add(2 * time.Millisecond)
	var which [4]bool
	for i, at := range o.due {
		which[i] = !at.IsZero() && !now.Before(at)
	}
	o.draw(which)
}

func (o *overlay) draw(which [4]bool) {
	now := time.Now()
	o.followResize()
	if which[0] {
		img, next := o.t.Render(now)
		o.setPart(partTracker, img)
		o.due[0] = next
		// The overlays only change with the scale.
		for i, img := range o.t.Overlays() {
			o.setPart(numParts+i, img)
		}
	}
	if which[1] {
		img, next := o.t.RenderCharacter(now)
		o.setPart(partChar, img)
		o.due[1] = next
	}
	if which[2] {
		img, next := o.t.RenderItem(now)
		o.setPart(partItem, img)
		o.due[2] = next
	}
	o.compose()
	if o.panel.hwnd != 0 {
		o.syncPanel(which[3], now)
	}
	o.schedule()
}

// compose redraws the changed area of the tracker window from the parts,
// first fitting the window to wherever the parts now are.
func (o *overlay) compose() {
	var bounds image.Rectangle
	for i := range o.parts {
		bounds = bounds.Union(o.partRect(i))
	}
	if bounds.Empty() {
		return
	}
	origin := point{int32(bounds.Min.X), int32(bounds.Min.Y)}
	moved := origin != o.origin || bounds.Size() != o.winSize
	if moved || o.main.bits == nil {
		// The bitmap's contents no longer line up with the screen.
		if !o.main.ensureBitmap(bounds.Size()) {
			return
		}
		o.dirty = bounds
	}
	dirty := o.dirty.Intersect(bounds)
	o.dirty = image.Rectangle{}
	if dirty.Empty() && !moved {
		return
	}

	// Clear the dirty area, then paint each part over it, bottom up.
	w := o.main.bmpSize.X
	dst := unsafe.Slice((*byte)(o.main.bits), w*o.main.bmpSize.Y*4)
	local := dirty.Sub(bounds.Min)
	for y := local.Min.Y; y < local.Max.Y; y++ {
		clear(dst[(y*w+local.Min.X)*4 : (y*w+local.Max.X)*4])
	}
	for _, i := range o.order {
		img := o.parts[i]
		r := o.partRect(i).Intersect(dirty)
		if img == nil || r.Empty() {
			continue
		}
		blendBGRA(dst, w, r.Sub(bounds.Min), img, r.Min.Sub(image.Pt(int(o.at[i].x), int(o.at[i].y))))
	}

	// Set origin first: moving the window sends wmMove, and followMain must
	// see that it is already where it should be.
	o.origin, o.winSize = origin, bounds.Size()
	s := size{int32(bounds.Dx()), int32(bounds.Dy())}
	dr := rect{int32(local.Min.X), int32(local.Min.Y), int32(local.Max.X), int32(local.Max.Y)}
	var src point
	blend := blendFunction{alphaFormat: acSrcAlpha, constantAlpha: 255}
	info := updateLayeredWindowInfo{
		pptDst: &origin, psize: &s, src: o.main.memDC, pptSrc: &src,
		pblend: &blend, flags: ulwAlpha, prcDirty: &dr,
	}
	info.size = uint32(unsafe.Sizeof(info))
	pUpdateLayeredWindowIndirect.Call(o.main.hwnd, uintptr(unsafe.Pointer(&info)))
}

// blendBGRA paints the part of src starting at sp (in src's coordinates) over r of a premultiplied
// BGRA bitmap w pixels wide.
func blendBGRA(dst []byte, w int, r image.Rectangle, src *image.RGBA, sp image.Point) {
	for y := 0; y < r.Dy(); y++ {
		d := dst[((r.Min.Y+y)*w+r.Min.X)*4:]
		s := src.Pix[src.PixOffset(sp.X, sp.Y+y):]
		for x := 0; x < r.Dx()*4; x += 4 {
			a := s[x+3]
			switch a {
			case 0:
			case 255:
				d[x], d[x+1], d[x+2], d[x+3] = s[x+2], s[x+1], s[x], 255
			default:
				k := uint32(255 - a)
				d[x] = s[x+2] + uint8((uint32(d[x])*k+127)/255)
				d[x+1] = s[x+1] + uint8((uint32(d[x+1])*k+127)/255)
				d[x+2] = s[x] + uint8((uint32(d[x+2])*k+127)/255)
				d[x+3] = a + uint8((uint32(d[x+3])*k+127)/255)
			}
		}
	}
}

// syncPanel shows, draws or hides the settings panel to match the tracker.
func (o *overlay) syncPanel(redraw bool, now time.Time) {
	switch {
	case o.t.panelOpen:
		if !o.panelShown || redraw {
			img, next := o.t.RenderPanel(now)
			o.panel.push(img)
			o.due[3] = next
		}
		if !o.panelShown {
			o.placePanel()
			o.panelShown = true
			pShowWindow.Call(o.panel.hwnd, swShowNA)
		}
	case o.panelShown:
		o.panelShown = false
		o.due[3] = time.Time{}
		pShowWindow.Call(o.panel.hwnd, swHide)
	}
}

// schedule sets a timer for the next redraw. WM_TIMER only arrives when no
// input is waiting, so animation never holds up clicks and keys, however
// long drawing takes.
func (o *overlay) schedule() {
	var next time.Time
	for _, at := range o.due {
		if !at.IsZero() && (next.IsZero() || at.Before(next)) {
			next = at
		}
	}
	if next.IsZero() {
		pKillTimer.Call(o.main.hwnd, 1)
		return
	}
	ms := max(time.Until(next).Milliseconds(), 1)
	pSetTimer.Call(o.main.hwnd, 1, uintptr(ms), 0)
}

// followResize keeps the item in step with size changes. Resizing the
// character scales the item (in the tracker) and its distance from the
// character, keeping the item where it is and moving the character instead,
// so the pair grows downwards, away from the icons. Resizing just the item
// keeps it centred where it was.
func (o *overlay) followResize() {
	ch, ip := o.t.CharacterSize().Y, o.t.ItemSize().Y
	switch {
	case o.charH > 0 && ch != o.charH:
		r := float64(ch) / float64(o.charH)
		o.itemOff[0] *= r
		o.itemOff[1] *= r
		i := o.at[partItem]
		o.moveChar(point{i.x - int32(math.Round(o.itemOff[0])), i.y - int32(math.Round(o.itemOff[1]))})
	case o.itemPx > 0 && ip != o.itemPx:
		d := float64(o.itemPx-ip) / 2
		o.itemOff[0] += d
		o.itemOff[1] += d
		o.placeItem()
	}
	o.charH, o.itemPx = ch, ip
}

// push copies img into the window's bitmap and shows it. The window keeps its
// top-left corner and takes img's size. Pushing the image already shown does
// nothing.
func (w *layered) push(img *image.RGBA) {
	sz := img.Rect.Size()
	if img == w.shown || sz.X <= 0 || sz.Y <= 0 || !w.ensureBitmap(sz) {
		return
	}
	w.shown = img
	// image.RGBA is premultiplied RGBA; the DIB wants premultiplied BGRA.
	dst := unsafe.Slice((*byte)(w.bits), sz.X*sz.Y*4)
	src := img.Pix
	for i := 0; i < len(dst); i += 4 {
		dst[i], dst[i+1], dst[i+2], dst[i+3] = src[i+2], src[i+1], src[i], src[i+3]
	}
	s := size{int32(sz.X), int32(sz.Y)}
	var origin point
	blend := blendFunction{alphaFormat: acSrcAlpha, constantAlpha: 255}
	pUpdateLayeredWindow.Call(w.hwnd, 0, 0, uintptr(unsafe.Pointer(&s)), w.memDC,
		uintptr(unsafe.Pointer(&origin)), 0, uintptr(unsafe.Pointer(&blend)), ulwAlpha)
}

func (w *layered) ensureBitmap(sz image.Point) bool {
	if w.bitmap != 0 && w.bmpSize == sz {
		return true
	}
	w.free()
	w.memDC, _, _ = pCreateCompatibleDC.Call(0)
	bi := bitmapInfoHeader{width: int32(sz.X), height: -int32(sz.Y), planes: 1, bitCount: 32}
	bi.size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	w.bitmap, _, _ = pCreateDIBSection.Call(w.memDC, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if w.bitmap == 0 || bits == nil {
		w.free()
		return false
	}
	pSelectObject.Call(w.memDC, w.bitmap)
	w.bits, w.bmpSize = bits, sz
	return true
}

func (w *layered) free() {
	if w.bitmap != 0 {
		pDeleteObject.Call(w.bitmap)
	}
	if w.memDC != 0 {
		pDeleteDC.Call(w.memDC)
	}
	w.bitmap, w.memDC, w.bits, w.shown = 0, 0, nil, nil
}

// useDarkTitleBar asks Windows 10 20H1+ / 11 to draw a window's title bar dark.
func useDarkTitleBar(hwnd uintptr) {
	on := int32(1)
	pDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&on)), 4)
}
