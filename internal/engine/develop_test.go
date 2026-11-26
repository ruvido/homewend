// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruvido/homewend/internal/thumb"
)

// A RAF's preview is its camera's JPEG, not kept, until its developed
// picture is there; then it is made from that, and kept. Its grid thumbnail
// is the camera's all along.
func TestARAFIsShownDevelopedOnceItIs(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no helper
	picture := func(w, h int) []byte {
		var b bytes.Buffer
		if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	// A RAF's head says where its JPEG is.
	preview := picture(600, 400)
	head := make([]byte, 148)
	copy(head, "FUJIFILMCCD-RAW 0201FF159501X-T3")
	binary.BigEndian.PutUint32(head[84:], 148)
	binary.BigEndian.PutUint32(head[88:], uint32(len(preview)))
	src := filepath.Join(t.TempDir(), "DSCF0001.RAF")
	if err := os.WriteFile(src, append(head, preview...), 0o644); err != nil {
		t.Fatal(err)
	}
	const hash = "abcdef"
	source := func() (string, string, error) { return src, "", nil }
	size := func(file string) (int, int) {
		t.Helper()
		w, h := thumb.Dimensions(file)
		return w, h
	}

	file, kept, err := cached(context.Background(), "previews", thumb.PreviewSize, hash, drawing{}, source)
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := size(file); kept || w != 600 || filepath.Base(file) != hash+cameraMark+".jpg" {
		t.Errorf("before: %s %d wide, kept %v; want the camera's, not kept", file, w, kept)
	}
	file, kept, _ = cached(context.Background(), "thumbs", thumb.Size, hash, drawing{}, source)
	if w, _ := size(file); !kept || w != thumb.Size {
		t.Errorf("the grid's thumbnail: %s %d wide, kept %v", file, w, kept)
	}

	// Developed since.
	folder, _ := picturesDir("developed")
	master := thumb.Path(folder, hash)
	os.MkdirAll(filepath.Dir(master), 0o755)
	if err := os.WriteFile(master, picture(300, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	forgetCamera(hash)
	file, kept, err = cached(context.Background(), "previews", thumb.PreviewSize, hash, drawing{}, source)
	if w, _ := size(file); err != nil || !kept || w != 300 || filepath.Base(file) != hash+".jpg" {
		t.Errorf("after: %s %d wide, kept %v, %v; want the developed one, kept", file, w, kept, err)
	}
	previews, _ := picturesDir("previews")
	if _, err := os.Stat(thumb.Path(previews, hash+cameraMark)); err == nil {
		t.Error("the camera's preview is left")
	}

	// Made from the camera's by an older version, under the developed one's name.
	os.Remove(master)
	forgetUndeveloped(hash, "")
	if _, err := os.Stat(thumb.Path(previews, hash)); err == nil {
		t.Error("an undeveloped RAF's preview is kept as developed")
	}
	thumbs, _ := picturesDir("thumbs")
	if _, err := os.Stat(thumb.Path(thumbs, hash)); err != nil {
		t.Error("its grid thumbnail is gone")
	}
}

func TestOnlyARAFIsDeveloped(t *testing.T) {
	for _, c := range []struct {
		src    string
		size   int
		edited bool
		want   bool
	}{
		{"a.RAF", thumb.PreviewSize, false, true},
		{"a.raf", thumb.ScreenSize, false, true},
		{"a.RAF", thumb.Size, false, false},
		{"a.RAF", thumb.Size, true, true},
		{"a.JPG", thumb.PreviewSize, true, false},
		{"a.CR3", thumb.PreviewSize, false, false},
	} {
		if got := developsFrom(c.src, c.size, c.edited); got != c.want {
			t.Errorf("%+v: %v", c, got)
		}
	}
}
