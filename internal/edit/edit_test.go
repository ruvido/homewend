// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package edit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"math/rand/v2"
	"strings"
	"testing"
)

const colourInputs = "0,0,0 60,40,20 117,117,117 115,140,128 204,158,133 80,120,220 230,60,180 255,255,255"

// What each setting makes of each input.
var colourVectors = []struct{ settings, want string }{
	{"Exposure 25", "0,0,0 85,58,31 161,161,161 158,191,176 236,209,182 111,165,243 246,85,224 255,255,255"},
	{"Exposure -25", "0,0,0 41,26,11 84,84,84 83,101,92 149,115,96 56,86,161 169,41,131 188,188,188"},
	{"Exposure 100", "0,0,0 211,157,94 245,245,245 245,249,247 253,250,248 232,246,254 254,211,252 255,255,255"},
	{"Contrast 100", "0,0,0 35,20,9 117,117,117 112,163,141 229,188,151 54,124,238 244,35,210 255,255,255"},
	{"Contrast -100", "0,0,0 85,66,40 117,117,117 116,127,122 173,137,124 99,118,192 206,85,152 255,255,255"},
	{"Highlights -100", "0,0,0 60,40,20 115,115,115 105,127,116 163,126,106 66,100,183 199,52,155 255,255,255"},
	{"Highlights 100", "0,0,0 60,40,20 118,118,118 120,146,133 222,172,145 86,129,237 244,64,191 255,255,255"},
	{"Shadows 100", "0,0,0 100,67,33 129,129,129 116,142,130 204,158,133 80,120,220 230,60,180 255,255,255"},
	{"Shadows -100", "0,0,0 42,28,14 112,112,112 114,139,127 204,158,133 80,120,220 230,60,180 255,255,255"},
	{"Whites 100", "0,0,0 53,34,16 117,117,117 114,148,131 255,174,138 74,121,255 255,53,210 255,255,255"},
	{"Whites -100", "0,0,0 64,44,22 117,117,117 115,137,127 187,151,131 83,120,198 205,64,169 223,223,223"},
	{"Blacks 100", "31,31,31 72,58,44 117,117,117 115,137,126 198,153,131 87,120,215 226,72,174 255,255,255"},
	{"Blacks -100", "0,0,0 19,0,0 117,117,117 114,147,132 215,169,139 58,121,228 236,19,192 255,255,255"},
	{"Warmth 100", "0,0,0 68,39,13 132,114,91 130,137,99 229,155,103 91,117,173 255,59,141 255,250,201"},
	{"Warmth -100", "0,0,0 52,40,27 103,118,145 101,141,158 181,159,164 70,121,255 204,60,221 227,255,255"},
	{"Vibrance 100", "0,0,0 66,39,12 117,117,117 98,146,123 216,155,122 63,121,255 255,45,205 255,255,255"},
	{"Vibrance -100", "0,0,0 54,41,28 117,117,117 132,134,133 192,161,144 97,119,174 188,75,155 255,255,255"},
	{"BlackWhite 100", "0,0,0 44,44,44 117,117,117 134,134,134 168,168,168 124,124,124 133,133,133 255,255,255"},
	{"BlackWhite 50", "0,0,0 52,42,32 117,117,117 125,137,131 186,163,150 102,122,172 181,96,156 255,255,255"},
	{"Exposure 30, Contrast 60, Blacks 40, Whites -30, Highlights -50, Shadows 70, Warmth 45", "16,16,16 111,65,36 187,177,161 180,200,171 230,209,169 111,167,225 237,78,217 241,241,237"},
	{"Exposure 80, Shadows 60, Highlights -80, Warmth 100, Vibrance 50", "0,0,0 199,118,47 241,233,214 238,241,217 253,243,219 199,230,253 255,161,255 255,255,250"},
	{"Exposure -40, Highlights 60, Shadows -50, Vibrance -30, BlackWhite 60", "0,0,0 22,18,14 67,67,67 77,82,79 114,103,97 66,75,96 98,61,87 155,155,155"},
	{"Exposure 35, Contrast 35, Highlights 35, Shadows 35, Whites 35, Blacks 35, Warmth 35, Vibrance 35, BlackWhite 35", "15,15,15 98,69,48 208,200,188 211,232,207 253,247,222 157,209,244 231,113,229 255,255,255"},
}

