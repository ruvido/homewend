// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
)

// The look below was fitted on 20 RAFs of five Fujifilm bodies against the
// JPEGs the cameras wrote beside them (26/11/2026): 1.8 ΔE2000 from the
// camera's picture on average. Every photo then has its own curve, matrix
// and hue table, fitted to the JPEG preview it carries: the camera's film
// simulation and its own exposure decisions come with it.

// ev0 is the exposure, in stops, that puts the camera's raw where its JPEG
// is, before the camera's own exposure shift.
const ev0 = 3.51

// baseM takes linear sRGB to the camera JPEG's primaries, before the curve.
var baseM = [3][3]float64{
	{1.2824396095183672, -0.4541545519643599, 0.18001221642125795},
	{0.10654907627231043, 0.7423093797053018, 0.1656933023421899},
	{0.010780984499740843, 0.055345449612253586, 0.9110245335929382},
}

// knots of the tone curve are on the log2 of linear light, -11 to 5, a
// quarter of a stop apart; baseCurve is the encoded value at each.
const firstKnot, knotStep, knots = -11.0, 0.25, 65

var baseCurve = [knots]float64{
	0.00190007, 0.00208142, 0.00228008, 0.0024977, 0.00273609, 0.00299724, 0.00328331, 0.00359669,
	0.00393998, 0.00431603, 0.00472797, 0.00517923, 0.00567357, 0.00621508, 0.00680828, 0.0074581,
	0.00816994, 0.00894972, 0.00980392, 0.0107843, 0.0117647, 0.0137255, 0.0147059, 0.0156863,
	0.0156863, 0.0166667, 0.0186275, 0.022549, 0.0284314, 0.0372549, 0.0480392, 0.0617647,
	0.0784314, 0.0960784, 0.117647, 0.141176, 0.165686, 0.195098, 0.233333, 0.283333,
	0.326471, 0.362745, 0.408824, 0.462745, 0.509804, 0.552941, 0.604902, 0.669608,
	0.72451, 0.768628, 0.79902, 0.838235, 0.87451, 0.907843, 0.934314, 0.964216,
	0.994118, 1, 1, 1, 1, 1, 1, 1, 1,
}

// shading is the lens shading the cameras correct, 1 + a r² + b r⁴ with r
// the distance from the middle over half the diagonal: generic, not a lens's.
const shadingA, shadingB = 0.8905141332669995, -0.21406793935459942

// body is how much a camera's JPEG smooths its noise and sharpens: chroma
// blur sigma = chromaK (chroma noise / 0.004)^chromaP, luma smoothing and
// unsharp amount as fitted to its JPEGs at 100%.
type body struct{ chromaK, luma, sharp float64 }

const chromaP = 0.9416076073236844

var bodies = map[string]body{
	"X-M1":   {0.3594982425240445, 4, 0.3},
	"X-Pro2": {0.6106375881211111, 4, 0.8},
	"X-T1":   {3.1635952667631098, 2, 0.95},
	"X-T3":   {0.6156165217229264, 2.5, 0.9},
}

var otherBody = body{0.9124763087680496, 4, 0.6}

// gentle is the share of a body's luma smoothing and sharpening used: the
// rest is the user's, with the editor's tools, and a picture too smooth or
// too sharp cannot be undone.
const gentle = 0.5

// The hue table: 48 hues by 10 saturations, in ProPhoto HSV.
const hues, sats = 48, 10

// fitLong is the long side the fit is done at, and fitIterations how many
// times the curve and the matrix are fitted in turn.
const fitLong, fitIterations = 768, 3

// stripRows is how many rows a core develops at a time: a 40 MP photo then
// takes about 25 MB a core, not the gigabytes of whole planes.
var stripRows = 128

// Colour spaces: ProPhoto (D50) to linear sRGB (D65), Bradford-adapted,
// from the published matrices.
var proToSRGB = mul3(mul3(
	[3][3]float64{{3.2404542, -1.5371385, -0.4985314}, {-0.9692660, 1.8760108, 0.0415560}, {0.0556434, -0.2040259, 1.0572252}},
	[3][3]float64{{0.9555766, -0.0230393, 0.0631636}, {-0.0282895, 1.0099416, 0.0210077}, {0.0122982, -0.0204830, 1.3299098}}),
	[3][3]float64{{0.7976749, 0.1351917, 0.0313534}, {0.2880402, 0.7118741, 0.0000857}, {0.0, 0.0, 0.8252100}})

var srgbToPro = inv3(proToSRGB)

var proToSRGB32, srgbToPro32 = to32(proToSRGB), to32(srgbToPro)

func to32(m [3][3]float64) (out [3][3]float32) {
	for i := range 3 {
		for j := range 3 {
			out[i][j] = float32(m[i][j])
		}
	}
	return out
}

// look is what one photo is developed with.
type look struct {
	m     [3][3]float64 // linear sRGB to the camera's, exposure included
	curve [knots]float64
	table [hues][sats][3]float32 // hue shift in degrees, saturation and value scales
	body  body
	toned []float32 // the curve as tone reads it
	slope float32   // below the first knot
}

