// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

func TestCountsByYear(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	put := func(hash string, taken time.Time) {
		if err := c.Put(Photo{Hash: hash, Path: hash, Name: hash, Taken: taken}); err != nil {
			t.Fatal(err)
		}
	}
	put("a", time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC))
	put("b", time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC))
	put("c", time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC))
	put("d", time.Time{})

	got, err := c.CountsByYear(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["2024"] != 2 || got["2019"] != 1 || got[""] != 1 || len(got) != 3 {
		t.Fatalf("got %v", got)
	}
}

func TestOwnersFilterThePhotos(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	y := time.Date(2024, 5, 1, 0, 0, 0, 0, time.UTC)
	for _, p := range []struct{ hash, owner string }{{"a", "ann@x"}, {"b", "bo@x"}, {"c", ""}, {"d", "ann@x"}} {
		c.Put(Photo{Hash: p.hash, Path: p.hash, Name: p.hash, Taken: y})
		c.addOwner(p.hash, p.owner)
	}
	c.addOwner("d", "bo@x") // in both exports
	c.addAlbum("Trip", "a", "ann@x")
	c.addAlbum("Kids", "b", "bo@x")

	count := func(accounts []string) int {
		got, err := c.CountsByYear(accounts)
		if err != nil {
			t.Fatal(err)
		}
		return got["2024"]
	}
	if count(nil) != 4 || count([]string{"ann@x"}) != 3 || count([]string{"bo@x"}) != 3 || count([]string{}) != 1 {
		t.Fatalf("all %d, ann %d, bo %d, nobody %d", count(nil), count([]string{"ann@x"}), count([]string{"bo@x"}), count([]string{}))
	}
	page, err := c.Page(Filter{Accounts: []string{"bo@x"}})
	if err != nil || len(page) != 3 {
		t.Fatalf("bo's page: %d, %v", len(page), err)
	}
	albums, err := c.AlbumsByOwner()
	if err != nil || len(albums) != 2 || albums[0] != (AlbumCount{"bo@x", "Kids", 1}) || albums[1] != (AlbumCount{"ann@x", "Trip", 1}) {
		t.Fatalf("albums %+v, %v", albums, err)
	}
}

func TestLatestAndAbout(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, h := range []string{"a", "b", "c"} {
		c.Put(Photo{Hash: h, Path: h, Name: h})
	}
	c.addAlbum("Trip", "b", "ann@x")
	c.addOwner("b", "ann@x")
	latest, err := c.Latest(2)
	if err != nil || len(latest) != 2 || latest[0].Hash != "c" || latest[1].Hash != "b" {
		t.Fatalf("latest %+v, %v", latest, err)
	}
	albums, accounts, err := c.About("b")
	if err != nil || len(albums) != 1 || albums[0] != "Trip" || len(accounts) != 1 || accounts[0] != "ann@x" {
		t.Fatalf("about: %v %v %v", albums, accounts, err)
	}
}

func TestATurnIsKeptAndComesRound(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Put(Photo{Hash: "a", Path: "a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []int{1, 2, 3, 0, 1} {
		if got, err := c.TurnLeft("a"); err != nil || got != want {
			t.Fatalf("turned to %d, %v; want %d", got, err, want)
		}
	}
	// Put again, as an import that finds it does: the turn stays.
	if err := c.Put(Photo{Hash: "a", Path: "a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	if page, err := c.Page(Filter{}); err != nil || len(page) != 1 || page[0].Turn != 1 {
		t.Fatalf("%+v, %v: want one photo turned once", page, err)
	}
	if _, err := c.TurnLeft("nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an unknown photo turned: %v", err)
	}
}

// An archived photo leaves the grid and the years, stays in its albums and
// in the archive, and only its profile's folder archives it.
func TestAnArchivedPhoto(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	taken := time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC)
	for _, h := range []string{"a", "b"} {
		if err := c.Put(Photo{Hash: h, Path: "ron/" + h, Name: h, Taken: taken}); err != nil {
			t.Fatal(err)
		}
	}
	c.addAlbum("Trip", "a", "")
	if done, _ := c.Archive([]string{"a"}, "ann", true); len(done) != 0 {
		t.Errorf("another profile archived %v", done)
	}
	for range 2 { // twice: archiving is idempotent
		if done, err := c.Archive([]string{"a", "zz"}, "ron", true); err != nil || len(done) != 1 {
			t.Fatalf("archived %v %v, want a", done, err)
		}
	}
	grid, _ := c.Page(Filter{})
	album, _ := c.Page(Filter{Album: "Trip"})
	archive, _ := c.Page(Filter{Archived: true})
	years, _ := c.CountsByYear(nil)
	if len(grid) != 1 || grid[0].Hash != "b" || len(album) != 1 || len(archive) != 1 || archive[0].Hash != "a" || years["2020"] != 1 {
		t.Errorf("grid %v album %v archive %v years %v", grid, album, archive, years)
	}
	if done, _ := c.Archive([]string{"a"}, "ron", false); len(done) != 1 {
		t.Errorf("unarchived %v", done)
	}
	if grid, _ := c.Page(Filter{}); len(grid) != 2 {
		t.Errorf("back: %v", grid)
	}
}
