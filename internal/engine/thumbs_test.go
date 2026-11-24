// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// Every photo of the library is given its thumbnail; one that cannot be is
// counted as done, and not tried again.
func TestThumbnailsAreMadeForTheLibrary(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	root := t.TempDir()
	if _, err := UpdateSettings(func(s *Settings) { s.Library = root }); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, library.WorkDir), 0o755)
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "good.png"))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 300, 200)))
	f.Close()
	os.WriteFile(filepath.Join(root, "bad.jpg"), []byte("no picture"), 0o644)
	for _, n := range []string{"good.png", "bad.jpg"} {
		c.Put(library.Photo{Hash: "hash-" + n, Path: n, Name: n})
	}
	c.Close()

	failed := map[string]bool{}
	for range 2 {
		makeThumbnails(context.Background(), "", failed)
		if got := ThumbsMade(); got != (Thumbs{Done: 2, Of: 2}) {
			t.Errorf("%+v, want all done", got)
		}
	}
	folder, err := picturesDir("thumbs")
	if err != nil {
		t.Fatal(err)
	}
	if kept := thumb.Kept(folder); len(kept) != 1 || !kept["hash-good.png"] {
		t.Errorf("kept %v; want the good one", kept)
	}
	if len(failed) != 1 || !failed["hash-bad.jpg"] {
		t.Errorf("failed %v; want the bad one", failed)
	}
}