// made is an edit of settings like "Exposure 12, Warmth -8".
func made(t testing.TB, settings, crop string) Edit {
	t.Helper()
	adjust := map[string]int{}
	for s := range strings.SplitSeq(settings, ", ") {
		if name, v, ok := strings.Cut(s, " "); ok {
			var n int
			fmt.Sscan(v, &n)
			adjust[name] = n
		}
	}
	text, _ := json.Marshal(adjust)
	if crop != "" {
		crop = `, "crop": ` + crop
	}
	e, err := Parse(fmt.Appendf(nil, `{"adjust": %s, "tools": 2%s}`, text, crop))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func cropJSON(turns int, flip bool, angle int, x, y, w, h float64) string {
	return fmt.Sprintf(`{"turns": %d, "flip": %v, "angle": %d, "x": %v, "y": %v, "w": %v, "h": %v, "shape": "Free"}`,
		turns, flip, angle, x, y, w, h)
}

func colours(t testing.TB, list string) []color.NRGBA {
	t.Helper()
	var out []color.NRGBA
	for s := range strings.FieldsSeq(list) {
		c := color.NRGBA{A: 255}
		if _, err := fmt.Sscanf(s, "%d,%d,%d", &c.R, &c.G, &c.B); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func apart(a, b byte) int { return max(int(a)-int(b), int(b)-int(a)) }

func near(t *testing.T, what string, got, want color.NRGBA, allow int) {
	t.Helper()
	if max(apart(got.R, want.R), apart(got.G, want.G), apart(got.B, want.B)) > allow || got.A != want.A {
		t.Errorf("%s: %v, want %v", what, got, want)
	}
}

func filled(w, h int, at func(x, y int) color.NRGBA) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, at(x, y))
		}
	}
	return img
}

func noise(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = byte(r.Uint32())
	}
	return img
}

// scene is smooth with a little grain, as a photo is.
func scene(w, h int) *image.NRGBA {
	img := noise(w, h)
	for i := 0; i < len(img.Pix); i += 4 {
		x, y := float64(i/4%w)/float64(w), float64(i/4/w)/float64(h)
		for ch := range 3 {
			v := 125 + 110*math.Sin(5*x+float64(ch))*math.Cos(4*y+2*x+0.3*float64(ch))
			img.Pix[i+ch] = byte(v) + img.Pix[i+ch]%8
		}
		img.Pix[i+3] = 255
	}
	return img
}

func grey(v byte) color.NRGBA { return color.NRGBA{v, v, v, 255} }

func TestColours(t *testing.T) {
	in := colours(t, colourInputs)
	src := filled(len(in), 1, func(x, _ int) color.NRGBA { return in[x] })
	for _, v := range colourVectors {
		want := colours(t, v.want)
		got := Draw(src, made(t, v.settings, ""))
		for i := range in {
			near(t, fmt.Sprintf("%s of %v", v.settings, in[i]), got.NRGBAAt(i, 0), want[i], 1)
		}
	}
}

