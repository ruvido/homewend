// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// DateKind is the difference between "when this was taken" and "when this
// reached you", and the reason it exists is that for a quarter of a real
// library nobody knows the first.
//
// A photo that arrived through WhatsApp has no EXIF — the app strips it when it
// recompresses — so the date Google records is when the file appeared on the
// phone. Every other tool calls that a capture date. It is not, and a library
// that says "taken 14 July 2019" about a picture someone forwarded is telling
// the user something it does not know.
type DateKind string

const (
	DateTaken    DateKind = "taken"    // a camera or a sidecar said so
	DateReceived DateKind = "received" // it arrived through a chat; nobody knows when it was shot
	DateGuessed  DateKind = "guessed"  // the year of Google's own folder, or the date in the file's name
	DateUnknown  DateKind = "unknown"
)

// Kind says what the date on this capture really is.
func (c Capture) Kind() DateKind {
	switch c.Source {
	case FromSidecar, FromVideo:
		// A video sent through a chat is made again on the way: its header
		// says when it was sent, not when it was filmed.
		if c.Origin.FromMessaging() || c.Origin == OriginScreenshot {
			return DateReceived
		}
		return DateTaken
	case FromEXIF, FromGiven:
		return DateTaken
	case FromFolder, FromName, FromNameDay:
		return DateGuessed
	default:
		return DateUnknown
	}
}

// Photo is one picture as the library knows it.
type Photo struct {
	Hash   string
	Path   string // relative to the library root, so the library can be moved
	Name   string
	Bytes  int64
	Taken  time.Time
	Source DateSource
	Kind   DateKind
	Origin Origin
	Albums []string
	// Turn is how many quarter turns anticlockwise the person gave it: kept
	// here, the file is never changed.
	Turn     int
	Archived bool
}

// photoColumns are what scanPhotos reads, in its order.
const photoColumns = `hash, path, name, bytes, taken, source, kind, origin, turn, archived`

// Catalog is the index the interface reads: what there is, where it is, and
// what can be said about it.
//
// SQLite rather than a file of JSON because the grid has to scroll through tens
// of thousands of thumbnails and filter them without loading the lot, and
// because the driver is already here for reading Chrome's cookies. No cgo, so
// the app still cross-compiles to the three desktops from one machine.
type Catalog struct{ db *sql.DB }

const schema = `
CREATE TABLE IF NOT EXISTS photos (
  hash    TEXT PRIMARY KEY,
  path    TEXT NOT NULL,
  name    TEXT NOT NULL,
  bytes   INTEGER NOT NULL,
  taken   INTEGER,          -- unix seconds, NULL when nothing is known
  source  TEXT NOT NULL,
  kind    TEXT NOT NULL,
  origin  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS album_members (
  album TEXT NOT NULL,
  hash  TEXT NOT NULL REFERENCES photos(hash),
  PRIMARY KEY (album, hash)
);
-- Where a photo came from: its sources, "<service>/<address>" for an
-- account ("google/ron@example.com"), "local/<folder>" for an import. A photo
-- that came from two is one photo with two; a photo recorded before sources
-- were kept has none, and is shown with every account.
CREATE TABLE IF NOT EXISTS owners (
  account TEXT NOT NULL,
  hash    TEXT NOT NULL,
  PRIMARY KEY (account, hash)
);
CREATE TABLE IF NOT EXISTS album_owners (
  account TEXT NOT NULL,
  album   TEXT NOT NULL,
  PRIMARY KEY (account, album)
);
-- The grid is always "newest first, maybe filtered": one index carries it.
CREATE INDEX IF NOT EXISTS photos_by_page   ON photos(taken DESC, name);
-- Of older catalogs, read by nothing.
DROP INDEX IF EXISTS photos_by_time;
DROP INDEX IF EXISTS photos_by_origin;
CREATE INDEX IF NOT EXISTS photos_by_path   ON photos(path);
`

// away is the SQL that says a photo is not in the library's lists: deleted
// and kept out, or in the Trash.
const away = "(out = 1 OR trashed = 1)"

