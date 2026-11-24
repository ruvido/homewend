// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// The catalog's user_version, as it grew.
const (
	// Every date is a clock. Before, Google's dates were kept as instants in
	// UTC while the camera's were clocks, and the grid, which reads a clock,
	// showed a photo from Takeout one or two hours off, near midnight on the
	// wrong day.
	clocksSince = 1
	// Each photo is of a library. Before, one row was everyone's.
	librariesSince = 2
	// Marks are kept. 3 is a catalog without them whose links were found.
	marksSince = 4
	// The names older programs gave a photo in a second profile's folder, and
	// not in the catalog, were looked for (FindLinks).
	linksSince = 5
)

// linksBeforeMarks is linksSince as a program from before marks writes it.
const linksBeforeMarks = 3

// beforeLibraries ends the name of the copy kept of an older catalog.
const beforeLibraries = ".before-libraries"

// olderSchema is how the catalog was before libraries; one older still lacks
// some of it.
const olderSchema = `
CREATE TABLE IF NOT EXISTS photos (
  hash    TEXT PRIMARY KEY,
  path    TEXT NOT NULL,
  name    TEXT NOT NULL,
  bytes   INTEGER NOT NULL,
  taken   INTEGER,
  source  TEXT NOT NULL,
  kind    TEXT NOT NULL,
  origin  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS album_members (album TEXT NOT NULL, hash TEXT NOT NULL, PRIMARY KEY (album, hash));
CREATE TABLE IF NOT EXISTS owners (account TEXT NOT NULL, hash TEXT NOT NULL, PRIMARY KEY (account, hash));
CREATE TABLE IF NOT EXISTS album_owners (account TEXT NOT NULL, album TEXT NOT NULL, PRIMARY KEY (account, album));
`

// setAside moves an older catalog's tables out of the way of the new ones.
const setAside = `
DROP INDEX IF EXISTS photos_by_page;
DROP INDEX IF EXISTS photos_by_path;
DROP INDEX IF EXISTS photos_by_time;
DROP INDEX IF EXISTS photos_by_origin;
ALTER TABLE photos RENAME TO old_photos;
ALTER TABLE album_members RENAME TO old_members;
ALTER TABLE owners RENAME TO old_owners;
ALTER TABLE album_owners RENAME TO old_album_owners;
`

// intoLibraries fills the new tables from those set aside: a photo is of the
// library its path is in: its first folder's, but for a path that is whole
// in the layout before profiles, which has none. What a name looks like says
// nothing: a profile may be "1985". An album is an account's in each
// library that holds a photo of both.
const intoLibraries = `
INSERT INTO library_photos (id, hash, profile, path, name, bytes, taken, source, kind, origin, out, turn, archived, trashed, favorite)
  SELECT id, hash, CASE
      WHEN depth = 0 OR top IN ('undated', 'messaging') AND depth = 1 OR top GLOB '[0-9][0-9][0-9][0-9]' AND depth = 1
        OR depth = 2 AND (path GLOB '[0-9][0-9][0-9][0-9]/[0-9][0-9]/*' OR path GLOB '[0-9][0-9][0-9][0-9]/unknown-month/*' OR path GLOB 'messaging/undated/*')
        OR depth = 3 AND (path GLOB 'messaging/[0-9][0-9][0-9][0-9]/[0-9][0-9]/*' OR path GLOB 'messaging/[0-9][0-9][0-9][0-9]/unknown-month/*')
      THEN '' ELSE top END,
    path, name, bytes, taken, source, kind, origin, out, turn, archived, trashed, favorite
  FROM (SELECT rowid AS id, substr(path, 1, max(instr(path, '/') - 1, 0)) AS top, length(path) - length(replace(path, '/', '')) AS depth, * FROM old_photos);
INSERT INTO owners SELECT o.account, p.id FROM old_owners o JOIN library_photos p ON p.hash = o.hash;
INSERT INTO album_members SELECT m.album, p.id FROM old_members m JOIN library_photos p ON p.hash = m.hash;
INSERT OR IGNORE INTO album_owners
  SELECT o.account, o.album, p.profile FROM old_album_owners o
  JOIN old_members m ON m.album = o.album
  JOIN old_owners w ON w.account = o.account AND w.hash = m.hash
  JOIN library_photos p ON p.hash = m.hash;
DROP TABLE old_album_owners;
DROP TABLE old_owners;
DROP TABLE old_members;
DROP TABLE old_photos;
`