// What is pinned without an allowance.
func TestColoursExact(t *testing.T) {
	for _, v := range []struct{ settings, in, want string }{
		{"Exposure 25", "117,117,117", "161,161,161"},
		{"Exposure -25", "117,117,117", "84,84,84"},
		{"BlackWhite 100", "255,0,0 0,255,0 0,0,255", "127,127,127 220,220,220 76,76,76"},
		{"Shadows 100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"Shadows -100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"Highlights 100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"Highlights -100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"Contrast 100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"Contrast -100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"Vibrance 100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"Vibrance -100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
		{"BlackWhite 100", "0,0,0 255,255,255", "0,0,0 255,255,255"},
	} {
		in, want := colours(t, v.in), colours(t, v.want)
		got := Draw(filled(len(in), 1, func(x, _ int) color.NRGBA { return in[x] }), made(t, v.settings, ""))
		for i := range in {
			near(t, v.settings, got.NRGBAAt(i, 0), want[i], 0)
		}
	}
}

func TestWarmthGains(t *testing.T) {
	for _, c := range []struct {
		warmth float64
		want   [3]float64
	}{
		{1, [3]float64{1.29632, 0.95405, 0.58266}},
		{-1, [3]float64{0.76525, 1.01114, 1.58092}},
		{0.45, [3]float64{1.12670, 0.98355, 0.78983}},
	} {
		got := newTones([toolCount]float64{warmth: c.warmth}).gain
		for i := range got {
			if math.Abs(got[i]-c.want[i]) > 6e-6 {
				t.Errorf("warmth %v: %v, want %v", c.warmth, got, c.want)
			}
		}
	}
}

// The tables and the 32 bits against the formulas computed as they are.
func TestTables(t *testing.T) {
	src := noise(32, 32)
	r := rand.New(rand.NewPCG(3, 4))
	for range 60 {
		var v [toolCount]float64
		e := Edit{}
		for i := range e.adjust {
			if i == clarity || i == vignette || r.IntN(2) == 0 {
				continue
			}
			e.adjust[i] = r.IntN(201) - 100
			if i == blackWhite {
				e.adjust[i] = r.IntN(101)
			}
			v[i] = float64(e.adjust[i]) / 100
		}
		tn := newTones(v)
		got := Draw(src, e)
		for i := 0; i < len(src.Pix); i += 4 {
			var c [3]float64
			for ch := range c {
				c[ch] = tn.exposed(ch, int(src.Pix[i+ch]))
			}
			lift := 1.0
			if v[shadows] != 0 || v[highlights] != 0 {
				lift = tn.lift(max(c[0], c[1], c[2]))
			}
			for ch := range c {
				c[ch] = tn.curve(c[ch] * lift)
			}
			top, low := max(c[0], c[1], c[2]), min(c[0], c[1], c[2])
			hue := 0.0
			if top == c[0] && low == c[2] && top > low {
				hue = (c[1] - c[2]) / (top - low)
			}
			s := 1 + v[vibrance]*min(max(1-(top-low), 0), 1)*(1-0.6*4*hue*(1-hue))
			g := wr*c[0] + wg*c[1] + wb*c[2]
			y := 0.0
			for ch, w := range [3]float64{wr, wg, wb} {
				c[ch] = g + (c[ch]-g)*s
				y += w * lin(min(max(c[ch], 0), 1))
			}
			for ch := range c {
				l := enc(y)
				want := byte(min(max(255*(l+(c[ch]-l)*(1-v[blackWhite]))+0.5, 0), 255))
				if apart(got.Pix[i+ch], want) > 1 {
					t.Fatalf("%v of %v: channel %d is %d, want %d", e.adjust, src.Pix[i:i+3], ch, got.Pix[i+ch], want)
				}
			}
		}
	}
}

func TestClarityOnAHeldBase(t *testing.T) {
	for _, v := range []struct{ settings, in, want string }{
		{"Clarity 100", "115,140,128", "153,178,166 126,151,139 83,108,96"},
		{"Clarity 100", "204,158,133", "242,196,171 228,182,157 184,138,113"},
		{"Clarity 100", "80,120,220", "115,155,255 79,119,219 44,84,184"},
		{"Clarity -100", "115,140,128", "77,102,90 104,129,117 147,172,160"},
		{"Clarity -100", "80,120,220", "45,85,185 81,121,221 116,156,255"},
		{"Clarity 35, Exposure 30, Shadows 40, Vibrance 20", "115,140,128", "174,213,195 167,206,189 156,194,177"},
		{"Clarity 35, Exposure 30, Shadows 40, Vibrance 20", "204,158,133", "247,223,199 245,221,196 237,213,188"},
	} {
		in, want := colours(t, v.in)[0], colours(t, v.want)
		src := filled(1, 1, func(_, _ int) color.NRGBA { return in })
		for i, base := range []float32{40, 120, 200} {
			d := newDeveloper(src, made(t, v.settings, "").adjust, newFrame(crop{}, 1, 1))
			for k := range d.base {
				d.base[k] = base
			}
			row := bytes.Clone(src.Pix)
			d.develop(row, 0)
			near(t, fmt.Sprint(v.settings, " of ", v.in, " on ", base), color.NRGBA{row[0], row[1], row[2], row[3]}, want[i], 2)
		}
	}
}

func baseOf(src *image.NRGBA) *developer {
	return newDeveloper(src, [toolCount]int{clarity: 1}, newFrame(crop{}, src.Rect.Dx(), src.Rect.Dy()))
}

func step(w, h int) *image.NRGBA {
	return filled(w, h, func(x, _ int) color.NRGBA {
		if x < w/2 {
			return grey(90)
		}
		return grey(150)
	})
}

func TestClarityOfAStep(t *testing.T) {
	// From column 40: the three boxes reach six px from the step, between 47 and 48.
	want := []float32{90, 90, 90, 92, 95, 100, 107, 115, 125, 133, 140, 145, 148, 150, 150}
	for _, size := range [][2]int{{96, 64}, {960, 640}, {1200, 800}, {30, 20}} {
		d := baseOf(step(size[0], size[1]))
		if d.baseW != 96 || d.baseH != 64 {
			t.Fatalf("%v: the base is %d × %d", size, d.baseW, d.baseH)
		}
		for y := range 64 {
			for i, v := range want {
				if got := d.base[y*97+40+i]; got != v {
					t.Errorf("%v: base column %d is %v, want %v", size, 40+i, got, v)
				}
			}
		}
	}
	for _, c := range []struct {
		w, h          int
		columns, want []int
	}{
		{96, 64, []int{40, 44, 46, 47, 48, 49, 51, 55}, []int{90, 86, 78, 74, 167, 163, 154, 150}},
		{960, 640, []int{440, 460, 470, 480, 490, 510, 550}, []int{87, 80, 75, 170, 165, 156, 150}},
	} {
		got := Draw(step(c.w, c.h), made(t, "Clarity 100", ""))
		for i, x := range c.columns {
			near(t, fmt.Sprint(c.w, " column ", x), got.NRGBAAt(x, 3), grey(byte(c.want[i])), 1)
		}
	}
}

func TestClarityOfATexture(t *testing.T) {
	src := filled(300, 200, func(x, y int) color.NRGBA {
		return color.NRGBA{byte(37*x + 91*y), byte(53*x + 17*y), byte(11*x + 71*y), 255}
	})
	d := baseOf(src)
	for _, c := range []struct {
		at   int
		want []float32
	}{
		{0, []float32{121, 122, 124, 125, 126, 126, 127, 127}},
		{32*97 + 40, []float32{128, 128, 128, 128, 128, 127, 127, 127}},
	} {
		for i, v := range c.want {
			if got := d.base[c.at+i]; math.Abs(float64(got-v)) > 1 {
				t.Errorf("base %d is %v, want %v", c.at+i, got, v)
			}
		}
	}
	sum := 0
	for y := range 64 {
		for x := range 96 {
			sum += int(d.base[y*97+x])
		}
	}
	if sum < 783292-100 || sum > 783292+100 {
		t.Errorf("the base sums to %d", sum)
	}
	got := Draw(src, made(t, "Clarity 60", ""))
	near(t, "0,0", got.NRGBAAt(0, 0), color.NRGBA{0, 0, 0, 255}, 1)
	near(t, "150,100", got.NRGBAAt(150, 100), color.NRGBA{66, 186, 54, 255}, 1)
	near(t, "299,199", got.NRGBAAt(299, 199), color.NRGBA{229, 15, 0, 255}, 1)

	// What is kept is developed on the base of the whole photo, turned with it.
	turned := Draw(src, made(t, "Clarity 60", cropJSON(1, true, 0, 0, 0, 1, 1)))
	for y := range 200 {
		for x := range 300 {
			near(t, "turned", turned.NRGBAAt(199-y, 299-x), got.NRGBAAt(x, y), 1)
		}
	}
}

func TestVignette(t *testing.T) {
	src := filled(100, 60, func(_, _ int) color.NRGBA { return grey(128) })
	at := []image.Point{{X: 50, Y: 30}, {X: 80, Y: 30}, {X: 99, Y: 30}, {X: 0, Y: 30}, {X: 50, Y: 0}, {X: 0, Y: 0}, {X: 99, Y: 59}}
	for v, want := range map[int][]byte{
		50:  {128, 124, 96, 95, 124, 90, 90},
		-50: {128, 132, 160, 161, 132, 166, 166},
		100: {128, 121, 65, 62, 121, 51, 52},
	} {
		got := Draw(src, made(t, fmt.Sprintf("Vignette %d", v), ""))
		for i, p := range at {
			near(t, fmt.Sprint(v, p), got.NRGBAAt(p.X, p.Y), grey(want[i]), 1)
		}
	}
}

func TestCropLandings(t *testing.T) {
	for _, v := range []struct {
		crop string
		w, h int
		m    [6]float64
		size image.Point
	}{
		{cropJSON(3, false, 0, 0, 0, 1, 1), 400, 300, [6]float64{0, 1, 0, -1, 0, 400}, image.Pt(300, 400)},
		{cropJSON(0, true, 0, 0, 0, 1, 1), 400, 300, [6]float64{-1, 0, 400, 0, 1, 0}, image.Pt(400, 300)},
		{cropJSON(1, false, 0, 0.125, 0, 0.75, 0.5), 128, 128, [6]float64{0, -1, 96, 1, 0, -32}, image.Pt(96, 64)},
		{cropJSON(0, false, 30, 0.1, -0.1, 0.4, 0.4), 400, 300, [6]float64{0.9659, -0.2588, -120.7639, 0.2588, 0.9659, -118.0277}, image.Pt(160, 120)},
		{cropJSON(1, true, 14, 0.05, -0.03, 0.5, 0.625), 128, 128, [6]float64{0.1219, -0.9925, 80.9030, -0.9925, -0.1219, 114.3540}, image.Pt(64, 80)},
		{cropJSON(3, true, -5, 0.1, -0.2, 0.5, 0.6), 4000, 3000, [6]float64{0.0436, 0.9990, -1100.6301, 0.9990, -0.0436, 79.6570}, image.Pt(1500, 2400)},
	} {
		f := newFrame(made(t, "", v.crop).crop, v.w, v.h)
		if f.w != v.size.X || f.h != v.size.Y {
			t.Errorf("%s: %d × %d, want %v", v.crop, f.w, f.h, v.size)
		}
		for i, got := range [6]float64{f.m0, f.m1, f.m2, f.m3, f.m4, f.m5} {
			allow := 0.00006
			if i%3 == 2 {
				allow = 0.001
			}
			if math.Abs(got-v.m[i]) > allow {
				t.Errorf("%s: m%d is %v, want %v", v.crop, i, got, v.m[i])
			}
		}
		x, y := f.back(f.m0*12+f.m1*34+f.m2, f.m3*12+f.m4*34+f.m5)
		if math.Abs(x-12) > 1e-6 || math.Abs(y-34) > 1e-6 {
			t.Errorf("%s: 12,34 comes back as %v,%v", v.crop, x, y)
		}
	}
}

// A ramp says where each px was taken from.
func TestCropSampling(t *testing.T) {
	src := filled(240, 200, func(x, y int) color.NRGBA { return color.NRGBA{byte(x), byte(y), 0, 255} })
	e := made(t, "", cropJSON(1, true, 14, 0.05, -0.03, 0.5, 0.625))
	f := newFrame(e.crop, 240, 200)
	got := Draw(src, e)
	for y := range f.h {
		for x := range f.w {
			px, py := f.back(float64(x)+0.5, float64(y)+0.5)
			c := got.NRGBAAt(x, y)
			wantX, wantY := min(max(px-0.5, 0), 239), min(max(py-0.5, 0), 199)
			if math.Abs(float64(c.R)-wantX) > 0.51 || math.Abs(float64(c.G)-wantY) > 0.51 {
				t.Fatalf("%d,%d is %v, want %.2f,%.2f", x, y, c, wantX, wantY)
			}
		}
	}
}

func TestCropEdges(t *testing.T) {
	src := noise(61, 40)
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] |= 1 // a clear px has no colour once resampled
	}
	// Turned and cut on whole px: the same bytes.
	for turns := range 4 {
		for _, flip := range []bool{false, true} {
			got := Draw(src, made(t, "", cropJSON(turns, flip, 0, 0, 0, 1, 1)))
			for y := range 40 {
				for x := range 61 {
					at := image.Pt(x, y)
					if flip {
						at.X = 60 - x
					}
					w, h := 61, 40
					for range turns {
						at = image.Pt(h-1-at.Y, at.X)
						w, h = h, w
					}
					if got.NRGBAAt(at.X, at.Y) != src.NRGBAAt(x, y) {
						t.Fatalf("turns %d, flip %v: %d,%d differs", turns, flip, x, y)
					}
				}
			}
		}
	}
	// Half a px across: the mean of two.
	half := Draw(filled(4, 1, func(x, _ int) color.NRGBA { return grey(byte(10 * x)) }), made(t, "", cropJSON(0, false, 0, 0.125, 0, 0.5, 1)))
	if half.Rect != image.Rect(0, 0, 2, 1) || half.NRGBAAt(0, 0) != grey(15) || half.NRGBAAt(1, 0) != grey(25) {
		t.Errorf("half a px: %v %v", half.Rect, half.Pix)
	}
	// A clear px gives no colour to the one beside it.
	clear := image.NewNRGBA(image.Rect(0, 0, 4, 1))
	copy(clear.Pix, []byte{0, 0, 0, 0, 200, 100, 50, 255, 40, 20, 10, 51, 9, 9, 9, 0})
	clear = Draw(clear, made(t, "", cropJSON(0, false, 0, 0, 0, 0.75, 1)))
	if want := []byte{200, 100, 50, 128, 173, 87, 43, 153, 40, 20, 10, 26}; !bytes.Equal(clear.Pix, want) {
		t.Errorf("half a px of clear ones: %v, want %v", clear.Pix, want)
	}
	// Outside, the edge goes on: however far the frame is.
	for _, x := range []float64{2, -2, 3e38, -3e38} {
		for _, angle := range []int{0, 17, -90} {
			got := Draw(src, made(t, "Clarity 50", cropJSON(2, true, angle, x, 0, 0.5, 0.5)))
			column := 0
			if x > 0 {
				column = 60
			}
			edge := map[[4]byte]bool{}
			plain := Draw(src, made(t, "", cropJSON(2, true, angle, x, 0, 0.5, 0.5)))
			if angle == 0 {
				for y := range 40 {
					c := src.NRGBAAt(column, y)
					edge[[4]byte{c.R, c.G, c.B, c.A}] = true
				}
				for i := 0; i < len(plain.Pix); i += 4 {
					if !edge[[4]byte(plain.Pix[i:])] {
						t.Fatalf("x %v: %v is not of the edge", x, plain.Pix[i:i+4])
					}
				}
			}
			if got.Rect != plain.Rect {
				t.Error(got.Rect, plain.Rect)
			}
		}
	}
	// Sizes round half up in 32 bits, and are never 0.
	for _, c := range []struct {
		of   int
		w    float64
		want int
	}{{5, 0.7, 4}, {5, 0.5, 3}, {3, 0.5, 2}, {1, 0.05, 1}, {100, 1e-40, 1}} {
		if got := newFrame(crop{turns: 1, x: 0, w: 1, h: float32(c.w)}, c.of, 7).h; got != c.want {
			t.Errorf("%v of %d is %d, want %d", c.w, c.of, got, c.want)
		}
	}
}