// Picture is img, whole or at half the size, developed as its camera would,
// 8-bit sRGB, cut to the camera's frame, or else to the shape of preview, the JPEG the camera
// embedded, which it is fitted to; long pixels on its long side at most, 0
// for all. It is not turned: the preview is not either.
func Picture(img *Image, meta *Meta, preview image.Image, long int) *image.RGBA {
	frame, ok := cameraFrame(img, meta)
	if !ok {
		frame = cut(img.Width, img.Height, preview.Bounds().Dx(), preview.Bounds().Dy())
	}
	if w, h := frame.Dx(), frame.Dy(); long > 0 && max(w, h) > long {
		// Developed at the size asked: a fifth of the work at 2560 of 24 MP.
		img, frame = scaled(img, frame, long)
	}
	return develop(img, frame, fit(img, frame, preview, meta))
}

// scaled is frame of img with long pixels on its long side, each the mean of
// the light it covers.
func scaled(img *Image, frame image.Rectangle, long int) (*Image, image.Rectangle) {
	w, h := frame.Dx(), frame.Dy()
	sw, sh := long, max(1, int(math.Round(float64(long*h)/float64(w))))
	if h > w {
		sw, sh = max(1, int(math.Round(float64(long*w)/float64(h)))), long
	}
	px := shrunk(w, h, sw, sh, img.cores, func(y int, dst []float32) {
		row := img.Pix[((frame.Min.Y+y)*img.Width+frame.Min.X)*3:]
		for i := range dst {
			dst[i] = float32(row[i])
		}
	})
	out := &Image{Width: sw, Height: sh, Pix: make([]uint16, len(px)), cores: img.cores}
	for i, v := range px {
		out.Pix[i] = uint16(min(max(v+0.5, 0), 65535))
	}
	return out, image.Rect(0, 0, sw, sh)
}

// cut is the middle of a w×h picture with the shape of pw×ph.
func cut(w, h, pw, ph int) image.Rectangle {
	cw, ch := w, h
	if pw*h > ph*w {
		ch = int(math.Round(float64(w) * float64(ph) / float64(pw)))
	} else {
		cw = int(math.Round(float64(h) * float64(pw) / float64(ph)))
	}
	return image.Rect((w-cw)/2, (h-ch)/2, (w-cw)/2+cw, (h-ch)/2+ch)
}

// fit is the look of the photo: the curve, the matrix and the hue table that
// bring it to the preview, near the global ones where the photo says little.
func fit(img *Image, frame image.Rectangle, preview image.Image, meta *Meta) look {
	w, h := frame.Dx(), frame.Dy()
	sw, sh := fitLong, max(1, int(math.Round(float64(fitLong*h)/float64(w))))
	if h > w {
		sw, sh = max(1, int(math.Round(float64(fitLong*w)/float64(h)))), fitLong
	}
	lin := shrunk(w, h, sw, sh, img.cores, func(y int, dst []float32) {
		row := img.Pix[((frame.Min.Y+y)*img.Width+frame.Min.X)*3:]
		for i := range dst {
			dst[i] = float32(row[i]) / 65535
		}
	})
	for i := 0; i < len(lin); i += 3 {
		lin[i], lin[i+1], lin[i+2] = apply3(proToSRGB, lin[i], lin[i+1], lin[i+2])
	}
	rgba := image.NewRGBA(preview.Bounds())
	draw.Draw(rgba, rgba.Bounds(), preview, preview.Bounds().Min, draw.Src)
	pw, ph := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	jpg := shrunk(pw, ph, sw, sh, img.cores, func(y int, dst []float32) {
		row := rgba.Pix[y*rgba.Stride:]
		for x := range pw {
			for c := range 3 {
				dst[3*x+c] = float32(row[4*x+c]) / 255
			}
		}
	})
	rgba = nil

	gain := math.Pow(2, ev0-meta.ExposureShift)
	vig, gains := shadingOf(sw, sh), make([]float32, sw)
	// The edges are left out, as the prototype did: a frame a pixel off.
	const edge = 8
	var lx, ly []float64 // linear light with the shading, and the preview's encoded value
	for y := edge; y < sh-edge; y++ {
		vig.row(y, gains)
		for x := edge; x < sw-edge; x++ {
			i, v := 3*(y*sw+x), gain*float64(gains[x])
			for c := range 3 {
				lx = append(lx, float64(lin[i+c])*v)
				ly = append(ly, float64(jpg[i+c]))
			}
		}
	}

	l := look{m: baseM, curve: baseCurve, body: otherBody}
	if b, ok := bodies[meta.Model]; ok {
		l.body = b
	}
	for range fitIterations {
		l.curve = fitCurve(lx, ly, l.m)
		l.m = fitMatrix(lx, ly, l.curve)
	}
	table := fitTable(lx, ly, l.m, l.curve)
	for a := range hues {
		for b := range sats {
			for c := range 3 {
				l.table[a][b][c] = float32(table[a][b][c])
			}
		}
	}
	l.tabled()
	// From here on m takes the raw's linear sRGB, exposure included.
	for r := range 3 {
		for c := range 3 {
			l.m[r][c] *= gain
		}
	}
	return l
}