// upgrading holds one opening of this process at a time that has an older
// catalog before it: the window opens it for several requests at once.
var upgrading sync.Mutex

// upgrade makes the catalog at path, or brings an older one up to date, once:
// a copy of one from before libraries is kept beside it first. An opening that has
// nothing to do reads one number.
//
// It is done under the write lock, taken at the start, and the version read
// again under it: a command in a terminal may open the catalog too, and one of
// them does it, the others find it done. On one connection: a second one, on
// macOS, waits for the first even to read (catalogDSN), and it for the second.
func upgrade(db *sql.DB, path string) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version >= marksSince {
		return err
	}
	upgrading.Lock()
	defer upgrading.Unlock()
	db.SetMaxOpenConns(1)
	defer db.SetMaxOpenConns(0)
	for try := 1; ; try++ {
		again, err := upgradeOnce(db, path, try >= upgradeTries)
		if err != nil && version >= librariesSince && unwritable(err) {
			return nil // where nothing can be written, it is shown without marks
		}
		if err != nil || !again {
			return err
		}
	}
}

// upgradeTries is how many copies are made before the last is taken as it is:
// an older program importing meanwhile writes between every copy and the
// lock, and the window would wait for its import to end. The copy is only to
// go back; the upgrade itself is one transaction and misses nothing.
const upgradeTries = 3

// upgradeOnce is upgrade, or again when the catalog changed between its copy
// and the lock: a copy cannot be made inside a transaction. With anyway, the
// copy is taken even so.
func upgradeOnce(db *sql.DB, path string, anyway bool) (again bool, err error) {
	// What other connections committed moves it; what this one does not.
	const changed = `PRAGMA data_version`
	var before, now, version int
	if err := db.QueryRow(changed).Scan(&before); err != nil {
		return false, err
	}
	part, err := copyBefore(db, path)
	if errors.Is(err, errTakenAway) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if part != "" {
		defer os.Remove(part) // left only when it was not needed
	}
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version >= marksSince {
		return false, err
	}
	if err := tx.QueryRow(changed).Scan(&now); err != nil || now != before && !anyway {
		return err == nil, err
	}
	kept, err := keepBefore(path, part)
	if errors.Is(err, errTakenAway) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	done := false
	// Not brought up to date, the catalog is as it was: no copy needed, and
	// one more at every try.
	defer func() {
		if kept != "" && !done {
			os.Remove(kept)
		}
	}()
	older, err := hasPhotos(tx)
	if err != nil {
		return false, err
	}
	steps, since := schema+marksSchema, linksSince
	switch {
	case older:
		if err := complete(tx, version); err != nil {
			return false, err
		}
		steps, since = setAside+schema+intoLibraries+marksSchema, marksSince
	case version >= librariesSince:
		if steps = marksSchema; version < linksBeforeMarks {
			since = marksSince
		}
		// A program from before them, finding links, has the version go back.
		var there bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE name = 'catalog_id')`).Scan(&there); err != nil {
			return false, err
		}
		if there {
			steps = ""
		}
	}
	if _, err := tx.Exec(steps); err != nil {
		return false, err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, since)); err != nil {
		return false, err
	}
	err = tx.Commit()
	done = err == nil
	return false, err
}

// hasPhotos is whether the catalog is one from before libraries.
func hasPhotos(db interface {
	QueryRow(query string, args ...any) *sql.Row
}) (older bool, err error) {
	err = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'photos')`).Scan(&older)
	return older, err
}

