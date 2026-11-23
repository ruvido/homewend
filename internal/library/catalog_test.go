// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if done, _ := c.Mark(Archived, []string{"a"}, "ann", true); len(done) != 0 {
		t.Errorf("another profile archived %v", done)
	}
	for range 2 { // twice: archiving is idempotent
		if done, err := c.Mark(Archived, []string{"a", "zz"}, "ron", true); err != nil || len(done) != 1 {
			t.Fatalf("archived %v %v, want a", done, err)
		}
	}
	grid, _ := c.Page(Filter{Grid: true})
	album, _ := c.Page(Filter{Album: "Trip"})
	archive, _ := c.Page(Filter{Archived: true})
	years, _ := c.CountsByYear(nil)
	if every, _ := c.Page(Filter{}); len(every) != 2 { // Relayout's: every photo
		t.Errorf("every photo: %v", every)
	}
	if len(grid) != 1 || grid[0].Hash != "b" || len(album) != 1 || len(archive) != 1 || archive[0].Hash != "a" || years["2020"] != 1 {
		t.Errorf("grid %v album %v archive %v years %v", grid, album, archive, years)
	}
	if done, _ := c.Mark(Archived, []string{"a"}, "ron", false); len(done) != 1 {
		t.Errorf("unarchived %v", done)
	}
	if grid, _ := c.Page(Filter{Grid: true}); len(grid) != 2 {
		t.Errorf("back: %v", grid)
	}
}

// A photo in the Trash is in no list, not even its album, and is held as
// deleted: no import and no phone brings it in again; restored, it is back.
func TestAPhotoInTheTrash(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, WorkDir), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCatalog(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, h := range []string{"a", "b"} {
		if err := c.Put(Photo{Hash: h, Path: "ron/" + h, Name: h}); err != nil {
			t.Fatal(err)
		}
	}
	c.addAlbum("Trip", "a", "")
	if done, err := c.Mark(Trashed, []string{"a"}, "ron", true); err != nil || len(done) != 1 {
		t.Fatalf("trashed %v %v", done, err)
	}
	every, _ := c.Page(Filter{})
	album, _ := c.Page(Filter{Album: "Trip"})
	albums, _ := c.AlbumNames()
	if len(every) != 1 || len(album) != 0 || albums["Trip"] != 0 {
		t.Errorf("every %v album %v albums %v", every, album, albums)
	}
	// Its file is still there: in the Trash, not deleted, and not missing.
	if h, err := Holds(c, root, "a"); err != nil || !h.Known || h.Deleted || !h.Trashed {
		t.Errorf("held %+v %v, want it in the Trash", h, err)
	}
	if gone, _ := c.Missing(root); len(gone) != 1 || gone[0].Hash != "b" {
		t.Errorf("missing %v, want b only", gone)
	}
	c.Mark(Trashed, []string{"a"}, "ron", false)
	if every, _ := c.Page(Filter{}); len(every) != 2 {
		t.Errorf("restored: %v", every)
	}
}

