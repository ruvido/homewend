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
	if !Filed(root, nil) {
		t.Fatal("an old library is not seen as one")
	}

	profileOf := func(source string) string {
		if source == "google/ron@example.com" {
			return "ron"
		}
		return ""
	}
	if err := Relayout(root, catalog, nil, profileOf, "ron", time.UTC); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(root, "ron", "2019", "07", "IMG_1.jpg"))
	mustExist(t, filepath.Join(root, "ron", "albums", "Greece 2019", "IMG_1.jpg"))
	for _, gone := range []string{"2019", "albums"} {
		if _, err := os.Stat(filepath.Join(root, gone)); !os.IsNotExist(err) {
			t.Errorf("%s is still there", gone)
		}
	}
	if Filed(root, nil) {
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
		if err := Relayout(root, catalog, nil, profileOf, "ron", time.UTC); err != nil {
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

// relaying is a library with these photos recorded before profiles.
func relaying(t *testing.T, files map[string]string) (string, *Catalog, map[string]string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, WorkDir), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCatalog(filepath.Join(root, WorkDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	hashes := map[string]string{}
	for path, body := range files {
		write(t, root, path, []byte(body))
		hashes[path] = mustHash(t, filepath.Join(root, path))
		if err := c.Put(Photo{Hash: hashes[path], Path: path, Name: filepath.Base(path), Bytes: int64(len(body)), Taken: time.Date(2019, 7, 5, 0, 0, 0, 0, time.UTC), Source: FromEXIF}, "google/ron@x"); err != nil {
			t.Fatal(err)
		}
	}
	return root, c, hashes
}

func ronsAccount(source string) string {
	return strings.TrimSuffix(strings.TrimPrefix(source, "google/"), "@x")
}

// A photo in the Trash is moved as the others are, its file is never cleared
// away as another's second name, and nothing is left to move at every start.
func TestAnOldLibrarysTrashKeepsItsFile(t *testing.T) {
	root, c, hashes := relaying(t, map[string]string{"2019/07/a.jpg": "a photo"})
	old := Key{Hash: hashes["2019/07/a.jpg"]}
	if _, err := c.Mark(Trashed, []Key{old}, true); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ann/2019/07"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "2019/07/a.jpg"), filepath.Join(root, "ann/2019/07/a.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(Photo{Hash: old.Hash, Profile: "ann", Path: "ann/2019/07/a.jpg", Name: "a.jpg", Bytes: 7}); err != nil {
		t.Fatal(err)
	}
	// With nobody to file it under yet, it is left, and its file too.
	if err := Relayout(root, c, nil, func(string) string { return "" }, "", time.UTC); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(root, "2019/07/a.jpg"))
	if err := Relayout(root, c, nil, ronsAccount, "ron", time.UTC); err != nil {
		t.Fatal(err)
	}
	trash, err := c.Page(Filter{Trashed: true})
	if err != nil || len(trash) != 1 || trash[0].Profile != "ron" || !onDisk(root, trash[0]) {
		t.Errorf("the Trash %+v %v, want ron's with its file", trash, err)
	}
	mustExist(t, filepath.Join(root, "ann/2019/07/a.jpg"))
	if Filed(root, nil) {
		t.Error("it would be moved again at every start")
	}
}

// A photo its library has already, imported since, gives it its heart, its
// archive, its turn, albums and sources; its file goes only when it is the
// same, and never unrecorded.
func TestAnOldPhotoImportedSinceIsOne(t *testing.T) {
	for name, body := range map[string]string{"a second name": "", "a copy": "the photo", "changed since": "another!!", "deleted since": "-"} {
		t.Run(name, func(t *testing.T) {
			root, c, hashes := relaying(t, map[string]string{"2019/07/a.jpg": "the photo"})
			hash := hashes["2019/07/a.jpg"]
			old, its := filepath.Join(root, "2019/07/a.jpg"), filepath.Join(root, "ron/2020/01/a.jpg")
			if err := os.MkdirAll(filepath.Dir(its), 0o755); err != nil {
				t.Fatal(err)
			}
			switch body {
			case "":
				if err := os.Link(old, its); err != nil {
					t.Fatal(err)
				}
			case "-":
			default:
				write(t, root, "ron/2020/01/a.jpg", []byte(body))
			}
			if err := c.Put(Photo{Hash: hash, Profile: "ron", Path: "ron/2020/01/a.jpg", Name: "a.jpg", Bytes: 9}, "local/CARD"); err != nil {
				t.Fatal(err)
			}
			for _, s := range []State{Favorite, Archived} {
				c.Mark(s, []Key{{Hash: hash}}, true)
			}
			c.TurnLeft(Key{Hash: hash})
			c.addAlbum("Greece", Key{Hash: hash}, "google/ron@x")
			if err := Relayout(root, c, nil, ronsAccount, "ron", time.UTC); err != nil {
				t.Fatal(err)
			}
			all, err := c.photos(``)
			if err != nil {
				t.Fatal(err)
			}
			if name == "changed since" {
				if len(all) != 2 || !exists(old) || !exists(its) {
					t.Errorf("%+v, old there %v: want both as they were", all, exists(old))
				}
				return
			}
			if len(all) != 1 || all[0].Profile != "ron" || !all[0].Favorite || !all[0].Archived || all[0].Turn != 1 || !onDisk(root, all[0]) {
				t.Fatalf("%+v, want ron's with all it had, and its file", all)
			}
			if got := mustHash(t, filepath.Join(root, all[0].Path)); got != hash {
				t.Error("its file is another")
			}
			albums, sources, _ := c.About(all[0].Key())
			if len(albums) != 1 || len(sources) != 2 {
				t.Errorf("albums %v sources %v", albums, sources)
			}
			if exists(old) {
				t.Error("the old file is still there, recorded by nothing")
			}
		})
	}
}

// A run killed between the photo's move and its record is finished by the
// next: the file is found where it went.
func TestAnOldPhotoMovedAndNotRecordedIsFound(t *testing.T) {
	root, c, hashes := relaying(t, map[string]string{"2019/07/a.jpg": "the photo"})
	write(t, root, "ron/2019/07/a.jpg", []byte("another of that name"))
	if err := move(filepath.Join(root, "2019/07/a.jpg"), filepath.Join(root, "ron/2019/07/a-2.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := Relayout(root, c, nil, ronsAccount, "ron", time.UTC); err != nil {
		t.Fatal(err)
	}
	if p, err := c.Photo(Key{hashes["2019/07/a.jpg"], "ron"}); err != nil || p.Path != "ron/2019/07/a-2.jpg" {
		t.Errorf("%+v %v", p, err)
	}
}

// The one path both layouts could have is the profile's, where there is one
// of that name.
func TestAProfileNamedMessaging(t *testing.T) {
	root, c, hashes := relaying(t, map[string]string{"messaging/2019/07/a.jpg": "the photo"})
	if err := Relayout(root, c, []string{"messaging"}, ronsAccount, "ron", time.UTC); err != nil {
		t.Fatal(err)
	}
	if p, err := c.Photo(Key{hashes["messaging/2019/07/a.jpg"], "messaging"}); err != nil || p.Path != "messaging/2019/07/a.jpg" || !onDisk(root, p) {
		t.Errorf("%+v %v", p, err)
	}
}
