// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruvido/homewend/internal/edit"
	"github.com/ruvido/homewend/internal/raw"
	"github.com/ruvido/homewend/internal/thumb/thumbtest"
)

func raf(t *testing.T, preview []byte, at, n uint32) string {
	return thumbtest.RAF(t, t.TempDir(), preview, at, n)
}

// A RAF shows its camera's JPEG; one that has none, or points past its end, is
// not drawn, and nothing panics.
func TestARAFShowsItsPreview(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 600, 400))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	src := raf(t, jpg.Bytes(), 148, uint32(jpg.Len()))
	dst := filepath.Join(t.TempDir(), "thumb.jpg")
	if err := Make(context.Background(), src, dst, Size, edit.Edit{}); err != nil {
		t.Fatal(err)
	}
	if w, h := Dimensions(dst); w != Size || h != Size*2/3 {
		t.Errorf("%d by %d", w, h)
	}
	for _, bad := range []string{raf(t, jpg.Bytes(), 148, 1<<30), raf(t, jpg.Bytes(), 10, 100), raf(t, nil, 0, 0)} {
		if err := Make(context.Background(), bad, dst, Size, edit.Edit{}); !errors.Is(err, ErrNoDecoder) {
			t.Errorf("%v", err)
		}
	}
}

// With no helper to develop it, a RAF is not developed, and says why; nothing
// is written.
func TestARAFWithNoHelper(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, image.NewRGBA(image.Rect(0, 0, 60, 40)), nil); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "developed.jpg")
	if err := Develop(context.Background(), raf(t, jpg.Bytes(), 148, uint32(jpg.Len())), dst, PreviewSize, nil, true); !errors.Is(err, raw.ErrNoHelper) {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("written")
	}
}
