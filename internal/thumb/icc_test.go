// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gen2brain/heic"

	"github.com/ruvido/homewend/internal/edit"
)

var be = binary.BigEndian

func fixeds(values ...float64) (b []byte) {
	for _, v := range values {
		b = be.AppendUint32(b, uint32(int32(math.Round(v*65536))))
	}
	return b
}

// anICC is a profile with these primaries and the curve trc.
func anICC(m [3][3]float64, trc []byte) []byte {
	type tag struct {
		name string
		data []byte
	}
	var tags []tag
	for c, name := range []string{"r", "g", "b"} {
		xyz := append([]byte("XYZ \x00\x00\x00\x00"), fixeds(m[0][c], m[1][c], m[2][c])...)
		tags = append(tags, tag{name + "XYZ", xyz}, tag{name + "TRC", trc})
	}
	icc := make([]byte, 128)
	copy(icc[12:], "mntrRGB XYZ ")
	copy(icc[36:], "acsp")
	icc = be.AppendUint32(icc, uint32(len(tags)))
	at := len(icc) + 12*len(tags)
	var data []byte
	for _, t := range tags {
		icc = be.AppendUint32(append(icc, t.name...), uint32(at+len(data)))
		icc = be.AppendUint32(icc, uint32(len(t.data)))
		data = append(data, t.data...)
	}
	icc = append(icc, data...)
	be.PutUint32(icc, uint32(len(icc)))
	return icc
}

func para(form uint16, values ...float64) []byte {
	return append(be.AppendUint16(be.AppendUint16([]byte("para\x00\x00\x00\x00"), form), 0), fixeds(values...)...)
}

func curv(values ...uint16) []byte {
	b := be.AppendUint32([]byte("curv\x00\x00\x00\x00"), uint32(len(values)))
	for _, v := range values {
		b = be.AppendUint16(b, v)
	}
	return b
}

func srgbTable() []byte {
	var values []uint16
	for i := range 1024 {
		values = append(values, uint16(math.Round(65535*srgbLinear(float64(i)/1023))))
	}
	return curv(values...)
}

var (
	srgbPara = para(3, 2.4, 1/1.055, 0.055/1.055, 1/12.92, 0.04045)
	p3ICC    = anICC(p3D50, srgbPara)
	proPhoto = [3][3]float64{{0.79772, 0.13516, 0.03132}, {0.28805, 0.71187, 0.00008}, {0, 0.00001, 0.82489}}
)

// patches are colours; those of P3 in sRGB, as Little CMS has them (relative
// colorimetric), are patchesP3.
var (
	patches     = [][3]uint8{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {200, 80, 60}, {60, 160, 80}, {128, 128, 128}, {20, 20, 20}, {255, 255, 255}}
	patchesP3   = [][3]uint8{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {216, 69, 50}, {0, 163, 69}, {128, 128, 128}, {20, 20, 20}, {255, 255, 255}}
	patchesA98  = [][3]uint8{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {230, 79, 56}, {0, 161, 73}, {129, 129, 129}}
	patchesPro  = [][3]uint8{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 15, 70}, {0, 190, 79}, {146, 146, 146}}
	patchesSize = 64
)

func patchesPicture() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, len(patches)*patchesSize, patchesSize))
	for i, c := range patches {
		draw.Draw(img, image.Rect(i*patchesSize, 0, (i+1)*patchesSize, patchesSize),
			image.NewUniform(color.RGBA{c[0], c[1], c[2], 255}), image.Point{}, draw.Src)
	}
	return img
}

func far(a, b uint8, most int) bool { return max(int(a)-int(b), int(b)-int(a)) > most }

func checkPatches(t *testing.T, name string, img image.Image, want [][3]uint8, most int) {
	t.Helper()
	side := img.Bounds().Dx() / len(patches)
	for i, w := range want {
		c := color.RGBAModel.Convert(img.At(i*side+side/2, side/2)).(color.RGBA)
		if far(c.R, w[0], most) || far(c.G, w[1], most) || far(c.B, w[2], most) {
			t.Errorf("%s: patch %d is %v, want %v", name, i, c, w)
		}
	}
}