// fitCurve is the median encoded value of the preview at each knot of our
// light through m, all channels together; where the photo has too little,
// the global curve, joined to it.
func fitCurve(lx, ly []float64, m [3][3]float64) [knots]float64 {
	const bins = 2048
	hist := make([][]int32, knots)
	count := make([]int, knots)
	for i := 0; i < len(lx); i += 3 {
		r, g, b := apply3d(m, lx[i], lx[i+1], lx[i+2])
		for c, v := range [3]float64{r, g, b} {
			f := (math.Log2(max(v, 1e-6)) - firstKnot) / knotStep
			y := min(max(int(ly[i+c]*bins), 0), bins-1)
			// Each value counts for the knots less than a step from it.
			k0 := int(math.Floor(f))
			for k := k0; k <= k0+1; k++ {
				if k >= 0 && k < knots && math.Abs(f-float64(k)) < 1 {
					if hist[k] == nil {
						hist[k] = make([]int32, bins)
					}
					hist[k][y]++
					count[k]++
				}
			}
		}
	}
	var curve [knots]float64
	var ok []int
	enough := max(50, len(lx)/1000)
	for k := range knots {
		if count[k] <= enough {
			continue
		}
		curve[k] = median(hist[k], count[k], bins)
		if firstKnot+float64(k)*knotStep >= -6.5 && curve[k] < 0.97 {
			ok = append(ok, k)
		}
	}
	if len(ok) == 0 {
		return baseCurve
	}
	// Between trusted knots, straight lines; outside, the global curve.
	lo, hi := ok[0], ok[len(ok)-1]
	for j := 0; j+1 < len(ok); j++ {
		a, b := ok[j], ok[j+1]
		for k := a + 1; k < b; k++ {
			curve[k] = curve[a] + (curve[b]-curve[a])*float64(k-a)/float64(b-a)
		}
	}
	for k := range lo {
		curve[k] = baseCurve[k] * (curve[lo] / max(baseCurve[lo], 1e-6))
	}
	for k := hi + 1; k < knots; k++ {
		curve[k] = min(1, curve[hi]+baseCurve[k]-baseCurve[hi])
	}
	top := 0.0
	for k := range curve {
		top = max(top, curve[k])
		curve[k] = min(top, 1)
	}
	return curve
}

// median of count values in a histogram of bins over 0 to 1, within a bin.
func median(hist []int32, count, bins int) float64 {
	half := float64(count) / 2
	seen := 0.0
	for i, n := range hist {
		if seen+float64(n) >= half && n > 0 {
			return (float64(i) + (half-seen)/float64(n)) / float64(bins)
		}
		seen += float64(n)
	}
	return 1
}

// fitMatrix is the 3×3 that takes our light to the preview's, read back
// through the curve, weighted toward the dark, pulled toward the global one
// so a scene of few colours cannot skew it, and scaled to leave exposure to
// the curve.
func fitMatrix(lx, ly []float64, curve [knots]float64) [3][3]float64 {
	inverse := inverseCurve(curve)
	var ata, atb [3][3]float64
	n, sq := 0, 0.0
	for i := 0; i < len(lx); i += 3 {
		j := ly[i : i+3]
		if min(j[0], j[1], j[2]) <= 0.01 || max(j[0], j[1], j[2]) >= 0.94 {
			continue
		}
		t := [3]float64{inverse(j[0]), inverse(j[1]), inverse(j[2])}
		w := 1 / ((t[0]+t[1]+t[2])/3 + 0.02)
		a := [3]float64{lx[i] * w, lx[i+1] * w, lx[i+2] * w}
		for r := range 3 {
			for c := range 3 {
				ata[r][c] += a[r] * a[c]
				atb[r][c] += a[r] * t[c] * w
			}
			sq += a[r] * a[r]
		}
		n++
	}
	if n == 0 {
		return baseM
	}
	// Ridge: λ² (n·3 values' mean square, times 0.05 of the rows) on the diagonal.
	lam2 := 0.05 * float64(n) * sq / float64(3*n)
	for r := range 3 {
		ata[r][r] += lam2
		for c := range 3 {
			atb[r][c] += lam2 * baseM[c][r]
		}
	}
	x := mul3(inv3(ata), atb)
	var m [3][3]float64
	sum := 0.0
	for r := range 3 {
		for c := range 3 {
			m[r][c] = x[c][r]
			sum += m[r][c]
		}
	}
	for r := range 3 {
		for c := range 3 {
			m[r][c] /= sum / 3
		}
	}
	return m
}

// inverseCurve is linear light from an encoded value, through curve.
func inverseCurve(curve [knots]float64) func(float64) float64 {
	var up [knots]float64
	top := math.Inf(-1)
	for k := range curve {
		top = max(top, curve[k]+float64(k)*1e-6)
		up[k] = top
	}
	return func(y float64) float64 {
		return math.Exp2(firstKnot + knotStep*interp(y, up[:]))
	}
}

// interp is where y falls among the increasing xs, as a fractional index,
// held at the ends.
func interp(y float64, xs []float64) float64 {
	if y <= xs[0] {
		return 0
	}
	n := len(xs)
	if y >= xs[n-1] {
		return float64(n - 1)
	}
	lo, hi := 0, n-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if xs[mid] <= y {
			lo = mid
		} else {
			hi = mid
		}
	}
	return float64(lo) + (y-xs[lo])/(xs[hi]-xs[lo])
}

