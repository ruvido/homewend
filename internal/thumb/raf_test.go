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
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruvido/homewend/internal/edit"
)

// raf is a Fujifilm RAF as a camera writes its head: the name, then where its
// JPEG is, then raw data that is not read.
func raf(t *testing.T, preview []byte, at, n uint32) string {
	t.Helper()
	head := make([]byte, 148)
	copy(head, "FUJIFILMCCD-RAW 0201FF159501X-T3")
	binary.BigEndian.PutUint32(head[84:], at)
	binary.BigEndian.PutUint32(head[88:], n)
	path := filepath.Join(t.TempDir(), "DSCF0050.RAF")
	data := append(append(head, preview...), bytes.Repeat([]byte{7}, 4096)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
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
