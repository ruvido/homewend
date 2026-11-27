// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"errors"
	"fmt"
	"image"
	"math"
	"slices"
)

// A film negative photographed on a light source, decoded in the camera's
// own RGB with no white balance (Negative), is turned into a print here. All
// in density, -log10 of the light, per channel: there a white balance is a
// subtraction and a contrast a multiplication. The film base, its orange
// mask, becomes black; the densest highlight white. Tried on a roll of C-41
// shot with an X-T3 in a black holder (26/11/2026).

const (
	// thinnest and densest are the shares of a frame's blocks, by mean
	// density, that are its film base and its white.
	thinnest, densest = 0.005, 0.995
	// blockSide is the side of the blocks a frame is sampled in: no single
	// noisy pixel or speck of dust sets a bound.
	blockSide = 4
	// inset is how much of the frame found is left out at each side, for
	// the analysis and the print's crop: the lit run reaches 1 to 1.8 % past
	// the picture into the holder's edge (three frames, 27/11).
	inset = 0.02
	// printK is the density range of the paper: a density step of the film
	// is an exposure step of the print.
	printK = 2.2
	// The auto balance: blocks of mid density no further than greyTol from
	// grey are taken as grey, when there are greyMin of them, and the frame
	// is moved greyStrength of the way there. Fully, it overcorrected.
	greyTol, greyStrength, greyMin = 0.08, 0.5, 0.02
	// thinLight is the least light read, so the density of black is finite.
	thinLight = 1e-5
)

// Bounds are a negative's film base and white, in density, per channel.
type Bounds struct{ Base, White [3]float64 }

// String is how the bounds are kept.
func (b Bounds) String() string {
	return fmt.Sprintf("%.4f %.4f %.4f %.4f %.4f %.4f", b.Base[0], b.Base[1], b.Base[2], b.White[0], b.White[1], b.White[2])
}

// ParseBounds reads bounds as String writes them.
func ParseBounds(s string) (Bounds, error) {
	var b Bounds
	if _, err := fmt.Sscanf(s, "%g %g %g %g %g %g", &b.Base[0], &b.Base[1], &b.Base[2], &b.White[0], &b.White[1], &b.White[2]); err != nil {
		return b, fmt.Errorf("negative bounds %q: %w", s, err)
	}
	for c := range 3 {
		if !(b.White[c] > b.Base[c]) {
			return b, errors.New("negative bounds: white not past the base")
		}
	}
	return b, nil
}

// MedianBounds are the bounds of photos of one film: the median of theirs,
// per channel, so that one damaged frame cannot move them. Of two, their mean.
func MedianBounds(all []Bounds) Bounds {
	var b Bounds
	for c := range 3 {
		var base, white []float64
		for _, a := range all {
			base, white = append(base, a.Base[c]), append(white, a.White[c])
		}
		b.Base[c], b.White[c] = exactMedian(base), exactMedian(white)
	}
	return b
}

// densityOf is the density of each 16-bit sample.
var densityOf = func() (t [65536]float32) {
	for v := range t {
		t[v] = float32(-math.Log10(max(float64(v)/65535, thinLight)))
	}
	return t
}()

// Analyse is the bounds of the negative img, decoded as one: those of its
// film inside the holder, as colours. The base is the median, per channel,
// of the thinnest blocks by mean density, the white that of the densest:
// a percentile of each channel alone would be set by whatever is extreme in
// that one (a streak in the red layer) and cast the whole frame.
//
// crop is the frame, inset, in shares of the print Positive
// makes of the same file (x0, y0, x1, y1), not turned: the holder and the
// film's edge are left out of the picture.
func Analyse(img *Image, meta *Meta) (b Bounds, crop [4]float64, err error) {
	whole := image.Rect(0, 0, img.Width, img.Height)
	frame := inside(frameOf(img, whole), inset)
	s := blocks(img, frame)
	if len(s) == 0 {
		return b, crop, errors.New("negative: no frame found")
	}
	crop = printShares(frame, img, meta)
	m := make([]float64, len(s))
	for i, b := range s {
		m[i] = (b[0] + b[1] + b[2]) / 3
	}
	sorted := slices.Sorted(slices.Values(m))
	lo, hi := sorted[int(thinnest*float64(len(m)-1))], sorted[int(math.Ceil(densest*float64(len(m)-1)))]
	for c := range 3 {
		var base, white []float64
		for i, v := range m {
			if v <= lo {
				base = append(base, s[i][c])
			}
			if v >= hi {
				white = append(white, s[i][c])
			}
		}
		b.Base[c], b.White[c] = exactMedian(base), exactMedian(white)
		if !(b.White[c] > b.Base[c]) {
			return b, crop, errors.New("negative: a frame of one density")
		}
	}
	return b, crop, nil
}