// fitTable is the hue and saturation table that takes our colours to the
// preview's, in ProPhoto HSV: per bin the median shift, shrunk toward none
// where a bin has few pixels, smoothed along hue, greys left alone.
func fitTable(lx, ly []float64, m [3][3]float64, curve [knots]float64) (table [hues][sats][3]float64) {
	inverse := inverseCurve(curve)
	type shift struct{ dh, ds, dv []float64 }
	var bins [hues][sats]shift
	for i := 0; i < len(lx); i += 3 {
		j := ly[i : i+3]
		if max(j[0], j[1], j[2]) >= 0.97 {
			continue
		}
		r, g, b := apply3d(m, lx[i], lx[i+1], lx[i+2])
		ho, so, vo := hsv(apply3d(srgbToPro, r, g, b))
		ht, st, vt := hsv(apply3d(srgbToPro, inverse(j[0]), inverse(j[1]), inverse(j[2])))
		if vo <= 1e-3 || vt <= 1e-3 || so <= 0.02 || st <= 0.01 {
			continue
		}
		hb := int(math.RoundToEven(ho*hues/6)) % hues
		sb := min(max(int(math.RoundToEven(so*(sats-1))), 0), sats-1)
		s := &bins[hb][sb]
		s.dh = append(s.dh, (mod(ht-ho+3, 6)-3)*60)
		s.ds = append(s.ds, st/so)
		s.dv = append(s.dv, vt/vo)
	}
	for a := range hues {
		for b := range sats {
			table[a][b] = [3]float64{0, 1, 1}
			s := bins[a][b]
			c := float64(len(s.dh))
			if c < 30 {
				continue
			}
			k := c / (c + 100)
			table[a][b] = [3]float64{k * exactMedian(s.dh), 1 + k*(exactMedian(s.ds)-1), 1 + k*(exactMedian(s.dv)-1)}
		}
	}
	smooth := table
	for a := range hues {
		for b := range sats {
			for c := range 3 {
				smooth[a][b][c] = (table[(a+hues-1)%hues][b][c] + 2*table[a][b][c] + table[(a+1)%hues][b][c]) / 4
			}
		}
	}
	for a := range hues {
		smooth[a][0] = [3]float64{0, 1, 1}
		for b := range sats {
			t := &smooth[a][b]
			t[0], t[1], t[2] = min(max(t[0], -25), 25), min(max(t[1], 0.6), 1.6), min(max(t[2], 0.7), 1.4)
		}
	}
	return smooth
}

