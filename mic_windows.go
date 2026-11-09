package main

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Microphone capture through WASAPI (the Windows audio API), called through
// its COM interfaces directly so no C compiler or extra package is needed.

// Mic is an audio input device: ID identifies it to Windows and stays the
// same across reboots and renames; Name is what the user sees.
type Mic struct {
	ID   string
	Name string
}

var (
	ole32              = windows.NewLazySystemDLL("ole32.dll")
	pCoInitializeEx    = ole32.NewProc("CoInitializeEx")
	pCoUninitialize    = ole32.NewProc("CoUninitialize")
	pCoCreateInstance  = ole32.NewProc("CoCreateInstance")
	pPropVariantClear  = ole32.NewProc("PropVariantClear")
	clsidMMDeviceEnum  = mustGUID("{BCDE0395-E52F-467C-8E3D-C4579291692E}")
	iidMMDeviceEnum    = mustGUID("{A95664D2-9614-4F35-A746-DE8DB63617E6}")
	iidAudioClient     = mustGUID("{1CB9AD4C-DBFA-4C32-B178-C2F568A703B2}")
	iidAudioCapture    = mustGUID("{C8ADBD64-E71E-48A0-A4DE-185C395CD317}")
	subtypeFloat       = mustGUID("{00000003-0000-0010-8000-00AA00389B71}")
	subtypePCM         = mustGUID("{00000001-0000-0010-8000-00AA00389B71}")
	pkeyDeviceFriendly = propertyKey{mustGUID("{A45C254E-DF1C-4EFD-8020-67D146A850E0}"), 14}
	errMicUnsupported  = errors.New("unsupported audio format")
	errMicNotAvailable = errors.New("microphone not available")
)

const (
	coinitMultithreaded = 0
	clsctxAll           = 0x17
	eCapture            = 1
	deviceStateActive   = 1
	stgmRead            = 0
	vtLPWSTR            = 31
	sharedMode          = 0
	bufferFlagSilent    = 0x2
	waveFormatPCM       = 1
	waveFormatFloat     = 3
	waveFormatExt       = 0xFFFE
)

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

// waveFormat is WAVEFORMATEXTENSIBLE; the fields after cbSize are only
// there when the tag is waveFormatExt.
type waveFormat struct {
	tag            uint16
	channels       uint16
	samplesPerSec  uint32
	avgBytesPerSec uint32
	blockAlign     uint16
	bitsPerSample  uint16
	cbSize         uint16
	validBits      uint16
	channelMask    uint32
	subFormat      windows.GUID
}

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

// comCall calls method idx of the COM object obj's interface.
func comCall(obj unsafe.Pointer, idx int, args ...uintptr) uint32 {
	vtbl := *(*unsafe.Pointer)(obj)
	fn := *(*uintptr)(unsafe.Add(vtbl, idx*int(unsafe.Sizeof(uintptr(0)))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	return uint32(r)
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		comCall(obj, 2)
	}
}

func hrErr(what string, hr uint32) error {
	if int32(hr) < 0 {
		return fmt.Errorf("%s: 0x%08X", what, hr)
	}
	return nil
}

// comThread locks the goroutine to its thread and starts COM on it; the
// returned func undoes both.
func comThread() func() {
	runtime.LockOSThread()
	hr, _, _ := pCoInitializeEx.Call(0, coinitMultithreaded)
	return func() {
		if int32(hr) >= 0 {
			pCoUninitialize.Call()
		}
		runtime.UnlockOSThread()
	}
}

func deviceEnumerator() (unsafe.Pointer, error) {
	var e unsafe.Pointer
	hr, _, _ := pCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidMMDeviceEnum)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidMMDeviceEnum)), uintptr(unsafe.Pointer(&e)))
	if err := hrErr("audio devices", uint32(hr)); err != nil {
		return nil, err
	}
	return e, nil
}

