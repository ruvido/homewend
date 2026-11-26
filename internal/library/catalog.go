// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"cmp"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/edit"

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

// Key names one photo of one library: its content, and whose library it is
// in, the profile. The same content in two libraries is two photos, each
// with a place, a date and a state of its own; on the disk its bytes are there once.
type Key struct {
	Hash    string `json:"hash"`
	Profile string `json:"profile"`
}

// Photo is one picture as a library knows it.
type Photo struct {
	Hash    string
	Profile string
	Path    string // relative to the library root, so the library can be moved
	Name    string
	Bytes   int64
	Taken   time.Time
	Source  DateSource
	Kind    DateKind
	Origin  Origin
	Albums  []string
	// Turn is how many quarter turns anticlockwise the person gave it: kept
	// here, the file is never changed. In a list, 0 of a photo with an Edit,
	// which says which way up it is.
	Turn int
	// Edit names the edit it is shown with, in a list: "" is none.
	Edit string
	// Newer: in a list, its edit is one made with tools this program does not
	// know, kept and not drawn.
	Newer    bool
	Archived bool
	Favorite bool
}

// Key is the photo's.
func (p Photo) Key() Key { return Key{p.Hash, p.Profile} }

// tileColumns are what a list shows of a photo, and photoColumns all of it,
// as scanPhotos reads them: of a hundred thousand rows no more than is shown.
const (
	tileColumns  = `hash, profile, name, taken, source, origin, turn, archived, favorite`
	photoColumns = tileColumns + `, kind, bytes, path`
)

// Catalog is the index the interface reads: what there is, where it is, and
// what can be said about it.
//
// SQLite rather than a file of JSON because the grid has to scroll through tens
// of thousands of thumbnails and filter them without loading the lot, and
// because the driver is already here for reading Chrome's cookies. No cgo, so
// the app still cross-compiles to the three desktops from one machine.
type Catalog struct {
	db   *sql.DB
	path string
	// held is Holds's query, prepared once: a phone asks about hundreds.
	held func() (*sql.Stmt, error)
}

func newCatalog(db *sql.DB, path string) *Catalog {
	return &Catalog{db: db, path: path, held: sync.OnceValues(func() (*sql.Stmt, error) {
		return db.Prepare(`SELECT profile, path, ` + inTheTrash + `, archived, ` + deletedForGood + ` FROM library_photos WHERE hash = ?`)
	})}
}