func exactMedian(v []float64) float64 {
	s := append([]float64(nil), v...)
	slices.Sort(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

// develop is img in frame through l, a strip of rows a core at a time.
func develop(img *Image, frame image.Rectangle, l look) *image.RGBA {
	w, h := frame.Dx(), frame.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	d := developer{img: img, frame: frame, look: l, vig: shadingOf(w, h)}
	// The noise is measured first, on an eighth of the strips, as the
	// smoothing is set by it.
	strips := (h + stripRows - 1) / stripRows
	var mu sync.Mutex
	var luma, chroma []float64
	parallel(strips, img.cores, func(s int, buf *buffers) {
		if s%8 != 0 && strips >= 8 {
			return
		}
		y0, y1 := s*stripRows, min(h, (s+1)*stripRows)
		pl := d.planes(y0, y1, noiseHalo, buf)
		a, b := blockNoise(pl.y, w, pl.rows, y0-pl.from, y1-pl.from), blockNoise(pl.cr, w, pl.rows, y0-pl.from, y1-pl.from)
		mu.Lock()
		luma, chroma = append(luma, a...), append(chroma, b...)
		mu.Unlock()
	})
	d.sigma, d.chromaSigma = percentile10(luma), percentile10(chroma)
	d.chromaBlur = l.body.chromaK * math.Pow(d.chromaSigma/0.004, chromaP)
	halo := 2 + 3*max(boxRadius(1), boxRadius(d.chromaBlur), boxRadius(lumaBlur)+boxRadius(sharpBlur))
	parallel(strips, img.cores, func(s int, buf *buffers) {
		y0, y1 := s*stripRows, min(h, (s+1)*stripRows)
		d.finish(d.planes(y0, y1, halo, buf), y0, y1, out, buf)
	})
	return out
}

const lumaBlur, sharpBlur = 0.7, 0.8

// fade is e^-t: past fadeEnd it is under 1e-7.
var fade = tabled(func(t float64) float64 { return math.Exp(-t) }, fadeEnd, 64)

const fadeEnd = 16

// table is a function read from its values at steps a unit, from 0 to end
// and held past it: a power or an exponential costs as much as a pixel's
// whole development.
type table struct {
	at0   []float32
	steps float32
}

func tabled(f func(float64) float64, end, steps int) table {
	t := table{make([]float32, end*steps+2), float32(steps)}
	for i := range t.at0 {
		t.at0[i] = float32(f(float64(i) / float64(steps)))
	}
	return t
}

func (t table) at(x float32) float32 {
	f := min(x*t.steps, float32(len(t.at0)-2))
	i := int(f)
	return t.at0[i] + (t.at0[i+1]-t.at0[i])*(f-float32(i))
}

// noiseHalo is the rows a strip is made with above and below for the noise:
// the hot pixels' 2 and the high-pass's blur.
var noiseHalo = 2 + 3*boxRadius(1)

type developer struct {
	img                *Image
	frame              image.Rectangle
	look               look
	vig                shading
	sigma, chromaSigma float64
	chromaBlur         float64
}

// buffers are a core's, kept from one strip to the next.
type buffers struct{ planes [6][]float32 }

func (b *buffers) plane(i, n int) []float32 {
	if cap(b.planes[i]) < n {
		b.planes[i] = make([]float32, n)
	}
	return b.planes[i][:n]
}

// planes are rows from to from+rows of the photo as luma and two chroma
// differences, toned, before any smoothing.
type planes struct {
	from, rows int
	y, cb, cr  []float32
}

// planes makes rows y0-halo to y1+halo, as far as the photo goes.
func (d *developer) planes(y0, y1, halo int, buf *buffers) planes {
	w, h := d.frame.Dx(), d.frame.Dy()
	from, to := max(0, y0-halo), min(h, y1+halo)
	n := (to - from) * w
	r, g, b := buf.plane(0, n), buf.plane(1, n), buf.plane(2, n)
	l := &d.look
	m := [3][3]float32{}
	full := mul3(l.m, proToSRGB)
	for i := range 3 {
		for j := range 3 {
			m[i][j] = float32(full[i][j] / 65535)
		}
	}
	gains := buf.plane(3, w)
	for y := from; y < to; y++ {
		d.vig.row(y, gains)
		row := d.img.Pix[((d.frame.Min.Y+y)*d.img.Width+d.frame.Min.X)*3:]
		o := (y - from) * w
		for x := range w {
			p0, p1, p2 := float32(row[3*x]), float32(row[3*x+1]), float32(row[3*x+2])
			v := gains[x]
			cr, cg, cb := (m[0][0]*p0+m[0][1]*p1+m[0][2]*p2)*v, (m[1][0]*p0+m[1][1]*p1+m[1][2]*p2)*v, (m[2][0]*p0+m[2][1]*p1+m[2][2]*p2)*v
			r[o+x], g[o+x], b[o+x] = l.hueTable(cr, cg, cb)
		}
	}
	yy, cb, cr := buf.plane(3, n), buf.plane(4, n), buf.plane(5, n)
	hotPixels(r, g, b, yy, w, to-from)
	for i := range n {
		R, G, B := compress(r[i], g[i], b[i])
		R, G, B = l.tone(R), l.tone(G), l.tone(B)
		Y := 0.299*R + 0.587*G + 0.114*B
		yy[i], cb[i], cr[i] = Y, B-Y, R-Y
	}
	return planes{from: from, rows: to - from, y: yy, cb: cb, cr: cr}
}

// finish smooths the noise, sharpens and writes rows y0 to y1 into out.
func (d *developer) finish(p planes, y0, y1 int, out *image.RGBA, buf *buffers) {
	w := d.frame.Dx()
	b := d.look.body
	tmp, blur := buf.plane(0, len(p.y)), buf.plane(1, len(p.y))
	if d.chromaBlur >= 0.3 {
		gauss(p.cb, tmp, w, p.rows, d.chromaBlur)
		gauss(p.cr, tmp, w, p.rows, d.chromaBlur)
	}
	if b.luma > 0 {
		// Each pixel toward a small blur of itself, where the step is within the noise.
		copy(blur, p.y)
		gauss(blur, tmp, w, p.rows, lumaBlur)
		k := float32(gentle * b.luma * math.Pow(d.sigma/0.003, 0.6) * d.sigma)
		for i, v := range p.y {
			if e := (v - blur[i]) / (k + 1e-6); e*e < fadeEnd {
				p.y[i] = v + fade.at(e*e)*(blur[i]-v)
			}
		}
	}
	copy(blur, p.y)
	gauss(blur, tmp, w, p.rows, sharpBlur)
	amount := float32(gentle * b.sharp)
	for y := y0; y < y1; y++ {
		o := (y - p.from) * w
		px := out.Pix[y*out.Stride:]
		for x := range w {
			i := o + x
			Y := p.y[i] + amount*(p.y[i]-blur[i])
			cb, cr := p.cb[i], p.cr[i]
			px[4*x] = byte8(Y + cr)
			px[4*x+1] = byte8(Y - (0.299*cr+0.114*cb)/0.587)
			px[4*x+2] = byte8(Y + cb)
			px[4*x+3] = 255
		}
	}
}

func byte8(v float32) byte {
	return byte(min(max(v, 0), 1)*255 + 0.5)
}

// parallel does n strips on every core, or on cores of them when not 0,
// each with its own buffers. A panic in one is handed to the caller, who can
// recover it.
func parallel(n, cores int, do func(strip int, buf *buffers)) {
	var wg sync.WaitGroup
	var next sync.Mutex
	var failed atomic.Value
	i := 0
	workers := min(n, runtime.GOMAXPROCS(0))
	if cores > 0 {
		workers = min(workers, cores)
	}
	for range workers {
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					failed.Store(fmt.Sprint(r))
				}
			}()
			var buf buffers
			for {
				next.Lock()
				s := i
				i++
				next.Unlock()
				if s >= n {
					return
				}
				do(s, &buf)
			}
		})
	}
	wg.Wait()
	if r := failed.Load(); r != nil {
		panic(r)
	}
}