// listMics returns the active audio input devices.
func listMics() ([]Mic, error) {
	defer comThread()()
	e, err := deviceEnumerator()
	if err != nil {
		return nil, err
	}
	defer release(e)
	var coll unsafe.Pointer
	if err := hrErr("list inputs", comCall(e, 3, eCapture, deviceStateActive, uintptr(unsafe.Pointer(&coll)))); err != nil {
		return nil, err
	}
	defer release(coll)
	var n uint32
	comCall(coll, 3, uintptr(unsafe.Pointer(&n)))
	var mics []Mic
	for i := range n {
		var dev unsafe.Pointer
		if comCall(coll, 4, uintptr(i), uintptr(unsafe.Pointer(&dev))) != 0 {
			continue
		}
		var m Mic
		var id *uint16
		if comCall(dev, 5, uintptr(unsafe.Pointer(&id))) == 0 {
			m.ID = windows.UTF16PtrToString(id)
			windows.CoTaskMemFree(unsafe.Pointer(id))
		}
		m.Name = deviceName(dev)
		if m.Name == "" {
			m.Name = m.ID
		}
		release(dev)
		if m.ID != "" {
			mics = append(mics, m)
		}
	}
	return mics, nil
}

// deviceName reads a device's friendly name, such as "Microphone (USB
// Audio)".
func deviceName(dev unsafe.Pointer) string {
	var store unsafe.Pointer
	if comCall(dev, 4, stgmRead, uintptr(unsafe.Pointer(&store))) != 0 {
		return ""
	}
	defer release(store)
	var pv [24]byte // PROPVARIANT
	if comCall(store, 5, uintptr(unsafe.Pointer(&pkeyDeviceFriendly)), uintptr(unsafe.Pointer(&pv))) != 0 {
		return ""
	}
	defer pPropVariantClear.Call(uintptr(unsafe.Pointer(&pv)))
	if *(*uint16)(unsafe.Pointer(&pv[0])) != vtLPWSTR {
		return ""
	}
	return windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&pv[8])))
}

// startMic captures from the device with the given ID and calls onLevel with
// the level in dBFS of each 20 ms of sound, on its own thread, until stop is
// called. If the device goes away, onLevel gets micSilence and capture ends.
func startMic(id string, onLevel func(dB float64)) (stop func(), err error) {
	quit := make(chan struct{})
	done := make(chan struct{})
	started := make(chan error, 1)
	go func() {
		defer close(done)
		defer comThread()()
		c, err := openCapture(id)
		started <- err
		if err != nil {
			return
		}
		defer c.close()
		c.run(quit, onLevel)
		onLevel(micSilence)
	}()
	if err := <-started; err != nil {
		<-done
		return nil, err
	}
	return func() { close(quit); <-done }, nil
}

type capture struct {
	client, capture unsafe.Pointer
	channels        int
	rate            int
	float           bool
	bits            int
}

func openCapture(id string) (*capture, error) {
	e, err := deviceEnumerator()
	if err != nil {
		return nil, err
	}
	defer release(e)
	wid, err := windows.UTF16PtrFromString(id)
	if err != nil {
		return nil, err
	}
	var dev unsafe.Pointer
	if comCall(e, 5, uintptr(unsafe.Pointer(wid)), uintptr(unsafe.Pointer(&dev))) != 0 {
		return nil, errMicNotAvailable
	}
	defer release(dev)
	c := &capture{}
	if err := hrErr("open input", comCall(dev, 3, uintptr(unsafe.Pointer(&iidAudioClient)), clsctxAll, 0,
		uintptr(unsafe.Pointer(&c.client)))); err != nil {
		return nil, err
	}
	var wf *waveFormat
	if err := hrErr("input format", comCall(c.client, 8, uintptr(unsafe.Pointer(&wf)))); err != nil {
		c.close()
		return nil, err
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(wf))
	c.channels, c.rate, c.bits = int(wf.channels), int(wf.samplesPerSec), int(wf.bitsPerSample)
	switch {
	case wf.tag == waveFormatFloat, wf.tag == waveFormatExt && wf.subFormat == subtypeFloat:
		c.float = true
	case wf.tag == waveFormatPCM, wf.tag == waveFormatExt && wf.subFormat == subtypePCM:
	default:
		c.close()
		return nil, errMicUnsupported
	}
	if c.channels < 1 || c.rate < 1 || (c.float && c.bits != 32) || (!c.float && c.bits != 16 && c.bits != 32) {
		c.close()
		return nil, errMicUnsupported
	}
	const bufferTime = 200 * time.Millisecond / 100 // in 100 ns units
	if err := hrErr("start input", comCall(c.client, 3, sharedMode, 0, uintptr(bufferTime), 0,
		uintptr(unsafe.Pointer(wf)), 0)); err != nil {
		c.close()
		return nil, err
	}
	if err := hrErr("start input", comCall(c.client, 14, uintptr(unsafe.Pointer(&iidAudioCapture)),
		uintptr(unsafe.Pointer(&c.capture)))); err != nil {
		c.close()
		return nil, err
	}
	if err := hrErr("start input", comCall(c.client, 10)); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *capture) close() {
	if c.client != nil {
		comCall(c.client, 11) // Stop
	}
	release(c.capture)
	release(c.client)
}

