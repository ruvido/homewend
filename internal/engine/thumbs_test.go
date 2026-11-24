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
	"time"

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
		info, err := os.Stat(filepath.Join(root, n))
		if err != nil {
			t.Fatal(err)
		}
		c.Put(library.Photo{Hash: "hash-" + n, Path: n, Name: n, Bytes: info.Size()})
	}
	c.Close()

	failed := map[string]bool{}
	for range 2 {
		if _, ok := makeThumbnails(context.Background(), "", failed); !ok {
			t.Error("the library was not looked at")
		}
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

// The library's disk taken out, nothing is said; the same put back is looked
// at again, though nothing in it changed.
func TestThumbnailsComeBackWithTheDisk(t *testing.T) {
	for _, home := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME", "HOME", "AppData", "LocalAppData"} {
		t.Setenv(home, t.TempDir())
	}
	defer func(every time.Duration) { thumbsEvery = every }(thumbsEvery)
	thumbsEvery = 10 * time.Millisecond
	root := filepath.Join(t.TempDir(), "library")
	os.MkdirAll(filepath.Join(root, library.WorkDir), 0o755)
	if _, err := UpdateSettings(func(s *Settings) { s.Library = root }); err != nil {
		t.Fatal(err)
	}
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	c.Put(library.Photo{Hash: "bad", Path: "bad.jpg", Name: "bad.jpg"})
	c.Close()
	ctx, stop := context.WithCancel(context.Background())
	ended := make(chan struct{})
	go func() { KeepThumbnails(ctx); close(ended) }()
	defer func() { stop(); <-ended }()
	wait := func(want Thumbs) {
		t.Helper()
		for range 500 {
			if ThumbsMade() == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%+v, want %+v", ThumbsMade(), want)
	}
	wait(Thumbs{1, 1})
	if err := os.Rename(root, root+"-out"); err != nil {
		t.Fatal(err)
	}
	wait(Thumbs{})
	if err := os.Rename(root+"-out", root); err != nil {
		t.Fatal(err)
	}
	wait(Thumbs{1, 1})
}
