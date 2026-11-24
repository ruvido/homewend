// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// A mark is what a person set on a photo of their library, kept with when it
// was set and a number that grows with each, for a phone to hear of it and to
// say its own. Today it is the heart alone.

// marksSchema is what keeps them. A heart is noted by the triggers, in the
// statement that sets it: nothing, not even a program from before marks, sets
// one and leaves it unnoted, or forgets a photo and leaves its mark.
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
CREATE TRIGGER mark_noted AFTER UPDATE OF ` + string(Favorite) + ` ON library_photos BEGIN
  INSERT INTO mark_seqs VALUES (NEW.profile, 1) ON CONFLICT DO UPDATE SET last = last + 1;
  INSERT INTO marks (profile, hash, state, value, at, seq)
    SELECT profile, NEW.hash, '` + string(Favorite) + `', NEW.` + string(Favorite) + `, CAST(unixepoch('subsec') * 1000 AS INTEGER), last
    FROM mark_seqs WHERE profile = NEW.profile
    ON CONFLICT DO UPDATE SET value = excluded.value, at = excluded.at, seq = excluded.seq;
END;
CREATE TRIGGER mark_forgotten AFTER DELETE ON library_photos BEGIN
  DELETE FROM marks WHERE profile = OLD.profile AND hash = OLD.hash;
END;
`

var (
	// ErrNotMarks is a question that is not one of marks.
	ErrNotMarks = errors.New("not a question of marks")
	// ErrNoMarks is a catalog from before marks that could not be given them.
	ErrNoMarks = errors.New("the library keeps no marks, and cannot be written")
)

// maxMarks is how many marks a question says and an answer has, and
// maxInstall how long an install's id is: it is kept on every row.
const (
	maxMarks   = 500
	maxInstall = 128
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
		// Asked only now: every other opening has them.
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

// Differs is whether a phone that knows the shelf id up to after has
// something to hear: another shelf, or a seq that is not its last.
func (s Shelf) Differs(id string, after int64) bool { return s.ID != id || s.Cursor != after }

// MarkQuestion is what a phone says and asks: its own marks, then what
// changed after a seq of the shelf it knows.
type MarkQuestion struct {
	Shelf   string
	After   int64
	Install string
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
}

// Mark is a photo's state in its library.
type Mark struct {
	Hash  string `json:"hash"`
	State State  `json:"state"`
	Value bool   `json:"value"`
}

// Check is whether the question is one.
func (q MarkQuestion) Check() error {
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
	a := MarkAnswer{Said: []Mark{}, Marks: []Mark{}, Unknown: []string{}}
	if err := q.Check(); err != nil {
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
		if m.State == Favorite {
			known = append(known, m)
		}
	}
	if len(known) > 0 {
		if err := c.inTx(func(tx *sql.Tx) error { return a.apply(tx, profile, q.Install, known, then) }); err != nil {
			return a, err
		}
		// Those just applied are heard too.
		if a.Shelf, err = c.Shelf(profile); err != nil {
			return a, err
		}
	}
	// Up to the last seq read above: one that comes meanwhile is the next
	// answer's.
	rows, err := c.db.Query(`SELECT hash, state, value, seq FROM marks WHERE profile = ? AND seq > ? AND seq <= ? ORDER BY seq LIMIT ?`,
		profile, q.After, a.Cursor, maxMarks+1)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	last := q.After
	for rows.Next() {
		var m Mark
		var seq int64
		if err := rows.Scan(&m.Hash, &m.State, &m.Value, &seq); err != nil {
			return a, err
		}
		if len(a.Marks) == maxMarks {
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
	hearts, err := markIn(tx, Favorite)
	if err != nil {
		return err
	}
	for _, timed := range []bool{false, true} {
		for _, m := range marks {
			if (m.Ago != nil) != timed {
				continue
			}
			k := Key{m.Hash, profile}
			was, err := hearts.of(k)
			if err != nil {
				return err
			}
			if !was.in {
				continue
			}
			is := mark{value: string(m.Value) == "true", install: install, n: m.N}
			if timed {
				// Never 0, which is no time, and never later than now.
				is.at = max(1, then.UnixMilli()-max(0, *m.Ago))
			}
			if err := hearts.give(k, was, is, then); err != nil {
				return err
			}
		}
	}
	answered := map[string]bool{}
	for _, m := range marks {
		if answered[m.Hash] {
			continue
		}
		answered[m.Hash] = true
		is, err := hearts.of(Key{m.Hash, profile})
		if err != nil {
			return err
		}
		if is.in {
			a.Said = append(a.Said, Mark{m.Hash, Favorite, is.value})
		} else {
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
	value   bool
	at      int64
	install string
	n       int64
}

// marker reads and sets the marks of one state in one write, each of its
// queries prepared once for the hundreds a phone says.
type marker struct {
	find, set, sign *sql.Stmt
	state           State
}

func markIn(tx *sql.Tx, state State) (marker, error) {
	m := marker{state: state}
	var err error
	for into, query := range map[**sql.Stmt]string{
		&m.find: `SELECT p.id, p.out = 0, p.` + string(state) + `, COALESCE(m.at, 0), COALESCE(m.install, ''), COALESCE(m.n, 0)
			FROM library_photos p LEFT JOIN marks m ON m.profile = p.profile AND m.hash = p.hash AND m.state = ?
			WHERE p.hash = ? AND p.profile = ?`,
		&m.set:  setting(state),
		&m.sign: `UPDATE marks SET at = ?, install = ?, n = ? WHERE profile = ? AND hash = ? AND state = ?`,
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
	err := m.find.QueryRow(m.state, k.Hash, k.Profile).Scan(&is.id, &is.in, &is.value, &is.at, &is.install, &is.n)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return is, err
}

// give makes the photo k, marked was, marked is, when is is the last of the
// two: by its time, the same time being the new one's; one with no time only
// adds, to what has none. None is later than now. One of the same install that
// is no newer was applied already.
func (m marker) give(k Key, was, is mark, now time.Time) error {
	if is.install != "" && is.install == was.install && is.n <= was.n {
		return nil
	}
	if is.at == 0 && (!is.value || was.value || was.at != 0) {
		return nil
	}
	if is.at != 0 {
		// The clock was ahead as that was set: it was set by now, and stays so.
		// Otherwise nothing could be set until the clock is there again.
		if was.at > now.UnixMilli() {
			was.at = now.UnixMilli()
			if err := m.signed(k, was); err != nil {
				return err
			}
		}
		if is.at < was.at {
			return nil
		}
	}
	if is.install == "" {
		is.install, is.n = was.install, was.n
	}
	is.id = was.id
	return m.put(k, is)
}

// put marks the photo k, whose id is in is.
func (m marker) put(k Key, is mark) error {
	if _, err := m.set.Exec(is.value, is.id); err != nil {
		return err
	}
	return m.signed(k, is)
}

// signed says when the mark of k was made and by which phone: until then it
// is as of now, and of the last phone.
func (m marker) signed(k Key, is mark) error {
	_, err := m.sign.Exec(is.at, is.install, is.n, k.Profile, k.Hash, m.state)
	return err
}

// setting is the SQL that sets state on a photo, or clears it, by its id; a
// mark is noted with it.
func setting(state State) string {
	return `UPDATE library_photos SET ` + string(state) + ` = ? WHERE id = ?`
}
