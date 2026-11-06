package main

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Drawing helpers for the tracker overlay. Everything works on premultiplied
// *image.RGBA so the result can go straight to a per-pixel-alpha window.

func premul(c color.NRGBA) color.RGBA {
	r, g, b, a := c.RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

func withAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

// blend paints c over the pixel at (x, y) with the given coverage (0..1).
func blend(dst *image.RGBA, x, y int, c color.RGBA, cov float64) {
	if cov <= 0 || !(image.Point{x, y}.In(dst.Rect)) {
		return
	}
	if cov > 1 {
		cov = 1
	}
	i := dst.PixOffset(x, y)
	p := dst.Pix[i : i+4 : i+4]
	sa := float64(c.A) * cov
	k := 1 - sa/255
	p[0] = uint8(float64(c.R)*cov + float64(p[0])*k + 0.5)
	p[1] = uint8(float64(c.G)*cov + float64(p[1])*k + 0.5)
	p[2] = uint8(float64(c.B)*cov + float64(p[2])*k + 0.5)
	p[3] = uint8(sa + float64(p[3])*k + 0.5)
}

// fillShape paints c wherever inside(x, y) holds, anti-aliased with 4×4
// supersampling. Coordinates passed to inside are in dst pixels.
func fillShape(dst *image.RGBA, bounds image.Rectangle, c color.NRGBA, inside func(x, y float64) bool) {
	pc := premul(c)
	bounds = bounds.Intersect(dst.Rect)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			n := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					if inside(float64(x)+(float64(sx)+0.5)/4, float64(y)+(float64(sy)+0.5)/4) {
						n++
					}
				}
			}
			blend(dst, x, y, pc, float64(n)/16)
		}
	}
}

// fillRoundRect fills r with rounded corners. Only the corner areas need
// anti-aliasing; the rest is painted solid, which keeps big panels cheap.
func fillRoundRect(dst *image.RGBA, r image.Rectangle, radius float64, c color.NRGBA) {
	x0, y0, x1, y1 := float64(r.Min.X), float64(r.Min.Y), float64(r.Max.X), float64(r.Max.Y)
	inside := func(x, y float64) bool {
		cx := math.Max(x0+radius, math.Min(x, x1-radius))
		cy := math.Max(y0+radius, math.Min(y, y1-radius))
		return math.Hypot(x-cx, y-cy) <= radius
	}
	k := int(math.Ceil(radius))
	if r.Dx() <= 2*k || r.Dy() <= 2*k {
		fillShape(dst, r, c, inside)
		return
	}
	// Corners, then the cross between them.
	for _, cr := range []image.Rectangle{
		image.Rect(r.Min.X, r.Min.Y, r.Min.X+k, r.Min.Y+k),
		image.Rect(r.Max.X-k, r.Min.Y, r.Max.X, r.Min.Y+k),
		image.Rect(r.Min.X, r.Max.Y-k, r.Min.X+k, r.Max.Y),
		image.Rect(r.Max.X-k, r.Max.Y-k, r.Max.X, r.Max.Y),
	} {
		fillShape(dst, cr, c, inside)
	}
	fillSolid(dst, image.Rect(r.Min.X+k, r.Min.Y, r.Max.X-k, r.Max.Y), c)
	fillSolid(dst, image.Rect(r.Min.X, r.Min.Y+k, r.Min.X+k, r.Max.Y-k), c)
	fillSolid(dst, image.Rect(r.Max.X-k, r.Min.Y+k, r.Max.X, r.Max.Y-k), c)
}

func fillSolid(dst *image.RGBA, r image.Rectangle, c color.NRGBA) {
	pc := premul(c)
	r = r.Intersect(dst.Rect)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			blend(dst, x, y, pc, 1)
		}
	}
}

// strokeRoundRect outlines r. Only the band along the edges is sampled.
func strokeRoundRect(dst *image.RGBA, r image.Rectangle, radius, width float64, c color.NRGBA) {
	x0, y0, x1, y1 := float64(r.Min.X), float64(r.Min.Y), float64(r.Max.X), float64(r.Max.Y)
	in := func(x, y, inset float64) bool {
		rr := math.Max(radius-inset, 0)
		cx := math.Max(x0+inset+rr, math.Min(x, x1-inset-rr))
		cy := math.Max(y0+inset+rr, math.Min(y, y1-inset-rr))
		return x >= x0+inset && x <= x1-inset && y >= y0+inset && y <= y1-inset && math.Hypot(x-cx, y-cy) <= rr
	}
	stroke := func(x, y float64) bool { return in(x, y, 0) && !in(x, y, width) }
	k := int(math.Ceil(math.Max(radius, width))) + 1
	if r.Dx() <= 2*k || r.Dy() <= 2*k {
		fillShape(dst, r, c, stroke)
		return
	}
	fillShape(dst, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+k), c, stroke)
	fillShape(dst, image.Rect(r.Min.X, r.Max.Y-k, r.Max.X, r.Max.Y), c, stroke)
	fillShape(dst, image.Rect(r.Min.X, r.Min.Y+k, r.Min.X+k, r.Max.Y-k), c, stroke)
	fillShape(dst, image.Rect(r.Max.X-k, r.Min.Y+k, r.Max.X, r.Max.Y-k), c, stroke)
}