// Changes is a token that moves each time the catalog at path is written,
// by whoever: a phone's backup, a card, a download, the window, a command in
// a terminal. It is read from the files alone, holding none open, so a
// library's disk can still be ejected: a commit rewrites the catalog, or, in
// one an older version still holds open in WAL mode, the -wal. An empty -wal
// is left out: SQLite makes one at every opening in that mode.
// A file gone (the disk ejected) is a change, not an error.
func Changes(path string) string {
	token := ""
	for i, name := range []string{path, path + "-wal"} {
		if fi, err := os.Stat(name); err == nil && (i == 0 || fi.Size() > 0) {
			token += fmt.Sprintf("%d.%d;", fi.ModTime().UnixNano(), fi.Size())
		}
	}
	return token
}

// catalogDSN is how the index at path is opened, with options after the
// path's "?".
func catalogDSN(path, options string) string {
	dsn := "file:" + path + "?" + options
	// On macOS SQLite picks its locking by filesystem, and on an SMB share it
	// picks AFP-style locks, which the Go SQLite cannot do: it panics on
	// fsctl. flock locks work there and on local disks alike, and go with the
	// process if it dies. Measured on a NAS share, 2026-09-27.
	if runtime.GOOS == "darwin" {
		dsn += "&vfs=unix-flock"
	}
	return dsn
}

// waiting is how long a connection waits for others to let go of the catalog.
// They take turns by chance, not in order: between two that write without
// pause, one was seen to wait more than 5 seconds on Windows.
const waiting = "_pragma=busy_timeout(30000)"

// openCatalog opens the index at path with options, out of WAL mode if it was
// left in it. Every system keeps the catalog the same way, with a rollback
// journal: WAL needs memory shared between processes, which macOS's flock
// locks do not have and no network share does, and a catalog left in it on
// Linux could not be opened on a Mac.
//
// SQLite is asked which mode it is in, never the file: on Linux closing a
// file drops every lock the process holds on it, an import's among them.
func openCatalog(path, options string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", catalogDSN(path, options))
	if err != nil {
		return nil, err
	}
	// On macOS a catalog in WAL mode cannot even be asked.
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err == nil && mode != "wal" {
		return db, nil
	}
	db.Close()
	_ = leaveWAL(path) // not left, using the catalog says whether it can be used
	return sql.Open("sqlite", catalogDSN(path, options))
}

