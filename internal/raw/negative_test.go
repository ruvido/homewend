// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"image"
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
	got, crop, err := Analyse(neg, &Meta{})
	if err != nil {
		t.Fatal(err)
	}
	// The film inside the holder, a tenth of each side, inset.
	for i, want := range [4]float64{0.115, 0.115, 0.885, 0.885} {
		if math.Abs(crop[i]-want) > 0.01 {
			t.Errorf("crop %v", crop)
		}
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

// A frame alone, scratched with bright streaks thinner than its film base,
// is printed with no cast: its greys stay grey. The streaks are too thin to
// be taken for the base.
func TestNegativeStreaksAlone(t *testing.T) {
	want := Bounds{Base: [3]float64{1.27, 1.27, 1.67}, White: [3]float64{2.01, 2.14, 2.58}}
	const w, h = 400, 300
	// A picture with a black square in the middle, white below it, greys.
	print := func(x, y int) [3]float64 {
		switch {
		case x > 2*w/5 && x < 3*w/5 && y > 2*h/5 && y < 3*h/5:
			return [3]float64{}
		case x > 2*w/5 && x < 3*w/5 && y >= 3*h/5:
			return [3]float64{1, 1, 1}
		}
		g := 0.1 + 0.25*(float64(x)/w+float64(y)/h)
		return [3]float64{g, g, g}
	}
	neg := negativeOf(w, h, want, print)
	// Ten streaks two pixels wide, thinner than the base in green and blue.
	thinner := [3]float64{want.Base[0] + 0.1, want.Base[1] - 0.3, want.Base[2] - 0.3}
	for k := range 10 {
		for y := h / 4; y < 3*h/4; y++ {
			for x := w/10 + 40 + 30*k; x < w/10+42+30*k; x++ {
				for c := range 3 {
					neg.Pix[(y*w+x)*3+c] = uint16(math.Round(65535 * math.Pow(10, -thinner[c])))
				}
			}
		}
	}
	got, _, err := Analyse(neg, &Meta{})
	if err != nil {
		t.Fatal(err)
	}
	for c := range 3 {
		if math.Abs(got.Base[c]-want.Base[c]) > 0.01 || math.Abs(got.White[c]-want.White[c]) > 0.01 {
			t.Fatalf("bounds %v, want %v", got, want)
		}
	}
	pos := Positive(neg, &Meta{RGBCam: identity}, got, 0)
	// A grey between the streaks.
	px := pos.Pix[(h/2)*pos.Stride+4*(w/10+55):]
	if px[0] != px[1] || px[1] != px[2] {
		t.Errorf("a grey prints %v", px[:3])
	}
}

// One frame of another film among three moves no base; of two, the mean.
// Each keeps its own white; one not past the shared base keeps its own base.
func TestSharedBase(t *testing.T) {
	a := Bounds{[3]float64{1, 1, 1}, [3]float64{2, 2, 2}}
	b := Bounds{[3]float64{1.2, 1.1, 1}, [3]float64{2, 2.2, 2}}
	odd := Bounds{[3]float64{0.1, 3, 1}, [3]float64{4, 4, 9}}
	got := SharedBase([]Bounds{a, odd, b})
	base := [3]float64{1, 1.1, 1}
	if got[0] != (Bounds{base, a.White}) || got[1] != (Bounds{base, odd.White}) || got[2] != (Bounds{base, b.White}) {
		t.Errorf("shared of three = %v", got)
	}
	if got := SharedBase([]Bounds{a, b}); math.Abs(got[0].Base[0]-1.1) > 1e-9 || got[1].White != b.White {
		t.Errorf("shared of two = %v", got)
	}
	thin := Bounds{[3]float64{0.1, 0.1, 0.1}, [3]float64{0.5, 0.5, 0.5}}
	if got := SharedBase([]Bounds{a, b, thin}); got[2] != thin {
		t.Errorf("another film's = %v", got[2])
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

// A frame found on a half decode is a share of the print cut to the
// camera's frame, in the middle of a sensor twice as large.
func TestPrintShares(t *testing.T) {
	img := &Image{Width: 100, Height: 50}
	meta := &Meta{}
	meta.Frame.Width, meta.Frame.Height = 180, 100
	got := printShares(image.Rect(10, 0, 90, 50), img, meta)
	// 20..180 of the sensor's 200, the frame 10..190; rows held to it.
	want := [4]float64{10.0 / 180, 0, 170.0 / 180, 1}
	got2 := printShares(image.Rect(50, 25, 98, 50), img, meta)
	if math.Abs(got[0]-want[0]) > 1e-9 || math.Abs(got[2]-want[2]) > 1e-9 || got[1] != 0 || got[3] != 1 || got2[2] != 1 || math.Abs(got2[1]-0.5) > 1e-9 {
		t.Errorf("%v %v", got, got2)
	}
}

// A grainy frame is balanced alike printed whole and smaller: the grain a
// block averages is not the same at both sizes.
func TestBalancedAtAnySize(t *testing.T) {
	b := Bounds{Base: [3]float64{1.27, 1.27, 1.67}, White: [3]float64{2.01, 2.14, 2.58}}
	const w, h = 1600, 1200
	grain := uint32(1)
	neg := negativeOf(w, h, b, func(x, y int) [3]float64 {
		var p [3]float64
		for c := range p {
			grain = grain*1664525 + 1013904223
			p[c] = min(1, (0.15+0.05*float64(c))*(0.4+1.2*float64(grain>>8)/(1<<24)))
		}
		return p
	})
	whole := image.Rect(0, 0, w, h)
	small, frame := scaled(neg, whole, 400)
	got, want := autoBalance(neg, whole, b), autoBalance(small, frame, b)
	for c := range 3 {
		if math.Abs(got[c]-want[c]) > 2e-3 {
			t.Errorf("whole %v, at 400 %v", got, want)
		}
	}
}
