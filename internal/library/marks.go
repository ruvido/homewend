// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/edit"
)

// A mark is what a person set on a photo of their library, kept with when it
// was set and a number that grows with each, for a phone to hear of it and to
// say its own: the heart, and the edit.

// marksSchema is what keeps them. A heart is noted by the triggers, in the
// statement that sets it: nothing, not even a program from before marks, sets
// one and leaves it unnoted, or forgets or refiles a photo and leaves its
// mark. A photo with no mark, given the state it has, has none after.
//
// at is the computer's, in ms; the hearts there are have no time, 0. install
// and n are of the last phone's mark it took. The seq is taken from a counter
// that outlives the marks: deleted, its number is never another's. Every opening reads this text: it is short.
const marksSchema = `
CREATE TABLE catalog_id (id TEXT NOT NULL);
INSERT INTO catalog_id VALUES (lower(hex(randomblob(16))));
CREATE TABLE marks (
  profile TEXT NOT NULL,
  hash    TEXT NOT NULL,
  state   TEXT NOT NULL,
  value   INTEGER NOT NULL,
  at      INTEGER NOT NULL,
  seq     INTEGER NOT NULL,
  install TEXT NOT NULL DEFAULT '',
  n       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (profile, hash, state)
) WITHOUT ROWID;
CREATE INDEX marks_by_seq ON marks(profile, seq);
CREATE TABLE mark_seqs (profile TEXT PRIMARY KEY, last INTEGER NOT NULL) WITHOUT ROWID;
INSERT INTO marks (profile, hash, state, value, at, seq)
  SELECT profile, hash, '` + string(Favorite) + `', 1, 0, ROW_NUMBER() OVER (PARTITION BY profile ORDER BY id)
  FROM library_photos WHERE ` + string(Favorite) + ` = 1;
INSERT INTO mark_seqs SELECT profile, MAX(seq) FROM marks GROUP BY profile;
CREATE TRIGGER mark_noted AFTER UPDATE OF ` + string(Favorite) + ` ON library_photos
WHEN NEW.` + string(Favorite) + ` <> OLD.` + string(Favorite) + ` OR EXISTS (SELECT 1 FROM marks WHERE profile = NEW.profile AND hash = NEW.hash) BEGIN
  INSERT INTO mark_seqs VALUES (NEW.profile, 1) ON CONFLICT DO UPDATE SET last = last + 1;
  INSERT INTO marks (profile, hash, state, value, at, seq)
    SELECT profile, NEW.hash, '` + string(Favorite) + `', NEW.` + string(Favorite) + `, CAST(unixepoch('subsec') * 1000 AS INTEGER), last
    FROM mark_seqs WHERE profile = NEW.profile
    ON CONFLICT DO UPDATE SET value = excluded.value, at = excluded.at, seq = excluded.seq;
END;
CREATE TRIGGER mark_refiled AFTER UPDATE OF profile ON library_photos
WHEN EXISTS (SELECT 1 FROM marks WHERE profile = OLD.profile AND hash = NEW.hash) BEGIN
  INSERT INTO mark_seqs VALUES (NEW.profile, 1) ON CONFLICT DO UPDATE SET last = last + 1;
  UPDATE marks SET profile = NEW.profile, seq = (SELECT last FROM mark_seqs WHERE profile = NEW.profile)
    WHERE profile = OLD.profile AND hash = NEW.hash;
END;
CREATE TRIGGER mark_forgotten AFTER DELETE ON library_photos BEGIN
  DELETE FROM marks WHERE profile = OLD.profile AND hash = OLD.hash;
END;
`

// editsSchema keeps the edits, under the marks' seqs and rules. They have a
// table of their own: an older program reads a mark's value as a number. A
// value is the JSON as it was said, NULL the photo as it came. Setting one
// takes a seq; a program from before edits that forgets a photo, or refiles
// it, does so to its edit.
const editsSchema = `
CREATE TABLE edits (
  profile TEXT NOT NULL,
  hash    TEXT NOT NULL,
  value   TEXT,
  at      INTEGER NOT NULL,
  seq     INTEGER NOT NULL,
  install TEXT NOT NULL DEFAULT '',
  n       INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (profile, hash)
);
CREATE INDEX edits_by_seq ON edits(profile, seq);
CREATE TRIGGER edit_noted AFTER UPDATE OF profile, value ON edits BEGIN
  INSERT INTO mark_seqs VALUES (NEW.profile, 1) ON CONFLICT DO UPDATE SET last = last + 1;
  UPDATE edits SET seq = (SELECT last FROM mark_seqs WHERE profile = NEW.profile)
    WHERE profile = NEW.profile AND hash = NEW.hash;
END;
CREATE TRIGGER edit_refiled AFTER UPDATE OF profile ON library_photos BEGIN
  UPDATE edits SET profile = NEW.profile WHERE profile = OLD.profile AND hash = NEW.hash;
END;
CREATE TRIGGER edit_forgotten AFTER DELETE ON library_photos BEGIN
  DELETE FROM edits WHERE profile = OLD.profile AND hash = OLD.hash;
END;
`