// printShares is r, of img, in shares of the print Positive makes of the
// same file: cut to the camera's frame, in the middle of the sensor.
// shortcut: a decode at half the size is taken as exactly half the sensor's
// sides; LibRaw rounds them, a pixel off at most.
func printShares(r image.Rectangle, img *Image, meta *Meta) [4]float64 {
	x0, y0 := float64(r.Min.X)/float64(img.Width), float64(r.Min.Y)/float64(img.Height)
	x1, y1 := float64(r.Max.X)/float64(img.Width), float64(r.Max.Y)/float64(img.Height)
	if f := meta.Frame; f.Width > 0 && f.Height > 0 {
		// The sensor's sides: twice a half decode's.
		scale := 1.0
		if img.Width < f.Width {
			scale = 2
		}
		sw, sh := scale*float64(img.Width), scale*float64(img.Height)
		ox, oy := (sw-float64(f.Width))/2, (sh-float64(f.Height))/2
		at := func(v, side, off, frame float64) float64 { return min(max((v*side-off)/frame, 0), 1) }
		x0, x1 = at(x0, sw, ox, float64(f.Width)), at(x1, sw, ox, float64(f.Width))
		y0, y1 = at(y0, sh, oy, float64(f.Height)), at(y1, sh, oy, float64(f.Height))
	}
	return [4]float64{x0, y0, x1, y1}
}

// inside is r with share of its sides left out at each one.
func inside(r image.Rectangle, share float64) image.Rectangle {
	dx, dy := int(float64(r.Dx())*share), int(float64(r.Dy())*share)
	return image.Rect(r.Min.X+dx, r.Min.Y+dy, r.Max.X-dx, r.Max.Y-dy)
}

// frameOf is the lit film inside the dark holder, within r: a row or a
// column is lit when its median log brightness is past the midpoint between
// the holder's (the 5th percentile) and the median; the longest lit run is
// the frame, not the neighbours' slivers at the sides. A picture with no
// holder is all one run, and only an inset keeps its edges out.
func frameOf(img *Image, r image.Rectangle) image.Rectangle {
	w, h := r.Dx(), r.Dy()
	light := make([]float32, w*h)
	for y := range h {
		row := img.Pix[((r.Min.Y+y)*img.Width+r.Min.X)*3:]
		for x := range w {
			px := row[3*x : 3*x+3]
			light[y*w+x] = float32(math.Log10((float64(px[0])+float64(px[1])+float64(px[2]))/(3*65535) + thinLight))
		}
	}
	rows, cols := make([]float64, h), make([]float64, w)
	line := make([]float64, max(w, h))
	for y := range h {
		for x := range w {
			line[x] = float64(light[y*w+x])
		}
		rows[y] = exactMedian(line[:w])
	}
	for x := range w {
		for y := range h {
			line[y] = float64(light[y*w+x])
		}
		cols[x] = exactMedian(line[:h])
	}
	y0, y1 := litRun(rows)
	x0, x1 := litRun(cols)
	return image.Rect(r.Min.X+x0, r.Min.Y+y0, r.Min.X+x1, r.Min.Y+y1)
}

// litRun is the longest run of profile past its midpoint.
func litRun(profile []float64) (from, to int) {
	sorted := slices.Sorted(slices.Values(profile))
	cut := (sorted[int(0.05*float64(len(sorted)-1))] + exactMedian(profile)) / 2
	start := -1
	for i := 0; i <= len(profile); i++ {
		on := i < len(profile) && profile[i] > cut
		switch {
		case on && start < 0:
			start = i
		case !on && start >= 0:
			if i-start > to-from {
				from, to = start, i
			}
			start = -1
		}
	}
	return from, to
}