// complete gives an older catalog what the last of them had: its tables and
// columns, its sources' service, its dates' clock.
func complete(tx *sql.Tx, version int) error {
	if _, err := tx.Exec(olderSchema); err != nil {
		return err
	}
	for _, column := range []string{"out", "turn", string(Archived), string(Trashed), string(Favorite)} {
		var there bool
		err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('photos') WHERE name = ?)`, column).Scan(&there)
		if err == nil && !there {
			_, err = tx.Exec(`ALTER TABLE photos ADD COLUMN ` + column + ` INTEGER NOT NULL DEFAULT 0`)
		}
		if err != nil {
			return err
		}
	}
	// Before sources named their service they were bare Google addresses. One
	// named twice is the same source.
	for _, table := range []string{"owners", "album_owners"} {
		if _, err := tx.Exec(`UPDATE OR REPLACE ` + table + ` SET account = 'google/' || account WHERE instr(account, '/') = 0`); err != nil {
			return err
		}
	}
	if version >= clocksSince {
		return nil
	}
	// One statement, run inside SQLite: a library of a hundred thousand
	// photos turned row by row from here held the lock for seconds. The
	// zone's offset changes a few times a year; each span between two changes
	// is one branch of the CASE.
	var first, last sql.NullInt64
	err := tx.QueryRow(`SELECT MIN(taken), MAX(taken) FROM photos WHERE source = ?`, string(FromSidecar)).Scan(&first, &last)
	if err == nil && first.Valid {
		_, err = tx.Exec(`UPDATE photos SET taken = taken + `+offsets(first.Int64, last.Int64, time.Local)+` WHERE source = ? AND taken IS NOT NULL`, string(FromSidecar))
	}
	return err
}

// errTakenAway is a copy another program, upgrading meanwhile, took for one
// left by a run killed: it is made again, if it is still to be.
var errTakenAway = errors.New("the copy was taken away")

// copyBefore copies an older catalog, when it holds photos, into a file of its
// own beside it: "" when none is needed.
func copyBefore(db *sql.DB, path string) (string, error) {
	some, err := hasPhotos(db)
	if err == nil && some {
		err = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM photos)`).Scan(&some)
	}
	if err != nil || !some {
		return "", err
	}
	// Two programs may be at it: each its own.
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+beforeLibraries+".part-*")
	if err != nil {
		return "", err
	}
	f.Close()
	// VACUUM INTO writes into an empty file, and reads one snapshot of the
	// catalog: a copy as it was at one moment.
	if _, err := db.Exec(`VACUUM INTO ?`, f.Name()); err != nil {
		if !exists(f.Name()) {
			return "", errTakenAway
		}
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// keepBefore gives the copy its name, under the write lock, and takes away
// what runs killed left of theirs. An earlier copy stays: a catalog made again
// by an older program is not what that one holds.
func keepBefore(path, part string) (string, error) {
	kept := ""
	if part != "" {
		kept = path + beforeLibraries
		for n := 2; exists(kept); n++ {
			kept = fmt.Sprintf("%s%s-%d", path, beforeLibraries, n)
		}
		if err := os.Rename(part, kept); errors.Is(err, fs.ErrNotExist) {
			return "", errTakenAway
		} else if err != nil {
			return "", err
		}
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(path)+beforeLibraries+".part") {
			os.Remove(filepath.Join(filepath.Dir(path), e.Name()))
		}
	}
	return kept, err
}

// share puts the photo k in the library of profile too, at path, with its
// date and no state: one that library has already stays as it is, but for
// what follows. Of k's sources the library takes its own (mine), which k keeps
// only when it has no other; a photo new to it has the cards' and folders' as
// k does too: nobody can tell whose they were. Of its albums, those named.
// With no path, only a photo it has already. in is whether it has it now.
func share(tx *sql.Tx, k Key, profile, path string, albums []string, mine func(source string) bool) (in bool, err error) {
	var fresh int64
	if path != "" {
		res, err := tx.Exec(`INSERT OR IGNORE INTO library_photos (hash, profile, path, name, bytes, taken, source, kind, origin)
			SELECT hash, ?, ?, ?, bytes, taken, source, kind, origin FROM library_photos WHERE hash = ? AND profile = ?`,
			profile, path, filepath.Base(path), k.Hash, k.Profile)
		if err != nil {
			return false, err
		}
		if fresh, err = res.RowsAffected(); err != nil {
			return false, err
		}
	}
	const idOf = `SELECT id FROM library_photos WHERE hash = ? AND profile = ?`
	var id, from int64
	err = tx.QueryRow(idOf, k.Hash, profile).Scan(&id)
	if err == nil {
		err = tx.QueryRow(idOf, k.Hash, k.Profile).Scan(&from)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // taken out meanwhile
	}
	if err != nil {
		return false, err
	}
	sources, err := texts(tx.Query(`SELECT account FROM owners WHERE photo = ?`, from))
	if err != nil {
		return false, err
	}
	var own []string
	others := 0
	for _, s := range sources {
		switch {
		case strings.HasPrefix(s, LocalSource):
			others++
			if fresh == 0 {
				continue
			}
			if _, err := tx.Exec(`INSERT INTO owners (account, photo) VALUES (?, ?)`, s, id); err != nil {
				return false, err
			}
		case mine(s):
			own = append(own, s)
		default:
			others++
		}
	}
	for _, s := range own {
		how := `INSERT OR IGNORE INTO owners (photo, account) SELECT ?, account FROM owners WHERE account = ? AND photo = ?`
		if others > 0 {
			how = `UPDATE OR REPLACE owners SET photo = ? WHERE account = ? AND photo = ?`
		}
		if _, err := tx.Exec(how, id, s, from); err != nil {
			return false, err
		}
	}
	for _, album := range albums {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO album_members (album, photo) VALUES (?, ?)`, album, id); err != nil {
			return false, err
		}
	}
	return true, ownAlbums(tx, id, k.Profile)
}

// ownAlbums says in the library of the photo id, which came from that of
// profile, which of its accounts its albums are of.
func ownAlbums(tx *sql.Tx, id int64, profile string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO album_owners
		SELECT o.account, o.album, p.profile FROM library_photos p
		JOIN album_members m ON m.photo = p.id
		JOIN owners w ON w.photo = p.id
		JOIN album_owners o ON o.account = w.account AND o.album = m.album AND o.profile = ?
		WHERE p.id = ?`, profile, id)
	return err
}

