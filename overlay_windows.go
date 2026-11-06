package main

import (
	"image"
	"math"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The tracker is a few borderless layered windows updated with
// UpdateLayeredWindow, which gives true per-pixel transparency: the desktop
// shows through wherever they are transparent, and clicks there pass through
// to what's behind. The character, the item display above its head and the
// settings panel each have their own window, owned by the tracker window, so
// they can be dragged anywhere on their own. The item follows the character
// when the character moves or is resized; everything follows the grip.

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
	wmMouseWheel   = 0x020A
	wmCaptureChg   = 0x0215
	wmExitSizeMove = 0x0232
	wmDpiChanged   = 0x02E0
	wmTimer        = 0x0113
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

// layered is one per-pixel-alpha window and the bitmap it is drawn from.
type layered struct {
	hwnd    uintptr
	memDC   uintptr
	bitmap  uintptr
	bits    unsafe.Pointer
	bmpSize image.Point
}

type overlay struct {
	t     *Tracker
	main  layered
	char  layered
	item  layered
	panel layered
	// When each window (main, character, item, panel) next needs redrawing;
	// zero when it is still until the next input.
	due      [4]time.Time
	mainLast point // where the tracker window was, to move the character with it

	// Where the item sits relative to the character's top-left corner. It is
	// kept unrounded so it scales exactly as the character is resized.
	itemOff    [2]float64
	charH      int // character and item heights last drawn, to spot resizes
	itemPx     int
	itemPlaced bool // itemOff is set and the item may follow the character

	// Where the settings panel sits relative to the tracker, once it has been
	// opened or loaded from the settings file.
	panelOff    point
	panelOffSet bool
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

	o := &overlay{t: t}
	current = o
	defer func() { current = nil }()

	title := windows.StringToUTF16Ptr("Pushitracker")
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
	o.mainLast = point{x, y}
	dpi, _, _ := pGetDpiForWindow.Call(o.main.hwnd)
	if dpi == 0 {
		dpi = 96
	}
	t.SetScale(float64(dpi) / 96)

	// The character starts under the item display until it has been dragged.
	cx, cy := screenPosition(t.gs.CharPos, func() (int32, int32) {
		off := t.CharacterOffset()
		return x + int32(off.X), y + int32(off.Y)
	})
	// Owned by the tracker window: it stays above it, has no taskbar button
	// of its own, and closes with it.
	charTitle := windows.StringToUTF16Ptr("Pushitracker character")
	o.char.hwnd, _, _ = pCreateWindowExW.Call(
		wsExLayered|wsExToolWindow,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(charTitle)),
		wsPopup, uintptr(cx), uintptr(cy), 100, 100, o.main.hwnd, 0, uintptr(instance), 0)

	ix, iy := screenPosition(t.gs.ItemPos, func() (int32, int32) {
		off := t.ItemOffset()
		return x + int32(off.X), y + int32(off.Y)
	})
	itemTitle := windows.StringToUTF16Ptr("Pushitracker item")
	o.item.hwnd, _, _ = pCreateWindowExW.Call(
		wsExLayered|wsExToolWindow,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(itemTitle)),
		wsPopup, uintptr(ix), uintptr(iy), 100, 100, o.main.hwnd, 0, uintptr(instance), 0)
	o.itemOff = [2]float64{float64(ix - cx), float64(iy - cy)}
	o.itemPlaced = o.char.hwnd != 0 && o.item.hwnd != 0

	// The settings panel starts hidden; draw shows it while it is open.
	if px, py, ok := savedPosition(t.gs.PanelPos); ok {
		o.panelOff, o.panelOffSet = point{px - x, py - y}, true
	}
	panelTitle := windows.StringToUTF16Ptr("Pushitracker settings")
	o.panel.hwnd, _, _ = pCreateWindowExW.Call(
		wsExLayered|wsExToolWindow,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(panelTitle)),
		wsPopup, uintptr(x), uintptr(y), 100, 100, o.main.hwnd, 0, uintptr(instance), 0)

	o.render()
	o.applyTopmost()
	pShowWindow.Call(o.main.hwnd, swShow)
	for _, h := range []uintptr{o.char.hwnd, o.item.hwnd} {
		if h != 0 {
			pShowWindow.Call(h, swShowNA)
		}
	}

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
	o.char.free()
	o.item.free()
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
		case o.char.hwnd:
			if r, ok := o.charProc(message, wParam, lParam); ok {
				return r
			}
		case o.item.hwnd:
			if r, ok := o.itemProc(message, wParam, lParam); ok {
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
		pDestroyWindow.Call(hwnd)
	case wmDestroy:
		pPostQuitMessage.Call(0)
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
		pDestroyWindow.Call(o.main.hwnd)
	default:
		return o.inputProc(SurfPanel, o.panel.hwnd, message, wParam, lParam)
	}
	return 0, true
}