// drawGear draws a cog centred in r.
func drawGear(dst *image.RGBA, r image.Rectangle, c color.NRGBA) {
	cx, cy := float64(r.Min.X+r.Max.X)/2, float64(r.Min.Y+r.Max.Y)/2
	R := float64(min(r.Dx(), r.Dy())) / 2
	fillShape(dst, r, c, func(x, y float64) bool {
		dx, dy := x-cx, y-cy
		d := math.Hypot(dx, dy)
		outer := 0.72 * R
		if math.Cos(8*math.Atan2(dy, dx)) > 0.1 {
			outer = R
		}
		return d <= outer && d >= 0.3*R
	})
}

// drawGrip draws a 2×3 grid of dots, the usual "drag me" handle.
func drawGrip(dst *image.RGBA, r image.Rectangle, c color.NRGBA) {
	w, h := float64(r.Dx()), float64(r.Dy())
	dot := math.Min(w, h) * 0.11
	var centres [][2]float64
	for _, fx := range []float64{0.36, 0.64} {
		for _, fy := range []float64{0.25, 0.5, 0.75} {
			centres = append(centres, [2]float64{float64(r.Min.X) + fx*w, float64(r.Min.Y) + fy*h})
		}
	}
	fillShape(dst, r, c, func(x, y float64) bool {
		for _, p := range centres {
			if math.Hypot(x-p[0], y-p[1]) <= dot {
				return true
			}
		}
		return false
	})
}

// drawCheck draws a tick mark inside r.
func drawCheck(dst *image.RGBA, r image.Rectangle, c color.NRGBA) {
	w, h := float64(r.Dx()), float64(r.Dy())
	ox, oy := float64(r.Min.X), float64(r.Min.Y)
	a := [2]float64{ox + 0.22*w, oy + 0.52*h}
	b := [2]float64{ox + 0.42*w, oy + 0.72*h}
	e := [2]float64{ox + 0.78*w, oy + 0.3*h}
	thick := 0.09 * w
	fillShape(dst, r, c, func(x, y float64) bool {
		return segDist(x, y, a, b) <= thick || segDist(x, y, b, e) <= thick
	})
}

func segDist(x, y float64, a, b [2]float64) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	t := ((x-a[0])*dx + (y-a[1])*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(x-(a[0]+t*dx), y-(a[1]+t*dy))
}

// drawImage paints src over dst at pt, with an overall opacity (0..1).
func drawImage(dst *image.RGBA, pt image.Point, src image.Image, opacity float64) {
	r := image.Rectangle{Min: pt, Max: pt.Add(src.Bounds().Size())}
	if opacity >= 1 {
		draw.Draw(dst, r, src, src.Bounds().Min, draw.Over)
		return
	}
	if opacity <= 0 {
		return
	}
	mask := image.NewUniform(color.Alpha{A: uint8(opacity*255 + 0.5)})
	draw.DrawMask(dst, r, src, src.Bounds().Min, mask, image.Point{}, draw.Over)
}

func newFace(px float64) font.Face {
	f, err := opentype.Parse(goregular.TTF)
	if err != nil {
		panic(err)
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: px, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	return face
}

// drawText draws s with its left edge at x, vertically centred on midY.
func drawText(dst *image.RGBA, face font.Face, s string, x, midY int, c color.NRGBA) {
	m := face.Metrics()
	baseline := midY + (m.Ascent.Ceil()-m.Descent.Ceil())/2
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(s)
}

// drawTextCentered draws s centred in r.
func drawTextCentered(dst *image.RGBA, face font.Face, s string, r image.Rectangle, c color.NRGBA) {
	w := font.MeasureString(face, s).Ceil()
	drawText(dst, face, s, r.Min.X+(r.Dx()-w)/2, (r.Min.Y+r.Max.Y)/2, c)
}
