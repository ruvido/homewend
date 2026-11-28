// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"image"
	"math"
	"math/rand/v2"
	"sync/atomic"
	"testing"
	"time"
)

// The three box blurs are, each way, the mean of the 2r+1 around, the edges
// held.
func TestGaussIsThreeBoxes(t *testing.T) {
	w, h := 23, 11
	p := make([]float32, w*h)
	for i := range p {
		p[i] = rand.Float32()
	}
	want := append([]float32(nil), p...)
	r := boxRadius(2)
	at := func(a []float32, x, y int) float32 { return a[min(max(y, 0), h-1)*w+min(max(x, 0), w-1)] }
	for range 3 {
		down := make([]float32, len(want))
		for y := range h {
			for x := range w {
				for d := -r; d <= r; d++ {
					down[y*w+x] += at(want, x, y+d) / float32(2*r+1)
				}
			}
		}
		clear(want)
		for y := range h {
			for x := range w {
				for d := -r; d <= r; d++ {
					want[y*w+x] += at(down, x+d, y) / float32(2*r+1)
				}
			}
		}
	}
	gauss(p, make([]float32, len(p)), w, h, 2)
	for i := range p {
		if math.Abs(float64(p[i]-want[i])) > 1e-5 {
			t.Fatalf("at %d: %v, want %v", i, p[i], want[i])
		}
	}
}

// The curve read from its table is the curve: straight between knots on the
// log of the light, straight to black below them, held above.
func TestToneIsTheCurve(t *testing.T) {
	l := look{curve: baseCurve}
	l.tabled()
	for _, v := range []float64{1e-5, 1.0 / 4096, 0.001, 0.01, 0.18, 0.5, 1, 7.3, 40} {
		want := baseCurve[0] * v * 2048
		if v >= 1.0/2048 {
			f := min((math.Log2(v)-firstKnot)/knotStep, knots-1)
			k := min(int(f), knots-2)
			want = baseCurve[k] + (baseCurve[k+1]-baseCurve[k])*(f-float64(k))
		}
		if got := float64(l.tone(float32(v))); math.Abs(got-want) > 1e-4 {
			t.Errorf("tone(%v) = %v, want %v", v, got, want)
		}
	}
}

// A colour in the gamut is left as it is; one out of it is brought in, and
// at 1.25 times its distance it lands on the edge.
func TestCompressBringsColoursIn(t *testing.T) {
	if r, g, b := compress(0.5, 0.3, 0.2); r != 0.5 || g != 0.3 || b != 0.2 {
		t.Errorf("in the gamut, moved to %v %v %v", r, g, b)
	}
	if r, g, b := compress(0.5, -0.4, 0.1); r != 0.5 || g <= -0.4 || g >= 0 || b != 0.1 {
		t.Errorf("out of the gamut, %v %v %v", r, g, b)
	}
	if _, g, _ := compress(1, -0.25, 1); math.Abs(float64(g)) > 1e-3 {
		t.Errorf("at the limit, %v", g)
	}
}

// A single bright pixel is the ring around it; a bright patch is a glint, kept.
func TestHotPixels(t *testing.T) {
	w, h := 9, 9
	r, g, b := make([]float32, w*h), make([]float32, w*h), make([]float32, w*h)
	for i := range r {
		r[i], g[i], b[i] = 0.1, 0.1, 0.1
	}
	r[4*w+4], g[4*w+4], b[4*w+4] = 1, 1, 1
	hotPixels(r, g, b, make([]float32, w*h), w, h)
	if math.Abs(float64(r[4*w+4]-0.1)) > 1e-6 {
		t.Errorf("hot pixel left at %v", r[4*w+4])
	}
	for _, i := range []int{4*w + 4, 4*w + 5, 5*w + 4, 5*w + 5} {
		r[i], g[i], b[i] = 1, 1, 1
	}
	hotPixels(r, g, b, make([]float32, w*h), w, h)
	if r[4*w+4] != 1 {
		t.Error("a patch was taken for a hot pixel")
	}
}

// A table that moves nothing gives the colour back.
func TestHueTableOfNothing(t *testing.T) {
	var l look
	for a := range hues {
		for b := range sats {
			l.table[a][b] = [3]float32{0, 1, 1}
		}
	}
	for range 1000 {
		r, g, b := rand.Float32(), rand.Float32(), rand.Float32()
		R, G, B := l.hueTable(r, g, b)
		if math.Abs(float64(R-r))+math.Abs(float64(G-g))+math.Abs(float64(B-b)) > 1e-5 {
			t.Fatalf("%v %v %v became %v %v %v", r, g, b, R, G, B)
		}
	}
}

