// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A library filed before profiles moves into the profile of its photos'
// source: the photo, its sidecar, its album; nothing is left behind and
// nothing is copied.
func TestAnOldLibraryMovesIntoItsProfile(t *testing.T) {
	src, root := t.TempDir(), t.TempDir()
	write(t, src, "Photos from 2019/IMG_1.jpg", []byte("a photo"))
	sidecar(t, src, "Photos from 2019/IMG_1.jpg", 1562345678)
	write(t, src, "Greece 2019/IMG_1.jpg", []byte("a photo"))
	sidecar(t, src, "Greece 2019/IMG_1.jpg", 1562345678)
	if err := os.MkdirAll(filepath.Join(root, WorkDir), 0o755); err != nil {
		t.Fatal(err)
	}
	catalog, err := OpenCatalog(filepath.Join(root, WorkDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	items, err := ReadTakeout(src)
	if err != nil {
		t.Fatal(err)
	}
	// As an older homewend filed it: at the root, by year and month.
	if _, err := Organize(items, root, catalog, Options{Location: time.UTC, Account: "google/ron@example.com"}, nil); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, "2019", "07", "IMG_1.jpg")
	if err := os.MkdirAll(filepath.Join(root, "2019", "07"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(old, filepath.Join(root, "2019", "07", "IMG_1.jpg")); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Dir(old))
	if err := catalog.setPath(Key{Hash: mustHash(t, filepath.Join(root, "2019", "07", "IMG_1.jpg"))}, "2019/07/IMG_1.jpg", "IMG_1.jpg"); err != nil {
		t.Fatal(err)
	}
	if !Filed(root) {
		t.Fatal("an old library is not seen as one")
	}

	profileOf := func(source string) string {
		if source == "google/ron@example.com" {
			return "ron"
		}
		return ""
	}
	if err := Relayout(root, catalog, profileOf, "ron", time.UTC); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(root, "ron", "2019", "07", "IMG_1.jpg"))
	mustExist(t, filepath.Join(root, "ron", "albums", "Greece 2019", "IMG_1.jpg"))
	for _, gone := range []string{"2019", "albums"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", gone)
		}
	}
	if Filed(root) {
		t.Error("the library is still seen as old")
	}
	if p, _, _ := catalog.PathOf(Key{mustHash(t, filepath.Join(root, "ron", "2019", "07", "IMG_1.jpg")), "ron"}); p != "ron/2019/07/IMG_1.jpg" {
		t.Errorf("the catalog says %q", p)
	}
}

// A photo of two people's accounts goes to both libraries, one file, each
// with its account's source and albums; one a library has already, imported
// before the move, is that one.
func TestAnOldLibraryMovesIntoTwoLibraries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, WorkDir, "catalog.db")
	if err := os.MkdirAll(filepath.Join(root, WorkDir), 0o755); err != nil {
		t.Fatal(err)
	}
	catalog, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	write(t, root, "2019/07/a.jpg", []byte("of two"))
	write(t, root, "2019/07/b.jpg", []byte("imported since"))
	write(t, root, "ron/2019/07/b.jpg", []byte("imported since"))
	a, b := mustHash(t, filepath.Join(root, "2019/07/a.jpg")), mustHash(t, filepath.Join(root, "2019/07/b.jpg"))
	taken := time.Date(2019, 7, 5, 0, 0, 0, 0, time.UTC)
	for _, p := range []struct {
		photo   Photo
		sources []string
	}{
		{Photo{Hash: a, Path: "2019/07/a.jpg", Name: "a.jpg", Taken: taken, Source: FromEXIF, Albums: []string{"Greece"}}, []string{"google/ron@x", "google/sam@x"}},
		{Photo{Hash: b, Path: "2019/07/b.jpg", Name: "b.jpg", Taken: taken, Source: FromEXIF}, []string{"google/ron@x"}},
		{Photo{Hash: b, Profile: "ron", Path: "ron/2019/07/b.jpg", Name: "b.jpg", Taken: taken, Source: FromEXIF}, []string{"local/CARD"}},
	} {
		if err := catalog.Put(p.photo, p.sources...); err != nil {
			t.Fatal(err)
		}
	}
	catalog.addAlbum("Greece", Key{Hash: a}, "google/ron@x")
	profileOf := func(source string) string { return strings.TrimSuffix(strings.TrimPrefix(source, "google/"), "@x") }
	for range 2 { // again finds nothing to move
		if err := Relayout(root, catalog, profileOf, "ron", time.UTC); err != nil {
			t.Fatal(err)
		}
		if !linked(filepath.Join(root, "ron/2019/07/a.jpg"), filepath.Join(root, "sam/2019/07/a.jpg")) {
			t.Error("not one file in the two libraries")
		}
		for _, profile := range []string{"ron", "sam"} {
			p, err := catalog.Photo(Key{a, profile})
			albums, sources, _ := catalog.About(Key{a, profile})
			if err != nil || p.Path != profile+"/2019/07/a.jpg" || len(sources) != 1 || sources[0] != "google/"+profile+"@x" || len(albums) != 1 {
				t.Errorf("%s's %+v %v: albums %v sources %v", profile, p, err, albums, sources)
			}
		}
		if n, _ := catalog.Count(Filter{}); n != 3 {
			t.Errorf("%d photos, want a twice and b once", n)
		}
		if list, _ := catalog.AlbumsByOwner(nil); len(list) != 2 || list[0] != (AlbumCount{"", "Greece", 1}) || list[1] != (AlbumCount{"google/ron@x", "Greece", 1}) {
			t.Errorf("albums %v", list)
		}
	}
	mustExist(t, filepath.Join(root, "ron/2019/07/b.jpg"))
}
