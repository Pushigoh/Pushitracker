package main

import (
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
)

// Anim is a decoded image. Still images have a single frame.
type Anim struct {
	Frames []*image.RGBA
	Delays []time.Duration
	Total  time.Duration
	Loops  int // number of extra plays; -1 means forever
}

// Frame returns the frame to show after elapsed time, and how long until the
// next frame change (0 when the image no longer changes).
func (a *Anim) Frame(elapsed time.Duration) (*image.RGBA, time.Duration) {
	i, d := a.frameIndex(elapsed)
	return a.Frames[i], d
}

func (a *Anim) frameIndex(elapsed time.Duration) (int, time.Duration) {
	last := len(a.Frames) - 1
	if last == 0 || a.Total <= 0 {
		return 0, 0
	}
	if a.Loops >= 0 && elapsed >= a.Total*time.Duration(a.Loops+1) {
		return last, 0
	}
	t := elapsed % a.Total
	for i, d := range a.Delays {
		if t < d {
			return i, d - t
		}
		t -= d
	}
	return last, 0
}

// Scaled returns a copy stretched to w×h, ignoring the aspect ratio.
func (a *Anim) Scaled(w, h int) *Anim { return a.ScaledWith(xdraw.CatmullRom, w, h) }

func (a *Anim) ScaledWith(k xdraw.Interpolator, w, h int) *Anim {
	if b := a.Frames[0].Bounds(); b.Dx() == w && b.Dy() == h {
		return a
	}
	out := *a
	out.Frames = make([]*image.RGBA, len(a.Frames))
	for i, f := range a.Frames {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		k.Scale(dst, dst.Bounds(), f, f.Bounds(), xdraw.Src, nil)
		out.Frames[i] = dst
	}
	return &out
}

func LoadAnim(path string) (*Anim, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if g, err := gif.DecodeAll(f); err == nil && len(g.Image) > 0 {
		return gifAnim(g), nil
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	rgba := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	return &Anim{Frames: []*image.RGBA{rgba}, Delays: []time.Duration{0}}, nil
}

// gifAnim composites GIF frames onto a full canvas, honouring each frame's
// disposal method, the same way browsers play them.
func gifAnim(g *gif.GIF) *Anim {
	bounds := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	if bounds.Empty() {
		bounds = g.Image[0].Bounds()
	}
	canvas := image.NewRGBA(bounds)
	a := &Anim{}
	switch {
	case g.LoopCount == 0:
		a.Loops = -1
	case g.LoopCount < 0:
		a.Loops = 0
	default:
		a.Loops = g.LoopCount
	}
	for i, frame := range g.Image {
		var restore *image.RGBA
		disposal := byte(gif.DisposalNone)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			restore = image.NewRGBA(bounds)
			copy(restore.Pix, canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)

		snap := image.NewRGBA(bounds)
		copy(snap.Pix, canvas.Pix)
		a.Frames = append(a.Frames, snap)

		// Browsers bump very short delays up to 100ms.
		d := time.Duration(g.Delay[i]) * 10 * time.Millisecond
		if d <= 10*time.Millisecond {
			d = 100 * time.Millisecond
		}
		a.Delays = append(a.Delays, d)
		a.Total += d

		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = restore
		}
	}
	return a
}

// grayscale converts to grey using Rec. 709 luminance. It works directly on
// premultiplied pixels because luminance is a linear mix of the channels.
func grayscale(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Bounds())
	for i := 0; i < len(img.Pix); i += 4 {
		p := img.Pix[i : i+4 : i+4]
		l := uint8(0.2126*float64(p[0]) + 0.7152*float64(p[1]) + 0.0722*float64(p[2]) + 0.5)
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = l, l, l, p[3]
	}
	return out
}

// AnimCache loads each file once, and keeps one scaled copy of each file at
// the size it was last drawn at. Missing or broken files are cached as nil.
type AnimCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	sized   map[string]*SizedAnim
}

type cacheEntry struct {
	once sync.Once
	anim *Anim
}

func NewAnimCache() *AnimCache {
	return &AnimCache{entries: map[string]*cacheEntry{}, sized: map[string]*SizedAnim{}}
}

// Native returns the file at path at its own size, or nil if it can't be
// loaded.
func (c *AnimCache) Native(path string) *Anim {
	if path == "" {
		return nil
	}
	c.mu.Lock()
	e, ok := c.entries[path]
	if !ok {
		e = &cacheEntry{}
		c.entries[path] = e
	}
	c.mu.Unlock()
	e.once.Do(func() {
		if a, err := LoadAnim(path); err == nil {
			e.anim = a
		}
	})
	return e.anim
}

// Sized returns the file at path stretched to w×h, or nil if it can't be
// loaded. Only the latest size per file is kept, since sizes change
// continuously while a size slider is dragged.
func (c *AnimCache) Sized(path string, w, h int) *SizedAnim {
	native := c.Native(path)
	if native == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sized[path]
	if s == nil || s.w != w || s.h != h {
		s = &SizedAnim{native: native, w: w, h: h, frames: make([]*image.RGBA, len(native.Frames))}
		if b := native.Frames[0].Bounds(); b.Dx() == w && b.Dy() == h {
			copy(s.frames, native.Frames)
		}
		c.sized[path] = s
	}
	return s
}

// SizedAnim is an Anim stretched to one size. Frames are scaled the first
// time they are shown, so a new size costs one frame rather than the whole
// animation, which keeps size sliders responsive.
type SizedAnim struct {
	mu     sync.Mutex
	native *Anim
	w, h   int
	frames []*image.RGBA
}

func (s *SizedAnim) Frame(elapsed time.Duration) (*image.RGBA, time.Duration) {
	i, d := s.native.frameIndex(elapsed)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.frames[i] == nil {
		src := s.native.Frames[i]
		dst := image.NewRGBA(image.Rect(0, 0, s.w, s.h))
		xdraw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Src, nil)
		s.frames[i] = dst
	}
	return s.frames[i], d
}

// Preload decodes files in the background so the first click doesn't stall.
func (c *AnimCache) Preload(paths []string) {
	go func() {
		for _, p := range paths {
			if fileExists(p) {
				c.Native(p)
			}
		}
	}()
}