// leaveWAL takes the catalog at path out of WAL mode, with what its -wal
// holds. The exclusive lock is how SQLite opens one without shared memory
// (sqlite.org/wal.html, "Use of WAL Without Shared-Memory"). One that an
// older version holds open stays as it is until the next opening, and is not
// waited for long: every opening would wait.
func leaveWAL(path string) error {
	db, err := sql.Open("sqlite", catalogDSN(path, "mode=rw&_pragma=busy_timeout(200)&_pragma=locking_mode(EXCLUSIVE)"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode = DELETE`)
	return err
}

// OpenCatalog opens, and creates if needed, the index at path. Its
// transactions take the write lock as they begin: SQLite does not wait for
// another's when one that has read comes to write.
func OpenCatalog(path string) (*Catalog, error) {
	db, err := openCatalog(path, waiting+"&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("opening the catalog: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("preparing the catalog: %w", err)
	}
	// A photo taken out of the library and kept out of the imports: out of
	// every list, still known, so that an import leaves it out as deleted.
	// Added to a catalog from before; a second process finds it there.
	if _, err := db.Exec(`ALTER TABLE photos ADD COLUMN out INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		db.Close()
		return nil, fmt.Errorf("preparing the catalog: %w", err)
	}
	// A photo turned by the person, in quarter turns; one archived: out of
	// the grid and the years, still in its albums, still backed up; one in
	// the Trash: out of every list and every import until restored. Added
	// the same way.
	for _, column := range []string{"turn", string(Archived), string(Trashed)} {
		if _, err := db.Exec(`ALTER TABLE photos ADD COLUMN ` + column + ` INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("preparing the catalog: %w", err)
		}
	}
	// A catalog from before sources named their service kept bare Google
	// addresses. Looked for first: an opening that has nothing to write does
	// not wait for those who write.
	for _, table := range []string{"owners", "album_owners"} {
		var bare bool
		err := db.QueryRow(`SELECT EXISTS (SELECT 1 FROM ` + table + ` WHERE instr(account, '/') = 0)`).Scan(&bare)
		if err == nil && bare {
			_, err = db.Exec(`UPDATE OR IGNORE ` + table + ` SET account = 'google/' || account WHERE instr(account, '/') = 0`)
		}
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("naming the catalog's sources: %w", err)
		}
	}
	if err := clockDates(db, path); err != nil {
		db.Close()
		return nil, fmt.Errorf("turning the catalog's dates into clocks: %w", err)
	}
	return &Catalog{db: db}, nil
}

// clocksSince is the catalog's user_version from which every date in it is
// a clock. Before, Google's dates were kept as instants in UTC while the
// camera's were clocks, and the grid, which reads a clock, showed a photo
// from Takeout one or two hours off, near midnight on the wrong day.
const clocksSince = 1

// clockDates turns the instants an older catalog kept for Google's dates
// into the clocks of this computer's zone, the one the photos were filed
// by, once: a copy of the catalog as it was is kept beside it first.
//
// The window opens the catalog for several requests at once, and a command
// in a terminal may too: the turn is made under the write lock, taken at
// the start (BEGIN IMMEDIATE), and the version read again under it, so one
// of them turns the dates and the others find it done. A deferred
// transaction would read, then fail to write on a snapshot grown old.
func clockDates(db *sql.DB, path string) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version >= clocksSince {
		return err
	}
	ctx := context.Background()
	backup, err := keepBefore(db, path)
	if err != nil {
		return err
	}
	if backup != "" {
		defer os.Remove(backup) // left only when it was not needed
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			conn.ExecContext(ctx, `ROLLBACK`)
		}
	}()
	if err := conn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version >= clocksSince {
		return err
	}
	// The copy taken before the lock is of the catalog as it still is: no
	// one turned the dates since, or the version would say so. One kept by
	// an earlier run that stopped halfway is as good, and stays.
	if backup != "" && !exists(path+".before-clocks") {
		if err := os.Rename(backup, path+".before-clocks"); err != nil {
			return err
		}
	}
	// One statement, run inside SQLite: a library of a hundred thousand
	// photos turned row by row from here held the lock for seconds, longer
	// than the others wait for it. The zone's offset changes a few times a
	// year; each span between two changes is one branch of the CASE.
	var first, last sql.NullInt64
	if err := conn.QueryRowContext(ctx, `SELECT MIN(taken), MAX(taken) FROM photos WHERE source = ? AND taken IS NOT NULL`, string(FromSidecar)).Scan(&first, &last); err != nil {
		return err
	}
	if first.Valid {
		if _, err := conn.ExecContext(ctx, `UPDATE photos SET taken = taken + `+offsets(first.Int64, last.Int64, time.Local)+` WHERE source = ? AND taken IS NOT NULL`, string(FromSidecar)); err != nil {
			return err
		}
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, clocksSince)); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		return err
	}
	done = true
	return nil
}

// offsets is an SQL expression of taken, an instant in Unix seconds between
// first and last: the seconds that turn it into the clock of zone, as
// asClock does. Only numbers made here go in it.
func offsets(first, last int64, zone *time.Location) string {
	var b strings.Builder
	// A date beyond is a mistake: its offset need not be right, and each year
	// on the way there is two more branches.
	last = min(last, time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
	at := time.Unix(first, 0).In(zone)
	for {
		_, offset := at.Zone()
		_, end := at.ZoneBounds()
		// Past the zone's last written change Go ends a span at 365 days from
		// 1 January too: on the last day of a leap year, where it began. The
		// last change is 2037 in Linux's and macOS's data, 1996 for Rome in
		// Go's own, which Windows uses. The next day is past it.
		if !end.IsZero() && !end.After(at) {
			end = at.AddDate(0, 0, 1)
		}
		if end.IsZero() || end.Unix() > last {
			// One offset over them all, as in UTC: no CASE to choose.
			if b.Len() == 0 {
				return strconv.Itoa(offset)
			}
			fmt.Fprintf(&b, " ELSE %d END", offset)
			return b.String()
		}
		if b.Len() == 0 {
			b.WriteString("CASE")
		}
		fmt.Fprintf(&b, " WHEN taken < %d THEN %d", end.Unix(), offset)
		at = end
	}
}

// keepBefore copies the catalog, when it has dates to turn and no copy is
// kept yet, into a file of its own beside it: "" when none is needed. Each
// opening writes its own, so two never write the same file, and a copy cut
// short (a crash, a full disk) is never the one kept.
func keepBefore(db *sql.DB, path string) (string, error) {
	if exists(path + ".before-clocks") {
		return "", nil
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM photos WHERE source = ? AND taken IS NOT NULL`, string(FromSidecar)).Scan(&n); err != nil || n == 0 {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".before-clocks-*")
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

// OpenCatalogReadOnly opens the index at path to look at it: no schema, no
// migration, nothing written. A look before an import changes nothing in the
// library, and a library on a read-only share can still be looked at.
//
// It waits like any reader for a write under way. A write cut short (a crash)
// is first undone, as SQLite asks of whoever opens next, and a catalog an
// older version left in WAL mode leaves it: hence mode=rw, which reads alone
// where nothing can be written. There a catalog in WAL mode is read as it
// lies, if its -wal holds nothing; what cannot be read whole is refused.
func OpenCatalogReadOnly(path string) (*Catalog, error) {
	db, err := openCatalog(path, "mode=rw&"+waiting+"&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("opening the catalog: %w", err)
	}
	var version int
	err = db.QueryRow(`PRAGMA schema_version`).Scan(&version) // the first look at the file
	var e *sqlite.Error
	if fi, serr := os.Stat(path + "-wal"); errors.As(err, &e) && (e.Code()&0xff == sqlite3.SQLITE_CANTOPEN || e.Code() == sqlite3.SQLITE_READONLY_DIRECTORY) && (serr != nil || fi.Size() == 0) {
		db.Close()
		if db, err = sql.Open("sqlite", catalogDSN(path, "mode=ro&immutable=1")); err == nil {
			err = db.Ping()
		}
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("opening the catalog: %w", err)
	}
	return &Catalog{db: db}, nil
}

func (c *Catalog) Close() error { return c.db.Close() }

// Put records a photo, replacing whatever was known about it before. Safe to
// run again over a library that was already indexed: a second pass corrects,
// it does not duplicate. The photo, its albums and its sources are one write:
// each write to the disk is waited for, and none is there without the others.
func (c *Catalog) Put(photo Photo, sources ...string) error {
	var taken any
	if !photo.Taken.IsZero() {
		taken = photo.Taken.Unix()
	}
	tx, err := c.db.Begin()
	if err != nil {
		return fmt.Errorf("recording %s: %w", photo.Name, err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(
		`INSERT INTO photos (hash, path, name, bytes, taken, source, kind, origin)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(hash) DO UPDATE SET
		   path=excluded.path, name=excluded.name, bytes=excluded.bytes,
		   taken=excluded.taken, source=excluded.source, kind=excluded.kind,
		   origin=excluded.origin`,
		photo.Hash, photo.Path, photo.Name, photo.Bytes, taken,
		string(photo.Source), string(photo.Kind), string(photo.Origin))
	if err != nil {
		return fmt.Errorf("recording %s: %w", photo.Name, err)
	}
	for _, album := range photo.Albums {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO album_members (album, hash) VALUES (?, ?)`,
			album, photo.Hash); err != nil {
			return fmt.Errorf("adding %s to %s: %w", photo.Name, album, err)
		}
	}
	for _, source := range sources {
		if err := addOwner(tx, photo.Hash, source); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("recording %s: %w", photo.Name, err)
	}
	return nil
}

// PathOf returns where the photo with this hash lives, relative to the library
// root, and whether the catalog knows it at all.
func (c *Catalog) PathOf(hash string) (string, bool, error) {
	var path string
	err := c.db.QueryRow(`SELECT path FROM photos WHERE hash = ?`, hash).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading the catalog: %w", err)
	}
	return path, true, nil
}

// PathTaken reports whether some photo is recorded at this relative path.
func (c *Catalog) PathTaken(path string) (bool, error) {
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM photos WHERE path = ?`, path).Scan(&n); err != nil {
		return false, fmt.Errorf("reading the catalog: %w", err)
	}
	return n > 0, nil
}

// addAlbum records that a photo belongs to an album, and the album to the
// account it came from. Separate from Put because a photo is placed once and
// may join several albums afterwards.
func (c *Catalog) addAlbum(album, hash, account string) error {
	_, err := c.db.Exec(
		`INSERT OR IGNORE INTO album_members (album, hash) VALUES (?, ?)`, album, hash)
	if err == nil && account != "" {
		_, err = c.db.Exec(
			`INSERT OR IGNORE INTO album_owners (account, album) VALUES (?, ?)`, account, album)
	}
	if err != nil {
		return fmt.Errorf("adding a photo to %s: %w", album, err)
	}
	return nil
}

// addOwner records that a photo came from an account. A photo already in the
// library gets one more owner when another account's export holds it too.
func (c *Catalog) addOwner(hash, account string) error { return addOwner(c.db, hash, account) }

func addOwner(db interface {
	Exec(query string, args ...any) (sql.Result, error)
}, hash, account string) error {
	if account == "" {
		return nil
	}
	_, err := db.Exec(`INSERT OR IGNORE INTO owners (account, hash) VALUES (?, ?)`, account, hash)
	return err
}

// LocalSource begins the source of a card or a folder imported,
// "local/<its name>": no account, never hidden with one.
const LocalSource = "local/"

// ownedBy is the SQL that keeps the photos of these accounts, those of
// nobody's (recorded before owners were kept), and those a card or a folder
// brought in ("local/…"): hiding an account hides what came from it, not a card's
// photos it happened to hold too. Nil accounts keeps them all.
func ownedBy(accounts []string) (string, []any) {
	if accounts == nil {
		return "", nil
	}
	clause := "(hash NOT IN (SELECT hash FROM owners) OR hash IN (SELECT hash FROM owners WHERE account LIKE '" + LocalSource + "%')"
	var args []any
	if len(accounts) > 0 {
		clause += " OR hash IN (SELECT hash FROM owners WHERE account IN (?" + strings.Repeat(",?", len(accounts)-1) + "))"
		for _, a := range accounts {
			args = append(args, a)
		}
	}
	return clause + ")", args
}

// Filter is what the interface asks for: a page of the grid, narrowed.
type Filter struct {
	Album  string
	Year   string
	NoDate bool // only the photos whose date nobody knows
	// Archived keeps only the archived photos; Grid leaves them out, as the
	// main grid and its years do; Trashed keeps only those in the Trash,
	// which are otherwise in no list.
	Archived, Grid, Trashed bool
	// Profile keeps the photos filed in this profile's folder.
	Profile string
	// Hashes keeps only these photos; nil is all, empty is none.
	// shortcut: one SQL variable each, SQLite takes 32,766; fine for the
	// photos not on the disk, a temporary table if a list ever grows past.
	Hashes []string
	// Accounts keeps the photos of these accounts, by address; nil is all.
	Accounts []string
	Limit    int // the first of them; 0 is all
}

// Page returns photos newest first, narrowed by the filter.
func (c *Catalog) Page(f Filter) ([]Photo, error) {
	query, args := f.where(`SELECT ` + photoColumns + ` FROM photos`)
	// A few are found by the index, in its order; all of them are read as
	// they lie and sorted here, three times as fast (100,000, 23/11).
	if f.Limit > 0 {
		query += " ORDER BY taken DESC, name LIMIT ?"
		args = append(args, f.Limit)
	}
	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the catalog: %w", err)
	}
	photos, err := scanPhotos(rows)
	if f.Limit == 0 {
		slices.SortStableFunc(photos, func(a, b Photo) int {
			if a.Taken.IsZero() != b.Taken.IsZero() {
				if a.Taken.IsZero() {
					return 1
				}
				return -1
			}
			return cmp.Or(b.Taken.Compare(a.Taken), cmp.Compare(a.Name, b.Name))
		})
	}
	return photos, err
}

// BytesByKind is how many bytes the library's files take, by what they are
// (kindOf: "video", "raw", "" for a photo), from the sizes the catalog
// keeps: those in the Trash too, their files still there.
// shortcut: every row is read on each call, milliseconds for 100,000
// photos; a sum kept by kind when measured slow.
func (c *Catalog) BytesByKind() (map[string]int64, error) {
	rows, err := c.db.Query(`SELECT name, bytes FROM photos WHERE out = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]int64{}
	for rows.Next() {
		var name string
		var n int64
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		by[kindOf(name)] += n
	}
	return by, rows.Err()
}

// Count is how many photos the filter keeps, its Limit aside.
func (c *Catalog) Count(f Filter) (int, error) {
	query, args := f.where(`SELECT COUNT(*) FROM photos`)
	var n int
	err := c.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

// where is query narrowed by the filter, and its arguments.
func (f Filter) where(query string) (string, []any) {
	where := []string{"NOT " + away}
	if f.Trashed {
		where = []string{"out = 0", "trashed = 1"}
	}
	var args []any
	if f.Album != "" {
		where = append(where,
			"hash IN (SELECT hash FROM album_members WHERE album = ?)")
		args = append(args, f.Album)
	}
	if f.Year != "" {
		// Compared as text on purpose: an unknown date must not silently match
		// a year, and strftime on NULL is NULL, which no year equals.
		where = append(where, "strftime('%Y', taken, 'unixepoch') = ?")
		args = append(args, f.Year)
	}
	if f.NoDate {
		where = append(where, "taken IS NULL")
	}
	if f.Archived {
		where = append(where, "archived = 1")
	} else if f.Grid {
		where = append(where, "archived = 0")
	}
	if f.Profile != "" {
		// "<profile>/" up to "<profile>0", '0' coming right after '/': every
		// path in its folder, by the bytes (LIKE would ignore the case).
		where = append(where, "path >= ? AND path < ?")
		args = append(args, f.Profile+"/", f.Profile+"0")
	}
	if f.Hashes != nil {
		where = append(where, "hash IN (NULL"+strings.Repeat(",?", len(f.Hashes))+")")
		for _, h := range f.Hashes {
			args = append(args, h)
		}
	}
	if clause, more := ownedBy(f.Accounts); clause != "" {
		where = append(where, clause)
		args = append(args, more...)
	}
	for i, clause := range where {
		if i == 0 {
			query += " WHERE "
		} else {
			query += " AND "
		}
		query += clause
	}
	return query, args
}

// Photo is the record of one photo.
func (c *Catalog) Photo(hash string) (Photo, error) {
	rows, err := c.db.Query(`SELECT `+photoColumns+` FROM photos WHERE hash = ?`, hash)
	if err != nil {
		return Photo{}, fmt.Errorf("reading the catalog: %w", err)
	}
	photos, err := scanPhotos(rows)
	if err != nil || len(photos) == 0 {
		return Photo{}, fmt.Errorf("reading the catalog: no photo %s: %v", hash, err)
	}
	return photos[0], nil
}

// capture is what the record knows of when the photo was taken.
func (p Photo) capture() Capture {
	return Capture{When: p.Taken, Source: p.Source, Origin: p.Origin, clock: true}
}

// scanPhotos reads the rows of a SELECT of the photos' columns, in order.
func scanPhotos(rows *sql.Rows) ([]Photo, error) {
	defer rows.Close()
	var photos []Photo
	for rows.Next() {
		var photo Photo
		var taken sql.NullInt64
		var source, kind, origin string
		if err := rows.Scan(&photo.Hash, &photo.Path, &photo.Name, &photo.Bytes,
			&taken, &source, &kind, &origin, &photo.Turn, &photo.Archived); err != nil {
			return nil, err
		}
		if taken.Valid {
			photo.Taken = time.Unix(taken.Int64, 0).UTC()
		}
		photo.Source, photo.Kind, photo.Origin = DateSource(source), DateKind(kind), Origin(origin)
		photos = append(photos, photo)
	}
	return photos, rows.Err()
}

// AlbumNames lists the albums, with how many photos each holds.
func (c *Catalog) AlbumNames() (map[string]int, error) {
	rows, err := c.db.Query(
		`SELECT album, COUNT(*) FROM album_members WHERE hash NOT IN (SELECT hash FROM photos WHERE ` + away + `) GROUP BY album ORDER BY album`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var album string
		var n int
		if err := rows.Scan(&album, &n); err != nil {
			return nil, err
		}
		counts[album] = n
	}
	return counts, rows.Err()
}

// CountsByYear is how many photos each year holds, and under "" those whose
// date nobody knows.
func (c *Catalog) CountsByYear(accounts []string) (map[string]int, error) {
	query := `SELECT COALESCE(strftime('%Y', taken, 'unixepoch'), ''), COUNT(*) FROM photos WHERE NOT ` + away + ` AND archived = 0`
	clause, args := ownedBy(accounts)
	if clause != "" {
		query += " AND " + clause
	}
	rows, err := c.db.Query(query+" GROUP BY 1", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := map[string]int{}
	for rows.Next() {
		var year string
		var n int
		if err := rows.Scan(&year, &n); err != nil {
			return nil, err
		}
		counts[year] = n
	}
	return counts, rows.Err()
}

// CountsByOwner is how many photos each account brought, by address.
func (c *Catalog) CountsByOwner() (map[string]int, error) {
	rows, err := c.db.Query(`SELECT account, COUNT(*) FROM owners WHERE hash NOT IN (SELECT hash FROM photos WHERE ` + away + `) GROUP BY account`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var a string
		var n int
		if err := rows.Scan(&a, &n); err != nil {
			return nil, err
		}
		counts[a] = n
	}
	return counts, rows.Err()
}

// Latest are the photos recorded last, newest first: the first to arrive in
// a download are the last of these once it is over, the latest while it runs.
func (c *Catalog) Latest(n int) ([]Photo, error) {
	rows, err := c.db.Query(`SELECT hash, name, turn FROM photos WHERE NOT `+away+` ORDER BY rowid DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.Hash, &p.Name, &p.Turn); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// State is what a photo's owner can set on it, by the catalog's column.
type State string

const (
	Archived State = "archived"
	Trashed  State = "trashed"
)

// Mark sets state on the photos of hashes filed in profile's folder, or
// clears it, and says which those were: a photo of another profile, or not
// in the library, is left as it is.
func (c *Catalog) Mark(state State, hashes []string, profile string, on bool) ([]string, error) {
	tx, err := c.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	done := []string{}
	for _, h := range hashes {
		var path string
		err := tx.QueryRow(`SELECT path FROM photos WHERE hash = ? AND out = 0`, h).Scan(&path)
		if errors.Is(err, sql.ErrNoRows) || err == nil && !filedIn(path, profile) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE photos SET `+string(state)+` = ? WHERE hash = ?`, on, h); err != nil {
			return nil, err
		}
		done = append(done, h)
	}
	return done, tx.Commit()
}

// TurnLeft gives the photo of hash a quarter turn more, anticlockwise, and
// says how many it has now; fs.ErrNotExist for a hash the catalog lacks.
func (c *Catalog) TurnLeft(hash string) (int, error) {
	var turn int
	err := c.db.QueryRow(`UPDATE photos SET turn = (turn + 1) % 4 WHERE hash = ? RETURNING turn`, hash).Scan(&turn)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fs.ErrNotExist
	}
	return turn, err
}

// About says which albums a photo is in and which accounts it came from.
func (c *Catalog) About(hash string) (albums, accounts []string, err error) {
	read := func(query string) ([]string, error) {
		rows, err := c.db.Query(query, hash)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []string{}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, rows.Err()
	}
	if albums, err = read(`SELECT album FROM album_members WHERE hash = ? ORDER BY album`); err != nil {
		return nil, nil, err
	}
	accounts, err = read(`SELECT account FROM owners WHERE hash = ? ORDER BY account`)
	return albums, accounts, err
}

// AlbumCount is an album and the account it came from ("" when not known).
type AlbumCount struct {
	Account string
	Name    string
	Count   int
}

// AlbumsByOwner lists the albums, each under every account that brought it.
func (c *Catalog) AlbumsByOwner() ([]AlbumCount, error) {
	rows, err := c.db.Query(`
		SELECT COALESCE(o.account, ''), m.album, COUNT(*)
		FROM album_members m LEFT JOIN album_owners o ON o.album = m.album
		WHERE m.hash NOT IN (SELECT hash FROM photos WHERE ` + away + `)
		GROUP BY 1, 2 ORDER BY 2, 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AlbumCount
	for rows.Next() {
		var a AlbumCount
		if err := rows.Scan(&a.Account, &a.Name, &a.Count); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// relativeTo makes a library path portable, so moving the library to another
// disk does not invalidate the index.
func relativeTo(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

// Gone is a photo of the catalog whose file is not in the library, and its
// year ("" with no date).
type Gone struct {
	Hash string
	Year string
}

// Missing are the photos of the library at root whose file is not there:
// deleted or moved by hand. A file that cannot be told (a share that
// drops) is not missing, nor is any when the library itself went away
// while it was looked at. A photo kept out whose file is back is in again.
//
// The catalog is closed while the files are looked at: on a share that
// takes minutes, and a disk it is open on could not be ejected.
func Missing(catalog, root string) ([]Gone, error) {
	type known struct {
		Gone
		path string
		out  bool
	}
	var all []known
	err := with(catalog, func(c *Catalog) error {
		rows, err := c.db.Query(`SELECT hash, path, COALESCE(strftime('%Y', taken, 'unixepoch'), ''), out FROM photos WHERE trashed = 0`)
		if err != nil {
			return fmt.Errorf("reading the catalog: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var k known
			if err := rows.Scan(&k.Hash, &k.path, &k.Year, &k.out); err != nil {
				return err
			}
			all = append(all, k)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	var gone, back []known
	for _, k := range all {
		switch _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(k.path))); {
		case errors.Is(err, fs.ErrNotExist) && !k.out:
			gone = append(gone, k)
		case err == nil && k.out:
			back = append(back, k)
		}
	}
	if _, err := os.Stat(filepath.Join(root, WorkDir)); err != nil || len(gone)+len(back) == 0 {
		return nil, nil
	}
	var missing []Gone
	err = with(catalog, func(c *Catalog) error {
		for _, k := range gone {
			// Given a date meanwhile, its file is at another path.
			path, in, err := c.PathOf(k.Hash)
			if err != nil {
				return err
			}
			if in && path == k.path {
				missing = append(missing, k.Gone)
			}
		}
		for _, k := range back {
			if _, err := c.db.Exec(`UPDATE photos SET out = 0 WHERE hash = ?`, k.Hash); err != nil {
				return err
			}
		}
		return nil
	})
	return missing, err
}

// with opens the catalog at path, which is there, for do alone.
func with(path string, do func(*Catalog) error) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	c, err := OpenCatalog(path)
	if err != nil {
		return err
	}
	defer c.Close()
	return do(c)
}

// TakeOut takes photos out of the library: forgotten, so that a card that
// still has them brings them in again; or, keepOut, known and hidden, so
// that an import leaves them out as deleted until brought back.
func (c *Catalog) TakeOut(hashes []string, keepOut bool) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, h := range hashes {
		queries := []string{`DELETE FROM album_members WHERE hash = ?`, `DELETE FROM owners WHERE hash = ?`, `DELETE FROM photos WHERE hash = ?`}
		if keepOut {
			queries = []string{`UPDATE photos SET out = 1 WHERE hash = ?`}
		}
		for _, q := range queries {
			if _, err := tx.Exec(q, h); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// keptOut says whether a photo was taken out and kept out.
func (c *Catalog) keptOut(hash string) (bool, error) {
	var out bool
	err := c.db.QueryRow(`SELECT out FROM photos WHERE hash = ?`, hash).Scan(&out)
	return out, err
}

// forget drops a photo whose file is gone from the library, so that a copy
// brought in again is placed and counted as new.
func (c *Catalog) forget(hash string) error {
	_, err := c.db.Exec(`DELETE FROM photos WHERE hash = ?`, hash)
	return err
}

// setDate records a date given to a photo already in the library.
func (c *Catalog) setDate(hash string, capture Capture) error {
	_, err := c.db.Exec(`UPDATE photos SET taken = ?, source = ?, kind = ? WHERE hash = ?`,
		capture.When.Unix(), string(capture.Source), string(capture.Kind()), hash)
	return err
}

// setPath records that a photo is now at path, under name.
func (c *Catalog) setPath(hash, path, name string) error {
	_, err := c.db.Exec(`UPDATE photos SET path = ?, name = ? WHERE hash = ?`, path, name, hash)
	return err
}