// A profile's photos are those in its folder, by its exact name.
func TestTheProfilesPhotos(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, p := range []string{"ron/a", "ron/2020/b", "Ron/c", "ronald/d", "ron"} {
		if err := c.Put(Photo{Hash: p, Path: p, Name: p}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := c.Count(Filter{Profile: "ron"}); err != nil || n != 2 {
		t.Errorf("%d %v, want 2", n, err)
	}
}

// The library's bytes by kind: photos, raw files, videos, the Trash's too.
func TestBytesByKind(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, p := range []Photo{{Hash: "a", Path: "ron/a.jpg", Name: "a.jpg", Bytes: 10}, {Hash: "b", Path: "ron/b.dng", Name: "b.dng", Bytes: 100},
		{Hash: "c", Path: "ron/c.mp4", Name: "c.mp4", Bytes: 1000}, {Hash: "d", Path: "ron/d.heic", Name: "d.heic", Bytes: 5}} {
		if err := c.Put(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Mark(Trashed, []string{"d"}, "", true); err != nil {
		t.Fatal(err)
	}
	by, err := c.BytesByKind()
	if err != nil || by[""] != 15 || by["raw"] != 100 || by["video"] != 1000 {
		t.Errorf("%v %v", by, err)
	}
}

// Changes moves when the catalog is written, through the catalog's own pool
// or from a second catalog opened on the same file, as another process
// would; opening the catalog and reading it do not move it, or a screen that
// follows Changes would read the library again forever. A catalog gone, its
// disk ejected, moves it once and then holds.
func TestChangesFollowEveryWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	last := Changes(path)
	step := func(what string, moves bool) {
		t.Helper()
		// The clock of a file's time may be coarse: the next write is later.
		time.Sleep(20 * time.Millisecond)
		n := Changes(path)
		if (n != last) != moves {
			t.Fatalf("%s: Changes went from %q to %q", what, last, n)
		}
		last = n
	}

	other, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.CountsByYear(nil); err != nil {
		t.Fatal(err)
	}
	other.Close()
	step("opened and read", false)

	if err := c.Put(Photo{Hash: "a", Path: "ron/2026/11/a.jpg", Name: "a.jpg"}); err != nil {
		t.Fatal(err)
	}
	step("a photo put", true)
	step("nothing done", false)

	other, err = OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Mark(Archived, []string{"a"}, "ron", true); err != nil {
		t.Fatal(err)
	}
	other.Close()
	step("a photo archived by another catalog", true)

	c.Close()
	step("the last one closed", false)
	if c, err = OpenCatalog(path); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CountsByYear(nil); err != nil {
		t.Fatal(err)
	}
	c.Close()
	step("opened, read and closed alone", false)

	os.Remove(path)
	step("the catalog gone", true)
	step("still gone", false)
}

// walMode says whether the catalog at path is in WAL mode: byte 19 of its
// header is 2 (sqlite.org/fileformat2.html). Only with none of it open here.
func walMode(t *testing.T, path string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b[19] == 2
}

// inWAL leaves at a new path a catalog as an older version did when its
// terminal was closed: in WAL mode, its one photo in the -wal alone, no -shm.
func inWAL(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	open := filepath.Join(dir, "open.db")
	db, err := sql.Open("sqlite", "file:"+open+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema + `;INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('late', '2026/08/late.jpg', 'late.jpg', 1, 1, '', '', '')`); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "catalog.db")
	for _, end := range []string{"", "-wal"} {
		b, err := os.ReadFile(open + end)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+end, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if !walMode(t, path) {
		t.Fatal("not in WAL mode: nothing is tried")
	}
	return path
}

// A catalog an older version, or another system, left in WAL mode opens on
// every system with all it holds, and is kept as every other from then on.
func TestACatalogLeftInWALIsOpened(t *testing.T) {
	for name, open := range map[string]func(string) (*Catalog, error){"to write": OpenCatalog, "to look": OpenCatalogReadOnly} {
		t.Run(name, func(t *testing.T) {
			path := inWAL(t)
			c, err := open(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, known, err := c.PathOf("late"); err != nil || !known {
				t.Errorf("known %v, %v: the row in the -wal is lost", known, err)
			}
			c.Close()
			if walMode(t, path) || exists(path+"-wal") || exists(path+"-shm") {
				t.Error("still in WAL mode")
			}
		})
	}
}

// The window opens the catalog for several requests at once, the first time
// after an update too, when it is still in WAL mode.
func TestACatalogInWALIsOpenedByManyAtOnce(t *testing.T) {
	path := inWAL(t)
	errs := make(chan error)
	for i := range 8 {
		go func() {
			c, err := OpenCatalog(path)
			if err == nil {
				err = c.Put(Photo{Hash: fmt.Sprint(i), Path: fmt.Sprint(i), Name: "a"})
				c.Close()
			}
			errs <- err
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if n, err := c.Count(Filter{}); err != nil || n != 9 {
		t.Errorf("%d photos, %v, want 9", n, err)
	}
}

// Two catalogs on one file write and a third reads, all at once: none is
// turned away, and a look never sees half a write.
func TestWritersAndAReaderWaitForEachOther(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	first, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	const each = 40
	errs := make(chan error)
	for w := range 2 {
		go func() {
			c, err := OpenCatalog(path)
			for i := 0; err == nil && i < each; i++ {
				err = c.Put(Photo{Hash: fmt.Sprint(w, "-", i), Path: fmt.Sprint(w, "-", i), Name: "a"})
			}
			if c != nil {
				c.Close()
			}
			errs <- err
		}()
	}
	go func() {
		var err error
		for i := 0; err == nil && i < each; i++ {
			var c *Catalog
			if c, err = OpenCatalogReadOnly(path); err == nil {
				_, err = c.Page(Filter{})
				c.Close()
			}
		}
		errs <- err
	}()
	for range 3 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	c, err := OpenCatalogReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if n, err := c.Count(Filter{}); err != nil || n != 2*each {
		t.Errorf("%d photos, %v, want %d", n, err, 2*each)
	}
}

// A second process on the same library, as a command in a terminal while the
// window imports: both write, and open the catalog again and again as pages
// do. No row is lost and the catalog is whole.
func TestTwoProcessesWriteTheSameCatalog(t *testing.T) {
	const each = 300
	write := func(path, name string) error {
		c, err := OpenCatalog(path)
		if err != nil {
			return err
		}
		defer c.Close()
		for i := range each {
			hash := fmt.Sprint(name, i)
			if err := c.Put(Photo{Hash: hash, Path: hash, Name: "a"}); err != nil {
				return err
			}
			if _, err := c.Mark(Archived, []string{hash}, "", true); err != nil {
				return err
			}
			time.Sleep(2 * time.Millisecond) // as between an import's files
		}
		return nil
	}
	// Pages open and close the catalog while the same process writes it: a
	// lock let go by a closing would let the other process in mid-write.
	work := func(path, name string) error {
		done := make(chan error, 1)
		go func() { done <- write(path, name) }()
		for {
			select {
			case err := <-done:
				return err
			case <-time.After(5 * time.Millisecond):
			}
			c, err := OpenCatalog(path)
			if err != nil {
				return err
			}
			_, err = c.Page(Filter{})
			c.Close()
			if err == nil {
				err = look(path)
			}
			if err != nil {
				return err
			}
		}
	}
	if path := os.Getenv("HOMEWEND_TEST_CATALOG"); path != "" {
		if err := work(path, "b"); err != nil {
			t.Fatal(err)
		}
		return
	}
	path := filepath.Join(t.TempDir(), "catalog.db")
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	other := exec.Command(os.Args[0], "-test.run=^TestTwoProcessesWriteTheSameCatalog$")
	other.Env = append(os.Environ(), "HOMEWEND_TEST_CATALOG="+path)
	var said bytes.Buffer
	other.Stdout, other.Stderr = &said, &said
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	if err := work(path, "a"); err != nil {
		t.Error(err)
	}
	if err := other.Wait(); err != nil {
		t.Errorf("the other process: %v\n%s", err, said.String())
	}
	if c, err = OpenCatalog(path); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var whole string
	if err := c.db.QueryRow(`PRAGMA integrity_check`).Scan(&whole); err != nil || whole != "ok" {
		t.Errorf("integrity %q, %v", whole, err)
	}
	var n, archived int
	if err := c.db.QueryRow(`SELECT COUNT(*), SUM(archived) FROM photos`).Scan(&n, &archived); err != nil || n != 2*each || archived != 2*each {
		t.Errorf("%d photos, %d archived, %v, want %d of each", n, archived, err, 2*each)
	}
}

func look(path string) error {
	c, err := OpenCatalogReadOnly(path)
	if err != nil {
		return err
	}
	defer c.Close()
	_, _, err = c.PathOf("late")
	return err
}

// On a read-only share an older version's catalog can be looked at when its
// -wal holds nothing, and is refused, not read short, when it holds rows.
func TestALookWhereNothingCanBeWritten(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("a mode takes no one's writing away")
	}
	for name, rows := range map[string]bool{"nothing in the -wal": false, "rows in the -wal": true} {
		t.Run(name, func(t *testing.T) {
			path := inWAL(t)
			dir := filepath.Dir(path)
			if !rows {
				// Its rows in the catalog, then back in WAL mode, no -wal.
				db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
					t.Fatal(err)
				}
				db.Close()
			}
			if err := os.Chmod(dir, 0o500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(dir, 0o700) })
			err := look(path)
			if rows != (err != nil) {
				t.Errorf("looked: %v", err)
			}
		})
	}
}