// inputProc passes mouse and keyboard input on the tracker or the panel to
// the tracker, and carries out what it asks for.
func (o *overlay) inputProc(s Surface, hwnd, message, wParam, lParam uintptr) (uintptr, bool) {
	now := time.Now()
	switch message {
	case wmLButtonDown:
		switch o.t.MouseDown(s, lParamPoint(lParam), now) {
		case ActDrag:
			// Windows runs the move. Moving the tracker brings the others
			// along (see followMain); moving the panel moves just the panel.
			pReleaseCapture.Call()
			pSendMessageW.Call(hwnd, wmNCLButtonDn, htCaption, 0)
		case ActSave:
			o.recordPosition(o.main.hwnd, &o.t.gs.TrackerPos)
			o.recordPosition(o.char.hwnd, &o.t.gs.CharPos)
			o.recordPosition(o.item.hwnd, &o.t.gs.ItemPos)
			if o.panelShown {
				o.recordPosition(o.panel.hwnd, &o.t.gs.PanelPos)
			}
			o.t.SaveGame()
		case ActReset:
			o.resetPositions()
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
	case wmLButtonUp:
		o.t.MouseUp(s, lParamPoint(lParam), now)
		pReleaseCapture.Call()
		o.render()
	case wmCaptureChg:
		o.t.CaptureLost()
		o.render()
	case wmMouseMove:
		if o.t.MouseMove(lParamPoint(lParam)) {
			o.render()
		}
	case wmMouseWheel:
		// Wheel positions are in screen coordinates.
		p := point{int32(int16(lParam & 0xffff)), int32(int16((lParam >> 16) & 0xffff))}
		pScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&p)))
		notches := int(int16((wParam>>16)&0xffff)) / 120
		if o.t.Wheel(s, image.Pt(int(p.x), int(p.y)), notches) {
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
		var p point
		pGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
		pScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&p)))
		id := uintptr(idcArrow)
		switch o.t.Cursor(s, image.Pt(int(p.x), int(p.y))) {
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

// charProc handles the character window: click and hold anywhere visible on
// the character to drag it, and the item above its head comes along.
func (o *overlay) charProc(message, wParam, lParam uintptr) (uintptr, bool) {
	switch message {
	case wmMove:
		o.placeItem()
	default:
		return o.dragProc(o.char.hwnd, message, lParam)
	}
	return 0, true
}

// itemProc handles the item display window: dragging it moves only the item,
// and it keeps its new place relative to the character.
func (o *overlay) itemProc(message, wParam, lParam uintptr) (uintptr, bool) {
	switch message {
	case wmExitSizeMove:
		o.measureItemOffset()
	default:
		return o.dragProc(o.item.hwnd, message, lParam)
	}
	return 0, true
}

// dragProc is what the character and item windows share: click and hold to
// drag, a move cursor, and Alt+F4 closing the whole tracker.
func (o *overlay) dragProc(hwnd, message, lParam uintptr) (uintptr, bool) {
	switch message {
	case wmLButtonDown:
		pReleaseCapture.Call()
		pSendMessageW.Call(hwnd, wmNCLButtonDn, htCaption, 0)
	case wmSetCursor:
		if lParam&0xffff != htClient {
			return 0, false
		}
		setCursor(idcSizeAll)
		return 1, true
	case wmClose:
		pDestroyWindow.Call(o.main.hwnd)
	default:
		return 0, false
	}
	return 0, true
}

// resetPositions moves the character and item back to where they start,
// relative to the tracker, at their (just reset) sizes.
func (o *overlay) resetPositions() {
	var m rect
	if ok, _, _ := pGetWindowRect.Call(o.main.hwnd, uintptr(unsafe.Pointer(&m))); ok == 0 {
		return
	}
	co, io := o.t.CharacterOffset(), o.t.ItemOffset()
	o.itemOff = [2]float64{float64(io.X - co.X), float64(io.Y - co.Y)}
	// Already at the new sizes, so followResize leaves the offset alone.
	o.charH, o.itemPx = o.t.CharacterSize().Y, o.t.ItemSize().Y
	if o.char.hwnd != 0 {
		// The character's wmMove brings the item along.
		pSetWindowPos.Call(o.char.hwnd, 0, uintptr(m.left+int32(co.X)), uintptr(m.top+int32(co.Y)), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
	}
	o.placeItem()
	o.panelOffSet = false
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
	var m rect
	if ok, _, _ := pGetWindowRect.Call(o.main.hwnd, uintptr(unsafe.Pointer(&m))); ok == 0 {
		return
	}
	pSetWindowPos.Call(o.panel.hwnd, 0, uintptr(m.left+o.panelOff.x), uintptr(m.top+o.panelOff.y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

// measurePanelOffset records where the panel was dragged to, relative to the
// tracker, so it opens there again and follows the grip.
func (o *overlay) measurePanelOffset() {
	var m, p rect
	ok1, _, _ := pGetWindowRect.Call(o.main.hwnd, uintptr(unsafe.Pointer(&m)))
	ok2, _, _ := pGetWindowRect.Call(o.panel.hwnd, uintptr(unsafe.Pointer(&p)))
	if ok1 != 0 && ok2 != 0 {
		o.panelOff, o.panelOffSet = point{p.left - m.left, p.top - m.top}, true
	}
}

// placeItem puts the item at its offset from the character.
func (o *overlay) placeItem() {
	if !o.itemPlaced {
		return
	}
	var c rect
	if ok, _, _ := pGetWindowRect.Call(o.char.hwnd, uintptr(unsafe.Pointer(&c))); ok == 0 {
		return
	}
	x := c.left + int32(math.Round(o.itemOff[0]))
	y := c.top + int32(math.Round(o.itemOff[1]))
	pSetWindowPos.Call(o.item.hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

// measureItemOffset records where the item now is relative to the character.
func (o *overlay) measureItemOffset() {
	if !o.itemPlaced {
		return
	}
	var c, i rect
	ok1, _, _ := pGetWindowRect.Call(o.char.hwnd, uintptr(unsafe.Pointer(&c)))
	ok2, _, _ := pGetWindowRect.Call(o.item.hwnd, uintptr(unsafe.Pointer(&i)))
	if ok1 != 0 && ok2 != 0 {
		o.itemOff = [2]float64{float64(i.left - c.left), float64(i.top - c.top)}
	}
}

// followMain moves the character by however far the tracker window just
// moved, so dragging the grip carries both. Dragging the character itself
// moves only the character.
func (o *overlay) followMain() {
	var r rect
	if ok, _, _ := pGetWindowRect.Call(o.main.hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	dx, dy := r.left-o.mainLast.x, r.top-o.mainLast.y
	o.mainLast = point{r.left, r.top}
	if o.panelShown {
		o.placePanel()
	}
	if (dx == 0 && dy == 0) || o.char.hwnd == 0 {
		return
	}
	var c rect
	if ok, _, _ := pGetWindowRect.Call(o.char.hwnd, uintptr(unsafe.Pointer(&c))); ok == 0 {
		return
	}
	pSetWindowPos.Call(o.char.hwnd, 0, uintptr(c.left+dx), uintptr(c.top+dy), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

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
	for _, h := range []uintptr{o.main.hwnd, o.char.hwnd, o.item.hwnd, o.panel.hwnd} {
		if h != 0 {
			pSetWindowPos.Call(h, after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
		}
	}
}

// recordPosition stores where a window is, for Save to write out.
func (o *overlay) recordPosition(hwnd uintptr, field **[2]int) {
	var r rect
	if hwnd == 0 {
		return
	}
	if ok, _, _ := pGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok == 0 {
		return
	}
	*field = &[2]int{int(r.left), int(r.top)}
}

// render redraws every window, after input or anything else that may have
// changed them.
func (o *overlay) render() {
	o.draw([4]bool{true, true, true, true})
}

// renderDue redraws just the windows whose animation has moved on, so a
// character gif doesn't keep redrawing the tracker and its settings panel.
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
	if which[0] {
		img, next := o.t.Render(now)
		o.main.push(img)
		o.due[0] = next
	}
	if which[1] && o.char.hwnd != 0 {
		img, next := o.t.RenderCharacter(now)
		o.char.push(img)
		o.due[1] = next
	}
	if which[2] && o.item.hwnd != 0 {
		o.followResize()
		img, next := o.t.RenderItem(now)
		o.item.push(img)
		o.due[2] = next
	}
	if o.panel.hwnd != 0 {
		o.syncPanel(which[3], now)
	}
	o.schedule()
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
		var i rect
		if o.itemPlaced {
			if ok, _, _ := pGetWindowRect.Call(o.item.hwnd, uintptr(unsafe.Pointer(&i))); ok != 0 {
				x := i.left - int32(math.Round(o.itemOff[0]))
				y := i.top - int32(math.Round(o.itemOff[1]))
				// The character's wmMove puts the item back at its offset.
				pSetWindowPos.Call(o.char.hwnd, 0, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
			}
		}
	case o.itemPx > 0 && ip != o.itemPx:
		d := float64(o.itemPx-ip) / 2
		o.itemOff[0] += d
		o.itemOff[1] += d
		o.placeItem()
	}
	o.charH, o.itemPx = ch, ip
}

// push copies img into the window's bitmap and shows it. The window keeps its
// top-left corner and takes img's size.
func (w *layered) push(img *image.RGBA) {
	sz := img.Rect.Size()
	if sz.X <= 0 || sz.Y <= 0 || !w.ensureBitmap(sz) {
		return
	}
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
	w.bitmap, w.memDC, w.bits = 0, 0, nil
}

// useDarkTitleBar asks Windows 10 20H1+ / 11 to draw a window's title bar dark.
func useDarkTitleBar(hwnd uintptr) {
	on := int32(1)
	pDwmSetWindowAttribute.Call(hwnd, 20, uintptr(unsafe.Pointer(&on)), 4)
}