// link is a name a photo has in a second profile's folder.
type link struct {
	of      Key
	profile string
	path    string   // "": the profile has its record already
	albums  []string // the profile's own
	// left are those the photo is in only because the profile's are.
	left []string
}

// FindLinks puts in the catalog, once, the photos a library at root has in a
// second profile's folder as older programs left them: a name on the disk
// and no record of its own. They come in with the date of the photo they are
// a name of and no state. profiles are those there are besides the
// catalog's: no other folder of the library is one. profileOf is whose a
// source is; turn takes the library's turn. The disk is looked at with the
// catalog closed: on a share that takes minutes.
func FindLinks(root, catalog string, profiles []string, profileOf func(source string) string, turn func() (func(), error)) error {
	var photos []Photo
	albums := map[Key][]string{}
	done := false
	err := with(catalog, func(c *Catalog) error {
		var version int
		if err := c.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version >= linksSince || version == linksBeforeMarks {
			done = true
			return err
		}
		var err error
		if photos, err = c.photos(``); err != nil {
			return err
		}
		rows, err := c.db.Query(`SELECT p.hash, p.profile, m.album FROM album_members m JOIN library_photos p ON p.id = m.photo`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k Key
			var album string
			if err := rows.Scan(&k.Hash, &k.Profile, &album); err != nil {
				return err
			}
			albums[k] = append(albums[k], album)
		}
		return rows.Err()
	})
	if err != nil || done {
		return err
	}
	for _, p := range photos {
		if p.Profile != "" && !slices.Contains(profiles, p.Profile) {
			profiles = append(profiles, p.Profile)
		}
	}
	profiles = slices.DeleteFunc(profiles, func(p string) bool { return !exists(filepath.Join(root, p)) })
	var links []link
	if len(profiles) > 1 {
		if links, err = linksIn(root, profiles, photos, albums); err != nil {
			return err
		}
	}
	unlock, err := turn()
	if err != nil {
		return err
	}
	defer unlock()
	// One taken away or filed elsewhere meanwhile is not there any more: no
	// photo is made of it, but one the profile has keeps what is its own.
	for i, l := range links {
		if !exists(filepath.Join(root, filepath.FromSlash(l.path))) {
			links[i].path = ""
		}
	}
	return with(catalog, func(c *Catalog) error {
		return c.inTx(func(tx *sql.Tx) error { return addLinks(tx, links, profileOf) })
	})
}

// addLinks records the links found, and that they were looked for.
func addLinks(tx *sql.Tx, links []link, profileOf func(source string) string) error {
	for _, l := range links {
		in, err := share(tx, l.of, l.profile, l.path, l.albums, func(s string) bool { return profileOf(s) == l.profile })
		if err != nil {
			return err
		}
		if !in {
			continue
		}
		for _, album := range l.left {
			if _, err := tx.Exec(`DELETE FROM album_members WHERE album = ? AND photo = (SELECT id FROM library_photos WHERE hash = ? AND profile = ?)`, album, l.of.Hash, l.of.Profile); err != nil {
				return err
			}
		}
	}
	// The albums whose account's photos all went to their own library.
	if _, err := tx.Exec(`DELETE FROM album_owners AS o WHERE NOT EXISTS (
		SELECT 1 FROM album_members m JOIN library_photos p ON p.id = m.photo JOIN owners w ON w.photo = p.id
		WHERE m.album = o.album AND p.profile = o.profile AND w.account = o.account)`); err != nil {
		return err
	}
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	since := linksSince
	if version < marksSince {
		since = linksBeforeMarks
	}
	_, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, since))
	return err
}