const everything = "Exposure 12, Contrast 20, Highlights -30, Shadows 40, Whites 10, Blacks -15, Warmth 8, Vibrance 25, Clarity 35, Vignette 20, BlackWhite 30"

func TestAlphaIsKept(t *testing.T) {
	src := noise(50, 40)
	before := bytes.Clone(src.Pix)
	got := Draw(src, made(t, everything, ""))
	for i := 3; i < len(src.Pix); i += 4 {
		if got.Pix[i] != src.Pix[i] {
			t.Fatal("alpha moved")
		}
	}
	if !bytes.Equal(before, src.Pix) {
		t.Error("the source was written on")
	}
}

func TestOnePixel(t *testing.T) {
	for _, size := range []image.Point{{X: 1, Y: 1}, {X: 1, Y: 300}, {X: 300, Y: 1}, {X: 5, Y: 0}} {
		src := noise(size.X, size.Y)
		for _, c := range []string{"", cropJSON(1, true, 77, 0.2, 0.1, 0.05, 0.05)} {
			Draw(src, made(t, everything, c))
		}
	}
}

func TestNothingToDraw(t *testing.T) {
	src := noise(20, 10)
	for _, text := range []string{
		`null`, `{"adjust": {}, "tools": 2}`, `{"adjust": {"Exposure": 0, "Later": 5}, "tools": 2, "at": 4}`,
		`{"adjust": {"BlackWhite": -3}, "tools": 2, "crop": ` + cropJSON(4, false, 0, 0.1, 0, 1, 0.9995) + `}`,
	} {
		e, err := Parse([]byte(text))
		if err != nil || !e.None() || Draw(src, e) != src {
			t.Errorf("%s: %v %v", text, e, err)
		}
	}
	// A crop alone develops nothing.
	if got := Draw(src, made(t, "", cropJSON(0, false, 0, 0, 0, 0.5, 1))); !bytes.Equal(got.Pix[:40], src.Pix[20:60]) {
		t.Error("the cropped px are others")
	}
}