// shading is the lens shading's gain across a w×h picture.
type shading struct {
	across   []float64 // the square of each column's distance from the middle
	middle   float64   // of the rows
	diagonal float64   // half the diagonal, squared
}

func shadingOf(w, h int) shading {
	s := shading{across: make([]float64, w), middle: float64(h) / 2, diagonal: float64(h*h)/4 + float64(w*w)/4}
	for x := range w {
		d := float64(x) - float64(w)/2
		s.across[x] = d * d
	}
	return s
}

// row is the gain at each pixel of row y, in gains.
func (s shading) row(y int, gains []float32) {
	dy := float64(y) - s.middle
	for x, dx2 := range s.across {
		r2 := (dx2 + dy*dy) / s.diagonal
		gains[x] = float32(1 + shadingA*r2 + shadingB*r2*r2)
	}
}

// tone is the curve, on linear light, straight to black below its first knot.
func (l *look) tone(v float32) float32 {
	if v < toneFirst {
		return v * l.slope
	}
	i := (math.Float32bits(v) - toneFirstBits) >> toneShift
	if i >= uint32(len(l.toned)-1) {
		return l.toned[len(l.toned)-1]
	}
	lo := math.Float32frombits(toneFirstBits + i<<toneShift)
	f := (v - lo) / lo * (1 << (23 - toneShift))
	return l.toned[i] + (l.toned[i+1]-l.toned[i])*f
}

// The curve is read from a table of 1024 values a stop, by the bits of a
// float: its exponent and the first 10 of its mantissa.
const toneShift = 13

var (
	toneFirst     = float32(math.Exp2(firstKnot))
	toneFirstBits = math.Float32bits(toneFirst)
)

// tabled fills the table tone reads.
func (l *look) tabled() {
	stops := int(knotStep * (knots - 1))
	l.toned = make([]float32, stops<<(23-toneShift)+1)
	for i := range l.toned {
		v := math.Float32frombits(toneFirstBits + uint32(i)<<toneShift)
		f := min((math.Log2(float64(v))-firstKnot)/knotStep, knots-1)
		k := min(int(f), knots-2)
		l.toned[i] = float32(l.curve[k] + (l.curve[k+1]-l.curve[k])*(f-float64(k)))
	}
	l.slope = float32(l.curve[0]) / toneFirst
}

// hueTable moves a colour of linear sRGB by the table, in ProPhoto HSV.
func (l *look) hueTable(r, g, b float32) (float32, float32, float32) {
	r, g, b = max(r, 0), max(g, 0), max(b, 0)
	p := &srgbToPro32
	h, s, v := hsv(p[0][0]*r+p[0][1]*g+p[0][2]*b, p[1][0]*r+p[1][1]*g+p[1][2]*b, p[2][0]*r+p[2][1]*g+p[2][2]*b)
	if s == 0 {
		// The table leaves greys alone.
		return r, g, b
	}
	hf := h * hues / 6
	h0 := int(hf)
	fh := hf - float32(h0)
	h0 %= hues
	h1 := (h0 + 1) % hues
	sf := s * (sats - 1)
	s0 := min(int(sf), sats-2)
	fs := min(sf-float32(s0), 1)
	t := &l.table
	var acc [3]float32
	for c := range 3 {
		acc[c] = (1-fh)*((1-fs)*t[h0][s0][c]+fs*t[h0][s0+1][c]) + fh*((1-fs)*t[h1][s0][c]+fs*t[h1][s0+1][c])
	}
	h += acc[0] / 60
	if h < 0 {
		h += 6
	} else if h >= 6 {
		h -= 6
	}
	R, G, B := rgb(h, min(max(s*acc[1], 0), 1), v*acc[2])
	q := &proToSRGB32
	return q[0][0]*R + q[0][1]*G + q[0][2]*B, q[1][0]*R + q[1][1]*G + q[1][2]*B, q[2][0]*R + q[2][1]*G + q[2][2]*B
}

// hsv is hue (0 to 6), saturation and value.
func hsv[F float32 | float64](r, g, b F) (h, s, v F) {
	v = max(r, g, b)
	d := v - min(r, g, b)
	if d > 0 {
		switch v {
		case r:
			if h = (g - b) / d; h < 0 {
				h += 6
			}
		case g:
			h = (b-r)/d + 2
		default:
			h = (r-g)/d + 4
		}
	}
	if v > 0 {
		s = d / v
	}
	return h, s, v
}

// rgb is the colour of hue h, 0 to 6, saturation s and value v.
func rgb[F float32 | float64](h, s, v F) (r, g, b F) {
	i := int(h)
	f := h - F(i)
	p, q, t := v*(1-s), v*(1-s*f), v*(1-s*(1-f))
	switch i % 6 {
	case 0:
		return v, t, p
	case 1:
		return q, v, p
	case 2:
		return p, v, t
	case 3:
		return p, q, v
	case 4:
		return t, p, v
	}
	return v, p, q
}

