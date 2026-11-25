// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"image"
	"io"
	"math"
	"sync"

	"github.com/ruvido/homewend/internal/library"
)

// profile is what the colours of a photo are, when they are not sRGB: a curve
// for each channel and a matrix, to linear sRGB (ICC.1, the three-component
// matrix-based profiles). A table of colours, a CMYK and a grey one are not
// known here: their photos are shown as they are. nil changes nothing.
//
// Our own: the standard library and x/image have nothing for ICC, and the Go
// packages that do are bindings to a C library, or read and do not convert.
type profile struct {
	linear [3][256]float32
	matrix [3][3]float32 // to the steps of encoding
}

// maxProfile is more than any matrix profile has; those of cameras and phones
// are some hundred bytes.
const maxProfile = 1 << 20

// profileOf is the profile in the photo f, at src; nil when its colours are
// sRGB, it does not say, or it is one not known here.
func profileOf(f io.Reader, src string) *profile {
	var head [12]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return nil
	}
	f = io.MultiReader(bytes.NewReader(head[:]), f)
	switch {
	case head[0] == 0xff && head[1] == 0xd8:
		icc := jpegICC(f)
		if len(icc) == 0 && library.IsAdobeRGB(src) {
			return adobeRGB()
		}
		return iccProfile(icc)
	case string(head[:8]) == "\x89PNG\r\n\x1a\n":
		return pngProfile(f)
	case string(head[:4]) == "RIFF" && string(head[8:]) == "WEBP":
		return iccProfile(webpICC(f))
	case string(head[4:8]) == "ftyp":
		return heicProfile(readHEIC(src))
	}
	return nil
}

// jpegICC is the profile in a JPEG's APP2 segments, in pieces numbered from 1
// (ICC.1, annex B.4), or nil when one is missing.
func jpegICC(f io.Reader) []byte {
	var pieces [256][]byte
	count, size := 0, 0
	for data := range library.APP2(f) {
		data, ok := bytes.CutPrefix(data, []byte("ICC_PROFILE\x00"))
		if !ok || len(data) < 2 {
			continue
		}
		if size += len(data); size > maxProfile {
			return nil
		}
		pieces[data[0]], count = data[2:], int(data[1])
	}
	var icc []byte
	for _, piece := range pieces[:count+1][1:] {
		if len(piece) == 0 {
			return nil
		}
		icc = append(icc, piece...)
	}
	return icc
}

// pngProfile is the profile of a PNG, from the chunks before its picture: cICP
// before iCCP (the PNG standard, third edition).
func pngProfile(f io.Reader) (p *profile) {
	io.CopyN(io.Discard, f, 8)
	for {
		var head [8]byte
		if _, err := io.ReadFull(f, head[:]); err != nil {
			return p
		}
		size, kind := int64(binary.BigEndian.Uint32(head[:])), string(head[4:])
		switch {
		case kind == "IDAT":
			return p
		case kind == "cICP" && size == 4:
			var cicp [4]byte
			io.ReadFull(f, cicp[:])
			return codePoints(uint64(cicp[0]), uint64(cicp[1]))
		case kind == "iCCP" && size <= maxProfile:
			chunk := make([]byte, size)
			if _, err := io.ReadFull(f, chunk); err != nil {
				return p
			}
			// Its name, how it is compressed and, compressed, the profile.
			if _, icc, ok := bytes.Cut(chunk, []byte{0}); ok && len(icc) > 0 {
				if z, err := zlib.NewReader(bytes.NewReader(icc[1:])); err == nil {
					whole, err := io.ReadAll(io.LimitReader(z, maxProfile))
					if err == nil {
						p = iccProfile(whole)
					}
				}
			}
			size = 0
		}
		if _, err := io.CopyN(io.Discard, f, size+4); err != nil { // and its check
			return p
		}
	}
}

// webpICC is the profile in a WebP, the chunk before its picture.
func webpICC(f io.Reader) []byte {
	io.CopyN(io.Discard, f, 12)
	for {
		var head [8]byte
		if _, err := io.ReadFull(f, head[:]); err != nil {
			return nil
		}
		size := int64(binary.LittleEndian.Uint32(head[4:]))
		switch string(head[:4]) {
		case "ICCP":
			icc, _ := io.ReadAll(io.LimitReader(f, min(size, maxProfile)))
			return icc
		case "VP8 ", "VP8L", "ALPH", "ANIM":
			return nil
		}
		if _, err := io.CopyN(io.Discard, f, size+size%2); err != nil {
			return nil
		}
	}
}

