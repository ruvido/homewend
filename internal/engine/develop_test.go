// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
	"github.com/ruvido/homewend/internal/thumb/thumbtest"
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
	preview := picture(600, 400)
	src := thumbtest.RAF(t, t.TempDir(), preview, 148, uint32(len(preview)))
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

// A RAF asked for goes before the library's, and still counts in its
// progress until it is developed.
func TestAskedGoesFirst(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	// Started already, as by an earlier test: no one develops in this one.
	set := func(asked, lib []rawJob, of int, started bool, wake chan struct{}) {
		developer.Lock()
		developer.asked, developer.library, developer.now, developer.of, developer.started, developer.wake = asked, lib, nil, of, started, wake
		developer.Unlock()
	}
	set(nil, []rawJob{{name: "a", library: true}, {name: "b", library: true}}, 3, true, make(chan struct{}, 1))
	defer set(nil, nil, 0, false, nil)
	if got := developed(rawJob{name: "b", src: "b.RAF"}, true); got != "" {
		t.Fatalf("%q", got)
	}
	developed(rawJob{name: "c", src: "c.RAF"}, true)
	developer.Lock()
	if len(developer.asked) != 2 || developer.asked[0].name != "b" || !developer.asked[0].library || len(developer.library) != 1 {
		t.Errorf("asked %+v, library %+v", developer.asked, developer.library)
	}
	developer.Unlock()
	if d := DevelopingNow(); d.Done != 1 || d.Of != 3 || len(d.Now) != 2 || d.Now[0] != "b" {
		t.Errorf("%+v", d)
	}
	// Asked ahead, not raised.
	developed(rawJob{name: "a", src: "a.RAF"}, false)
	if d := DevelopingNow(); len(d.Now) != 2 {
		t.Errorf("%+v", d)
	}
	// Asked twice, once in the queue.
	developed(rawJob{name: "c", src: "c.RAF"}, true)
	if d := DevelopingNow(); len(d.Now) != 2 {
		t.Errorf("%+v", d)
	}
}

// The library's RAFs are listed without their paths: each is found by its
// photo when its turn comes, not taken for the library's folder.
func TestALibraryRAFIsFoundByItsPhoto(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	root := t.TempDir()
	if _, err := UpdateSettings(func(s *Settings) { s.Library = root }); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, library.WorkDir, "x"), 0o755)
	os.MkdirAll(filepath.Join(root, "ron", "2026"), 0o755)
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 60, 40)), nil)
	src := thumbtest.RAF(t, filepath.Join(root, "ron", "2026"), b.Bytes(), 148, uint32(b.Len()))
	info, _ := os.Stat(src)
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(root, src)
	c.Put(library.Photo{Hash: "rafhash", Profile: "ron", Path: filepath.ToSlash(rel), Name: filepath.Base(src), Bytes: info.Size()})
	photos, err := c.Page(library.Filter{})
	c.Close()
	if err != nil || len(photos) != 1 {
		t.Fatal(photos, err)
	}
	k := rawJob{name: "rafhash", key: photos[0].Key(), library: true}
	file, _, err := pictureOf(k.key, os.Stat)
	if err != nil || file != src {
		t.Errorf("found %q, %v; want %q", file, err, src)
	}
}

