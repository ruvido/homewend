// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ruvido/homewend/internal/edit"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

func TestMakeFitsTheLongEdge(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "wide.png")
	writePNG(t, src, 3000, 1500)
	// The grid's thumbnail and the viewer's preview, each at its own size.
	for _, size := range []int{Size, PreviewSize} {
		dst := Path(filepath.Join(dir, "cache", strconv.Itoa(size)), "abcdef")
		if err := Make(context.Background(), src, dst, size, edit.Edit{}); err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(dst)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width != size || cfg.Height != size/2 {
			t.Fatalf("got %dx%d, %v; want %dx%d", cfg.Width, cfg.Height, err, size, size/2)
		}
		if filepath.Base(filepath.Dir(dst)) != "ab" {
			t.Fatalf("not filed by hash: %s", dst)
		}
	}
}

func TestUnknownFormat(t *testing.T) {
	src := filepath.Join(t.TempDir(), "x.txt")
	os.WriteFile(src, []byte("hello"), 0o644)
	if err := Make(context.Background(), src, src+".jpg", Size, edit.Edit{}); !errors.Is(err, ErrNoDecoder) {
		t.Fatalf("got %v", err)
	}
}

func TestUprightTurnsSix(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	out := upright(img, 6) // rotate 90° clockwise
	if b := out.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Fatalf("bounds %v", b)
	}
	if r, _, _, _ := out.At(1, 0).RGBA(); r == 0 {
		t.Fatal("top-left did not move to top-right")
	}
}