// The primaries of three spaces, in D50 as a profile has them: the rows are X,
// Y and Z.
var (
	srgbD50 = [3][3]float64{{0.43607, 0.38515, 0.14307}, {0.22249, 0.71687, 0.06061}, {0.01392, 0.09708, 0.71410}}
	// What turns them to linear sRGB: the one above, inverted.
	toLinear = inverse(srgbD50)
	p3D50    = [3][3]float64{{0.51512, 0.29198, 0.15710}, {0.24120, 0.69225, 0.06657}, {-0.00105, 0.04189, 0.78407}}
	adobeD50 = [3][3]float64{{0.60974, 0.20528, 0.14919}, {0.31111, 0.62567, 0.06322}, {0.01947, 0.06087, 0.74457}}
	white    = [3]float64{0.9642, 1, 0.8249}
)

// codePoints is the profile with the primaries and the curve of these numbers
// (ITU-T H.273). Display P3 with sRGB's curve is the one known: HDR's curves
// need more than a profile.
func codePoints(primaries, curve uint64) *profile {
	if primaries != 12 || curve != 13 {
		return nil
	}
	return newProfile(p3D50, [3]func(float64) float64{srgbLinear, srgbLinear, srgbLinear})
}

// adobeRGB is Adobe RGB (1998).
func adobeRGB() *profile {
	gamma := func(x float64) float64 { return math.Pow(x, 563.0/256) }
	return newProfile(adobeD50, [3]func(float64) float64{gamma, gamma, gamma})
}

// iccProfile reads an ICC profile.
func iccProfile(icc []byte) *profile {
	if len(icc) < 132 || string(icc[16:24]) != "RGB XYZ " || string(icc[36:40]) != "acsp" {
		return nil
	}
	tags := map[string][]byte{}
	r := reader{b: icc[128:]}
	for range min(r.u(4), 100) { // a profile has some twenty
		name := string(r.next(4))
		in := reader{b: icc}
		in.next(r.u(4))
		if data := in.next(r.u(4)); !r.bad && !in.bad {
			tags[name] = data
		}
	}
	// With a table of colours too, that is the profile.
	for _, name := range []string{"A2B0", "A2B1"} {
		if _, ok := tags[name]; ok {
			return nil
		}
	}
	var primaries [3][3]float64
	var curves [3]func(float64) float64
	for c, name := range []string{"r", "g", "b"} {
		r := reader{b: tags[name+"XYZ"]}
		if string(r.next(4)) != "XYZ " {
			return nil
		}
		r.u(4)
		for row := range 3 {
			primaries[row][c] = fixed(&r)
		}
		if curves[c] = iccCurve(tags[name+"TRC"]); r.bad || curves[c] == nil {
			return nil
		}
	}
	return newProfile(primaries, curves)
}

// fixed is r's next s15Fixed16Number.
func fixed(r *reader) float64 { return float64(int32(r.u(4))) / 65536 }

// iccCurve is the curve in a tag, or nil.
func iccCurve(tag []byte) func(float64) float64 {
	r := reader{b: tag}
	kind := string(r.next(4))
	r.u(4)
	switch kind {
	case "curv":
		n := r.u(4)
		switch {
		case r.bad:
			return nil
		case n == 0:
			return func(x float64) float64 { return x }
		case n == 1:
			gamma := float64(r.u(2)) / 256
			return func(x float64) float64 { return math.Pow(x, gamma) }
		}
		table := r.next(2 * n)
		if r.bad {
			return nil
		}
		at := func(i int) float64 { return float64(binary.BigEndian.Uint16(table[2*i:])) / 65535 }
		return func(x float64) float64 {
			i, part := math.Modf(x * float64(n-1))
			low := min(int(i), int(n)-2)
			part += i - float64(low)
			return at(low)*(1-part) + at(low+1)*part
		}
	case "para":
		form := r.u(2)
		r.u(2)
		if form > 4 {
			return nil
		}
		var v [7]float64 // g, a, b, c, d, e, f
		for i := range [5]int{1, 3, 4, 5, 7}[form] {
			v[i] = fixed(&r)
		}
		if r.bad {
			return nil
		}
		g, a, b, c, d, e, f := v[0], v[1], v[2], v[3], v[4], v[5], v[6]
		switch form {
		case 0:
			a = 1
		case 1, 2: // where a power begins is not said, nor what is before it
			d, e, f, c = -b/a, c, c, 0
		}
		return func(x float64) float64 {
			if x < d {
				return c*x + f
			}
			return math.Pow(a*x+b, g) + e
		}
	}
	return nil
}