func mod(a, b float64) float64 {
	m := math.Mod(a, b)
	if m < 0 {
		m += b
	}
	return m
}

// compress brings a colour out of the gamut back in, ACES-style: each
// channel's distance from the largest, over 0.8, compressed so that 1.25
// lands on 1.
func compress(r, g, b float32) (float32, float32, float32) {
	ach := max(r, g, b)
	a := max(float32(math.Abs(float64(ach))), 1e-6)
	if (ach-min(r, g, b))/a <= compressFrom {
		return r, g, b
	}
	in := func(x float32) float32 {
		d := (ach - x) / a
		if d <= compressFrom {
			return x
		}
		return ach - (compressFrom+compressScale*squeezed.at(float32((d-compressFrom)/compressScale)))*a
	}
	return in(r), in(g), in(b)
}

const compressFrom, compressTo, compressPower = 0.8, 1.25, 1.2

var compressScale = float32((compressTo - compressFrom) / math.Pow(math.Pow((1-compressFrom)/(compressTo-compressFrom), -compressPower)-1, 1/compressPower))

// squeezed is e / (1 + e^p)^(1/p), which nears 1: at 64 it is 1 - 2e-3.
var squeezed = tabled(func(e float64) float64 {
	return e / math.Pow(1+math.Pow(e, compressPower), 1/compressPower)
}, 64, 64)

// hotPixels replaces each pixel far brighter than all around it, a single
// one, with the mean of the ring two pixels away.
func hotPixels(r, g, b, lum []float32, w, h int) {
	for i := range lum {
		lum[i] = max(r[i], g[i], b[i])
	}
	type fix struct {
		i       int
		r, g, b float32
	}
	var fixes []fix
	ring := make([]int, 0, 16)
	near := make([]int, 0, 8)
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			switch max(abs(dy), abs(dx)) {
			case 2:
				ring = append(ring, dy*w+dx)
			case 1:
				near = append(near, dy*w+dx)
			}
		}
	}
	for y := 2; y < h-2; y++ {
	next:
		for x := 2; x < w-2; x++ {
			i := y*w + x
			// Brighter than three times the whole ring, and than its eight neighbours together.
			top := (lum[i] - 0.05) / 3
			for _, o := range ring {
				if lum[i+o] >= top {
					continue next
				}
			}
			var sum float32
			for _, o := range near {
				sum += lum[i+o]
			}
			if sum/8 >= 0.35*lum[i] {
				continue
			}
			var sr, sg, sb float32
			for _, o := range ring {
				sr, sg, sb = sr+r[i+o], sg+g[i+o], sb+b[i+o]
			}
			fixes = append(fixes, fix{i, sr / 16, sg / 16, sb / 16})
		}
	}
	for _, f := range fixes {
		r[f.i], g[f.i], b[f.i] = f.r, f.g, f.b
	}
}

func abs(v int) int { return max(v, -v) }

// boxRadius is that of the three box blurs that make a Gaussian of sigma.
func boxRadius(sigma float64) int {
	return max(1, int(math.RoundToEven(math.Sqrt(12*sigma*sigma/3+1)/2-0.5)))
}

// gauss blurs the w×h plane p in place, three box blurs each way, the edges
// held; tmp is as large.
func gauss(p, tmp []float32, w, h int, sigma float64) {
	r := boxRadius(sigma)
	for range 3 {
		boxDown(p, tmp, w, h, r)
		boxAcross(tmp, p, w, h, r)
	}
}

// boxAcross is each row's mean over 2r+1 pixels.
func boxAcross(src, dst []float32, w, h, r int) {
	n := 1 / float32(2*r+1)
	for y := range h {
		row, out := src[y*w:(y+1)*w], dst[y*w:(y+1)*w]
		var sum float32
		for x := -r; x <= r; x++ {
			sum += row[min(max(x, 0), w-1)]
		}
		x := 0
		// The ends held, the middle straight.
		for ; x < w && x < r; x++ {
			out[x] = sum * n
			sum += row[min(x+r+1, w-1)] - row[0]
		}
		if x == r && w > 2*r+1 {
			ahead := row[2*r+1:]
			behind, o := row[:len(ahead)], out[r:r+len(ahead)]
			for k, v := range ahead {
				o[k] = sum * n
				sum += v - behind[k]
			}
			x += len(ahead)
		}
		for ; x < w; x++ {
			out[x] = sum * n
			sum += row[w-1] - row[max(x-r, 0)]
		}
	}
}

// boxDown is each column's mean over 2r+1 rows, a row at a time.
func boxDown(src, dst []float32, w, h, r int) {
	n := 1 / float32(2*r+1)
	sum := make([]float32, w)
	for y := -r; y <= r; y++ {
		row := src[min(max(y, 0), h-1)*w:][:w]
		for x, v := range row {
			sum[x] += v
		}
	}
	for y := range h {
		out := dst[y*w : (y+1)*w]
		in, gone := src[min(y+r+1, h-1)*w:][:w], src[max(y-r, 0)*w:][:w]
		for x := range out {
			out[x] = sum[x] * n
			sum[x] += in[x] - gone[x]
		}
	}
}