// Edit is the state of a photo's edit, in a table of its own.
const Edit State = "edit"

var (
	// ErrNotMarks is a question that is not one of marks.
	ErrNotMarks = errors.New("not a question of marks")
	// ErrNoMarks is a catalog that cannot be written: it takes no marks, or is
	// from before them and could not be given any.
	ErrNoMarks = errors.New("the library cannot be written")
)

// maxMarks is how many marks a question says and an answer has, and
// maxInstall how long an install's id is: it is kept on every row. An answer's
// marks take maxPage bytes at most, when they are more than one: each takes
// markBytes and its own.
const (
	maxMarks   = 500
	maxInstall = 128
	maxPage    = 256 << 10
	markBytes  = len(`{"hash":"","state":"","value":},`)
)

// Shelf is a library's marks as a phone names them: the catalog and the
// library as one id, and its last seq.
type Shelf struct {
	ID     string `json:"shelf"`
	Cursor int64  `json:"cursor"`
}

// Shelf is the marks' of profile.
func (c *Catalog) Shelf(profile string) (Shelf, error) {
	var s Shelf
	var id string
	err := c.db.QueryRow(`SELECT id, COALESCE((SELECT last FROM mark_seqs WHERE profile = ?), 0) FROM catalog_id`, profile).Scan(&id, &s.Cursor)
	if err != nil {
		// Asked only now: every other catalog has them.
		var version int
		if c.db.QueryRow(`PRAGMA user_version`).Scan(&version) == nil && version < marksSince {
			err = ErrNoMarks
		}
		return s, err
	}
	sum := sha256.Sum256([]byte(id + "/" + profile))
	s.ID = hex.EncodeToString(sum[:16])
	return s, nil
}

// Differs is whether a phone that knows profile's shelf id up to after has
// something to hear of states: another shelf, a seq not reached, or a mark.
func (c *Catalog) Differs(profile, id string, after int64, states []State) (bool, error) {
	s, err := c.Shelf(profile)
	if err != nil || s.ID != id || s.Cursor <= after {
		return err == nil && (s.ID != id || s.Cursor < after), err
	}
	heard, err := c.heard(states)
	if err != nil || heard == "" {
		return false, err
	}
	var some bool
	err = c.db.QueryRow(`SELECT EXISTS (`+heard+`)`, profile, after, s.Cursor).Scan(&some)
	return some, err
}

// heard is the SQL of the marks of states, those known here: of a profile,
// from after a seq to one.
func (c *Catalog) heard(states []State) (string, error) {
	const of = ` WHERE profile = ?1 AND seq > ?2 AND seq <= ?3`
	var parts []string
	if asks(states, Favorite) {
		parts = append(parts, `SELECT hash, '`+string(Favorite)+`', iif(value, 'true', 'false'), seq AS seq FROM marks`+of)
	}
	if asks(states, Edit) {
		kept, err := hasTable(c.db, "edits") // not where nothing can be written
		if err != nil {
			return "", err
		}
		if kept {
			parts = append(parts, `SELECT hash, '`+string(Edit)+`', COALESCE(value, 'null'), seq AS seq FROM edits`+of)
		}
	}
	return strings.Join(parts, ` UNION ALL `), nil
}

// asks is whether a phone asks of state: one that names none is from before
// edits.
func asks(states []State, state State) bool {
	return slices.Contains(states, state) || len(states) == 0 && state == Favorite
}