// synthetic is a raw picture of patches of random colours, and the camera's
// JPEG of it, made through a look with the given curve and matrix.
func synthetic(curve [knots]float64, m [3][3]float64, w, h int) (*Image, *Meta, *image.RGBA) {
	meta := &Meta{Model: "X-T3", ExposureShift: -0.72}
	img := &Image{Width: w, Height: h, Pix: make([]uint16, w*h*3)}
	rng := rand.New(rand.NewPCG(1, 2))
	const patch = 16
	colours := make([][3]float64, (w/patch+1)*(h/patch+1))
	for i := range colours {
		// Linear ProPhoto across eight stops, under the camera's white.
		light := math.Exp2(-10 + 8*rng.Float64())
		colours[i] = [3]float64{light * (0.3 + rng.Float64()), light * (0.3 + rng.Float64()), light * (0.3 + rng.Float64())}
	}
	for y := range h {
		for x := range w {
			c := colours[(y/patch)*(w/patch+1)+x/patch]
			for k := range 3 {
				img.Pix[(y*w+x)*3+k] = uint16(min(c[k], 1) * 65535)
			}
		}
	}
	l := look{m: m, curve: curve}
	gain := math.Exp2(ev0 - meta.ExposureShift)
	for r := range 3 {
		for c := range 3 {
			l.m[r][c] *= gain
		}
	}
	for a := range hues {
		for b := range sats {
			l.table[a][b] = [3]float32{0, 1, 1}
		}
	}
	l.tabled()
	return img, meta, develop(img, image.Rect(0, 0, w, h), l)
}

// The fit finds the curve the camera's JPEG was made with.
func TestFitFindsTheCurve(t *testing.T) {
	brighter := baseCurve
	for k := range brighter {
		brighter[k] = min(1, brighter[k]*1.15)
	}
	img, meta, preview := synthetic(brighter, baseM, 768, 512)
	l := fit(img, image.Rect(0, 0, 768, 512), preview, meta)
	far := 0.0
	for k := 30; k < 55; k++ {
		far = max(far, math.Abs(l.curve[k]-brighter[k]))
	}
	if far > 0.03 {
		t.Errorf("the fitted curve is %.3f off the camera's\n%v\n%v", far, l.curve, brighter)
	}
}

// Strips make the picture a whole would.
func TestStripsAreSeamless(t *testing.T) {
	img, meta, preview := synthetic(baseCurve, baseM, 200, 300)
	in := image.Rect(0, 0, 200, 300)
	l := fit(img, in, preview, meta)
	whole := develop(img, in, l)
	defer func(rows int) { stripRows = rows }(stripRows)
	stripRows = 64
	strips := develop(img, in, l)
	for i := range whole.Pix {
		if d := int(whole.Pix[i]) - int(strips.Pix[i]); d > 1 || d < -1 {
			t.Fatalf("byte %d: %d whole, %d in strips", i, whole.Pix[i], strips.Pix[i])
		}
	}
}

func TestInverse(t *testing.T) {
	m := mul3(proToSRGB, srgbToPro)
	for i := range 3 {
		for j := range 3 {
			want := 0.0
			if i == j {
				want = 1
			}
			if math.Abs(m[i][j]-want) > 1e-9 {
				t.Fatalf("%v", m)
			}
		}
	}
}

// Scaled, a picture keeps its shape and its light.
func TestScaled(t *testing.T) {
	img := &Image{Width: 601, Height: 400, Pix: make([]uint16, 601*400*3)}
	for i := range img.Pix {
		img.Pix[i] = []uint16{20000, 1000, 50}[i%3]
	}
	out, frame := scaled(img, image.Rect(0, 0, 601, 400), 256)
	if frame.Dx() != 256 || frame.Dy() != 170 || out.Width != 256 || len(out.Pix) != 256*170*3 {
		t.Fatalf("%v %dx%d", frame, out.Width, out.Height)
	}
	for i, v := range out.Pix {
		if want := []uint16{20000, 1000, 50}[i%3]; v < want-1 || v > want+1 {
			t.Fatalf("sample %d is %d, want %d", i, v, want)
		}
	}
}

// Developed in the background, a photo takes one core.
func TestBackgroundTakesOneCore(t *testing.T) {
	var at, most atomic.Int32
	parallel(64, 1, func(int, *buffers) {
		most.Store(max(most.Load(), at.Add(1)))
		time.Sleep(time.Millisecond)
		at.Add(-1)
	})
	if most.Load() != 1 {
		t.Errorf("%d at once", most.Load())
	}
}