const schema = `
-- One row for each photo of each library: the same content in two libraries
-- is two rows. Its id is what the other tables name it by.
-- What lists are narrowed by comes first: SQLite reads a row up to the last
-- column asked.
CREATE TABLE library_photos (
  id      INTEGER PRIMARY KEY,
  taken   INTEGER,          -- unix seconds, NULL when nothing is known
  -- Taken out of the library and kept out of the imports: out of every list,
  -- still known, so that an import leaves it out as deleted. With trashed:
  -- trash.go.
  out     INTEGER NOT NULL DEFAULT 0,
  -- In the Trash: out of every list and every import until restored; archived:
  -- out of the grid and the years, still in its albums, still backed up;
  -- turned by the person, in quarter turns; given a heart.
  trashed  INTEGER NOT NULL DEFAULT 0,
  archived INTEGER NOT NULL DEFAULT 0,
  turn     INTEGER NOT NULL DEFAULT 0,
  favorite INTEGER NOT NULL DEFAULT 0,
  hash    TEXT NOT NULL,
  profile TEXT NOT NULL,    -- whose library; '' before profiles
  name    TEXT NOT NULL,
  source  TEXT NOT NULL,
  origin  TEXT NOT NULL,
  kind    TEXT NOT NULL,
  bytes   INTEGER NOT NULL,
  path    TEXT NOT NULL,
  UNIQUE (hash, profile)
);
CREATE TABLE album_members (
  album TEXT NOT NULL,
  photo INTEGER NOT NULL REFERENCES library_photos(id),
  PRIMARY KEY (album, photo)
) WITHOUT ROWID;
-- Where a photo came from: its sources, "<service>/<address>" for an
-- account ("google/ron@example.com"), "local/<folder>" for an import. A photo
-- that came to one library from two is one photo with two; a photo recorded before sources
-- were kept has none, and is shown with every account.
CREATE TABLE owners (
  account TEXT NOT NULL,
  photo   INTEGER NOT NULL REFERENCES library_photos(id),
  PRIMARY KEY (account, photo)
) WITHOUT ROWID;
CREATE TABLE album_owners (
  account TEXT NOT NULL,
  album   TEXT NOT NULL,
  profile TEXT NOT NULL,
  PRIMARY KEY (account, album, profile)
) WITHOUT ROWID;
-- The grid is always "newest first, maybe filtered": one index carries it.
CREATE INDEX photos_by_page ON library_photos(taken DESC, name);
CREATE INDEX photos_by_path ON library_photos(path);
-- A program from before libraries stops here: it cannot index a view as it
-- opens, nor read a photo from this one. It would change the state
-- of a photo in every library that has it.
CREATE VIEW photos AS SELECT 'this library needs a newer Homewend' AS message;
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
	if vfs := os.Getenv("HOMEWEND_TEST_VFS"); vfs != "" {
		dsn += "&vfs=" + vfs // locks as exclusive as flock's, tried elsewhere
	} else if runtime.GOOS == "darwin" {
		dsn += "&vfs=unix-flock"
	}
	return dsn
}

// waiting is how long a connection waits for others to let go of the catalog.
// They take turns by chance, not in order: between two that write without
// pause, one was seen to wait more than 5 seconds on Windows.
var waiting = "_pragma=busy_timeout(30000)"

// upgradeWait is how long an opening waits for another that is bringing the
// catalog up to date, seconds for each hundred thousand photos: past waiting,
// it asks again.
const upgradeWait = 10 * time.Minute

// patient is do, asked again while another has the catalog.
func patient(do func() error) error {
	until := time.Now().Add(upgradeWait)
	for {
		err := do()
		if sqliteCode(err) != sqlite3.SQLITE_BUSY || time.Now().After(until) {
			return err
		}
	}
}

// unwritable is whether err is of a catalog that cannot be written.
func unwritable(err error) bool {
	return slices.Contains([]int{sqlite3.SQLITE_READONLY, sqlite3.SQLITE_CANTOPEN, sqlite3.SQLITE_FULL}, sqliteCode(err))
}

// sqliteCode is the primary code of SQLite's error, 0 when err is none.
func sqliteCode(err error) int {
	var e *sqlite.Error
	if !errors.As(err, &e) {
		return 0
	}
	return e.Code() & 0xff
}

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
	if err := patient(func() error { return upgrade(db, path) }); err != nil {
		db.Close()
		return nil, fmt.Errorf("preparing the catalog: %w", err)
	}
	return newCatalog(db, path), nil
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

// OpenCatalogReadOnly opens the index at path to look at it: no schema, no
// migration, nothing written. A look before an import changes nothing in the
// library, and a library on a read-only share can still be looked at.
//
// It waits like any reader for a write under way. A write cut short (a crash)
// is first undone, as SQLite asks of whoever opens next, and a catalog an
// older version left in WAL mode leaves it: hence mode=rw, which reads alone
// where nothing can be written. There a catalog in WAL mode is read as it
// lies, if its -wal holds nothing; what cannot be read whole is refused. One
// an older version made is first brought up to date, and where it cannot be
// refused, or read without what it lacks when it has libraries.
func OpenCatalogReadOnly(path string) (*Catalog, error) {
	db, err := openCatalog(path, "mode=rw&"+waiting+"&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("opening the catalog: %w", err)
	}
	var version int
	err = patient(func() error { return db.QueryRow(`PRAGMA user_version`).Scan(&version) }) // the first look at the file
	var e *sqlite.Error
	if fi, serr := os.Stat(path + "-wal"); errors.As(err, &e) && (e.Code()&0xff == sqlite3.SQLITE_CANTOPEN || e.Code() == sqlite3.SQLITE_READONLY_DIRECTORY) && (serr != nil || fi.Size() == 0) {
		db.Close()
		if db, err = sql.Open("sqlite", catalogDSN(path, "mode=ro&immutable=1")); err == nil {
			err = db.QueryRow(`PRAGMA user_version`).Scan(&version)
		}
	}
	if err == nil && version < editsSince {
		c, err := OpenCatalog(path)
		if err == nil {
			c.Close()
		}
		if version < librariesSince {
			db.Close()
			if err != nil {
				return nil, err
			}
			return OpenCatalogReadOnly(path)
		}
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("opening the catalog: %w", err)
	}
	return newCatalog(db, path), nil
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
	err := c.inTx(func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRow(
			`INSERT INTO library_photos (hash, profile, path, name, bytes, taken, source, kind, origin)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(hash, profile) DO UPDATE SET
			   path=excluded.path, name=excluded.name, bytes=excluded.bytes,
			   taken=excluded.taken, source=excluded.source, kind=excluded.kind,
			   origin=excluded.origin, trashed=CASE WHEN `+deletedForGood+` THEN 0 ELSE trashed END, out=0
			 RETURNING id`,
			photo.Hash, photo.Profile, photo.Path, photo.Name, photo.Bytes, taken,
			string(photo.Source), string(photo.Kind), string(photo.Origin)).Scan(&id)
		if err != nil {
			return err
		}
		for _, album := range photo.Albums {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO album_members (album, photo) VALUES (?, ?)`, album, id); err != nil {
				return err
			}
		}
		for _, source := range sources {
			if source == "" {
				continue
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO owners (account, photo) VALUES (?, ?)`, source, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("recording %s: %w", photo.Name, err)
	}
	return nil
}

// PathOf returns where the photo lives, relative to the library root, and
// whether the catalog knows it at all.
func (c *Catalog) PathOf(k Key) (string, bool, error) {
	var path string
	err := c.db.QueryRow(`SELECT path FROM library_photos WHERE hash = ? AND profile = ?`, k.Hash, k.Profile).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading the catalog: %w", err)
	}
	return path, true, nil
}

// erased is whether the photo k was deleted for good from its library.
func (c *Catalog) erased(k Key) (is bool, err error) {
	err = c.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM library_photos WHERE hash = ? AND profile = ? AND `+deletedForGood+`)`, k.Hash, k.Profile).Scan(&is)
	return is, err
}