// The same picture whichever image holds it, a part of a larger one too.
func TestImages(t *testing.T) {
	whole := noise(40, 30)
	for i := 3; i < len(whole.Pix); i += 4 {
		whole.Pix[i] = 255
	}
	r := image.Rect(3, 5, 33, 25)
	rgba := image.NewRGBA(whole.Rect)
	draw.Draw(rgba, rgba.Rect, whole, image.Point{}, draw.Src)
	apart := image.NewNRGBA(image.Rect(0, 0, 30, 20))
	draw.Draw(apart, apart.Rect, whole, r.Min, draw.Src)
	for _, c := range []string{"", cropJSON(1, false, 9, 0, 0, 0.6, 0.6)} {
		e := made(t, everything, c)
		want := Draw(apart, e)
		for _, img := range []image.Image{whole.SubImage(r), rgba.SubImage(r)} {
			if got := Draw(img, e); !bytes.Equal(got.Pix, want.Pix) || got.Rect != want.Rect {
				t.Errorf("%T differs", img)
			}
		}
	}
	clear := image.NewRGBA(image.Rect(0, 0, 1, 1))
	clear.SetRGBA(0, 0, color.RGBA{50, 25, 0, 128})
	near(t, "premultiplied", Draw(clear, Edit{}).NRGBAAt(0, 0), color.NRGBA{99, 49, 0, 128}, 0)
}

