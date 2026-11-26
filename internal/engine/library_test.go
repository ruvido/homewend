// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

func TestOverviewOfTheLibrary(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, library.WorkDir), 0o755)
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	y := func(year int) time.Time { return time.Date(year, 6, 1, 0, 0, 0, 0, time.UTC) }
	for _, p := range []library.Photo{
		{Hash: "a", Path: "a", Name: "a", Taken: y(2024)},
		{Hash: "b", Path: "b", Name: "b", Taken: y(2024)},
		{Hash: "c", Path: "c", Name: "c", Taken: y(2019)},
		{Hash: "d", Path: "d", Name: "d"},
	} {
		c.Put(p)
	}
	c.Close()
	accounts := []AccountInfo{
		{ID: "profile", Profile: t.TempDir()}, // signed out
		{ID: "profile-sam", Email: "sam@example.com", Profile: t.TempDir()},
	}

	// Photos of no profile yet are nobody's to hide.
	o, err := overview(root, accounts, []string{"sam"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Accounts) != 1 || o.Total != 4 {
		t.Fatalf("got %+v", o)
	}
	want := []YearCount{{"2024", 2}, {"2019", 1}, {"", 1}}
	for i, w := range want {
		if o.Years[i] != w {
			t.Fatalf("years %+v, want %+v", o.Years, want)
		}
	}
}

func TestOverviewWithoutALibrary(t *testing.T) {
	o, err := overview("", nil, nil, "")
	if err != nil || o.Total != 0 {
		t.Fatalf("got %+v, %v", o, err)
	}
	// Chosen, on a disk that is not there: said, not taken for empty.
	away := filepath.Join(t.TempDir(), "unplugged")
	if o, err := overview(away, nil, nil, ""); err != nil || o.Gone != away {
		t.Errorf("got %+v, %v; want the library said gone", o, err)
	}
	// A share not mounted leaves its folder there, empty: a library whose
	// catalog was seen is away, not new. A folder never filled is new.
	empty := t.TempDir()
	if o, err := overview(empty, nil, nil, empty); err != nil || o.Gone != empty {
		t.Errorf("got %+v, %v; want the emptied library said gone", o, err)
	}
	if o, err := overview(empty, nil, nil, "/elsewhere"); err != nil || o.Gone != "" {
		t.Errorf("got %+v, %v; want a new folder taken for a new library", o, err)
	}
}

func TestFolderIsInTheProfile(t *testing.T) {
	accounts := []AccountInfo{
		{ID: "google/ann@example.com", Email: "ann@example.com", Profile: "ann"},
		{ID: "google/bob@example.com", Email: "bob@example.com", Profile: "bob"},
	}
	cases := []struct {
		name                 string
		hidden               []string
		account, year, album string
		noDate               bool
		want                 string
	}{
		{"two profiles shown", nil, "", "2025", "", false, "lib"},
		{"one shown", []string{"bob", "eve"}, "", "2025", "", false, "lib/ann/2025"},
		{"an account's album", nil, "google/bob@example.com", "", "Greece", false, "lib/bob/albums/Greece"},
		{"no date", []string{"ann", "eve"}, "", "", "", true, "lib/bob/undated"},
		{"the profile", []string{"ann", "eve"}, "", "", "", false, "lib/bob"},
		{"a person with no account", []string{"ann", "bob"}, "", "", "", false, "lib/eve"},
	}
	for _, c := range cases {
		got := folder("lib", accounts, []string{"ann", "bob", "eve"}, c.hidden, c.account, c.year, c.album, c.noDate)
		if got != filepath.FromSlash(c.want) {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

// The photos not on the disk are those of the people shown, everywhere:
// a hidden person's are not counted, nor taken out with the rest. None
// gone is none listed, not the whole library.
func TestMissingAreThoseOfThePeopleShown(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, library.WorkDir), 0o755)
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if gone := shownOf(missingIn(root), []string{"sam"}); len(gone) != 0 {
		t.Fatalf("%v; want none gone", gone)
	}
	if page, err := c.Page(library.Filter{Only: keysOf(nil)}); err != nil || len(page) != 0 {
		t.Fatalf("%d photos, %v; none gone must list none", len(page), err)
	}
	for _, h := range []string{"sam", "bob"} {
		c.Put(library.Photo{Hash: h, Profile: h, Path: h + "/" + h + ".jpg", Name: h + ".jpg"})
	}
	// Sam's photo in ann's library too: one tile, missing once.
	c.Put(library.Photo{Hash: "sam", Profile: "ann", Path: "ann/sam.jpg", Name: "sam.jpg"})
	c.Close()
	if _, err := lookForMissing(root); err != nil {
		t.Fatal(err)
	}
	o, err := overview(root, nil, []string{"bob"}, "")
	if err != nil || o.Missing[""] != 1 {
		t.Errorf("missing %v, %v; want sam's only, once", o.Missing, err)
	}
}

// What is taken off the list is off at once, and
// another library's list is never shown for this one.
func TestTheMissingListIsThisLibrarysNow(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, library.WorkDir), 0o755)
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, h := range []string{"a", "b"} {
		c.Put(library.Photo{Hash: h, Path: h + ".jpg", Name: h + ".jpg"})
	}
	missingIn(root)
	if list, err := lookForMissing(root); err != nil || len(list) != 2 {
		t.Fatalf("%v, %v", list, err)
	}
	offMissing([]library.Key{{Hash: "a"}})
	if list := missingIn(root); len(list) != 1 || list[0].Hash != "b" {
		t.Errorf("%v, want b", list)
	}
	if list := missingIn(t.TempDir()); len(list) != 0 {
		t.Errorf("another library: %v", list)
	}
}