// Edits are the edits of the photos that have one; those of only, nil being all.
func (c *Catalog) Edits(only []Key) (map[Key]string, error) {
	kept, err := hasTable(c.db, "edits") // not where nothing can be written
	if err != nil || !kept {
		return nil, err
	}
	query, args := `SELECT hash, profile, value FROM edits WHERE value IS NOT NULL`, []any(nil)
	if only != nil {
		list, _ := json.Marshal(only)
		query += ` AND (hash, profile) IN (SELECT value ->> 'hash', value ->> 'profile' FROM json_each(?))`
		args = append(args, string(list))
	}
	rows, err := c.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	edits := map[Key]string{}
	for rows.Next() {
		var k Key
		var value string
		if err := rows.Scan(&k.Hash, &k.Profile, &value); err != nil {
			return nil, err
		}
		edits[k] = value
	}
	return edits, rows.Err()
}

// digests are the names of the edits met: to name one, and read whether it is
// drawn here, takes longer than all else a list does with it.
// shortcut: never forgotten, some 300 bytes each; bounded if ever met as memory.
var digests sync.Map

// EditDigest names the edit that value, its JSON, is, when it is one this
// program draws; "" is none, or one to be kept and not drawn. Two libraries'
// equal ones have one name.
func EditDigest(value string) string {
	if name, known := digests.Load(value); known {
		return name.(string)
	}
	name := ""
	if _, err := edit.Parse([]byte(value)); err == nil && value != "null" {
		sum := sha256.Sum256([]byte(value))
		name = hex.EncodeToString(sum[:12])
	}
	// Its own: value may be a piece of all the edits.
	digests.Store(strings.Clone(value), name)
	return name
}

// fewEdits is up to how many photos' edits are looked for one by one.
const fewEdits = 500