func TestEqual(t *testing.T) {
	a := made(t, "Exposure 12, Warmth 0", cropJSON(1, false, 0, 0.10000000149011612, 0, 0.5, 0.5))
	b, err := Parse([]byte(`{"tools": 2, "crop": {"shape": "Later", "h": 0.5, "w": 0.5, "y": -0.0, "x": 0.1, "angle": 0, "flip": false, "turns": -3}, "adjust": {"Exposure": 12}}`))
	if err != nil || a != b {
		t.Errorf("%v %v %v", a, b, err)
	}
	if a == made(t, "Exposure 12", "") {
		t.Error("a crop does not count")
	}
	if made(t, "Exposure 900", cropJSON(0, false, 200, 0, 0, 1, 1)) != made(t, "Exposure 100", cropJSON(0, false, 90, 0, 0, 1, 1)) {
		t.Error("a value past its end is not its end")
	}
	if made(t, "Exposure 1", "") != made(t, "Exposure 1", cropJSON(0, false, 0, 0.3, 0.3, 1, 1)) {
		t.Error("a crop of everything counts")
	}
	if e, err := Parse([]byte(`{"adjust": {"Exposure": 99999999999999999999}, "tools": 2}`)); err != nil || e != made(t, "Exposure 100", "") {
		t.Error("a value past an int is not its end")
	}
}