// withThumbnail is a JPEG of w by h in one grey, its EXIF carrying a white
// thumbnail of tw by th.
func withThumbnail(t *testing.T, w, h, tw, th int) string {
	t.Helper()
	picture := func(w, h int, c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, nil); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	small := picture(tw, th, color.White)
	le := binary.LittleEndian
	tiff := append([]byte("II*\x00"), 8, 0, 0, 0) // IFD0 at 8
	tiff = append(tiff, 0, 0)                     // empty
	tiff = le.AppendUint32(tiff, 14)              // IFD1 at 14
	tiff = le.AppendUint16(tiff, 2)
	for _, e := range [][2]uint32{{0x0201, 44}, {0x0202, uint32(len(small))}} {
		tiff = le.AppendUint16(tiff, uint16(e[0]))
		tiff = le.AppendUint16(tiff, 4)
		tiff = le.AppendUint32(tiff, 1)
		tiff = le.AppendUint32(tiff, e[1])
	}
	tiff = append(le.AppendUint32(tiff, 0), small...)
	exif := append([]byte("Exif\x00\x00"), tiff...)
	whole := picture(w, h, color.Gray{100})
	file := append([]byte{0xff, 0xd8, 0xff, 0xe1}, binary.BigEndian.AppendUint16(nil, uint16(len(exif)+2))...)
	file = append(append(file, exif...), whole[2:]...)
	path := filepath.Join(t.TempDir(), "a.jpg")
	if err := os.WriteFile(path, file, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The camera's own thumbnail is used when it is the photo's, large enough;
// never for the viewer's picture.
func TestTheCamerasThumbnail(t *testing.T) {
	for _, c := range []struct {
		name         string
		w, h, tw, th int
		size         int
		used         bool
	}{
		{"its own", 1024, 768, 512, 384, Size, true},
		{"rounded", 4128, 3096, 504, 378, Size, true},
		{"of another shape", 768, 768, 512, 384, Size, false},
		{"too small", 1024, 768, 160, 120, Size, false},
		{"for the viewer", 1024, 768, 512, 384, PreviewSize, false},
	} {
		dst := filepath.Join(t.TempDir(), "out.jpg")
		if err := Make(context.Background(), withThumbnail(t, c.w, c.h, c.tw, c.th), dst, c.size, edit.Edit{}); err != nil {
			t.Fatal(c.name, err)
		}
		f, err := os.Open(dst)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		r, _, _, _ := img.At(5, 5).RGBA()
		if used := r > 0xc000; used != c.used {
			t.Errorf("%s: used %v, want %v", c.name, used, c.used)
		}
	}
}

// withStream is a JPEG of w by h in one grey with a FlashPix thumbnail, in
// two pieces, of 320 by 240: white, between bars of bar where it is wider
// than the photo's shape. With no thumbnail, none.
func withStream(t *testing.T, w, h int, bar color.Color, some bool) string {
	t.Helper()
	encode := func(img image.Image) []byte {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 95}); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	big := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(big, big.Bounds(), image.NewUniform(color.Gray{100}), image.Point{}, draw.Src)
	whole := encode(big)
	file := []byte{0xff, 0xd8}
	if some {
		img := image.NewRGBA(image.Rect(0, 0, 320, 240))
		draw.Draw(img, img.Bounds(), image.NewUniform(bar), image.Point{}, draw.Src)
		high := 320 * h / w
		top := (240 - high + 1) / 2
		draw.Draw(img, image.Rect(0, top, 320, top+high), image.NewUniform(color.White), image.Point{}, draw.Src)
		stream := append(make([]byte, 47), encode(img)...)
		for at := 0; at < len(stream); at += 900 {
			head := binary.BigEndian.AppendUint32([]byte("FPXR\x00\x00\x02\x00\x02"), uint32(at))
			piece := append(head, stream[at:min(at+900, len(stream))]...)
			file = append(file, 0xff, 0xe2)
			file = binary.BigEndian.AppendUint16(file, uint16(len(piece)+2))
			file = append(file, piece...)
		}
	}
	path := filepath.Join(t.TempDir(), "a.jpg")
	if err := os.WriteFile(path, append(file, whole[2:]...), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// A camera's thumbnail has black bars where it is not of the photo's shape:
// they are cut, to the last line. With none, or with something else than
// bars, the photo is read whole.
func TestNoBarsInAThumbnail(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
		bar  color.Color
		some bool
		used bool
	}{
		{"3:2 between black bars", 1500, 1000, color.Black, true, true},
		{"16:9 between black bars", 1600, 900, color.Black, true, true},
		{"not black: a picture of another shape", 1500, 1000, color.Gray{90}, true, false},
		{"no thumbnail", 1500, 1000, nil, false, false},
	} {
		dst := filepath.Join(t.TempDir(), "out.jpg")
		if err := Make(context.Background(), withStream(t, c.w, c.h, c.bar, c.some), dst, Size, edit.Edit{}); err != nil {
			t.Fatal(c.name, err)
		}
		f, err := os.Open(dst)
		if err != nil {
			t.Fatal(err)
		}
		img, err := jpeg.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		b := img.Bounds()
		least, most := 100, 100
		if c.used {
			least, most = 240, 255
		}
		for y := b.Min.Y; y < b.Max.Y; y++ {
			g := int(color.GrayModel.Convert(img.At(b.Dx()/2, y)).(color.Gray).Y)
			if g < least-3 || g > most+3 {
				t.Errorf("%s: line %d is %d", c.name, y, g)
			}
		}
		if shape, want := float64(b.Dx())/float64(b.Dy()), float64(c.w)/float64(c.h); shape < want*0.97 || shape > want*1.03 {
			t.Errorf("%s: shape %.2f, want %.2f", c.name, shape, want)
		}
	}
}

// A JPEG that says it is 0 wide is a file with no picture, whatever its
// thumbnail.
func TestANoWidePhoto(t *testing.T) {
	path := withStream(t, 1500, 1000, color.Black, true)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	at := bytes.LastIndex(b, []byte{0xff, 0xc0})
	b[at+7], b[at+8] = 0, 0
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Make(context.Background(), path, filepath.Join(t.TempDir(), "out.jpg"), Size, edit.Edit{}); err == nil {
		t.Error("made one")
	}
}

// A video's frame has its colours, whatever matrix and range they are in.
func TestAVideosColours(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	for _, c := range []struct{ matrix, levels string }{
		{"bt709", "tv"}, {"bt709", "pc"}, {"smpte170m", "tv"}, {"bt2020nc", "tv"},
	} {
		src := filepath.Join(t.TempDir(), "a.mkv")
		out, err := exec.CommandContext(t.Context(), ffmpeg, "-v", "error",
			"-f", "lavfi", "-i", "color=c=0x00c000:s=1280x720:d=0.2",
			"-vf", "scale=out_color_matrix="+c.matrix+":out_range="+c.levels+",format=yuv420p",
			"-colorspace", c.matrix, "-color_range", c.levels, "-c:v", "ffv1", src).CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		img, err := frame(t.Context(), src, Size)
		if err != nil {
			t.Fatal(err)
		}
		got := color.RGBAModel.Convert(img.At(50, 50)).(color.RGBA)
		if far(got.R, 0, 6) || far(got.G, 192, 6) || far(got.B, 0, 6) {
			t.Errorf("%s, %s: %v", c.matrix, c.levels, got)
		}
	}
}
