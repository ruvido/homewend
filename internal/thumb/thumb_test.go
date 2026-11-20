// homewend — Copyright (C) 2026 Ron Alder
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"context"
	"errors"
	"image"
	"image/color"
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