// TrashedNames are the names of profile's photos in the Trash or deleted from
// it: they were in the library.
func (c *Catalog) TrashedNames(profile string) ([]string, error) {
	return texts(c.db.Query(`SELECT name FROM library_photos WHERE trashed = 1 AND profile = ?`, profile))
}

// Copies are the photos with the content of hash, one for each library that
// has it.
func (c *Catalog) Copies(hash string) ([]Photo, error) {
	return c.photos(`WHERE hash = ?`, hash)
}

// photos are the rows a WHERE keeps, those in the Trash or kept out too.
func (c *Catalog) photos(where string, args ...any) ([]Photo, error) {
	rows, err := c.db.Query(`SELECT `+photoColumns+` FROM library_photos `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("reading the catalog: %w", err)
	}
	return scanPhotos(rows)
}

// texts are the rows of a query of one text column, none being an empty list.
func texts(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, fmt.Errorf("reading the catalog: %w", err)
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

// PathTaken reports whether a photo other than k is recorded at this relative
// path.
func (c *Catalog) PathTaken(path string, k Key) (bool, error) {
	var n int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM library_photos WHERE path = ? AND (hash, profile) <> (?, ?)`, path, k.Hash, k.Profile).Scan(&n); err != nil {
		return false, fmt.Errorf("reading the catalog: %w", err)
	}
	return n > 0, nil
}

// addAlbum records that a photo belongs to an album, and the album to the
// account it came from. Separate from Put because a photo is placed once and
// may join several albums afterwards.
func (c *Catalog) addAlbum(album string, k Key, account string) error {
	_, err := c.db.Exec(
		`INSERT OR IGNORE INTO album_members (album, photo) SELECT ?, id FROM library_photos WHERE hash = ? AND profile = ?`, album, k.Hash, k.Profile)
	if err == nil && account != "" {
		_, err = c.db.Exec(
			`INSERT OR IGNORE INTO album_owners (account, album, profile) VALUES (?, ?, ?)`, account, album, k.Profile)
	}
	if err != nil {
		return fmt.Errorf("adding a photo to %s: %w", album, err)
	}
	return nil
}

// addOwner records that a photo came from an account. A photo already in its
// library gets one more owner when another account's export holds it too.
func (c *Catalog) addOwner(k Key, account string) error {
	if account == "" {
		return nil
	}
	_, err := c.db.Exec(`INSERT OR IGNORE INTO owners (account, photo) SELECT ?, id FROM library_photos WHERE hash = ? AND profile = ?`, account, k.Hash, k.Profile)
	return err
}

// LocalSource begins the source of a card or a folder imported,
// "local/<its name>": no account.
const LocalSource = "local/"

// shownOnly is the SQL that leaves out the photos of the hidden profiles,
// column naming the photo's profile; none hidden keeps them all. A photo of
// no profile yet (”, relayout's) is nobody's to hide.
func shownOnly(column string, hidden []string) (string, []any) {
	if len(hidden) == 0 {
		return "", nil
	}
	args := make([]any, len(hidden))
	for i, p := range hidden {
		args[i] = p
	}
	return column + " NOT IN (?" + strings.Repeat(",?", len(hidden)-1) + ")", args
}

// Filter is what the interface asks for: a page of the grid, narrowed.
type Filter struct {
	// Album, as AlbumOf's library has it: the account it came from, "" when
	// not known.
	Album, AlbumOf string
	Year           string
	NoDate         bool // only the photos whose date nobody knows
	// Archived keeps only the archived photos; Grid leaves them out, as the
	// main grid and its years do; Trashed keeps only those in the Trash,
	// which are otherwise in no list; Favorite only those with a heart,
	// archived or not; Video only the videos, archived or not.
	Archived, Grid, Trashed, Favorite, Video bool
	// Profile keeps the photos of this profile's library.
	Profile string
	// Only keeps only these photos; nil is all, empty is none.
	Only []Key
	// Hidden leaves out the photos of these profiles.
	Hidden []string
	Limit  int // the first of them; 0 is all
}

// Page returns photos newest first, narrowed by the filter: what a list shows
// of them, without their path, bytes and kind.
func (c *Catalog) Page(f Filter) ([]Photo, error) {
	query, args := f.where(`SELECT ` + tileColumns + ` FROM library_photos`)
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
	if err != nil {
		return nil, err
	}
	return photos, c.giveEdits(photos)
}

// BytesByKind is how many bytes the library's files take, by what they are
// (kindOf: "video", "raw", "" for a photo), from the sizes the catalog
// keeps: those in the Trash too, their files still there. A photo of two
// libraries is one file.
// shortcut: every row is read on each call, milliseconds for 100,000
// photos; a sum kept by kind when measured slow.
func (c *Catalog) BytesByKind() (map[string]int64, error) {
	// More than one library has these: looked for in the index alone.
	shared, err := texts(c.db.Query(`SELECT hash FROM library_photos GROUP BY hash HAVING COUNT(*) > 1`))
	if err != nil {
		return nil, err
	}
	counted := make(map[string]bool, len(shared)) // false until its bytes are in
	for _, hash := range shared {
		counted[hash] = false
	}
	rows, err := c.db.Query(`SELECT hash, name, bytes FROM library_photos WHERE out = 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	by := map[string]int64{}
	for rows.Next() {
		var hash, name string
		var n int64
		if err := rows.Scan(&hash, &name, &n); err != nil {
			return nil, err
		}
		if in, two := counted[hash]; two {
			if in {
				continue
			}
			counted[hash] = true
		}
		by[kindOf(name)] += n
	}
	return by, rows.Err()
}

// Count is how many photos the filter keeps, its Limit aside.
func (c *Catalog) Count(f Filter) (int, error) {
	query, args := f.where(`SELECT COUNT(*) FROM library_photos`)
	var n int
	err := c.db.QueryRow(query, args...).Scan(&n)
	return n, err
}

// CountsByState is how many photos are archived, in the Trash, given a heart
// and videos, in one reading of the rows: a photo two libraries have is one
// tile of each list, and counted once.
func (c *Catalog) CountsByState(hidden []string) (archived, trashed, favorite, videos int, err error) {
	query := `SELECT COUNT(DISTINCT CASE WHEN trashed = 0 AND archived THEN hash END), COUNT(DISTINCT CASE WHEN trashed THEN hash END),
		COUNT(DISTINCT CASE WHEN trashed = 0 AND favorite THEN hash END), COUNT(DISTINCT CASE WHEN trashed = 0 AND ` + aVideo + ` THEN hash END)
		FROM library_photos WHERE out = 0 AND (trashed = 1 OR archived = 1 OR favorite = 1 OR ` + aVideo + `)`
	clause, args := shownOnly("profile", hidden)
	if clause != "" {
		query += " AND " + clause
	}
	err = c.db.QueryRow(query, args...).Scan(&archived, &trashed, &favorite, &videos)
	return archived, trashed, favorite, videos, err
}

// aVideo is the SQL that says a photo is a video, by its name as IsVideo.
var aVideo = func() string {
	var by []string
	for _, ext := range slices.Sorted(maps.Keys(videoTypes)) {
		by = append(by, `lower(name) GLOB '*`+ext+`'`)
	}
	return `(` + strings.Join(by, ` OR `) + `)`
}()

// where is query narrowed by the filter, and its arguments.
func (f Filter) where(query string) (string, []any) {
	where := []string{"NOT " + away}
	if f.Trashed {
		where = []string{inTheTrash}
	}
	var args []any
	if f.Album != "" {
		where = append(where,
			"id IN (SELECT m.photo"+albumRows+" WHERE m.album = ? AND COALESCE(o.account, '') = ?)")
		args = append(args, f.Album, f.AlbumOf)
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
	if f.Favorite {
		where = append(where, "favorite = 1")
	}
	if f.Video {
		where = append(where, aVideo)
	}
	if f.Profile != "" {
		where = append(where, "profile = ?")
		args = append(args, f.Profile)
	}
	if f.Only != nil {
		// One variable for them all: SQLite takes 32,766.
		list, _ := json.Marshal(f.Only)
		where = append(where, "(hash, profile) IN (SELECT value ->> 'hash', value ->> 'profile' FROM json_each(?))")
		args = append(args, string(list))
	}
	if clause, more := shownOnly("profile", f.Hidden); clause != "" {
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
func (c *Catalog) Photo(k Key) (Photo, error) {
	photos, err := c.photos(`WHERE hash = ? AND profile = ?`, k.Hash, k.Profile)
	if err != nil || len(photos) == 0 {
		return Photo{}, fmt.Errorf("reading the catalog: no photo %s of %q: %v", k.Hash, k.Profile, err)
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
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var photos []Photo
	var photo Photo
	var taken sql.NullInt64
	var source, kind, origin string
	into := []any{&photo.Hash, &photo.Profile, &photo.Name, &taken, &source, &origin, &photo.Turn, &photo.Archived, &photo.Favorite, &kind, &photo.Bytes, &photo.Path}[:len(columns)]
	for rows.Next() {
		if err := rows.Scan(into...); err != nil {
			return nil, err
		}
		photo.Taken = time.Time{}
		if taken.Valid {
			photo.Taken = time.Unix(taken.Int64, 0).UTC()
		}
		photo.Source, photo.Kind, photo.Origin = DateSource(source), DateKind(kind), Origin(origin)
		photos = append(photos, photo)
	}
	return photos, rows.Err()
}

// CountsByYear is how many photos each year holds, and under "" those whose
// date nobody knows: a photo two libraries have, once.
func (c *Catalog) CountsByYear(hidden []string) (map[string]int, error) {
	query := `SELECT COALESCE(strftime('%Y', taken, 'unixepoch'), ''), COUNT(DISTINCT hash) FROM library_photos WHERE NOT ` + away + ` AND archived = 0`
	clause, args := shownOnly("profile", hidden)
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

// Latest are the photos recorded last, newest first: the first to arrive in
// a download are the last of these once it is over, the latest while it runs.
func (c *Catalog) Latest(n int) ([]Photo, error) {
	rows, err := c.db.Query(`SELECT hash, profile, name, turn FROM library_photos WHERE NOT `+away+` ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Photo
	for rows.Next() {
		var p Photo
		if err := rows.Scan(&p.Hash, &p.Profile, &p.Name, &p.Turn); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, c.giveEdits(out)
}

// State is what a photo's owner can set on it, by the catalog's column.
type State string

const (
	Archived State = "archived"
	Trashed  State = "trashed"
	Favorite State = "favorite"
)

// Mark sets state on photos, or clears it, and says which those were: one
// not in its library is left out, and the same photo in another library is
// left as it is.
func (c *Catalog) Mark(state State, photos []Key, on bool) (done []Key, err error) {
	err = c.inTx(func(tx *sql.Tx) error {
		done, err = setState(tx, state, photos, on)
		return err
	})
	return done, err
}

func setState(tx *sql.Tx, state State, photos []Key, on bool) ([]Key, error) {
	done := []Key{}
	find, err := tx.Prepare(`SELECT id FROM library_photos WHERE hash = ? AND profile = ? AND out = 0`)
	if err != nil {
		return done, err
	}
	set, err := tx.Prepare(setting(state))
	if err != nil {
		return done, err
	}
	for _, k := range photos {
		var id int64
		err := find.QueryRow(k.Hash, k.Profile).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err == nil {
			_, err = set.Exec(on, id)
		}
		if err != nil {
			return done, err
		}
		done = append(done, k)
	}
	return done, nil
}

// Turn gives a photo quarters more quarter turns anticlockwise (3: one
// clockwise), and says how it is shown now, its turn and its edit; fs.ErrNotExist for one the catalog
// lacks. Of a photo with an edit it is the edit that turns, and a phone hears
// of it; one this program cannot draw stays as it is, and so does the photo.
func (c *Catalog) Turn(k Key, quarters int) (turn int, digest string, err error) {
	err = c.inTx(func(tx *sql.Tx) error {
		var name string
		err := tx.QueryRow(`SELECT name, turn FROM library_photos WHERE hash = ? AND profile = ?`, k.Hash, k.Profile).Scan(&name, &turn)
		if errors.Is(err, sql.ErrNoRows) {
			return fs.ErrNotExist
		} else if err != nil {
			return err
		}
		edits, err := markIn(tx, Edit, false)
		if err != nil {
			return err
		}
		was, err := edits.of(k, "")
		if err != nil {
			return err
		}
		if !was.on() || IsVideo(name) {
			turn = (turn + quarters) % 4
			_, err := tx.Exec(`UPDATE library_photos SET turn = ? WHERE id = ?`, turn, was.id)
			return err
		}
		turned := []byte(was.value)
		for range quarters {
			if turned, err = edit.TurnLeft(turned); err != nil {
				return nil
			}
		}
		turn, digest = 0, EditDigest(string(turned))
		_, err = edits.give(k, "", was, mark{value: string(turned), at: time.Now().UnixMilli()})
		return err
	})
	return turn, digest, err
}

// About says which albums a photo is in and which accounts it came from.
func (c *Catalog) About(k Key) (albums, accounts []string, err error) {
	const of = ` = (SELECT id FROM library_photos WHERE hash = ? AND profile = ?) ORDER BY 1`
	if albums, err = texts(c.db.Query(`SELECT album FROM album_members WHERE photo`+of, k.Hash, k.Profile)); err != nil {
		return nil, nil, err
	}
	accounts, err = texts(c.db.Query(`SELECT account FROM owners WHERE photo`+of, k.Hash, k.Profile))
	return albums, accounts, err
}

// AlbumCount is an album and the account it came from ("" when not known).
type AlbumCount struct {
	Account string
	Name    string
	Count   int
}

// albumRows are the albums' photos p, each under every account o that
// brought the album to its library.
const albumRows = ` FROM album_members m JOIN library_photos p ON p.id = m.photo LEFT JOIN album_owners o ON o.album = m.album AND o.profile = p.profile`

// AlbumsByOwner lists the albums, each under every account that brought it,
// with the photos of the profiles not hidden, a photo two libraries have once.
func (c *Catalog) AlbumsByOwner(hidden []string) ([]AlbumCount, error) {
	query := `SELECT COALESCE(o.account, ''), m.album, COUNT(DISTINCT p.hash)` + albumRows + ` WHERE NOT ` + away
	clause, args := shownOnly("p.profile", hidden)
	if clause != "" {
		query += " AND " + clause
	}
	rows, err := c.db.Query(query+` GROUP BY 1, 2 ORDER BY 2, 1`, args...)
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
	Key
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
		rows, err := c.db.Query(`SELECT hash, profile, path, COALESCE(strftime('%Y', taken, 'unixepoch'), ''), out FROM library_photos WHERE trashed = 0`)
		if err != nil {
			return fmt.Errorf("reading the catalog: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var k known
			if err := rows.Scan(&k.Hash, &k.Profile, &k.path, &k.Year, &k.out); err != nil {
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
			// Given a date meanwhile, or moved, its file is at another path.
			path, in, err := c.PathOf(k.Key)
			if err != nil {
				return err
			}
			if in && path == k.path && !exists(under(root, At(root, path))) {
				missing = append(missing, k.Gone)
			}
		}
		for _, k := range back {
			if _, err := c.db.Exec(`UPDATE library_photos SET out = 0 WHERE hash = ? AND profile = ?`, k.Hash, k.Profile); err != nil {
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

// TakeOut takes photos out of their libraries: forgotten, so that a card that
// still has them brings them in again; or, keepOut, known and hidden, so
// that an import leaves them out as deleted until brought back.
func (c *Catalog) TakeOut(photos []Key, keepOut bool) error {
	return c.inTx(func(tx *sql.Tx) error {
		for _, k := range photos {
			if err := takeOut(tx, k, keepOut); err != nil {
				return err
			}
		}
		return nil
	})
}

func takeOut(tx *sql.Tx, k Key, keepOut bool) error {
	const id = `(SELECT id FROM library_photos WHERE hash = ? AND profile = ?)`
	queries := []string{`DELETE FROM album_members WHERE photo = ` + id, `DELETE FROM owners WHERE photo = ` + id, `DELETE FROM library_photos WHERE id = ` + id}
	if keepOut {
		queries = []string{`UPDATE library_photos SET out = 1 WHERE id = ` + id}
	}
	for _, q := range queries {
		if _, err := tx.Exec(q, k.Hash, k.Profile); err != nil {
			return err
		}
	}
	return nil
}

// inTx is do in one write: all of it, or nothing.
func (c *Catalog) inTx(do func(*sql.Tx) error) error {
	tx, err := c.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := do(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// setDate records a date given to a photo already in its library.
func (c *Catalog) setDate(k Key, capture Capture) error {
	var taken any // a file that says no date has none
	if !capture.When.IsZero() {
		taken = capture.When.Unix()
	}
	_, err := c.db.Exec(`UPDATE library_photos SET taken = ?, source = ?, kind = ? WHERE hash = ? AND profile = ?`,
		taken, string(capture.Source), string(capture.Kind()), k.Hash, k.Profile)
	return err
}

// setPath records that a photo is now at path, under name.
func (c *Catalog) setPath(k Key, path, name string) error {
	_, err := c.db.Exec(`UPDATE library_photos SET path = ?, name = ? WHERE hash = ? AND profile = ?`, path, name, k.Hash, k.Profile)
	return err
}

// refile records that the photo k, of no library, is of profile's, at path.
// One that library has already is that photo: it takes what k has that it has
// not, its archive, turn, albums and sources, its heart and its edit when they
// were the last set, is out of the Trash when either is, and is where it was, or at path
// when its file is gone.
func (c *Catalog) refile(k Key, profile, path string, gone bool) error {
	return c.inTx(func(tx *sql.Tx) error {
		var id int64
		err := tx.QueryRow(`UPDATE OR IGNORE library_photos SET profile = ?, path = ?, name = ? WHERE hash = ? AND profile = ? RETURNING id`,
			profile, path, filepath.Base(path), k.Hash, k.Profile).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			err = tx.QueryRow(`UPDATE library_photos AS t SET archived = max(t.archived, f.archived),
				turn = CASE t.turn WHEN 0 THEN f.turn ELSE t.turn END, trashed = CASE f.out WHEN 1 THEN t.trashed ELSE min(t.trashed, f.trashed) END
				FROM library_photos AS f WHERE t.hash = ? AND t.profile = ? AND f.hash = t.hash AND f.profile = ? RETURNING id`,
				k.Hash, profile, k.Profile).Scan(&id)
			if err != nil {
				return err
			}
			into := Key{k.Hash, profile}
			for _, state := range []State{Favorite, Edit} {
				marks, err := markIn(tx, state, false)
				if err != nil {
					return err
				}
				its, err := marks.of(k, "")
				if err != nil {
					return err
				}
				was, err := marks.of(into, "")
				if err != nil {
					return err
				}
				if _, err := marks.give(into, "", was, its); err != nil {
					return err
				}
			}
			if err := carryExtra(tx, k, into); err != nil {
				return err
			}
			queries := []string{
				`INSERT OR IGNORE INTO owners SELECT account, ?1 FROM owners WHERE photo = (SELECT id FROM library_photos WHERE hash = ?2 AND profile = ?3)`,
				`INSERT OR IGNORE INTO album_members SELECT album, ?1 FROM album_members WHERE photo = (SELECT id FROM library_photos WHERE hash = ?2 AND profile = ?3)`,
			}
			for _, q := range queries {
				if _, err := tx.Exec(q, id, k.Hash, k.Profile); err != nil {
					return err
				}
			}
			if gone {
				if _, err := tx.Exec(`UPDATE library_photos SET path = ?, name = ?, out = 0 WHERE id = ?`, path, filepath.Base(path), id); err != nil {
					return err
				}
			}
			if err := takeOut(tx, k, false); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		return ownAlbums(tx, id, k.Profile)
	})
}