func TestWholeNumbers(t *testing.T) {
	whole := `{"turns": 1, "flip": false, "angle": 0, "x": 0, "y": 0, "w": 1, "h": 1, "shape": "Free"}`
	if made(t, "", whole) != made(t, "", strings.NewReplacer(`"x": 0`, `"x": 0.0`, `"w": 1`, `"w": 1.0`).Replace(whole)) {
		t.Error("1 is not 1.0")
	}
}

func TestNewer(t *testing.T) {
	for _, text := range []string{`{"tools": 3}`, `{"tools": 99999999999999999999, "adjust": 4}`} {
		if _, err := Parse([]byte(text)); !errors.Is(err, ErrNewer) {
			t.Errorf("%s: %v", text, err)
		}
	}
}

func TestRefused(t *testing.T) {
	good := cropJSON(1, true, 3, 0.1, 0, 0.5, 0.5)
	bad := []string{
		``, `4`, `[]`, `"null"`, `{`, `{"adjust": {}, "tools": 2} x`,
		`{"adjust": {}}`, `{"adjust": {}, "tools": 1}`, `{"adjust": {}, "tools": -2}`, `{"adjust": {}, "tools": 2.5}`,
		`{"adjust": {}, "tools": "2"}`, `{"adjust": {}, "tools": null}`, `{"adjust": {}, "Tools": 2}`,
		`{"tools": 2}`, `{"adjust": null, "tools": 2}`, `{"adjust": [], "tools": 2}`,
		`{"adjust": {"Exposure": "1"}, "tools": 2}`, `{"adjust": {"Exposure": 1.5}, "tools": 2}`,
		`{"adjust": {"Exposure": null}, "tools": 2}`,
		`{"adjust": {}, "tools": 2, "crop": null}`, `{"adjust": {}, "tools": 2, "crop": []}`,
		"{\"adjust\": {\"\xff\": 1}, \"tools\": 2}",
		`{"adjust": {}, "tools": 2, "x": "` + strings.Repeat("x", MaxSize) + `"}`,
	}
	for _, change := range [][2]string{
		{`"turns": 1, `, ``}, {`"flip": true, `, ``}, {`"angle": 3, `, ``}, {`"x": 0.1, `, ``}, {`"y": 0, `, ``},
		{`"w": 0.5, `, ``}, {`"h": 0.5, `, ``}, {`, "shape": "Free"`, ``},
		{`"turns": 1`, `"turns": 1.5`}, {`"flip": true`, `"flip": 0`}, {`"flip": true`, `"flip": null`},
		{`"angle": 3`, `"angle": "3"`}, {`"x": 0.1`, `"x": 1e39`}, {`"x": 0.1`, `"x": null`}, {`"x": 0.1`, `"x": NaN`},
		{`"x": 0.1`, `"x": "0.1"`}, {`"y": 0`, `"y": "0"`}, {`"w": 0.5`, `"w": "0.5"`}, {`"h": 0.5`, `"h": "1"`},
		{`"w": 0.5`, `"w": 0`}, {`"w": 0.5`, `"w": -0.5`}, {`"w": 0.5`, `"w": 1.01`}, {`"w": 0.5`, `"w": 1e-60`},
		{`"h": 0.5`, `"h": 0`}, {`"h": 0.5`, `"h": 2`}, {`"Free"`, `4`},
	} {
		crop := strings.Replace(good, change[0], change[1], 1)
		if crop == good {
			t.Fatal(change)
		}
		bad = append(bad, `{"adjust": {}, "tools": 2, "crop": `+crop+`}`)
	}
	for _, text := range bad {
		if e, err := Parse([]byte(text)); err == nil || errors.Is(err, ErrNewer) {
			t.Errorf("%.120s: %v %v", text, e, err)
		}
	}
}

