// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"bytes"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// older makes a catalog as a program from before libraries left it, at this
// user_version, then filled by fill.
func older(t *testing.T, version int, fill string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	olderAt(t, path, version, olderSchema+fill)
	return path
}

func olderAt(t *testing.T, path string, version int, make string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(make + fmt.Sprintf(`;PRAGMA user_version = %d`, version)); err != nil {
		t.Fatal(err)
	}
}

// A photo of an older catalog is of the library its path is in, with its
// state, its albums and its sources; a catalog of any age opens, once, with a
// copy of it as it was.
func TestAnOlderCatalogIsBroughtUpToDate(t *testing.T) {
	const photos = `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES
		('a', 'ron/2019/07/a.jpg', 'a.jpg', 1, 100, 'exif', 'taken', 'camera'),
		('b', 'sam/messaging/2019/07/b.jpg', 'b.jpg', 2, 200, 'exif', 'taken', 'camera'),
		('c', '2019/07/c.jpg', 'c.jpg', 3, NULL, 'none', 'unknown', 'other'),
		('d', 'undated/d.jpg', 'd.jpg', 4, NULL, 'none', 'unknown', 'other'),
		('e', 'messaging/2019/07/e.jpg', 'e.jpg', 4, NULL, 'none', 'unknown', 'other'),
		('f', 'f.jpg', 'f.jpg', 4, NULL, 'none', 'unknown', 'other');`
	const photosOnly = `CREATE TABLE photos (hash TEXT PRIMARY KEY, path TEXT NOT NULL, name TEXT NOT NULL, bytes INTEGER NOT NULL,
		taken INTEGER, source TEXT NOT NULL, kind TEXT NOT NULL, origin TEXT NOT NULL);
		CREATE INDEX photos_by_time ON photos(taken);`
	const belongs = `INSERT INTO album_members VALUES ('Greece', 'a'), ('Kids', 'b'), ('Gone', 'nowhere');
		INSERT INTO owners VALUES ('%[1]sron@x', 'a'), ('%[1]ssam@x', 'b'), ('local/CARD', 'b'), ('%[1]sron@x', 'nowhere');
		INSERT INTO album_owners VALUES ('%[1]sron@x', 'Greece'), ('%[1]ssam@x', 'Kids');`
	const columns = `ALTER TABLE photos ADD COLUMN out INTEGER NOT NULL DEFAULT 0;`
	const more = `ALTER TABLE photos ADD COLUMN turn INTEGER NOT NULL DEFAULT 0; ALTER TABLE photos ADD COLUMN archived INTEGER NOT NULL DEFAULT 0;
		ALTER TABLE photos ADD COLUMN trashed INTEGER NOT NULL DEFAULT 0; ALTER TABLE photos ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0;
		UPDATE photos SET turn = 2, archived = 1 WHERE hash = 'a'; UPDATE photos SET favorite = 1, trashed = 1 WHERE hash = 'b';
		UPDATE photos SET out = 1 WHERE hash = 'c';`
	for name, c := range map[string]struct {
		version          int
		make             string
		sources, columns bool
	}{
		"with photos alone":              {0, photosOnly + photos, false, false},
		"with bare sources":              {0, olderSchema + photos + fmt.Sprintf(belongs, ""), true, false},
		"with those kept out":            {1, olderSchema + photos + fmt.Sprintf(belongs, "google/") + columns, true, false},
		"with everything":                {1, olderSchema + photos + fmt.Sprintf(belongs, "google/") + columns + more, true, true},
		"with everything, dates to turn": {0, olderSchema + photos + fmt.Sprintf(belongs, "google/") + columns + more, true, true},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "catalog.db")
			olderAt(t, path, c.version, c.make)
			for range 2 { // the second opening finds it done
				cat, err := OpenCatalog(path)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := cat.db.Query(`SELECT profile FROM library_photos ORDER BY hash`)
				got, err := texts(rows, err)
				if want := []string{"ron", "sam", "", "", "", ""}; err != nil || !reflect.DeepEqual(got, want) {
					t.Errorf("libraries %q %v, want %q", got, err, want)
				}
				a, err := cat.Photo(Key{"a", "ron"})
				if err != nil || a.Path != "ron/2019/07/a.jpg" || a.Taken.Unix() != 100 || a.Bytes != 1 {
					t.Errorf("a: %+v %v", a, err)
				}
				albums, sources, err := cat.About(Key{"a", "ron"})
				if c.sources && (err != nil || len(albums) != 1 || len(sources) != 1 || sources[0] != "google/ron@x") {
					t.Errorf("about a: %v %v %v", albums, sources, err)
				}
				if c.sources {
					by, err := cat.CountsByOwner()
					want := map[string]int{"google/ron@x": 1, "google/sam@x": 1, "local/CARD": 1}
					if c.columns { // sam's is in the Trash
						want = map[string]int{"google/ron@x": 1}
					}
					if err != nil || !reflect.DeepEqual(by, want) {
						t.Errorf("by owner %v %v, want %v", by, err, want)
					}
					list, err := cat.AlbumsByOwner(nil)
					if err != nil || len(list) == 0 || list[0] != (AlbumCount{"google/ron@x", "Greece", 1}) {
						t.Errorf("albums %v %v", list, err)
					}
				}
				if c.columns {
					trash, _ := cat.Page(Filter{Trashed: true})
					if a.Turn != 2 || !a.Archived || len(trash) != 1 || !trash[0].Favorite {
						t.Errorf("a %+v trash %+v", a, trash)
					}
					if n, _ := cat.Count(Filter{}); n != 4 {
						t.Errorf("%d in the lists, want 4: one kept out, one in the Trash", n)
					}
				}
				var old int
				if err := cat.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'old%'`).Scan(&old); err != nil || old != 0 {
					t.Errorf("%d left of the old tables, %v", old, err)
				}
				cat.Close()
			}
			kept, err := sql.Open("sqlite", path+beforeLibraries)
			if err != nil {
				t.Fatal(err)
			}
			defer kept.Close()
			var n int
			if err := kept.QueryRow(`SELECT COUNT(*) FROM photos`).Scan(&n); err != nil || n != 6 {
				t.Errorf("the copy as it was: %d photos, %v", n, err)
			}
		})
	}
}

// A new catalog has nothing to keep a copy of.
func TestANewCatalogKeepsNoCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if left, _ := filepath.Glob(path + "?*"); len(left) != 0 {
		t.Errorf("beside it: %v", left)
	}
}

// Two processes open an older catalog at once, the window and a command in a
// terminal: one brings it up to date, and neither fails.
func TestTwoProcessesBringItUpToDateOnce(t *testing.T) {
	if path := os.Getenv("HOMEWEND_TEST_OLDER"); path != "" {
		c, err := OpenCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		return
	}
	var rows strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&rows, `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('%[1]d', 'ron/2019/%[1]d.jpg', 'a.jpg', 1, 1, 'sidecar', '', '');`, i)
	}
	path := older(t, 1, "BEGIN;"+rows.String()+"COMMIT;")
	var others []*exec.Cmd
	for range 2 {
		other := exec.Command(os.Args[0], "-test.run=^TestTwoProcessesBringItUpToDateOnce$")
		other.Env = append(os.Environ(), "HOMEWEND_TEST_OLDER="+path)
		other.Stdout = &bytes.Buffer{}
		if err := other.Start(); err != nil {
			t.Fatal(err)
		}
		others = append(others, other)
	}
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, o := range others {
		if err := o.Wait(); err != nil {
			t.Errorf("another process: %v\n%s", err, o.Stdout)
		}
	}
	var whole string
	if err := c.db.QueryRow(`PRAGMA integrity_check`).Scan(&whole); err != nil || whole != "ok" {
		t.Errorf("integrity %q, %v", whole, err)
	}
	if n, err := c.Count(Filter{Profile: "ron"}); err != nil || n != 5000 {
		t.Errorf("%d photos, %v", n, err)
	}
	if copies, _ := filepath.Glob(path + beforeLibraries + "*"); len(copies) != 1 {
		t.Errorf("copies kept: %v, want one", copies)
	}
}

// A program from before libraries fails as it opens the catalog, writing or
// looking: it never changes a photo in every library that has it.
func TestAnOlderProgramIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Put(Photo{Hash: "a", Profile: "ron", Path: "ron/a", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	// What it runs at every opening, its index on photos included.
	if _, err := c.db.Exec(olderSchema + `CREATE INDEX IF NOT EXISTS photos_by_page ON photos(taken DESC, name);`); err == nil || !strings.Contains(err.Error(), "views may not be indexed") {
		t.Errorf("opened: %v", err)
	}
	for _, q := range []string{`SELECT path, trashed FROM photos WHERE hash = 'a'`, `UPDATE photos SET trashed = 1 WHERE hash = 'a'`, `DELETE FROM photos WHERE hash = 'a'`,
		`INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('b', '', '', 1, 1, '', '', '')`} {
		if rows, err := c.db.Query(q); err == nil {
			rows.Close()
			t.Errorf("done by an older program: %s", q)
		}
	}
	if n, err := c.Count(Filter{}); err != nil || n != 1 {
		t.Errorf("%d photos, %v", n, err)
	}
}

// The names an older program gave a photo in a second profile's folder are
// found once, by the file or, on a disk with no hard links, by its content:
// with the first's date, no state, the profile's own sources and albums.
func TestASecondProfilesNamesAreFound(t *testing.T) {
	for name, name2 := range map[string]func(a, b string) error{"linked": os.Link, "copied": func(a, b string) error {
		data, err := os.ReadFile(a)
		if err == nil {
			err = os.WriteFile(b, data, 0o644)
		}
		return err
	}} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "ron/2019/07/a.jpg", []byte("a photo of two"))
			write(t, root, "ron/2019/07/b.jpg", []byte("ron's alone"))
			write(t, root, "sam/2019/07/c.jpg", []byte("sam's alone"))
			write(t, root, "sam/2019/07/x.jpg", []byte("by hand, of 14")) // the size of a.jpg, nobody's
			write(t, root, "backup/a.jpg", []byte("a photo of two"))      // no profile's folder
			for _, to := range []string{"sam/2019/07/a-2.jpg", "sam/albums/Greece/a-2.jpg"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, to)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := name2(filepath.Join(root, "ron/2019/07/a.jpg"), filepath.Join(root, to)); err != nil {
					t.Fatal(err)
				}
			}
			a := mustHash(t, filepath.Join(root, "ron/2019/07/a.jpg"))
			fill := fmt.Sprintf(`INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES
				('%s', 'ron/2019/07/a.jpg', 'a.jpg', 14, 1562345678, 'given', 'taken', 'camera'),
				('%s', 'ron/2019/07/b.jpg', 'b.jpg', 11, 1562345678, 'exif', 'taken', 'camera'),
				('%s', 'sam/2019/07/c.jpg', 'c.jpg', 11, 1562345678, 'exif', 'taken', 'camera');`,
				a, mustHash(t, filepath.Join(root, "ron/2019/07/b.jpg")), mustHash(t, filepath.Join(root, "sam/2019/07/c.jpg"))) +
				fmt.Sprintf(`INSERT INTO owners VALUES ('google/ron@x', '%[1]s'), ('google/sam@x', '%[1]s'), ('local/CARD', '%[1]s');
				INSERT INTO album_members VALUES ('Greece', '%[1]s'), ('Ours', '%[1]s');
				INSERT INTO album_owners VALUES ('google/sam@x', 'Greece'), ('google/ron@x', 'Ours');
				ALTER TABLE photos ADD COLUMN favorite INTEGER NOT NULL DEFAULT 0; ALTER TABLE photos ADD COLUMN trashed INTEGER NOT NULL DEFAULT 0;
				UPDATE photos SET favorite = 1, trashed = 1 WHERE hash = '%[1]s';`, a)
			if err := os.MkdirAll(filepath.Join(root, "ron", "albums", "Ours"), 0o755); err != nil {
				t.Fatal(err)
			}
			path := older(t, 1, fill)
			find := func() {
				t.Helper()
				err := FindLinks(root, path, []string{"sam"}, func(s string) string {
					return strings.TrimSuffix(strings.TrimPrefix(s, "google/"), "@x")
				}, func() (func(), error) { return func() {}, nil })
				if err != nil {
					t.Fatal(err)
				}
			}
			find()
			c, err := OpenCatalog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if n, err := c.Count(Filter{Profile: "sam"}); err != nil || n != 2 {
				t.Errorf("sam's: %d %v, want 2", n, err)
			}
			if n, _ := c.Count(Filter{Profile: "backup"}); n != 0 {
				t.Errorf("a folder that is no profile's became one: %d", n)
			}
			p, err := c.Photo(Key{a, "sam"})
			if err != nil || p.Path != "sam/2019/07/a-2.jpg" || p.Name != "a-2.jpg" || p.Taken.Unix() != 1562345678 || p.Source != FromGiven || p.Favorite {
				t.Errorf("sam's %+v %v", p, err)
			}
			if trash, err := c.Page(Filter{Trashed: true}); err != nil || len(trash) != 1 || trash[0].Profile != "ron" || !trash[0].Favorite {
				t.Errorf("the Trash %+v %v, want ron's alone", trash, err)
			}
			for profile, want := range map[string][2][]string{"ron": {{"Ours"}, {"google/ron@x", "local/CARD"}}, "sam": {{"Greece"}, {"google/sam@x", "local/CARD"}}} {
				albums, sources, err := c.About(Key{a, profile})
				if err != nil || !reflect.DeepEqual(albums, want[0]) || !reflect.DeepEqual(sources, want[1]) {
					t.Errorf("%s's albums %v sources %v %v, want %v", profile, albums, sources, err, want)
				}
			}
			rows, err := c.db.Query(`SELECT account || ' ' || album || ' ' || profile FROM album_owners ORDER BY 1`)
			if got, err := texts(rows, err); err != nil || !reflect.DeepEqual(got, []string{"google/ron@x Ours ron", "google/sam@x Greece sam"}) {
				t.Errorf("album owners %q %v", got, err)
			}
			// Once: a name taken out since is not found again.
			if err := c.TakeOut([]Key{{a, "sam"}}, false); err != nil {
				t.Fatal(err)
			}
			find()
			if n, _ := c.Count(Filter{Profile: "sam"}); n != 1 {
				t.Errorf("sam's: %d, looked for a second time", n)
			}
		})
	}
}

// The same content in two libraries is two photos: what is done to one is
// not done to the other.
func TestAPhotoOfTwoLibraries(t *testing.T) {
	root := t.TempDir()
	write(t, root, "ron/a.jpg", []byte("a photo of two"))
	path := filepath.Join(root, WorkDir, "catalog.db")
	if err := os.MkdirAll(filepath.Join(root, WorkDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "sam"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "ron/a.jpg"), filepath.Join(root, "sam/a.jpg")); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	hash := mustHash(t, filepath.Join(root, "ron/a.jpg"))
	ron, sam := Key{hash, "ron"}, Key{hash, "sam"}
	y2019 := time.Date(2019, 5, 1, 10, 0, 0, 0, time.UTC)
	for _, k := range []Key{ron, sam} {
		if err := c.Put(Photo{Hash: hash, Profile: k.Profile, Path: k.Profile + "/a.jpg", Name: "a.jpg", Bytes: 14, Taken: y2019, Albums: []string{"Trip"}}, "google/"+k.Profile+"@x"); err != nil {
			t.Fatal(err)
		}
	}
	c.addAlbum("Trip", ron, "google/ron@x")
	c.addAlbum("Trip", sam, "google/sam@x")
	if by, err := c.BytesByKind(); err != nil || by[""] != 14 {
		t.Errorf("bytes %v %v, want the file once", by, err)
	}
	if n, _ := c.Count(Filter{}); n != 2 {
		t.Errorf("%d photos, want two", n)
	}
	for _, p := range []string{"ron", "sam"} {
		page, err := c.Page(Filter{Album: "Trip", AlbumOf: "google/" + p + "@x"})
		if err != nil || len(page) != 1 || page[0].Profile != p {
			t.Errorf("%s's album: %+v %v", p, page, err)
		}
		if page, _ := c.Page(Filter{Accounts: []string{"google/" + p + "@x"}}); len(page) != 1 || page[0].Profile != p {
			t.Errorf("%s's account alone: %+v", p, page)
		}
	}
	if list, _ := c.AlbumsByOwner([]string{"google/ron@x"}); len(list) != 1 || list[0] != (AlbumCount{"google/ron@x", "Trip", 1}) {
		t.Errorf("albums with sam's account hidden: %v", list)
	}
	for _, state := range []State{Archived, Trashed, Favorite} {
		if done, err := c.Mark(state, []Key{ron, {hash, "ann"}}, true); err != nil || len(done) != 1 || done[0] != ron {
			t.Fatalf("%s: %v %v", state, done, err)
		}
	}
	if n, err := c.TurnLeft(ron); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if p, err := c.Photo(sam); err != nil || p.Archived || p.Favorite || p.Turn != 0 {
		t.Errorf("sam's %+v %v", p, err)
	}
	if h, err := Holds(c, root, sam); err != nil || h.Out() || h.Archived || h.Elsewhere {
		t.Errorf("sam holds %+v %v", h, err)
	}
	if h, err := Holds(c, root, Key{hash, "ann"}); err != nil || h.Known || h.Out() || !h.Elsewhere {
		t.Errorf("ann holds %+v %v", h, err)
	}
	if page, err := c.Page(Filter{Trashed: true, Only: []Key{ron, sam}}); err != nil || len(page) != 1 || page[0].Key() != ron {
		t.Errorf("of the two, in the Trash: %+v %v", page, err)
	}
	// A date given in one library moves that one's name.
	if n, err := GiveDates(root, c, []Key{sam}, time.Date(2020, 6, 1, 0, 0, 0, 0, time.UTC)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	mustExist(t, filepath.Join(root, "ron/a.jpg"))
	mustExist(t, filepath.Join(root, "sam/2020/06/a.jpg"))
	if p, _ := c.Photo(ron); !p.Taken.Equal(y2019) {
		t.Errorf("ron's date moved to %v", p.Taken)
	}
	// Deleted from one's folder, it is missing there alone, and taken out there alone.
	if _, err := c.Mark(Trashed, []Key{ron}, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "ron/a.jpg")); err != nil {
		t.Fatal(err)
	}
	gone, err := Missing(path, root)
	if err != nil || len(gone) != 1 || gone[0].Key != ron {
		t.Fatalf("missing %v %v, want ron's", gone, err)
	}
	if h, _ := Holds(c, root, ron); !h.Deleted || !h.Elsewhere {
		t.Errorf("ron holds %+v, want it deleted", h)
	}
	if err := c.TakeOut([]Key{ron}, false); err != nil {
		t.Fatal(err)
	}
	albums, sources, err := c.About(sam)
	if n, _ := c.Count(Filter{}); err != nil || n != 1 || len(albums) != 1 || len(sources) != 1 {
		t.Errorf("sam's left: %d, albums %v sources %v %v", n, albums, sources, err)
	}
	mustExist(t, filepath.Join(root, "sam/2020/06/a.jpg"))
}

// A file changed by another program is no second name to give: its bytes are
// not the photo's any more.
func TestAChangedFileIsNotShared(t *testing.T) {
	card, root := cameraCard(t), t.TempDir()
	importCard(t, card, root, false)
	c, err := OpenCatalog(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	write(t, root, "ron/2018/01/DSCF0002.JPG", []byte("changed"))
	folder, err := ReadFolder(card)
	if err != nil {
		t.Fatal(err)
	}
	done, err := Import(t.Context(), folder, root, c, Options{Profile: "sam"}, ImportOptions{Fits: func(int64) error { return nil }}, nil)
	if err != nil || done.Placed != 3 || done.Bytes == 0 {
		t.Fatalf("%+v %v", done, err)
	}
	if got, want := mustHash(t, filepath.Join(root, "sam/2018/01/DSCF0002.JPG")), mustHash(t, filepath.Join(card, "DCIM", "100_FUJI", "DSCF0002.JPG")); got != want {
		t.Error("sam was given the changed file")
	}
}

// A photo the second library has a record of already, received before the
// names on the disk were looked for or while they are, still takes what is its own of
// the first's record: none of it is lost. An album's entry of the same name and
// another content is not its.
func TestAPhotoReceivedMeanwhileTakesWhatIsItsOwn(t *testing.T) {
	for _, when := range []string{"before", "during"} {
		t.Run(when, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, "ron/2019/07/a.jpg", []byte("a photo of two"))
			for _, to := range []string{"sam/2019/07/a-2.jpg", "sam/albums/Greece/a-2.jpg"} {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, to)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(filepath.Join(root, "ron/2019/07/a.jpg"), filepath.Join(root, to)); err != nil {
					t.Fatal(err)
				}
			}
			write(t, root, "sam/albums/Other/a-2.jpg", []byte("another photo!"))
			a := mustHash(t, filepath.Join(root, "ron/2019/07/a.jpg"))
			path := older(t, 1, fmt.Sprintf(`INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES
				('%[1]s', 'ron/2019/07/a.jpg', 'a.jpg', 14, 1562345678, 'exif', 'taken', 'camera');
				INSERT INTO owners VALUES ('google/ron@x', '%[1]s'), ('google/sam@x', '%[1]s');
				INSERT INTO album_members VALUES ('Greece', '%[1]s'), ('Other', '%[1]s');
				INSERT INTO album_owners VALUES ('google/sam@x', 'Greece'), ('google/ron@x', 'Other');`, a))
			c, err := OpenCatalog(path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			receive := func() {
				if err := c.Put(Photo{Hash: a, Profile: "sam", Path: "sam/2019/07/a-2.jpg", Name: "a-2.jpg", Bytes: 14}, "local/Pixel"); err != nil {
					t.Fatal(err)
				}
			}
			if when == "before" {
				receive()
			}
			err = FindLinks(root, path, []string{"sam"}, func(s string) string {
				return strings.TrimSuffix(strings.TrimPrefix(s, "google/"), "@x")
			}, func() (func(), error) {
				if when == "during" {
					receive()
				}
				return func() {}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			for profile, want := range map[string][2][]string{"ron": {{"Other"}, {"google/ron@x"}}, "sam": {{"Greece"}, {"google/sam@x", "local/Pixel"}}} {
				albums, sources, err := c.About(Key{a, profile})
				if err != nil || !reflect.DeepEqual(albums, want[0]) || !reflect.DeepEqual(sources, want[1]) {
					t.Errorf("%s's albums %v sources %v %v, want %v", profile, albums, sources, err, want)
				}
			}
			if list, _ := c.AlbumsByOwner(nil); len(list) != 2 || list[0] != (AlbumCount{"google/sam@x", "Greece", 1}) {
				t.Errorf("albums %v", list)
			}
		})
	}
}

// A profile's folder is its own whatever its name looks like; the layout
// before profiles is known by its whole path.
func TestAProfileNamedLikeAYear(t *testing.T) {
	want := map[string]string{"12345/2019/07/a.jpg": "12345", "1985/2019/07/a.jpg": "1985", "1985/undated/a.jpg": "1985", "1985/messaging/2019/07/a.jpg": "1985", "undated/2019/07/a.jpg": "undated",
		"1985/07/a.jpg": "", "1985/unknown-month/a.jpg": "", "undated/a.jpg": "", "messaging/2019/07/a.jpg": "", "messaging/undated/a.jpg": "", "a.jpg": "", "ron/undated/a.jpg": "ron"}
	var rows strings.Builder
	for p := range want {
		fmt.Fprintf(&rows, `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('%[1]s', '%[1]s', 'a.jpg', 1, 1, '', '', '');`, p)
	}
	c, err := OpenCatalog(older(t, 1, rows.String()))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	all, err := c.photos(``)
	if err != nil || len(all) != len(want) {
		t.Fatal(len(all), err)
	}
	for _, p := range all {
		if p.Profile != want[p.Path] {
			t.Errorf("%s is of %q, want %q", p.Path, p.Profile, want[p.Path])
		}
		if want[p.Path] == "" && p.Path != "a.jpg" && !datePath.MatchString(p.Path) {
			t.Errorf("%s: the two ways to know the layout differ", p.Path)
		}
	}
}

// What a killed upgrade left of its copy goes at the next opening; a catalog
// an older program made again, after going back to it, has a copy of its own.
func TestTheCopyOfAnOlderCatalog(t *testing.T) {
	const photo = `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('%s', 'ron/2019/07/a.jpg', 'a.jpg', 1, 1, '', '', '')`
	path := older(t, 1, fmt.Sprintf(photo, "first"))
	for _, left := range []string{".part", ".part-journal"} {
		if err := os.WriteFile(path+beforeLibraries+left, []byte("half"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, hash := range []string{"first", "second"} {
		c, err := OpenCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		if hash == "first" {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			olderAt(t, path, 1, olderSchema+fmt.Sprintf(photo, "second"))
		}
	}
	for name, want := range map[string]string{path + beforeLibraries: "first", path + beforeLibraries + "-2": "second"} {
		db, err := sql.Open("sqlite", name)
		if err != nil {
			t.Fatal(err)
		}
		var got string
		if err := db.QueryRow(`SELECT hash FROM photos`).Scan(&got); err != nil || got != want {
			t.Errorf("%s holds %q %v, want %q", filepath.Base(name), got, err, want)
		}
		db.Close()
	}
	if left, _ := filepath.Glob(path + beforeLibraries + ".part*"); len(left) != 0 {
		t.Errorf("left: %v", left)
	}
}

// An opening waits for another that takes longer than any other wait to bring
// the catalog up to date: it does not fail.
func TestAnOpeningWaitsForALongUpgrade(t *testing.T) {
	was := waiting
	waiting = "_pragma=busy_timeout(50)"
	t.Cleanup(func() { waiting = was })
	for name, open := range map[string]func(string) (*Catalog, error){"to write": OpenCatalog, "to look": OpenCatalogReadOnly} {
		path := older(t, 1, `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('a', 'ron/2019/07/a.jpg', 'a.jpg', 1, 1, '', '', '')`)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		// As an upgrade does once it writes what it has in memory.
		if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(time.Second)
			db.Exec(`COMMIT`)
			db.Close()
		}()
		c, err := open(path)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n, err := c.Count(Filter{Profile: "ron"}); err != nil || n != 1 {
			t.Errorf("%s: %d %v", name, n, err)
		}
		c.Close()
	}
}

// On macOS a connection that reads keeps every other out, this program's own
// too: nothing holds two at once. Locks as exclusive are tried here.
func TestAnUpgradeUsesOneConnection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no such locks here")
	}
	t.Setenv("HOMEWEND_TEST_VFS", "unix-dotfile")
	path := older(t, 1, `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('a', 'ron/2019/07/a.jpg', 'a.jpg', 1, 1, '', '', '')`)
	done := make(chan error, 8)
	for range 8 {
		go func() {
			c, err := OpenCatalog(path)
			if err == nil {
				_, err = c.CountsByYear(nil)
				c.Close()
			}
			done <- err
		}()
	}
	for range 8 {
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("it waits for itself")
		}
	}
	if !exists(path + beforeLibraries) {
		t.Error("no copy kept")
	}
}

// A catalog written between its copy and the lock is copied again: the copy
// kept is what was brought up to date.
func TestTheCopyIsOfWhatWasUpgraded(t *testing.T) {
	path := older(t, 1, `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('a', 'ron/2019/07/a.jpg', 'a.jpg', 1, 1, '', '', '')`)
	db, err := sql.Open("sqlite", catalogDSN(path, waiting+"&_txlock=immediate"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA data_version`); err != nil {
		t.Fatal(err)
	}
	// An older program, holding the lock as the copy is made, then writing.
	other, err := sql.Open("sqlite", catalogDSN(path, waiting))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.SetMaxOpenConns(1)
	if _, err := other.Exec(`BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	go func() {
		for i := 0; i < 200; i++ {
			if left, _ := filepath.Glob(path + beforeLibraries + ".part*"); len(left) > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(300 * time.Millisecond)
		other.Exec(`INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('late', 'ron/2019/07/b.jpg', 'b.jpg', 1, 1, '', '', '')`)
		other.Exec(`COMMIT`)
	}()
	if err := upgrade(db, path); err != nil {
		t.Fatal(err)
	}
	kept, err := sql.Open("sqlite", path+beforeLibraries)
	if err != nil {
		t.Fatal(err)
	}
	defer kept.Close()
	var n int
	if err := kept.QueryRow(`SELECT COUNT(*) FROM photos`).Scan(&n); err != nil || n != 2 {
		t.Errorf("the copy holds %d %v, want both", n, err)
	}
}

// An album's entry keeps the name the photo had as it was made: the album is
// hers all the same.
func TestAnAlbumEntryOfAnotherName(t *testing.T) {
	root := t.TempDir()
	write(t, root, "ron/2019/07/a.jpg", []byte("a photo of two"))
	for _, to := range []string{"sam/2020/01/a-2.jpg", "sam/albums/Greece/a.jpg"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, to)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(filepath.Join(root, "ron/2019/07/a.jpg"), filepath.Join(root, to)); err != nil {
			t.Fatal(err)
		}
	}
	a := mustHash(t, filepath.Join(root, "ron/2019/07/a.jpg"))
	path := older(t, 1, fmt.Sprintf(`INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('%[1]s', 'ron/2019/07/a.jpg', 'a.jpg', 14, 1, '', '', '');
		INSERT INTO album_members VALUES ('Greece', '%[1]s')`, a))
	if err := FindLinks(root, path, []string{"sam"}, func(string) string { return "" }, func() (func(), error) { return func() {}, nil }); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if albums, _, err := c.About(Key{a, "sam"}); err != nil || len(albums) != 1 {
		t.Errorf("sam's albums %v %v", albums, err)
	}
	if albums, _, _ := c.About(Key{a, "ron"}); len(albums) != 0 {
		t.Errorf("ron's albums %v", albums)
	}
}

// An older program that never stops writing, an import, does not keep the
// window from opening.
func TestAnUpgradeDoesNotWaitForAnImport(t *testing.T) {
	path := older(t, 1, `INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES ('a', 'ron/2019/07/a.jpg', 'a.jpg', 1, 1, '', '', '')`)
	other, err := sql.Open("sqlite", catalogDSN(path, waiting))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.SetMaxOpenConns(1)
	stop, stopped := make(chan struct{}), make(chan struct{})
	wrote := 0
	go func() {
		defer close(stopped)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := other.Exec(`INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin) VALUES (?, ?, 'b.jpg', 1, 1, '', '', '')`, fmt.Sprint("w", wrote), fmt.Sprint("ron/2019/07/w", wrote)); err != nil {
				return // upgraded under it: it fails clearly
			}
			wrote++
			time.Sleep(5 * time.Millisecond) // the file's copying, less than any was seen to take
		}
	}()
	opened := make(chan error, 1)
	go func() {
		c, err := OpenCatalog(path)
		if err == nil {
			c.Close()
		}
		opened <- err
	}()
	select {
	case err := <-opened:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(60 * time.Second):
		t.Error("never opened")
	}
	close(stop)
	<-stopped
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if n, err := c.Count(Filter{}); err != nil || n != wrote+1 {
		t.Errorf("%d photos, %v, want %d", n, err, wrote+1)
	}
}

// A photo kept out is not live: its copy in the Trash stays there.
func TestAPhotoKeptOutBringsNothingBack(t *testing.T) {
	c, err := OpenCatalog(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, k := range []struct{ profile, path string }{{"", "2019/07/a.jpg"}, {"ron", "ron/2019/07/a.jpg"}} {
		if err := c.Put(Photo{Hash: "a", Profile: k.profile, Path: k.path, Name: "a.jpg"}); err != nil {
			t.Fatal(err)
		}
	}
	for q, p := range map[string]string{`UPDATE library_photos SET out = 1 WHERE profile = ?`: "", `UPDATE library_photos SET trashed = 1 WHERE profile = ?`: "ron"} {
		if _, err := c.db.Exec(q, p); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.refile(Key{Hash: "a"}, "ron", "ron/2019/07/a.jpg", false); err != nil {
		t.Fatal(err)
	}
	var trashed bool
	if err := c.db.QueryRow(`SELECT trashed FROM library_photos WHERE profile = 'ron'`).Scan(&trashed); err != nil || !trashed {
		t.Errorf("trashed %v %v, want it kept in the Trash", trashed, err)
	}
}
