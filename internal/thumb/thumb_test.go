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
	"path/filepath"
	"strconv"
	"testing"
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
		if err := Make(context.Background(), src, dst, size); err != nil {
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
	if err := Make(context.Background(), src, src+".jpg", Size); !errors.Is(err, ErrNoDecoder) {
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
		if err := Make(context.Background(), withThumbnail(t, c.w, c.h, c.tw, c.th), dst, c.size); err != nil {
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