// linksIn are the files under the profiles' folders that the catalog does
// not know by their path and that are one of photos: the same file where the
// disk has hard links, else the same content, read only when the size is one
// of theirs. An album's entry is no photo of its own.
func linksIn(root string, profiles []string, photos []Photo, albums map[Key][]string) ([]link, error) {
	known := make(map[string]bool, len(photos))
	have := make(map[Key]bool, len(photos))
	bySize := map[int64][]Photo{}
	for _, p := range photos {
		known[p.Path], have[p.Key()] = true, true
		bySize[p.Bytes] = append(bySize[p.Bytes], p)
	}
	var links []link
	// Those a profile has already, received since the catalog was brought up
	// to date: what is its own of the other's record is still there.
	byHash := map[string][]Photo{}
	in := entries{}
	for _, p := range photos {
		byHash[p.Hash] = append(byHash[p.Hash], p)
	}
	for _, copies := range byHash {
		for _, of := range copies {
			for _, p := range copies {
				if p.Profile != of.Profile && p.Profile != "" {
					l := linkOf(root, of, p.Profile, p.Path, albums[of.Key()], in)
					l.path = ""
					links = append(links, l)
				}
			}
		}
	}
	for _, profile := range profiles {
		home := filepath.Join(root, profile)
		err := filepath.WalkDir(home, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path == filepath.Join(home, "albums") || path == filepath.Join(home, Unassigned) {
					return filepath.SkipDir
				}
				return nil
			}
			rel := relativeTo(root, path)
			if known[rel] || !d.Type().IsRegular() || !IsMedia(path) {
				return nil
			}
			info, err := d.Info()
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			of, ok := whose(path, root, bySize[info.Size()])
			if !ok || have[Key{of.Hash, profile}] {
				return nil
			}
			have[Key{of.Hash, profile}] = true
			links = append(links, linkOf(root, of, profile, rel, albums[of.Key()], in))
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return links, nil
}

// entries are the files of albums' folders, by folder and size, each folder
// read once.
type entries map[string]map[int64][]string

// of are the entries of the album's folder dir that are the photo at file,
// under whatever name: the same file or the same content.
func (e entries) of(dir, file string) []string {
	info, err := os.Stat(file)
	if err != nil {
		return nil
	}
	if e[dir] == nil {
		e[dir] = map[int64][]string{}
		list, _ := os.ReadDir(dir)
		for _, d := range list {
			if i, err := d.Info(); err == nil && i.Mode().IsRegular() {
				e[dir][i.Size()] = append(e[dir][i.Size()], filepath.Join(dir, d.Name()))
			}
		}
	}
	var of []string
	for _, entry := range e[dir][info.Size()] {
		if same(file, entry) {
			of = append(of, entry)
		}
	}
	return of
}

// linkOf is the name path of the photo of in profile's folder, with the albums
// of it that have their entry there: the same photo, by the file or by its
// content, not another of that name.
func linkOf(root string, of Photo, profile, path string, albums []string, in entries) link {
	l := link{of: of.Key(), profile: profile, path: path}
	file := filepath.Join(root, filepath.FromSlash(path))
	for _, album := range albums {
		if len(in.of(filepath.Join(root, profile, "albums", album), file)) > 0 {
			l.albums = append(l.albums, album)
			if !exists(filepath.Join(root, of.Profile, "albums", album)) {
				l.left = append(l.left, album)
			}
		}
	}
	return l
}

// whose is the photo, of those of its size, that the file at path is. A file
// that cannot be read is none: it is not a reason to look at no other.
func whose(path, root string, photos []Photo) (Photo, bool) {
	for _, p := range photos {
		if linked(path, filepath.Join(root, filepath.FromSlash(p.Path))) {
			return p, true
		}
	}
	if len(photos) == 0 {
		return Photo{}, false
	}
	hash, err := hashOf(path)
	if err != nil {
		return Photo{}, false
	}
	for _, p := range photos {
		if p.Hash == hash {
			return p, true
		}
	}
	return Photo{}, false
}