func FuzzParse(f *testing.F) {
	f.Add(`{"adjust": {"Exposure": 12, "Warmth": -8}, "tools": 2, "crop": ` + cropJSON(1, true, 14, 0.05, -0.03, 0.5, 0.625) + `}`)
	f.Add(`{"tools": 3}`)
	f.Add(`null`)
	src := noise(7, 5)
	f.Fuzz(func(t *testing.T, text string) {
		if e, err := Parse([]byte(text)); err == nil {
			Draw(src, e)
		}
	})
}

func BenchmarkDraw(b *testing.B) {
	tilt := cropJSON(0, false, 14, 0.01, 0.01, 0.8, 0.8)
	type run struct{ name, settings, crop string }
	runs := []run{{"all", everything, ""}, {"all+tilt", everything, tilt}, {"all+tilt+turn", everything, cropJSON(1, true, 14, 0.01, 0.01, 0.8, 0.8)},
		{"tilt", "", tilt}, {"turn", "", cropJSON(1, false, 0, 0, 0, 1, 1)}}
	for _, name := range toolNames {
		runs = append(runs, run{name, name + " 35", ""})
	}
	for _, size := range []image.Point{{X: 2560, Y: 1920}, {X: 4000, Y: 3000}} {
		src := scene(size.X, size.Y)
		for i, r := range runs {
			if size.X == 4000 && i > 2 {
				break
			}
			b.Run(fmt.Sprintf("%dx%d/%s", size.X, size.Y, r.name), func(b *testing.B) {
				b.ReportAllocs()
				e := made(b, r.settings, r.crop)
				for b.Loop() {
					Draw(src, e)
				}
			})
		}
	}
}

// Removed is null, or nothing set and no crop key.
func TestRemoves(t *testing.T) {
	const none = `"crop":{"turns":0,"flip":false,"angle":0,"x":0,"y":0,"w":1,"h":1,"shape":"Free"}`
	for data, want := range map[string]bool{
		`null`:                                 true,
		`{"adjust":{},"tools":2}`:              true,
		`{"adjust":{"Exposure":0},"tools":2}`:  true,
		`{"adjust":{},"tools":2,"Crop":1}`:     true,
		`{"adjust":{},"tools":2,` + none + `}`: false,
		`{"adjust":{"Exposure":1},"tools":2}`:  false,
		`{"adjust":{"Grain":5},"tools":2}`:     false,
		`{"adjust":{},"tools":3}`:              false,
		`{"adjust":{},"tools":1}`:              false,
		`7`:                                    false,
	} {
		if got := Removes([]byte(data)); got != want {
			t.Errorf("%s: %v, want %v", data, got, want)
		}
	}
}