// The values are Little CMS's; what is sRGB, however it says it, is nil.
func TestProfiles(t *testing.T) {
	table := srgbTable()
	for _, c := range []struct {
		name string
		icc  []byte
		want [][3]uint8
	}{
		{"P3", p3ICC, patchesP3},
		{"P3, a table", anICC(p3D50, table), patchesP3},
		{"Adobe RGB", anICC(adobeD50, curv(563)), patchesA98},
		{"Adobe RGB, parametric", anICC(adobeD50, para(0, 563.0/256)), patchesA98},
		{"ProPhoto", anICC(proPhoto, curv(461)), patchesPro},
		{"sRGB", anICC(srgbD50, srgbPara), nil},
		{"sRGB, a table", anICC(srgbD50, table), nil},
		{"none", nil, nil},
		{"black is white", anICC(p3D50, curv(65535, 0)), nil},
		{"no red", anICC([3][3]float64{{0, 0.3, 0.2}, {0, 0.7, 0.1}, {0, 0.1, 0.7}}, srgbPara), nil},
	} {
		p := iccProfile(c.icc)
		if (p != nil) != (c.want != nil) {
			t.Errorf("%s: known %v", c.name, p != nil)
			continue
		}
		checkPatches(t, c.name, p.toSRGB(patchesPicture()), c.want, 1)
	}
	if iccProfile(anICC(srgbD50, para(0, 1.8))) == nil {
		t.Error("another curve is sRGB")
	}
	if iccProfile(bytes.Replace(p3ICC, []byte("rTRC"), []byte("A2B0"), 1)) != nil {
		t.Error("a table of colours is read")
	}
	if codePoints(12, 13) == nil || codePoints(1, 13) != nil || codePoints(9, 16) != nil {
		t.Error("code points")
	}
}

// Each parametric form is the power it says.
func TestParametricForms(t *testing.T) {
	for form, c := range []struct {
		values   []float64
		low, mid float64 // at 0.02 and at 0.5
	}{
		{[]float64{2}, 0.0004, 0.25},
		{[]float64{2, 1.25, -0.25}, 0, 0.140625},
		{[]float64{2, 1.25, -0.25, 0.01}, 0.01, 0.150625},
		{[]float64{2, 1, 0, 0.5, 0.1}, 0.01, 0.25},
		{[]float64{2, 1, 0, 0.5, 0.1, 0.02, 0.03}, 0.04, 0.27},
	} {
		curve := iccCurve(para(uint16(form), c.values...))
		if low, mid := curve(0.02), curve(0.5); math.Abs(low-c.low) > 1e-4 || math.Abs(mid-c.mid) > 1e-4 {
			t.Errorf("form %d: %v, %v", form, low, mid)
		}
	}
}

// Alpha is kept, and the colours under it are what they are with none.
func TestProfileUnderAlpha(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{200, 80, 60, 128})
	out := iccProfile(p3ICC).toSRGB(img)
	c := color.NRGBAModel.Convert(out.At(0, 0)).(color.NRGBA)
	if c.A != 128 || far(c.R, 216, 3) || far(c.G, 69, 3) || far(c.B, 50, 3) {
		t.Error(c)
	}
	if _, _, _, a := out.At(1, 0).RGBA(); a != 0 {
		t.Error("transparent is not")
	}
}

func withSegments(file []byte, marker byte, segments ...[]byte) []byte {
	out := bytes.Clone(file[:2])
	for _, s := range segments {
		out = append(be.AppendUint16(append(out, 0xff, marker), uint16(len(s)+2)), s...)
	}
	return append(out, file[2:]...)
}

func jpegPieces(icc []byte, size int) (pieces [][]byte) {
	count := (len(icc) + size - 1) / size
	for i := range count {
		head := append([]byte("ICC_PROFILE\x00"), byte(i+1), byte(count))
		pieces = append(pieces, append(head, icc[i*size:min((i+1)*size, len(icc))]...))
	}
	return pieces
}

func pngChunk(kind string, data []byte) []byte {
	b := be.AppendUint32(nil, uint32(len(data)))
	b = append(append(b, kind...), data...)
	return be.AppendUint32(b, crc32.ChecksumIEEE(b[4:]))
}

func iccp(icc []byte) []byte {
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	w.Write(icc)
	w.Close()
	return pngChunk("iCCP", append([]byte("a\x00\x00"), z.Bytes()...))
}