// A RAF printed from a negative has every picture, its grid thumbnail too,
// made from its print and named after it; shown as it came again, the
// camera's.
func TestANegativesPicturesAreItsPrint(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	t.Setenv("PATH", t.TempDir()) // no helper
	root := t.TempDir()
	if _, err := UpdateSettings(func(s *Settings) { s.Library = root }); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(root, library.WorkDir, "x"), 0o755)
	os.MkdirAll(filepath.Join(root, "ron", "2026"), 0o755)
	picture := func(w, h int) []byte {
		var b bytes.Buffer
		jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil)
		return b.Bytes()
	}
	camera := picture(600, 400)
	src := thumbtest.RAF(t, filepath.Join(root, "ron", "2026"), camera, 148, uint32(len(camera)))
	info, _ := os.Stat(src)
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := filepath.Rel(root, src)
	k := library.Key{Hash: "aaaa", Profile: "ron"}
	c.Put(library.Photo{Hash: k.Hash, Profile: k.Profile, Path: filepath.ToSlash(rel), Name: filepath.Base(src), Bytes: info.Size()})
	c.Close()
	const bounds = "1.2744 1.2720 1.6659 2.0183 2.1397 2.5716"
	positive := library.PositiveName(bounds)
	// Its print, at 2560 and whole, as convert and an edit leave them.
	for _, dir := range developedDirs {
		folder, _ := picturesDir(dir)
		print := thumb.Path(folder, printed(k.Hash, positive))
		os.MkdirAll(filepath.Dir(print), 0o755)
		if err := os.WriteFile(print, picture(300, 300), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := printing(func(c *library.Catalog) error { return c.Print(k, bounds, [4]float64{0.25, 0, 0.75, 1}) }); err != nil {
		t.Fatal(err)
	}
	s, _ := LoadSettings()
	list, err := Photos(s, "", "", "", false, "", 0)
	if err != nil || len(list.Photos) != 1 || list.Photos[0].Positive != positive || list.Photos[0].Edit == "" {
		t.Fatalf("listed %+v %v", list, err)
	}
	// Cropped to the film's frame: half as wide as high.
	cropped := list.Photos[0].Edit
	for _, ask := range []func(context.Context, library.Key, string, string) (string, bool, error){Thumbnail, Preview} {
		file, kept, err := ask(context.Background(), k, cropped, positive)
		if w, h := thumb.Dimensions(file); err != nil || !kept || 2*w != h || filepath.Base(file) != thumb.Edited(printed(k.Hash, positive), cropped)+".jpg" {
			t.Errorf("printed: %s %dx%d kept %v %v", file, w, h, kept, err)
		}
	}
	// Asked as it was before, it is not kept: the window's list is old.
	if _, kept, _ := Thumbnail(context.Background(), k, cropped, "0123"); kept {
		t.Error("another print kept")
	}
	if err := Convert([]library.Key{k}, false); err != nil {
		t.Fatal(err)
	}
	file, kept, err := Thumbnail(context.Background(), k, "", "")
	if w, h := thumb.Dimensions(file); err != nil || !kept || w == h || filepath.Base(file) != k.Hash+".jpg" {
		t.Errorf("as it came: %s %dx%d kept %v %v", file, w, h, kept, err)
	}
}

// Negatives to print are said as developing, and counted, until they are.
func TestConvertingIsDeveloping(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	developer.Lock()
	developer.started, developer.wake = true, make(chan struct{}, 1) // no one prints in this one
	developer.of = 3                                                 // all developed
	developer.Unlock()
	defer func() {
		developer.Lock()
		developer.started, developer.wake, developer.converts, developer.of = false, nil, nil, 0
		developer.Unlock()
	}()
	keys := []library.Key{{Hash: "n1"}, {Hash: "n2"}}
	if err := Convert(keys, true); err != nil {
		t.Fatal(err)
	}
	if d := DevelopingNow(); len(d.Now) != 2 || d.Now[0] != "n1" || d.Done != 0 || d.Of != 2 {
		t.Errorf("asked: %+v", d)
	}
	developer.Lock()
	developer.converting, developer.converts = slices.Clone(keys), nil
	developer.Unlock()
	developer.converted(keys[0])
	if d := DevelopingNow(); len(d.Now) != 1 || d.Now[0] != "n2" || d.Done != 1 || d.Of != 2 {
		t.Errorf("one printed: %+v", d)
	}
	// No library to find it in: done with, not printed.
	convert(keys[1:])
	if d := DevelopingNow(); len(d.Now) != 0 || d.Done != 0 || d.Of != 0 {
		t.Errorf("all printed: %+v", d)
	}
}

// A job that failed is not asked again, however it is asked: the library's
// has no file, the window's has one.
func TestAFailedRAFIsNotTriedAgain(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	developer.Lock()
	developer.started, developer.wake, developer.failed = true, make(chan struct{}, 1), map[jobID]bool{}
	developer.Unlock()
	defer func() {
		developer.Lock()
		developer.started, developer.wake, developer.failed, developer.asked = false, nil, nil, nil
		developer.Unlock()
	}()
	// No library to find its file in.
	develop(rawJob{name: "h", key: library.Key{Hash: "h", Profile: "ron"}, library: true}, false)
	developed(rawJob{name: "h", src: "h.RAF"}, true)
	developer.Lock()
	defer developer.Unlock()
	if len(developer.asked) != 0 {
		t.Errorf("asked again: %+v", developer.asked)
	}
}