// giveEdits gives photos, of a list, the edits they are shown with. What is
// read is the edits there are, as one row: a list without any
// pays for none.
func (c *Catalog) giveEdits(photos []Photo) error {
	names := map[Key]string{}
	if len(photos) <= fewEdits {
		keys := make([]Key, len(photos))
		for i, p := range photos {
			keys[i] = p.Key()
		}
		edits, err := c.Edits(keys)
		if err != nil {
			return err
		}
		for k, value := range edits {
			names[k] = EditDigest(value)
		}
	} else {
		kept, err := hasTable(c.db, "edits") // not where nothing can be written
		if err != nil || !kept {
			return err
		}
		// No JSON has a 0 in it.
		rows, err := c.db.Query(`SELECT COALESCE(group_concat(profile || char(0) || hash || char(0) || value, char(0)), '') FROM edits NOT INDEXED WHERE value IS NOT NULL`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var keys []Key
		var values []string
		for rows.Next() {
			k, all := Key{}, ""
			if err := rows.Scan(&all); err != nil {
				return err
			}
			for all != "" {
				var value string
				k.Profile, all, _ = strings.Cut(all, "\x00")
				k.Hash, all, _ = strings.Cut(all, "\x00")
				value, all, _ = strings.Cut(all, "\x00")
				keys, values = append(keys, k), append(values, value)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		// Read the first time, they are the slowest part: on every core.
		var wg sync.WaitGroup
		for some := range slices.Chunk(values, 1000) {
			wg.Go(func() {
				for i, value := range some {
					some[i] = EditDigest(value)
				}
			})
		}
		wg.Wait()
		names = make(map[Key]string, len(keys))
		for i, k := range keys {
			names[k] = values[i]
		}
	}
	if len(names) == 0 {
		return nil
	}
	for i := range photos {
		p := &photos[i]
		if name := names[p.Key()]; name != "" && !IsVideo(p.Name) {
			p.Edit, p.Turn = name, 0
		}
	}
	return nil
}

// MarkQuestion is what a phone says and asks: its own marks, then what
// changed after a seq of the shelf it knows.
type MarkQuestion struct {
	Shelf   string
	After   int64
	Install string
	States  []State // it speaks of no other
	Marks   []MarkAction
}

// MarkAction is what a person did on the phone Ago ms before it was said; with
// no Ago, nobody knows when. N counts the install's actions.
type MarkAction struct {
	N     int64
	Hash  string
	State State
	Value json.RawMessage // a state this program does not know has its own
	Ago   *int64
}

// MarkAnswer is what the library answers.
type MarkAnswer struct {
	Shelf
	// Other: the shelf asked of is another, or was another time's: nothing was applied.
	Other bool   `json:"other"`
	More  bool   `json:"more"`
	Said  []Mark `json:"said"`
	Marks []Mark `json:"marks"`
	// Unknown are the photos said that the library does not have.
	Unknown []string `json:"unknown"`
	// Refused count the edits said that are none.
	Refused []int64 `json:"refused"`
}

// Mark is a photo's state in its library.
type Mark struct {
	Hash  string          `json:"hash"`
	State State           `json:"state"`
	Value json.RawMessage `json:"value"`
}

// check is whether the question is one.
func (q MarkQuestion) check() error {
	if q.After < 0 || len(q.Marks) > maxMarks || len(q.Install) > maxInstall || len(q.Marks) > 0 && q.Install == "" {
		return ErrNotMarks
	}
	for _, m := range q.Marks {
		sum, err := hex.DecodeString(m.Hash)
		if err != nil || len(sum) != sha256.Size || m.Hash != strings.ToLower(m.Hash) || m.N < 0 || m.State == "" ||
			m.State == Favorite && string(m.Value) != "true" && string(m.Value) != "false" {
			return ErrNotMarks
		}
	}
	return nil
}

// Marks answers, for the library of profile, a question received at the
// computer's time then: each mark said applied by when it was made, then what
// changed. A plain question takes no lock to write.
func (c *Catalog) Marks(profile string, q MarkQuestion, then time.Time) (MarkAnswer, error) {
	a := MarkAnswer{Said: []Mark{}, Marks: []Mark{}, Unknown: []string{}, Refused: []int64{}}
	if err := q.check(); err != nil {
		return a, err
	}
	var err error
	if a.Shelf, err = c.Shelf(profile); err != nil {
		return a, err
	}
	a.Other = q.Shelf != a.ID && (q.Shelf != "" || len(q.Marks) > 0) || q.After > a.Cursor
	if a.Other {
		a.Cursor = 0
		return a, nil
	}
	var known []MarkAction
	for _, m := range q.Marks {
		if !asks(q.States, m.State) {
			continue
		}
		switch m.State {
		case Edit:
			if _, err := edit.Parse(m.Value); err != nil && !errors.Is(err, edit.ErrNewer) {
				a.Refused = append(a.Refused, m.N)
				continue
			}
			if edit.Removes(m.Value) {
				m.Value = json.RawMessage("null")
			}
		case Favorite:
		default:
			continue
		}
		known = append(known, m)
	}
	if len(known) > 0 {
		if err := c.inTx(func(tx *sql.Tx) error { return a.apply(tx, profile, q.Install, known, then) }); unwritable(err) {
			return a, ErrNoMarks
		} else if err != nil {
			// No table for them: it could not be given one.
			if kept, has := hasTable(c.db, "edits"); has == nil && !kept {
				err = ErrNoMarks
			}
			return a, err
		}
		// Those just applied are heard too.
		if a.Shelf, err = c.Shelf(profile); err != nil {
			return a, err
		}
	}
	heard, err := c.heard(q.States)
	if err != nil || heard == "" {
		return a, err
	}
	// Up to the last seq read above: one that comes meanwhile is the next
	// answer's.
	rows, err := c.db.Query(heard+` ORDER BY seq LIMIT ?4`, profile, q.After, a.Cursor, maxMarks+1)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	last, size := q.After, 0
	for rows.Next() {
		var m Mark
		var seq int64
		if err := rows.Scan(&m.Hash, &m.State, (*[]byte)(&m.Value), &seq); err != nil {
			return a, err
		}
		size += markBytes + len(m.Hash) + len(m.State) + len(m.Value)
		if len(a.Marks) == maxMarks || size > maxPage && len(a.Marks) > 0 {
			a.More, a.Cursor = true, last
			break
		}
		last = seq
		a.Marks = append(a.Marks, m)
	}
	return a, rows.Err()
}

// apply applies the marks an install says, those without a time first, and
// says what the library then holds of each photo.
func (a *MarkAnswer) apply(tx *sql.Tx, profile, install string, marks []MarkAction, then time.Time) error {
	in := map[State]marker{}
	for _, m := range marks {
		if _, ok := in[m.State]; ok {
			continue
		}
		var err error
		if in[m.State], err = markIn(tx, m.State); err != nil {
			return err
		}
	}
	for _, timed := range []bool{false, true} {
		for _, m := range marks {
			if (m.Ago != nil) != timed {
				continue
			}
			k := Key{m.Hash, profile}
			was, err := in[m.State].of(k)
			if err != nil {
				return err
			}
			if !was.in {
				continue
			}
			is := mark{value: string(m.Value), install: install, n: m.N}
			if timed {
				// Never 0, which is no time, and never later than now.
				is.at = max(1, then.UnixMilli()-max(0, *m.Ago))
			}
			if err := in[m.State].give(k, was, is); err != nil {
				return err
			}
		}
	}
	answered, unknown := map[[2]string]bool{}, map[string]bool{}
	for _, m := range marks {
		if one := [2]string{m.Hash, string(m.State)}; unknown[m.Hash] || answered[one] {
			continue
		} else {
			answered[one] = true
		}
		is, err := in[m.State].of(Key{m.Hash, profile})
		if err != nil {
			return err
		}
		if is.in {
			a.Said = append(a.Said, Mark{m.Hash, m.State, json.RawMessage(is.value)})
		} else {
			unknown[m.Hash] = true
			a.Unknown = append(a.Unknown, m.Hash)
		}
	}
	return nil
}

// mark is a mark as the catalog keeps it, of the photo id, which is in its
// library or kept out of it.
type mark struct {
	id      int64
	in      bool
	value   string // its JSON
	at      int64
	install string
	n       int64
}

// on is whether there is a heart, or an edit.
func (m mark) on() bool { return m.value != "false" && m.value != "null" }

// marker reads and sets the marks of one state in one write, each of its
// queries prepared once for the hundreds a phone says.
type marker struct {
	find, begin, set, sign *sql.Stmt
	state                  State
}

func markIn(tx *sql.Tx, state State) (marker, error) {
	m := marker{state: state}
	table, value := `marks`, `iif(p.`+string(state)+`, 'true', 'false')`
	begin, set := `marks (profile, hash, state, value, at, seq) VALUES (?, ?, '`+string(state)+`', 0, 0, 0)`, setting(state)
	if state == Edit {
		table, value = `edits`, `COALESCE(m.value, 'null')`
		begin, set = `edits (profile, hash, at, seq) VALUES (?, ?, 0, 0)`,
			`UPDATE edits SET value = ? WHERE (profile, hash) = (SELECT profile, hash FROM library_photos WHERE id = ?)`
	}
	var err error
	for into, query := range map[**sql.Stmt]string{
		&m.find: `SELECT p.id, p.out = 0, ` + value + `, COALESCE(m.at, 0), COALESCE(m.install, ''), COALESCE(m.n, 0)
			FROM library_photos p LEFT JOIN ` + table + ` m ON m.profile = p.profile AND m.hash = p.hash
			WHERE p.hash = ? AND p.profile = ?`,
		&m.begin: `INSERT OR IGNORE INTO ` + begin,
		&m.set:   set,
		&m.sign:  `UPDATE ` + table + ` SET at = ?, install = ?, n = ? WHERE profile = ? AND hash = ?`,
	} {
		if *into, err = tx.Prepare(query); err != nil {
			return m, err
		}
	}
	return m, nil
}

// of is the mark of the photo k; with none, or no photo, no time and no value.
func (m marker) of(k Key) (mark, error) {
	var is mark
	err := m.find.QueryRow(k.Hash, k.Profile).Scan(&is.id, &is.in, &is.value, &is.at, &is.install, &is.n)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return is, err
}

// give makes the photo k, marked was, marked is, when is is the last of the
// two: by its time, the same time being the new one's; one with no time only
// adds, to what has none. One of the same install that is no newer was applied
// already.
func (m marker) give(k Key, was, is mark) error {
	if is.install != "" && is.install == was.install && is.n <= was.n {
		return nil
	}
	if is.at == 0 && (!is.on() || was.on() || was.at != 0) {
		return nil
	}
	// The clock was ahead as a later one was set: it is not believed. Now, not
	// when the question came: one set here meanwhile is.
	if is.at != 0 && is.at < was.at && was.at <= time.Now().UnixMilli() {
		return nil
	}
	// One with no time is counted nowhere: said before a timed one, it must
	// not keep that out.
	if is.install == "" || is.at == 0 {
		is.install, is.n = was.install, was.n
	}
	// An off where there was no mark is one too.
	if _, err := m.begin.Exec(k.Profile, k.Hash); err != nil {
		return err
	}
	var value any = is.on()
	if m.state == Edit {
		value = sql.NullString{String: is.value, Valid: is.on()}
	}
	if _, err := m.set.Exec(value, was.id); err != nil {
		return err
	}
	_, err := m.sign.Exec(is.at, is.install, is.n, k.Profile, k.Hash)
	return err
}

// setting is the SQL that sets state on a photo, or clears it, by its id; a
// mark is noted with it.
func setting(state State) string {
	return `UPDATE library_photos SET ` + string(state) + ` = ? WHERE id = ?`
}
