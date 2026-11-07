package main

import (
	"image"
	"image/draw"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"os"
	"time"

	xdraw "golang.org/x/image/draw"
)

// animSource is a loaded file. A gif is kept in its compact paletted form and
// turned into full-colour frames only as they are shown, since decoded frames
// take many times the memory the gif does.
type animSource struct {
	gif    *gif.GIF    // nil for a still image
	still  *image.RGBA // set for a still image
	bounds image.Rectangle
	delays []time.Duration
	total  time.Duration
	loops  int // number of extra plays; -1 means forever
}

func loadSource(path string) *animSource {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	if g, err := gif.DecodeAll(f); err == nil && len(g.Image) > 0 {
		return gifSource(g)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	s := toRGBA(img)
	return &animSource{still: s, bounds: s.Rect, delays: []time.Duration{0}}
}

func gifSource(g *gif.GIF) *animSource {
	s := &animSource{gif: g, bounds: image.Rect(0, 0, g.Config.Width, g.Config.Height)}
	if s.bounds.Empty() {
		s.bounds = g.Image[0].Bounds()
	}
	switch {
	case g.LoopCount == 0:
		s.loops = -1
	case g.LoopCount < 0:
		s.loops = 0
	default:
		s.loops = g.LoopCount
	}
	for i := range g.Image {
		// Browsers bump very short delays up to 100ms.
		d := time.Duration(g.Delay[i]) * 10 * time.Millisecond
		if d <= 10*time.Millisecond {
			d = 100 * time.Millisecond
		}
		s.delays = append(s.delays, d)
		s.total += d
	}
	return s
}

// frameIndex returns the frame to show after elapsed time, and how long until
// the next frame change (0 when the image no longer changes).
func (s *animSource) frameIndex(elapsed time.Duration) (int, time.Duration) {
	last := len(s.delays) - 1
	if last == 0 || s.total <= 0 {
		return 0, 0
	}
	if s.loops >= 0 && elapsed >= s.total*time.Duration(s.loops+1) {
		return last, 0
	}
	t := elapsed % s.total
	for i, d := range s.delays {
		if t < d {
			return i, d - t
		}
		t -= d
	}
	return last, 0
}

// SizedAnim is a file stretched to one size. Gif frames are composited and
// scaled the first time they are shown, so showing a gif or changing its size
// costs a frame at a time rather than the whole animation up front.
type SizedAnim struct {
	path   string
	src    *animSource
	w, h   int
	frames []*image.RGBA

	// Gif playback state: canvas holds frames up to next composited at the
	// gif's own size, the way browsers play them. It is dropped once every
	// frame has been built.
	canvas *image.RGBA
	next   int

	// stream is set when keeping every frame would take too much memory (a
	// big, long gif): only the frame on show is kept, and each loop is built
	// again from the start.
	stream bool
}

// maxFrameBytes is the most memory one file's scaled frames may take before
// it is played without keeping them.
const maxFrameBytes = 64 << 20

func newSized(path string, src *animSource, w, h int) *SizedAnim {
	s := &SizedAnim{path: path, src: src, w: w, h: h}
	if src != nil {
		s.frames = make([]*image.RGBA, len(src.delays))
		s.stream = src.gif != nil && w*h*4*len(s.frames) > maxFrameBytes
	}
	return s
}

// Frame returns the frame to show after elapsed time, and how long until the
// next frame change (0 when the image no longer changes).
func (s *SizedAnim) Frame(elapsed time.Duration) (*image.RGBA, time.Duration) {
	i, d := s.src.frameIndex(elapsed)
	if s.frames[i] == nil {
		if s.src.gif == nil {
			s.frames[i] = scaleTo(s.src.still, s.w, s.h, xdraw.CatmullRom)
		} else {
			if s.stream && i < s.next {
				// Looped round: build again from the first frame.
				clear(s.frames)
				s.canvas, s.next = nil, 0
			}
			for s.next <= i {
				// Streaming, only the frame wanted is scaled and kept.
				s.composite(!s.stream || s.next == i)
			}
		}
	}
	return s.frames[i], d
}

// composite draws the next gif frame onto the canvas, honouring each frame's
// disposal method, and keeps a scaled copy of the result if keep is set.
func (s *SizedAnim) composite(keep bool) {
	g := s.src.gif
	if s.canvas == nil {
		s.canvas = image.NewRGBA(s.src.bounds)
	}
	i, frame := s.next, g.Image[s.next]
	disposal := byte(gif.DisposalNone)
	if i < len(g.Disposal) {
		disposal = g.Disposal[i]
	}
	var restore *image.RGBA
	if disposal == gif.DisposalPrevious {
		restore = image.NewRGBA(s.canvas.Rect)
		copy(restore.Pix, s.canvas.Pix)
	}
	draw.Draw(s.canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
	if keep {
		if s.stream {
			clear(s.frames) // just the one on show
		}
		s.frames[i] = scaleTo(s.canvas, s.w, s.h, xdraw.BiLinear)
	}

	switch disposal {
	case gif.DisposalBackground:
		draw.Draw(s.canvas, frame.Bounds(), image.Transparent, image.Point{}, draw.Src)
	case gif.DisposalPrevious:
		s.canvas = restore
	}
	s.next++
	if s.next == len(g.Image) && !s.stream {
		s.canvas = nil
	}
}

// scaleTo returns a copy of img stretched to w×h, ignoring the aspect ratio.
func scaleTo(img *image.RGBA, w, h int, k xdraw.Interpolator) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	if img.Rect.Dx() == w && img.Rect.Dy() == h {
		copy(dst.Pix, img.Pix)
		return dst
	}
	k.Scale(dst, dst.Bounds(), img, img.Rect, xdraw.Src, nil)
	return dst
}

func toRGBA(img image.Image) *image.RGBA {
	rgba := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
	draw.Draw(rgba, rgba.Bounds(), img, img.Bounds().Min, draw.Src)
	return rgba
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

// keepShown is how many recently shown files AnimCache keeps: enough for the
// character and item, each with its idle image and the one clicked last.
const keepShown = 4

// AnimCache keeps the icon images, and the few files shown most recently.
// Anything else is loaded again from disk when it is next shown. Missing or
// broken files are cached as nil, so they aren't retried every frame.
type AnimCache struct {
	icons map[string]*image.RGBA
	shown []*SizedAnim // most recently used first
}

func NewAnimCache() *AnimCache {
	return &AnimCache{icons: map[string]*image.RGBA{}}
}

// Icon returns the first frame of the image at path at its own size, or nil
// if it can't be loaded.
func (c *AnimCache) Icon(path string) *image.RGBA {
	img, ok := c.icons[path]
	if !ok {
		img = loadStill(path)
		c.icons[path] = img
	}
	return img
}

// loadStill returns the first frame of the image at path, or nil if it can't
// be loaded.
func loadStill(path string) *image.RGBA {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	d, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	return toRGBA(d)
}

// Sized returns the file at path stretched to w×h, or nil if it can't be
// loaded.
func (c *AnimCache) Sized(path string, w, h int) *SizedAnim {
	return c.sized(path, func(image.Rectangle) (int, int) { return w, h })
}

// SizedHeight returns the file at path scaled to h pixels high, keeping its
// own shape, or nil if it can't be loaded.
func (c *AnimCache) SizedHeight(path string, h int) *SizedAnim {
	return c.sized(path, func(b image.Rectangle) (int, int) {
		if b.Dy() <= 0 {
			return h, h
		}
		return max(int(math.Round(float64(h)*float64(b.Dx())/float64(b.Dy()))), 1), h
	})
}

// sized returns the file at path at the size given for its own bounds.
func (c *AnimCache) sized(path string, size func(image.Rectangle) (int, int)) *SizedAnim {
	if path == "" {
		return nil
	}
	var s *SizedAnim
	at := -1
	for i, e := range c.shown {
		if e.path == path {
			s, at = e, i
			break
		}
	}
	var src *animSource
	if s != nil {
		src = s.src
	} else {
		src = loadSource(path)
	}
	var w, h int
	if src != nil {
		w, h = size(src.bounds)
	}
	switch {
	case s == nil:
		s = newSized(path, src, w, h)
	case s.w != w || s.h != h:
		// Only the latest size is kept, since sizes change continuously
		// while a size slider is dragged.
		s = newSized(path, s.src, w, h)
	}
	// Move it to the front, dropping the least recently shown if full.
	if at < 0 {
		c.shown = append(c.shown, nil)
		at = len(c.shown) - 1
	}
	copy(c.shown[1:at+1], c.shown[:at])
	c.shown[0] = s
	if len(c.shown) > keepShown {
		c.shown[keepShown] = nil
		c.shown = c.shown[:keepShown]
	}
	if s.src == nil {
		return nil
	}
	return s
}