// run reads captured sound until quit is closed or the device fails.
func (c *capture) run(quit <-chan struct{}, onLevel func(float64)) {
	var m levelMeter
	m.block = max(c.rate/50, 1) // 20 ms
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-quit:
			return
		case <-tick.C:
		}
		for {
			var size uint32
			if comCall(c.capture, 5, uintptr(unsafe.Pointer(&size))) != 0 {
				return // device unplugged or disabled
			}
			if size == 0 {
				break
			}
			var data unsafe.Pointer
			var frames, flags uint32
			if comCall(c.capture, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)),
				uintptr(unsafe.Pointer(&flags)), 0, 0) != 0 {
				return
			}
			n := int(frames) * c.channels
			switch {
			case flags&bufferFlagSilent != 0 || data == nil:
				m.addSilence(int(frames), onLevel)
			case c.float:
				m.addFloat(unsafe.Slice((*float32)(data), n), c.channels, onLevel)
			case c.bits == 16:
				m.addInt16(unsafe.Slice((*int16)(data), n), c.channels, onLevel)
			default:
				m.addInt32(unsafe.Slice((*int32)(data), n), c.channels, onLevel)
			}
			comCall(c.capture, 4, uintptr(frames))
		}
	}
}

// micSilence is the level reported for silence, in dBFS.
const micSilence = -100

// levelMeter turns samples into a level per block of frames: the RMS of all
// channels, in dB below full scale.
type levelMeter struct {
	block  int // frames per level
	frames int
	sumSq  float64
}

func (m *levelMeter) frame(sq float64, onLevel func(float64)) {
	m.sumSq += sq
	m.frames++
	if m.frames >= m.block {
		onLevel(dBFS(m.sumSq / float64(m.frames)))
		m.frames, m.sumSq = 0, 0
	}
}

func (m *levelMeter) addSilence(frames int, onLevel func(float64)) {
	for range frames {
		m.frame(0, onLevel)
	}
}

func (m *levelMeter) addFloat(s []float32, ch int, onLevel func(float64)) {
	for i := 0; i+ch <= len(s); i += ch {
		var sq float64
		for _, v := range s[i : i+ch] {
			sq += float64(v) * float64(v)
		}
		m.frame(sq/float64(ch), onLevel)
	}
}

func (m *levelMeter) addInt16(s []int16, ch int, onLevel func(float64)) {
	for i := 0; i+ch <= len(s); i += ch {
		var sq float64
		for _, v := range s[i : i+ch] {
			f := float64(v) / 32768
			sq += f * f
		}
		m.frame(sq/float64(ch), onLevel)
	}
}

func (m *levelMeter) addInt32(s []int32, ch int, onLevel func(float64)) {
	for i := 0; i+ch <= len(s); i += ch {
		var sq float64
		for _, v := range s[i : i+ch] {
			f := float64(v) / 2147483648
			sq += f * f
		}
		m.frame(sq/float64(ch), onLevel)
	}
}

// dBFS converts a mean square to decibels below full scale.
func dBFS(meanSq float64) float64 {
	if meanSq <= 0 {
		return micSilence
	}
	return max(10*math.Log10(meanSq), micSilence)
}