// newProfile is the profile with these primaries and curves; nil when it is
// sRGB, near enough, or makes no sense: white is not white, black not black,
// or more is less.
func newProfile(primaries [3][3]float64, curves [3]func(float64) float64) *profile {
	p := &profile{}
	srgb := true
	for c, curve := range curves {
		for i := range 256 {
			v := curve(float64(i) / 255)
			switch {
			case !(v >= -0.05 && v <= 1.05), // a NaN too
				i == 0 && v > 0.05, i == 255 && v < 0.95,
				i > 0 && float32(v) < p.linear[c][i-1]:
				return nil
			}
			p.linear[c][i] = float32(v)
			if math.Abs(encoded(v)-float64(i)) > 2 {
				srgb = false
			}
		}
	}
	for row := range 3 {
		if sum := primaries[row][0] + primaries[row][1] + primaries[row][2]; math.Abs(sum-white[row]) > 0.03 {
			return nil
		}
		for c := range 3 {
			v, same := 0.0, 0.0
			for k := range 3 {
				v += toLinear[row][k] * primaries[k][c]
			}
			if row == c {
				same = 1
			}
			p.matrix[row][c] = float32(v * encodingSteps)
			// Different makers' sRGB differ by some thousandths.
			if math.Abs(v-same) > 0.005 {
				srgb = false
			}
		}
	}
	if srgb {
		return nil
	}
	return p
}

// srgbLinear is sRGB's curve.
func srgbLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// encoded is the sRGB value, of 255, of the linear l.
func encoded(l float64) float64 {
	if l <= 0.0031308 {
		return 255 * 12.92 * l
	}
	return 255 * (1.055*math.Pow(l, 1/2.4) - 0.055)
}

// encoding is encoded for each of 2^14 steps of l, rounded: where it is
// steepest, a step is a fifth of a value.
const encodingSteps = 1 << 14

var encoding = sync.OnceValue(func() *[encodingSteps + 1]uint8 {
	var t [encodingSteps + 1]uint8
	for i := range t {
		t[i] = uint8(min(encoded(float64(i)/encodingSteps)+0.5, 255))
	}
	return &t
})

// toSRGB is img, whose colours are p's, in sRGB: relative colorimetric, what
// sRGB has not clipped. It may be img, changed.
func (p *profile) toSRGB(img image.Image) image.Image {
	if p == nil {
		return img
	}
	out := rgba(img)
	b := out.Bounds()
	table := encoding()
	inStrips(b.Dy(), func(from, to int) {
		for y := from; y < to; y++ {
			line := out.Pix[y*out.Stride:][:4*b.Dx()]
			for ; len(line) >= 4; line = line[4:] {
				px := line[:4:4]
				if px[3] == 255 {
					px[0], px[1], px[2] = p.convert(table, px[0], px[1], px[2])
				} else if a := int(px[3]); a > 0 {
					// The channels are multiplied by alpha, the curves are not.
					straight := func(v uint8) uint8 { return uint8(min(255, (int(v)*255+a/2)/a)) }
					r, g, b := p.convert(table, straight(px[0]), straight(px[1]), straight(px[2]))
					px[0], px[1], px[2] = uint8((int(r)*a+127)/255), uint8((int(g)*a+127)/255), uint8((int(b)*a+127)/255)
				}
			}
		}
	})
	return out
}

// convert is a colour, not multiplied by alpha, in sRGB.
func (p *profile) convert(table *[encodingSteps + 1]uint8, r, g, b uint8) (uint8, uint8, uint8) {
	l0, l1, l2 := p.linear[0][r], p.linear[1][g], p.linear[2][b]
	at := func(m *[3]float32) uint8 {
		// Whole numbers clamp in half the time of floats.
		i := int(m[0]*l0 + m[1]*l1 + m[2]*l2 + 0.5)
		return table[min(max(i, 0), encodingSteps)]
	}
	return at(&p.matrix[0]), at(&p.matrix[1]), at(&p.matrix[2])
}

// inverse of m.
func inverse(m [3][3]float64) (out [3][3]float64) {
	minor := func(r, c int) float64 {
		r1, r2, c1, c2 := (r+1)%3, (r+2)%3, (c+1)%3, (c+2)%3
		return m[r1][c1]*m[r2][c2] - m[r1][c2]*m[r2][c1]
	}
	det := m[0][0]*minor(0, 0) + m[0][1]*minor(0, 1) + m[0][2]*minor(0, 2)
	for r := range 3 {
		for c := range 3 {
			out[c][r] = minor(r, c) / det
		}
	}
	return out
}
