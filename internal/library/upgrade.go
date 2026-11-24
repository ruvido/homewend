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
	// The names older programs gave a photo in a second profile's folder, and
	// not in the catalog, were looked for (FindLinks).
	linksSince = 3
)

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
// library its path is in (profileOf). An album is an account's in each
// library that holds a photo of both.
const intoLibraries = `
INSERT INTO library_photos (id, hash, profile, path, name, bytes, taken, source, kind, origin, out, turn, archived, trashed, favorite)
  SELECT id, hash, CASE WHEN top IN ('', 'undated', 'messaging') OR top NOT GLOB '*[^0-9]*' THEN '' ELSE top END,
    path, name, bytes, taken, source, kind, origin, out, turn, archived, trashed, favorite
  FROM (SELECT rowid AS id, substr(path, 1, max(instr(path, '/') - 1, 0)) AS top, * FROM old_photos);
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

// upgrade makes the catalog at path, or brings an older one up to date, once:
// a copy of it as it was is kept beside it first. An opening that has
// nothing to do reads one number.
//
// The window opens the catalog for several requests at once, and a command
// in a terminal may too: it is all done under the write lock, taken at the
// start, and the version read again under it, so one of them does it and the
// others find it done.
func upgrade(db *sql.DB, path string) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version >= librariesSince {
		return err
	}
	backup, err := keepBefore(db, path)
	if err != nil {
		return err
	}
	if backup != "" {
		defer os.Remove(backup) // left only when it was not needed
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version >= librariesSince {
		return err
	}
	// The copy taken before the lock is of the catalog as it still is: no
	// one brought it up to date since, or the version would say so. One kept
	// by an earlier run that stopped halfway is as good, and stays.
	if backup != "" && !exists(path+beforeLibraries) {
		if err := os.Rename(backup, path+beforeLibraries); err != nil {
			return err
		}
	}
	var older bool
	if err := tx.QueryRow(hasPhotos).Scan(&older); err != nil {
		return err
	}
	steps, now := schema, linksSince
	if older {
		if err := complete(tx, version); err != nil {
			return err
		}
		steps, now = setAside+schema+intoLibraries, librariesSince
	}
	if _, err := tx.Exec(steps); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, now)); err != nil {
		return err
	}
	return tx.Commit()
}

const hasPhotos = `SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'photos')`

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

// keepBefore copies an older catalog, when it holds photos and no copy is
// kept yet, into a file of its own beside it: "" when none is needed. Each
// opening writes its own, so two never write the same file, and a copy cut
// short (a crash, a full disk) is never the one kept.
func keepBefore(db *sql.DB, path string) (string, error) {
	if exists(path + beforeLibraries) {
		return "", nil
	}
	var any bool
	if err := db.QueryRow(hasPhotos).Scan(&any); err != nil || !any {
		return "", err
	}
	if err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM photos)`).Scan(&any); err != nil || !any {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+beforeLibraries+"-*")
	if err != nil {
		return "", err
	}
	f.Close()
	// VACUUM INTO writes into an empty file, and reads one snapshot of the
	// catalog: a copy as it was at one moment.
	if _, err := db.Exec(`VACUUM INTO ?`, f.Name()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// share puts the photo k in the library of profile too, at path, with its
// date and no state: one that library has already is left as it is. Of its
// sources the library takes its own (mine), which k keeps only when it has
// no other, and has the cards' and folders' as k does: nobody can tell whose
// they were. Of its albums, those named.
func share(tx *sql.Tx, k Key, profile, path string, albums []string, mine func(source string) bool) error {
	res, err := tx.Exec(`INSERT OR IGNORE INTO library_photos (hash, profile, path, name, bytes, taken, source, kind, origin)
		SELECT hash, ?, ?, ?, bytes, taken, source, kind, origin FROM library_photos WHERE hash = ? AND profile = ?`,
		profile, path, filepath.Base(path), k.Hash, k.Profile)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	var from int64
	if err := tx.QueryRow(`SELECT id FROM library_photos WHERE hash = ? AND profile = ?`, k.Hash, k.Profile).Scan(&from); err != nil {
		return err
	}
	sources, err := texts(tx.Query(`SELECT account FROM owners WHERE photo = ?`, from))
	if err != nil {
		return err
	}
	var own []string
	others := 0
	for _, s := range sources {
		switch {
		case strings.HasPrefix(s, LocalSource):
			others++
			if _, err := tx.Exec(`INSERT INTO owners (account, photo) VALUES (?, ?)`, s, id); err != nil {
				return err
			}
		case mine(s):
			own = append(own, s)
		default:
			others++
		}
	}
	for _, s := range own {
		how := `INSERT INTO owners (photo, account) SELECT ?, account FROM owners WHERE account = ? AND photo = ?`
		if others > 0 {
			how = `UPDATE owners SET photo = ? WHERE account = ? AND photo = ?`
		}
		if _, err := tx.Exec(how, id, s, from); err != nil {
			return err
		}
	}
	for _, album := range albums {
		if _, err := tx.Exec(`INSERT INTO album_members (album, photo) VALUES (?, ?)`, album, id); err != nil {
			return err
		}
	}
	return ownAlbums(tx, id, k.Profile)
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
	path    string
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
		if err := c.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version >= linksSince {
			done = true
			return err
		}
		rows, err := c.db.Query(`SELECT ` + photoColumns + ` FROM library_photos`)
		if err != nil {
			return err
		}
		if photos, err = scanPhotos(rows); err != nil {
			return err
		}
		if rows, err = c.db.Query(`SELECT p.hash, p.profile, m.album FROM album_members m JOIN library_photos p ON p.id = m.photo`); err != nil {
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
	// One taken away or filed elsewhere meanwhile is not there any more.
	here := links[:0]
	for _, l := range links {
		if exists(filepath.Join(root, filepath.FromSlash(l.path))) {
			here = append(here, l)
		}
	}
	return with(catalog, func(c *Catalog) error {
		return c.inTx(func(tx *sql.Tx) error { return addLinks(tx, here, profileOf) })
	})
}

// addLinks records the links found, and that they were looked for.
func addLinks(tx *sql.Tx, links []link, profileOf func(source string) string) error {
	for _, l := range links {
		if err := share(tx, l.of, l.profile, l.path, l.albums, func(s string) bool { return profileOf(s) == l.profile }); err != nil {
			return err
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
	_, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, linksSince))
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
			l := link{of: of.Key(), profile: profile, path: rel}
			for _, album := range albums[of.Key()] {
				if exists(filepath.Join(home, "albums", album, filepath.Base(path))) {
					l.albums = append(l.albums, album)
					if !exists(filepath.Join(root, of.Profile, "albums", album)) {
						l.left = append(l.left, album)
					}
				}
			}
			links = append(links, l)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return links, nil
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
