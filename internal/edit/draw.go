// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package edit

import (
	"image"
	"image/draw"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
)

const piece = 64

// Draw makes the edited picture of img, which is upright, in sRGB and whole.
// It is img itself, or shares its bytes, if the edit is none.
func Draw(img image.Image, e Edit) *image.NRGBA {
	src := pixels(img)
	if e.None() || src.Rect.Empty() {
		return src
	}
	f := newFrame(e.crop, src.Rect.Dx(), src.Rect.Dy())
	var dev *developer
	if e.adjust != [toolCount]int{} {
		dev = newDeveloper(src, e.adjust, f)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, f.w, f.h))
	rows(f.h, func(from, to int) {
		if e.crop != (crop{}) {
			// In pieces: a turned row is a column of the photo, and px of one
			// line of the cache are to be read together.
			for x := 0; x < f.w; x += piece {
				for y := from; y < to; y++ {
					f.sample(dst.Pix[y*dst.Stride+4*x:y*dst.Stride+4*min(x+piece, f.w)], src, x, y)
				}
			}
		}
		for y := from; y < to; y++ {
			row := dst.Pix[y*dst.Stride:][:4*f.w]
			if e.crop == (crop{}) {
				copy(row, src.Pix[y*src.Stride:])
			}
			if dev != nil {
				dev.develop(row, y)
			}
		}
	})
	return dst
}

// pixels gives img as not premultiplied, without a copy where the bytes are
// already that.
func pixels(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok {
		return n
	}
	b := img.Bounds()
	if o, ok := img.(interface{ Opaque() bool }); ok && o.Opaque() {
		rgba, ok := img.(*image.RGBA)
		if !ok {
			// Into RGBA, for which image/draw is fast.
			rgba = image.NewRGBA(b)
			draw.Draw(rgba, b, img, b.Min, draw.Src)
		}
		return &image.NRGBA{Pix: rgba.Pix, Stride: rgba.Stride, Rect: rgba.Rect}
	}
	n := image.NewNRGBA(b)
	draw.Draw(n, b, img, b.Min, draw.Src)
	return n
}

// rows runs do for all the rows, on all the cores, a few rows at a time.
func rows(h int, do func(from, to int)) {
	const some = 16
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), (h+some-1)/some) {
		wg.Go(func() {
			for {
				end := int(next.Add(some))
				if end-some >= h {
					return
				}
				do(end-some, min(end, h))
			}
		})
	}
	wg.Wait()
}

// frame is a crop of a photo of a known size. A point (X, Y) of the photo is
// at m0·X + m1·Y + m2, m3·X + m4·Y + m5 of what is kept.
type frame struct {
	w, h                   int
	m0, m1, m2, m3, m4, m5 float64
}

func newFrame(c crop, w, h int) frame {
	if c == (crop{}) {
		return frame{w: w, h: h, m0: 1, m4: 1}
	}
	fullW, fullH := w, h
	if c.turns%2 == 1 {
		fullW, fullH = h, w
	}
	// In 32 bits as on the phone: in 64, 5 × 0.7 is under 3.5 and the size
	// comes out another.
	fw, fh := float64(float32(fullW)*c.w), float64(float32(fullH)*c.h)
	vx, vy := float64(c.x)*float64(fullW), float64(c.y)*float64(fullH)
	for range c.turns {
		vx, vy = vy, -vx
	}
	flip := 1.0
	if c.flip {
		flip, vx = -1, -vx
	}
	// A frame this far is all past the edge and cannot be told from one
	// farther, whose numbers would not fit.
	far := float64(w + h)
	ax := min(max(float64(w)/2+vx, -far), 2*far)
	ay := min(max(float64(h)/2+vy, -far), 2*far)
	sin, cos := math.Sincos(math.Pi / 360 * float64(180*c.turns+c.angle))
	f := frame{
		w: int(max(math.Floor(fw+0.5), 1)), h: int(max(math.Floor(fh+0.5), 1)),
		m0: cos * flip, m1: -sin, m3: sin * flip, m4: cos,
	}
	f.m2 = fw/2 - (f.m0*ax + f.m1*ay)
	f.m5 = fh/2 - (f.m3*ax + f.m4*ay)
	return f
}

// back is the photo's point at a point of what is kept.
func (f frame) back(x, y float64) (float64, float64) {
	x, y = x-f.m2, y-f.m5
	return f.m0*x + f.m3*y, f.m1*x + f.m4*y
}

// sample fills row y of what is kept from x on, bilinearly, the edge going on outside.
func (f frame) sample(row []byte, src *image.NRGBA, x, y int) {
	// Positions are in 1/2³² of a px, and read to 1/256.
	const one = 1 << 32
	lastX, lastY := src.Rect.Dx()-1, src.Rect.Dy()-1
	x0, y0 := f.back(float64(x)+0.5, float64(y)+0.5)
	px, py := int64((x0-0.5)*one)+one/512, int64((y0-0.5)*one)+one/512
	dx, dy := int64(math.Round(f.m0*one)), int64(math.Round(f.m1*one))
	for i := 0; i+4 <= len(row); i, px, py = i+4, px+dx, py+dy {
		ix, iy := int(px>>32), int(py>>32)
		u, v := uint32(px>>24)&255, uint32(py>>24)&255
		left, right := min(max(ix, 0), lastX), min(max(ix+1, 0), lastX)
		top := src.Pix[min(max(iy, 0), lastY)*src.Stride:]
		to := row[i : i+4 : i+4]
		p := top[4*left:][:4:4]
		if u|v == 0 {
			copy(to, p)
			continue
		}
		bottom := src.Pix[min(max(iy+1, 0), lastY)*src.Stride:]
		q, r, s := top[4*right:][:4:4], bottom[4*left:][:4:4], bottom[4*right:][:4:4]
		wp, wq, wr, ws := (256-u)*(256-v), u*(256-v), (256-u)*v, u*v
		to[0] = byte((uint32(p[0])*wp + uint32(q[0])*wq + uint32(r[0])*wr + uint32(s[0])*ws + 1<<15) >> 16)
		to[1] = byte((uint32(p[1])*wp + uint32(q[1])*wq + uint32(r[1])*wr + uint32(s[1])*ws + 1<<15) >> 16)
		to[2] = byte((uint32(p[2])*wp + uint32(q[2])*wq + uint32(r[2])*wr + uint32(s[2])*ws + 1<<15) >> 16)
		to[3] = byte((uint32(p[3])*wp + uint32(q[3])*wq + uint32(r[3])*wr + uint32(s[3])*ws + 1<<15) >> 16)
	}
}
