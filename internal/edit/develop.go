// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package edit

import (
	"image"
	"math"
	"sync"
)

// The weights of light in R, G, B.
const wr, wg, wb = 0.2126, 0.7152, 0.0722

func lin(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func enc(l float64) float64 {
	if l <= 0.0031308 {
		return 12.92 * l
	}
	return 1.055*math.Pow(l, 1/2.4) - 0.055
}

func light(r, g, b float32) float32 { return wr*r + wg*g + wb*b }

// tones are the tools that work on a channel, or on the brightest one, alone.
type tones struct {
	stops, contrast, highlights, shadows, whites, blacks float64
	gain                                                 [3]float64
	white                                                float64 // where the photo's white is after Exposure
}

func newTones(v [toolCount]float64) tones {
	t := tones{
		stops:    math.Exp2(4 * v[exposure]),
		contrast: v[contrast], highlights: v[highlights], shadows: v[shadows],
		whites: v[whites], blacks: v[blacks],
	}
	t.white = enc(t.stops)
	const m0 = 1000000.0 / 6504
	from, to := daylight(m0), daylight(m0+75*v[warmth])
	for i := range to {
		to[i] /= from[i]
	}
	keep := wr*to[0] + wg*to[1] + wb*to[2]
	for i := range to {
		t.gain[i] = to[i] / keep * t.stops
	}
	return t
}

// daylight is its linear sRGB at m mired.
func daylight(m float64) [3]float64 {
	t := m / 1000
	x := ((-2.0064*t+1.9018)*t+0.24748)*t + 0.237040
	if m >= 1000.0/7 {
		x = ((-4.6070*t+2.9678)*t+0.09911)*t + 0.244063
	}
	y := -3*x*x + 2.870*x - 0.275
	X, Z := x/y, (1-x-y)/y
	return [3]float64{
		3.2404542*X - 1.5371385 - 0.4985314*Z,
		-0.9692660*X + 1.8760108 + 0.0415560*Z,
		0.0556434*X - 0.2040259 + 1.0572252*Z,
	}
}

// exposed is a byte of a channel after Exposure and Warmth.
func (t *tones) exposed(channel, c int) float64 {
	return enc(lin(float64(c)/255) * t.gain[channel])
}

// lift is what Shadows and Highlights multiply a px by, its brightest channel
// being m.
func (t *tones) lift(m float64) float64 {
	m = max(m, 4.0/2049)
	if m >= t.white {
		return 1
	}
	a, b := 0.8, 0.8
	if t.shadows > 0 {
		a = 1.8
	}
	if t.highlights < 0 {
		b = 1.8
	}
	k := min(t.white, 1)
	s := m / k
	u := max(1-s/0.6, 0)
	s *= 1 + t.shadows*a*u*u
	h := min(k*s/t.white, 1)
	u = max(1-(1-h)/0.6, 0)
	h = 1 - (1-h)*(1-t.highlights*b*u*u)
	return t.white * h / m
}

// curve is the shoulder, Contrast, Whites and Blacks.
func (t *tones) curve(e float64) float64 {
	if l := lin(e); t.stops > 1 && l > 0.5 {
		p, u := (t.stops-0.5)/0.5, (l-0.5)/0.5
		e = enc(0.5 + 0.5*u*(1+u/(p*p))/(1+u))
	}
	e = min(max(e, 0), 1)
	if t.contrast != 0 {
		const g = 0.46
		f := math.Pow(2.5, t.contrast) * (g - e)
		if e < g {
			e = g * e / (e + f)
		} else {
			e = 1 + (1-g)*(e-1)/(1-e-f)
		}
		e = min(max(e, 0), 1)
	}
	return end(1-end(1-e, -t.whites, 0.54), t.blacks, 0.46)
}

// end moves one end of a channel, its still value staying where it is.
func end(x, up, still float64) float64 {
	if up == 0 {
		return x
	}
	n := -0.2 * up
	t, s := max((x-n)/(1-n), 0), max((still-n)/(1-n), 0)
	return t / (t + s*(1-still)/(still*(1-s))*(1-t))
}

// table is a function of 0 to 1 at every 1/steps.
type table [steps + 2]float32

const steps = 4096

func newTable(f func(x float64) float64) *table {
	t := new(table)
	for i := range t {
		t[i] = float32(f(float64(min(i, steps)) / steps))
	}
	return t
}

// at reads the function at x/steps, the nearest there is.
func (t *table) at(x float32) float32 { return t[min(max(int(x+0.5), 0), steps)] }

// between reads it at x/steps, straight from one to the next.
func (t *table) between(x float32) float32 {
	i := min(int(x), steps)
	return t[i] + (t[i+1]-t[i])*(x-float32(i))
}

// enc is by the square root of light.
var (
	linTable = sync.OnceValue(func() *table { return newTable(lin) })
	encTable = sync.OnceValue(func() *table { return newTable(func(x float64) float64 { return enc(x * x) }) })
)

// developer is the adjustments, ready for a picture. Rows are developed in
// any order.
type developer struct {
	v     [toolCount]float32
	f     frame
	plain *[3][256]byte // the whole work, if each channel goes by itself

	exposed [3][256]float32
	lifted  [3][256]float32 // by the brightest channel
	curve   *table          // after the lift; without one it is in exposed
	perUnit float32         // the curve's steps in 1

	lin, enc *table // for BlackWhite
	dim      *table // Vignette's, by the square of the distance
	stands   *table // Clarity's, by how far from the base: -255 to 255

	base         []float32 // Clarity's, with its last column and row twice
	baseW, baseH int
	perX, perY   float64 // base px in a px
}

func newDeveloper(src *image.NRGBA, adjust [toolCount]int, f frame) *developer {
	d := &developer{f: f}
	var v [toolCount]float64
	for i, a := range adjust {
		v[i] = float64(a) / 100
		d.v[i] = float32(v[i])
	}
	t := newTones(v)
	lifts := adjust[shadows] != 0 || adjust[highlights] != 0
	for ch := range 3 {
		for c := range 256 {
			e := t.exposed(ch, c)
			if lifts {
				d.lifted[ch][c] = float32(t.lift(e))
			} else {
				e = t.curve(e)
			}
			d.exposed[ch][c] = float32(e)
		}
	}
	if lifts {
		// The curve does not move past the photo's white.
		top := max(t.white, 1)
		d.curve = newTable(func(x float64) float64 { return t.curve(x * top) })
		d.perUnit = steps / float32(top)
	}
	if adjust[blackWhite] != 0 {
		d.lin, d.enc = linTable(), encTable()
	}
	if adjust[clarity] != 0 {
		d.setBase(src)
		d.stands = newTable(func(x float64) float64 {
			by := 2*x - 1
			return v[clarity] * by / (1 + 4*math.Abs(by)) * 4
		})
	}
	if adjust[vignette] != 0 {
		d.dim = newTable(func(x float64) float64 {
			t := min(max((math.Sqrt(x)-0.4)/0.6, 0), 1)
			return 1 - 0.6*v[vignette]*t*t*(3-2*t)
		})
	}
	if !lifts && adjust[clarity] == 0 && adjust[vibrance] == 0 && adjust[blackWhite] == 0 && adjust[vignette] == 0 {
		d.plain = new([3][256]byte)
		for ch := range 3 {
			for c := range 256 {
				d.plain[ch][c] = toByte(d.exposed[ch][c])
			}
		}
	}
	return d
}

func toByte(e float32) byte { return byte(min(max(int(255*e+0.5), 0), 255)) }

// setBase makes Clarity's base: the light of the whole picture, blurred, 96
// on the long side whatever the picture's size.
func (d *developer) setBase(src *image.NRGBA) {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	long := max(w, h)
	bw, bh := max((192*w+long)/(2*long), 1), max((192*h+long)/(2*long), 1)
	values := make([]float32, bw*bh)
	rows(bh, func(from, to int) {
		for j := 8 * from; j < 8*to; j++ {
			line := src.Pix[(2*j+1)*h/(16*bh)*src.Stride:]
			for i := range 8 * bw {
				p := line[4*((2*i+1)*w/(16*bw)):][:4:4]
				values[j/8*bw+i/8] += light(float32(p[0]), float32(p[1]), float32(p[2])) / 64
			}
		}
	})
	blurred := make([]float32, bw*bh)
	for range 3 {
		blur(blurred, values, bh, bw, 1)
		blur(values, blurred, bw, bh, bw)
	}
	d.baseW, d.baseH = bw, bh
	d.perX, d.perY = float64(bw)/float64(w), float64(bh)/float64(h)
	d.base = make([]float32, 0, (bw+1)*(bh+1))
	for y := range bh + 1 {
		row := values[min(y, bh-1)*bw:][:bw]
		for x := range bw + 1 {
			d.base = append(d.base, float32(toByte(row[min(x, bw-1)]/255)))
		}
	}
}

// blur is a box of 5 along lines of n values, step apart.
func blur(to, from []float32, lines, n, step int) {
	across := 1
	if step == 1 {
		across = n
	}
	for l := range lines {
		for i := range n {
			var sum float32
			for k := i - 2; k <= i+2; k++ {
				sum += from[l*across+min(max(k, 0), n-1)*step]
			}
			to[l*across+i*step] = sum / 5
		}
	}
}

// develop works a row of what is kept, in place.
func (d *developer) develop(row []byte, y int) {
	if t := d.plain; t != nil {
		for i := 0; i+4 <= len(row); i += 4 {
			p := row[i : i+3 : i+3]
			p[0], p[1], p[2] = t[0][p[0]], t[1][p[1]], t[2][p[2]]
		}
		return
	}
	vVibrance, colour := d.v[vibrance], 1-d.v[blackWhite]
	curve, base, stands, dims, lins, encs := d.curve, d.base, d.stands, d.dim, d.lin, d.enc
	lit := base != nil || vVibrance != 0
	across := d.baseW + 1

	// Where the row's first px is in the base, and how far the next.
	var u0, v0, du, dv float32
	if d.base != nil {
		x, y := d.f.back(0.5, float64(y)+0.5)
		u0, v0 = float32(x*d.perX-0.5), float32(y*d.perY-0.5)
		du, dv = float32(d.f.m0*d.perX), float32(d.f.m1*d.perY)
	}
	lastU, lastV := float32(d.baseW-1), float32(d.baseH-1)

	midX, midY := float32(d.f.w)/2, float32(d.f.h)/2
	downY := (float32(y) - midY) * (float32(y) - midY)
	perR := steps / (midX*midX + midY*midY)

	for i := 0; i+4 <= len(row); i += 4 {
		p := row[i : i+3 : i+3]
		r, g, b := d.exposed[0][p[0]], d.exposed[1][p[1]], d.exposed[2][p[2]]
		if curve != nil {
			lift := d.lifted[0][p[0]]
			if g > r && g >= b {
				lift = d.lifted[1][p[1]]
			} else if b > r {
				lift = d.lifted[2][p[2]]
			}
			lift *= d.perUnit
			r, g, b = curve.between(r*lift), curve.between(g*lift), curve.between(b*lift)
		}
		var l float32 // the light of r, g, b
		if lit {
			l = light(r, g, b)
		}
		if base != nil {
			n := float32(i / 4)
			u, v := u0+n*du, v0+n*dv
			if u < 0 {
				u = 0
			} else if u > lastU {
				u = lastU
			}
			if v < 0 {
				v = 0
			} else if v > lastV {
				v = lastV
			}
			iu, iv := int(u), int(v)
			u, v = u-float32(iu), v-float32(iv)
			top := base[iv*across+iu:]
			bottom := top[across:][:2]
			t := top[0] + (top[1]-top[0])*u
			t += (bottom[0] + (bottom[1]-bottom[0])*u - t) * v
			by := light(float32(p[0]), float32(p[1]), float32(p[2])) - t
			add := stands.at((by+255)*(steps/510.0)) * l * (1 - l)
			l += add
			r, g, b = r+add, g+add, b+add
		}
		if vVibrance != 0 {
			top, low := r, b
			warm := r >= g && g >= b && r > b
			if !warm {
				top, low = max(r, g, b), min(r, g, b)
			}
			if vivid := 1 - (top - low); vivid > 0 {
				s := vVibrance * vivid
				if warm {
					hue := (g - b) / (top - low)
					s *= 1 - 2.4*hue*(1-hue)
				}
				r, g, b = r+(r-l)*s, g+(g-l)*s, b+(b-l)*s
			}
		}
		if encs != nil {
			y := light(lins.at(r*steps), lins.at(g*steps), lins.at(b*steps))
			l := encs.at(float32(math.Sqrt(float64(y))) * steps)
			r, g, b = l+(r-l)*colour, l+(g-l)*colour, l+(b-l)*colour
		}
		if dims != nil {
			x := float32(i/4) - midX
			dim := dims.at((x*x + downY) * perR)
			r, g, b = r*dim, g*dim, b*dim
		}
		p[0], p[1], p[2] = toByte(r), toByte(g), toByte(b)
	}
}