// blocks are the mean densities of r's blocks of blockSide pixels a side.
func blocks(img *Image, r image.Rectangle) [][3]float64 {
	bw, bh := r.Dx()/blockSide, r.Dy()/blockSide
	out := make([][3]float64, 0, max(0, bw*bh))
	for by := range bh {
		for bx := range bw {
			var sum [3]float64
			for y := range blockSide {
				row := img.Pix[((r.Min.Y+by*blockSide+y)*img.Width+r.Min.X+bx*blockSide)*3:]
				for i := range 3 * blockSide {
					sum[i%3] += float64(densityOf[row[i]])
				}
			}
			for c := range sum {
				sum[c] /= blockSide * blockSide
			}
			out = append(out, sum)
		}
	}
	return out
}

// greyShift is how far, in normalised density, the frame's near-grey mid
// tones are from grey, per channel, greyStrength of it; none where too few
// are near grey to trust (a sunset, a lawn).
func greyShift(s [][3]float64, b Bounds) (shift [3]float64) {
	var dev [3][]float64
	for _, v := range s {
		var n [3]float64
		for c := range 3 {
			n[c] = (v[c] - b.Base[c]) / (b.White[c] - b.Base[c])
		}
		m := (n[0] + n[1] + n[2]) / 3
		if m <= 0.3 || m >= 0.85 || max(math.Abs(n[0]-m), math.Abs(n[1]-m), math.Abs(n[2]-m)) >= greyTol {
			continue
		}
		for c := range 3 {
			dev[c] = append(dev[c], n[c]-m)
		}
	}
	if float64(len(dev[0])) < greyMin*float64(len(s)) {
		return shift
	}
	for c := range 3 {
		shift[c] = greyStrength * exactMedian(dev[c])
	}
	return shift
}

// Positive is the print of the negative img, decoded as one, with the
// bounds b and its own auto balance: 8-bit sRGB, cut to the camera's frame,
// long pixels on its long side at most, 0 for all. It is not turned.
func Positive(img *Image, meta *Meta, b Bounds, long int) *image.RGBA {
	frame := image.Rect(0, 0, img.Width, img.Height)
	if f := meta.Frame; f.Width > 0 && f.Height > 0 && f.Width <= img.Width && f.Height <= img.Height {
		frame = image.Rect(0, 0, f.Width, f.Height).Add(image.Pt((img.Width-f.Width)/2, (img.Height-f.Height)/2))
	}
	if w, h := frame.Dx(), frame.Dy(); long > 0 && max(w, h) > long {
		img, frame = scaled(img, frame, long)
	}
	shift := greyShift(blocks(img, inside(frameOf(img, frame), inset)), b)
	// The print of each sample, per channel: a pixel is then three looks
	// and a matrix.
	var printed [3][]float32
	floor := math.Pow(10, -printK)
	for c := range 3 {
		printed[c] = make([]float32, 65536)
		for v := range printed[c] {
			n := (float64(densityOf[v])-b.Base[c])/(b.White[c]-b.Base[c]) - shift[c]
			printed[c][v] = float32(max(0, (math.Pow(10, printK*(n-1))-floor)/(1-floor)))
		}
	}
	var m [3][3]float32
	for i := range 3 {
		for j := range 3 {
			m[i][j] = float32(meta.RGBCam[i][j])
		}
	}
	w, h := frame.Dx(), frame.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	strips := (h + stripRows - 1) / stripRows
	parallel(strips, func(s int, _ *buffers) {
		for y := s * stripRows; y < min(h, (s+1)*stripRows); y++ {
			row := img.Pix[((frame.Min.Y+y)*img.Width+frame.Min.X)*3:]
			o := out.Pix[y*out.Stride:]
			for x := range w {
				r, g, bl := printed[0][row[3*x]], printed[1][row[3*x+1]], printed[2][row[3*x+2]]
				for c := range 3 {
					o[4*x+c] = encoded8(m[c][0]*r + m[c][1]*g + m[c][2]*bl)
				}
				o[4*x+3] = 255
			}
		}
	})
	return out
}

// srgb8 is the 8-bit sRGB of linear light, in steps of 1/4095.
var srgb8 = func() (t [4096]byte) {
	for i := range t {
		l := float64(i) / 4095
		e := 12.92 * l
		if l > 0.0031308 {
			e = 1.055*math.Pow(l, 1/2.4) - 0.055
		}
		t[i] = byte(e*255 + 0.5)
	}
	return t
}()

func encoded8(l float32) byte {
	return srgb8[int(min(max(l, 0), 1)*4095+0.5)]
}
