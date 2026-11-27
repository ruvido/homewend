// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"math"
	"testing"
)

// negativeOf is the negative a print of linear light print(x, y) would come
// from, with the bounds b, photographed in a dark holder a tenth of the
// picture wide at each side.
func negativeOf(w, h int, b Bounds, print func(x, y int) [3]float64) *Image {
	img := &Image{Width: w, Height: h, Pix: make([]uint16, w*h*3)}
	floor := math.Pow(10, -printK)
	for y := range h {
		for x := range w {
			if x < w/10 || x >= w-w/10 || y < h/10 || y >= h-h/10 {
				continue // the holder: no light
			}
			p := print(x, y)
			for c := range 3 {
				n := 1 + math.Log10(p[c]*(1-floor)+floor)/printK
				d := b.Base[c] + n*(b.White[c]-b.Base[c])
				img.Pix[(y*w+x)*3+c] = uint16(math.Round(65535 * math.Pow(10, -d)))
			}
		}
	}
	return img
}

var identity = [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}

// scene is black in its top band, white in its bottom one, greys between,
// and a few colours: what a print has.
func scene(w, h int) func(x, y int) [3]float64 {
	return func(x, y int) [3]float64 {
		switch t := float64(y) / float64(h); {
		case t < 0.2:
			return [3]float64{0, 0, 0}
		case t > 0.8:
			return [3]float64{1, 1, 1}
		case x < w/3:
			return [3]float64{0.6, 0.2, 0.1}
		default:
			g := 0.05 + 0.5*float64(x)/float64(w)
			return [3]float64{g, g, g}
		}
	}
}

func TestNegativeRoundTrip(t *testing.T) {
	want := Bounds{Base: [3]float64{1.27, 1.27, 1.67}, White: [3]float64{2.01, 2.14, 2.58}}
	const w, h = 400, 300
	print := scene(w, h)
	neg := negativeOf(w, h, want, print)
	got, err := Analyse(neg)
	if err != nil {
		t.Fatal(err)
	}
	for c := range 3 {
		if math.Abs(got.Base[c]-want.Base[c]) > 0.002 || math.Abs(got.White[c]-want.White[c]) > 0.002 {
			t.Fatalf("bounds %v, want %v", got, want)
		}
	}
	// Kept as text and read back, as the catalog keeps them.
	if got, err = ParseBounds(got.String()); err != nil {
		t.Fatal(err)
	}
	pos := Positive(neg, &Meta{RGBCam: identity}, got, 0)
	worst := 0.0
	for y := h / 10; y < h-h/10; y++ {
		for x := w / 10; x < w-w/10; x++ {
			p := print(x, y)
			for c := range 3 {
				e := float64(encoded8(float32(p[c])))
				worst = max(worst, math.Abs(e-float64(pos.Pix[y*pos.Stride+4*x+c])))
			}
		}
	}
	if worst > 3 {
		t.Errorf("the positive is %v of 255 off the print", worst)
	}
}

// One frame of another film among three moves nothing; of two, the mean.
func TestMedianBounds(t *testing.T) {
	a := Bounds{[3]float64{1, 1, 1}, [3]float64{2, 2, 2}}
	b := Bounds{[3]float64{1.2, 1.1, 1}, [3]float64{2, 2.2, 2}}
	odd := Bounds{[3]float64{0.1, 3, 1}, [3]float64{4, 4, 9}}
	if got := MedianBounds([]Bounds{a, odd, b}); got != (Bounds{[3]float64{1, 1.1, 1}, [3]float64{2, 2.2, 2}}) {
		t.Errorf("median of three = %v", got)
	}
	if got := MedianBounds([]Bounds{a, b}); math.Abs(got.Base[0]-1.1) > 1e-9 || math.Abs(got.White[1]-2.1) > 1e-9 {
		t.Errorf("median of two = %v", got)
	}
	if got := MedianBounds([]Bounds{odd}); got != odd {
		t.Errorf("median of one = %v", got)
	}
}

func TestParseBoundsRefuses(t *testing.T) {
	for _, s := range []string{"", "1 2 3", "2 2 2 1 1 1", "a b c d e f"} {
		if _, err := ParseBounds(s); err == nil {
			t.Errorf("%q read", s)
		}
	}
}

// A cast frame is brought half way back to grey; one with no greys is left.
func TestGreyShift(t *testing.T) {
	b := Bounds{[3]float64{0, 0, 0}, [3]float64{1, 1, 1}}
	cast := make([][3]float64, 100)
	for i := range cast {
		cast[i] = [3]float64{0.55, 0.5, 0.45}
	}
	if s := greyShift(cast, b); math.Abs(s[0]-0.025) > 1e-9 || math.Abs(s[1]) > 1e-9 || math.Abs(s[2]+0.025) > 1e-9 {
		t.Errorf("shift = %v", s)
	}
	for i := range cast {
		cast[i] = [3]float64{0.9, 0.2, 0.1}
	}
	if s := greyShift(cast, b); s != [3]float64{} {
		t.Errorf("shift of no greys = %v", s)
	}
}