// blockNoise is the spread of the high-pass of rows y0 to y1 of p (rows tall),
// every other pixel, in blocks of 64: in the flattest, it is the noise. The
// blocks are the photo's, from its first row.
func blockNoise(p []float32, w, rows, y0, y1 int) []float64 {
	hp := make([]float32, len(p))
	tmp := make([]float32, len(p))
	copy(hp, p)
	gauss(hp, tmp, w, rows, 1)
	for i := range hp {
		hp[i] = p[i] - hp[i]
	}
	var out []float64
	for by := y0; by+64 <= y1; by += 64 {
		for bx := 0; bx+64 <= w; bx += 64 {
			sum, sq := 0.0, 0.0
			for y := by; y < by+64; y += 2 {
				for x := bx; x < bx+64; x += 2 {
					v := float64(hp[y*w+x])
					sum, sq = sum+v, sq+v*v
				}
			}
			mean := sum / 1024
			out = append(out, math.Sqrt(max(sq/1024-mean*mean, 0)))
		}
	}
	return out
}

func percentile10(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	slices.Sort(v)
	at := 0.1 * float64(len(v)-1)
	i := int(at)
	if i+1 >= len(v) {
		return v[i]
	}
	return v[i] + (v[i+1]-v[i])*(at-float64(i))
}

// shrunk is the area average of a w×h picture, three floats a pixel that
// row puts in its dst, at sw×sh, on cores (parallel).
func shrunk(w, h, sw, sh, cores int, row func(y int, dst []float32)) []float32 {
	xs, ys := shares(w, sw), shares(h, sh)
	// A band of rows a core, each into the sums of the rows it reaches.
	type part struct {
		from int
		sums []float32
	}
	bands := min(h, runtime.GOMAXPROCS(0))
	parts := make([]part, bands)
	parallel(bands, cores, func(band int, _ *buffers) {
		y0, y1 := h*band/bands, h*(band+1)/bands
		from := ys[y0].to
		sums := make([]float32, (ys[y1-1].next-from+1)*sw*3)
		line, across := make([]float32, w*3), make([]float32, sw*3)
		for y := y0; y < y1; y++ {
			row(y, line)
			clear(across)
			for x, s := range xs {
				px := line[3*x : 3*x+3]
				to := across[3*s.to : 3*s.to+3]
				if s.share == 1 {
					to[0], to[1], to[2] = to[0]+px[0], to[1]+px[1], to[2]+px[2]
					continue
				}
				next := across[3*s.next : 3*s.next+3]
				for c, v := range px {
					to[c] += v * s.share
					next[c] += v * (1 - s.share)
				}
			}
			for _, part := range [2]struct {
				to    int
				share float32
			}{{ys[y].to, ys[y].share}, {ys[y].next, 1 - ys[y].share}} {
				if part.share == 0 {
					continue
				}
				o := sums[3*(part.to-from)*sw:][:len(across)]
				for i, v := range across {
					o[i] += v * part.share
				}
			}
		}
		parts[band] = part{from, sums}
	})
	out := make([]float32, sw*sh*3)
	for _, p := range parts {
		o := out[3*p.from*sw:]
		for i, v := range p.sums {
			o[i] += v
		}
	}
	scale := float32(float64(sw) / float64(w) * float64(sh) / float64(h))
	for i := range out {
		out[i] *= scale
	}
	return out
}

// share is where a source pixel falls on n pixels fewer: share of it in to,
// the rest in next.
type share struct {
	to, next int
	share    float32
}

func shares(from, n int) []share {
	f := float64(n) / float64(from)
	s := make([]share, from)
	for x := range s {
		a, b := float64(x)*f, float64(x+1)*f
		i := min(int(a), n-1)
		s[x] = share{i, i, 1}
		if end := float64(i + 1); b > end && i+1 < n {
			s[x] = share{i, i + 1, float32((end - a) / f)}
		}
	}
	return s
}

func apply3(m [3][3]float64, r, g, b float32) (float32, float32, float32) {
	R, G, B := apply3d(m, float64(r), float64(g), float64(b))
	return float32(R), float32(G), float32(B)
}

func apply3d(m [3][3]float64, r, g, b float64) (float64, float64, float64) {
	return m[0][0]*r + m[0][1]*g + m[0][2]*b, m[1][0]*r + m[1][1]*g + m[1][2]*b, m[2][0]*r + m[2][1]*g + m[2][2]*b
}

func mul3(a, b [3][3]float64) (c [3][3]float64) {
	for i := range 3 {
		for j := range 3 {
			for k := range 3 {
				c[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return c
}

func inv3(m [3][3]float64) (r [3][3]float64) {
	det := m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) - m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) + m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
	for i := range 3 {
		for j := range 3 {
			// The cofactor of (j, i), over the determinant.
			a, b := (j+1)%3, (j+2)%3
			c, d := (i+1)%3, (i+2)%3
			r[i][j] = (m[a][c]*m[b][d] - m[a][d]*m[b][c]) / det
		}
	}
	return r
}