// made is the picture Make makes of file, named name.
func made(t *testing.T, name string, file []byte, size int) image.Image {
	t.Helper()
	dir := t.TempDir()
	src, dst := filepath.Join(dir, name), filepath.Join(dir, "out.jpg")
	if err := os.WriteFile(src, file, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Make(t.Context(), src, dst, size, edit.Edit{}); err != nil {
		t.Fatal(name, err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// Whatever file the profile is in, the picture made is in sRGB.
func TestAProfileInEachFile(t *testing.T) {
	var j, p bytes.Buffer
	if err := jpeg.Encode(&j, patchesPicture(), &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&p, patchesPicture()); err != nil {
		t.Fatal(err)
	}
	afterIHDR := func(chunks ...[]byte) []byte {
		return bytes.Join([][]byte{p.Bytes()[:33], bytes.Join(chunks, nil), p.Bytes()[33:]}, nil)
	}
	pieces := jpegPieces(p3ICC, 100)
	backwards := [][]byte{[]byte("MPF\x00")}
	for i := range pieces {
		backwards = append(backwards, pieces[len(pieces)-1-i])
	}
	// A camera's, set to Adobe RGB: the main directory, EXIF's and interoperability's.
	r03 := []byte("Exif\x00\x00MM\x00*\x00\x00\x00\x08" +
		"\x00\x01\x87\x69\x00\x04\x00\x00\x00\x01\x00\x00\x00\x1a\x00\x00\x00\x00" +
		"\x00\x02\xa0\x01\x00\x03\x00\x00\x00\x01\xff\xff\x00\x00\xa0\x05\x00\x04\x00\x00\x00\x01\x00\x00\x00\x38\x00\x00\x00\x00" +
		"\x00\x01\x00\x01\x00\x02\x00\x00\x00\x04R03\x00\x00\x00\x00\x00")
	asSRGB := bytes.Replace(r03, []byte{0xff, 0xff}, []byte{0, 1}, 1)
	for _, c := range []struct {
		name string
		file []byte
		want [][3]uint8
	}{
		{"whole.jpg", withSegments(j.Bytes(), 0xe2, jpegPieces(p3ICC, 60000)...), patchesP3},
		{"pieces.jpg", withSegments(j.Bytes(), 0xe2, backwards...), patchesP3},
		{"lost.jpg", withSegments(j.Bytes(), 0xe2, pieces[1:]...), patches},
		{"named.heic", withSegments(j.Bytes(), 0xe2, pieces...), patchesP3},
		{"adobe.jpg", withSegments(j.Bytes(), 0xe1, r03), patchesA98},
		{"not-adobe.jpg", withSegments(j.Bytes(), 0xe1, asSRGB), patches},
		{"none.jpg", j.Bytes(), patches},
		{"iccp.png", afterIHDR(iccp(p3ICC)), patchesP3},
		{"first.png", afterIHDR(iccp(anICC(srgbD50, srgbPara)), iccp(p3ICC)), patches},
		{"wrecked.png", afterIHDR(pngChunk("iCCP", []byte("a\x00\x00no")), iccp(p3ICC)), patches},
		{"cicp.png", afterIHDR(iccp(anICC(adobeD50, curv(563))), pngChunk("cICP", []byte{12, 13, 0, 1})), patchesP3},
		{"srgb.png", afterIHDR(pngChunk("sRGB", []byte{0})), patches},
	} {
		// JPEG rounds, twice.
		checkPatches(t, c.name, made(t, c.name, c.file, Size), c.want, 4)
		checkPatches(t, c.name, made(t, c.name, c.file, PreviewSize), c.want, 4)
	}
	// Grey and CMYK are not a profile's.
	grey := image.NewGray(image.Rect(0, 0, 64, 64))
	draw.Draw(grey, grey.Bounds(), image.NewUniform(color.Gray{100}), image.Point{}, draw.Src)
	var g bytes.Buffer
	if err := jpeg.Encode(&g, grey, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := made(t, "grey.jpg", withSegments(g.Bytes(), 0xe2, pieces...), Size).(*image.Gray); !ok {
		t.Error("a grey photo is not grey")
	}
	t.Run("webp", func(t *testing.T) {
		ffmpeg, err := exec.LookPath("ffmpeg")
		if err != nil {
			t.Skip("no ffmpeg")
		}
		for _, lossless := range []string{"0", "1"} {
			cmd := exec.CommandContext(t.Context(), ffmpeg, "-v", "error", "-f", "png_pipe", "-i", "-", "-c:v", "libwebp", "-lossless", lossless, "-quality", "95", "-f", "webp", "-")
			cmd.Stdin = bytes.NewReader(p.Bytes())
			plain, err := cmd.Output()
			if err != nil {
				t.Skip("no libwebp", err)
			}
			b := patchesPicture().Bounds()
			le := binary.LittleEndian
			inside := append([]byte("WEBPVP8X\x0a\x00\x00\x00\x20\x00\x00\x00"), byte(b.Dx()-1), byte((b.Dx()-1)>>8), 0, byte(b.Dy()-1), byte((b.Dy()-1)>>8), 0)
			inside = le.AppendUint32(append(inside, "ICCP"...), uint32(len(p3ICC)))
			inside = append(append(inside, p3ICC...), make([]byte, len(p3ICC)%2)...)
			inside = append(inside, plain[12:]...)
			file := append(le.AppendUint32([]byte("RIFF"), uint32(len(inside))), inside...)
			without := rgba(made(t, "plain.webp", plain, Size))
			with := made(t, "icc.webp", file, Size)
			if want := iccProfile(p3ICC).toSRGB(without); !alikeBy(with, want, 12) {
				t.Error("lossless", lossless, "not converted")
			}
		}
	})
}

// alikeBy says whether the middles of the patches of a and b are within most.
func alikeBy(a, b image.Image, most int) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	side := a.Bounds().Dx() / len(patches)
	for i := range patches {
		x, y := i*side+side/2, side/2
		p, q := color.RGBAModel.Convert(a.At(x, y)).(color.RGBA), color.RGBAModel.Convert(b.At(x, y)).(color.RGBA)
		if far(p.R, q.R, most) || far(p.G, q.G, most) || far(p.B, q.B, most) {
			return false
		}
	}
	return true
}

// A HEIC by its tiles, with a profile, and whole, with code points, is the
// decoder's picture converted; its thumbnail has the photo's colours, or its
// own.
func TestAProfileInAHEIC(t *testing.T) {
	prof := aBox("colr", append([]byte("prof"), p3ICC...)...)
	nclx := aBox("colr", []byte("nclx\x00\x0c\x00\x0d\x00\x06\x80")...)
	whole, err := heic.Decode(bytes.NewReader(defaults().file()))
	if err != nil {
		t.Fatal(err)
	}
	want := iccProfile(p3ICC).toSRGB(rgba(whole))
	for name, colr := range map[string][]byte{"tiles": prof, "whole": nclx} {
		a := defaults()
		a.ofPhoto = colr
		path := filepath.Join(t.TempDir(), "a.heic")
		if err := os.WriteFile(path, a.file(), 0o644); err != nil {
			t.Fatal(err)
		}
		if tiles := gridOf(a.file()) != nil; tiles != (name == "tiles") {
			t.Fatal(name, "by tiles:", tiles)
		}
		img, colours, err := decode(t.Context(), path, PreviewSize)
		if err != nil || colours == nil {
			t.Fatal(name, err, colours)
		}
		if !alike(colours.toSRGB(img), want) {
			t.Error(name)
		}
	}

	// What is not a HEIC by its name is not read whole for its colours: a CR3
	// begins as one.
	a := defaults()
	a.ofPhoto = prof
	raw := filepath.Join(t.TempDir(), "a.cr3")
	if err := os.WriteFile(raw, a.file(), 0o644); err != nil {
		t.Fatal(err)
	}
	if profileOf(bytes.NewReader(a.file()), raw) != nil {
		t.Error("a CR3 is read")
	}

	// The thumbnail is tile 6, of the photo 7.
	a = defaults()
	a.ofPhoto, a.refs = prof, aBox("thmb", 0, 6, 0, 1, 0, 7)
	for name, c := range map[string]struct {
		ofThumbnail []byte
		wide        bool
	}{
		"says nothing": {nil, true},
		"sRGB":         {aBox("colr", append([]byte("prof"), anICC(srgbD50, srgbPara)...)...), false},
	} {
		a.ofTile = c.ofThumbnail
		data := a.file()
		img, colours := heicThumbnail(data)
		if !bytes.Equal(data, a.file()) {
			t.Error(name, "the photo's bytes are changed")
		}
		if img == nil || img.Bounds().Dx() != 64 || (colours != nil) != c.wide {
			t.Error(name, img == nil, colours != nil)
		}
	}
}

// No profile, whatever it says, is more than a photo with no profile.
func FuzzProfile(f *testing.F) {
	f.Add(p3ICC)
	f.Add(anICC(adobeD50, curv(563)))
	f.Add(anICC(p3D50, srgbTable()))
	f.Add(anICC(p3D50, para(4, 2.4, 0, 0, 0, 0, 0, 0)))
	f.Add(anICC(p3D50, []byte("curv\x00\x00\x00\x00\xff\xff\xff\xff\x00\x00")))
	huge := bytes.Clone(p3ICC)
	be.PutUint32(huge[128:], math.MaxUint32)
	f.Add(huge)
	img := patchesPicture()
	img.Rect.Max.Y = 1
	f.Fuzz(func(t *testing.T, data []byte) {
		iccProfile(data).toSRGB(&image.RGBA{Pix: bytes.Clone(img.Pix[:img.Stride]), Stride: img.Stride, Rect: img.Rect})
		colrProfile(data)
		heicProfile(data)
		for _, head := range []string{"", "\xff\xd8", "\x89PNG\r\n\x1a\n", "RIFF\x00\x00\x00\x00WEBP"} {
			profileOf(bytes.NewReader(append([]byte(head), data...)), "")
		}
	})
}
