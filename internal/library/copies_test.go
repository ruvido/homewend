// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// dropExtra makes a catalog one from before copies.
const dropExtra = `DROP TRIGGER extra_mark_noted; DROP TRIGGER extra_mark_refiled; DROP TRIGGER extra_mark_forgotten;
DROP TRIGGER restore_noted; DROP TABLE extra_marks; ALTER TABLE library_photos DROP COLUMN restored;`

var all = []State{Favorite, Edit, Date, Copy}

// onCopy is a of the copy named copy.
func onCopy(a MarkAction, copy string) MarkAction {
	a.Copy = copy
	return a
}

// dumped is every row of the tables a catalog's marks are in, as text.
func dumped(t *testing.T, c *Catalog) string {
	t.Helper()
	var out strings.Builder
	for _, q := range []string{
		`SELECT profile, hash, state, value, at, seq, install, n FROM marks ORDER BY 1, 2, 3`,
		`SELECT profile, hash, value, at, seq, install, n FROM edits ORDER BY 1, 2`,
		`SELECT profile, last FROM mark_seqs ORDER BY 1`,
		`SELECT id, hash, profile, path, favorite, trashed, out FROM library_photos ORDER BY 1`,
	} {
		rows, err := c.db.Query(q)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			row := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range row {
				ptrs[i] = &row[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintln(&out, row...)
		}
		rows.Close()
	}
	return out.String()
}

// A catalog of version 7 is given what copies need once, in one write: every
// mark and edit as it was; one stopped halfway is as it was, and done again.
func TestACatalogFromBeforeCopies(t *testing.T) {
	c, path := marking(t, 3)
	tell(t, c, "A", 0, act(1, true, 100), edited(2, bright, 50))
	other := edited(3, warm, noTime)
	other.Hash = hashed(1)
	tell(t, c, "A", 0, other)
	was := dumped(t, c)
	c.Close()
	// What a program killed halfway leaves is as nothing done: a name taken
	// makes the step fail after its first statement.
	olderAt(t, path, linksSince, dropExtra+`CREATE TABLE extra_marks_by_seq (x);`)
	if c, err := OpenCatalog(path); err == nil {
		c.Close()
		t.Fatal("it opened")
	}
	c, err := OpenCatalogReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	there, _ := hasTable(c.db, "extra_marks")
	if c.db.QueryRow(`PRAGMA user_version`).Scan(&version); version != linksSince || there || dumped(t, c) != was {
		t.Errorf("halfway: version %d, table %v", version, there)
	}
	c.Close()
	olderAt(t, path, linksSince, `DROP TABLE extra_marks_by_seq`)
	if c, err = OpenCatalog(path); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.db.QueryRow(`PRAGMA user_version`).Scan(&version); version != copiesSince {
		t.Errorf("version %d", version)
	}
	if now := dumped(t, c); now != was {
		t.Errorf("rows changed:\n%s\nwant\n%s", now, was)
	}
	// Once: the next opening writes nothing.
	before := Changes(path)
	time.Sleep(20 * time.Millisecond)
	again, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if Changes(path) != before {
		t.Error("an opening wrote")
	}
	// And it takes a copy's marks.
	tell(t, c, "A", 0, onCopy(edited(4, warm, 0), "c1"))
	editIs(t, c, "ron", bright)
}

// A copy has its own heart and edit, nothing of its photo's, and is said and
// heard with its name; a phone that names none is the photo's.
func TestACopyIsItsOwn(t *testing.T) {
	c, _ := marking(t, 1)
	s, _ := c.Shelf("ron")
	a, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: "A", States: all, Marks: []MarkAction{
		onCopy(act(1, true, 10), "c1"), onCopy(edited(2, warm, 10), "c1"), edited(3, bright, 10),
	}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := []Mark{{hashed(0), "c1", Favorite, json.RawMessage("true")}, {hashed(0), "c1", Edit, json.RawMessage(warm)}, {hashed(0), "", Edit, json.RawMessage(bright)}}
	if !reflect.DeepEqual(a.Said, want) || !reflect.DeepEqual(a.Marks, want) {
		t.Errorf("%+v", a)
	}
	heartIs(t, c, "ron", false)
	editIs(t, c, "ron", bright)
	// An older phone hears hearts alone, the copy's with its name, and its own
	// heart is the photo's.
	a, err = c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: "B", Marks: []MarkAction{act(1, true, 0)}}, time.Now())
	if err != nil || len(a.Marks) != 2 || a.Marks[0].Copy != "c1" || a.Marks[1].Copy != "" {
		t.Errorf("%+v %v", a, err)
	}
	heartIs(t, c, "ron", true)
	// The copy's heart taken off leaves the photo's.
	tell(t, c, "A", 0, onCopy(act(4, false, 0), "c1"))
	heartIs(t, c, "ron", true)
}

// A copy deleted is a tombstone: out of the list, its photo there.
func TestACopyDeletedLeavesItsPhoto(t *testing.T) {
	c, _ := marking(t, 1)
	if _, err := c.db.Exec(`UPDATE library_photos SET taken = 1700000000`); err != nil {
		t.Fatal(err)
	}
	month := func() string {
		_, list, err := c.Month("ron", "2023-11")
		if err != nil {
			t.Fatal(err)
		}
		return string(list)
	}
	tag := func() string {
		l, _, err := c.Listing("ron")
		if err != nil || len(l.Months) != 1 {
			t.Fatalf("%+v %v", l, err)
		}
		return l.Months[0].Tag
	}
	alone, before := month(), tag()
	tell(t, c, "A", 0, onCopy(edited(1, warm, 10), "c1"))
	if got := month(); strings.Count(got, `"copy":"c1"`) != 1 || strings.Count(got, hashed(0)) != 2 || tag() == before {
		t.Errorf("with a copy: %s", got)
	}
	deleted := onCopy(act(2, false, 0), "c1")
	deleted.State = Copy
	s, _ := c.Shelf("ron")
	if _, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: "A", States: all, Marks: []MarkAction{deleted}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := month(); got != alone || tag() != before {
		t.Errorf("deleted: %s", got)
	}
	editIs(t, c, "ron", "")
}

// A date said by a phone wins by the marks' rules and is said back; a date
// given in the window is a mark too, and a phone's earlier one does not undo it.
func TestADateIsAMark(t *testing.T) {
	c, _ := marking(t, 1)
	date := func(n int64, value string, ago int64) MarkAction {
		a := act(n, true, ago)
		a.State, a.Value = Date, json.RawMessage(value)
		return a
	}
	const may = `{"taken":1715000000000,"zone":"+02:00"}`
	s, _ := c.Shelf("ron")
	ask := func(marks ...MarkAction) MarkAnswer {
		a, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: "A", States: all, Marks: marks}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := ask(date(1, may, 5000), date(2, `{"taken":"x"}`, 0), onCopy(date(3, may, 0), "c1"), date(6, `{"taken":1000000000000000,"zone":"+00:00"}`, 0))
	if len(a.Dated) != 1 || a.Dated[0] != (Key{hashed(0), "ron"}) || !slices.Equal(a.Refused, []int64{2, 6}) || len(a.Said) != 1 {
		t.Errorf("%+v", a)
	}
	if when, err := dateOf(may); err != nil || when != time.Date(2024, 5, 6, 14, 53, 20, 0, time.UTC) {
		t.Errorf("%v %v", when, err)
	}
	if err := c.noteDate(Key{hashed(0), "ron"}, time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if a = ask(date(4, `null`, 1000)); len(a.Dated) != 0 || !strings.Contains(string(a.Said[0].Value), `"taken":`) {
		t.Errorf("an earlier one won: %+v", a)
	}
	if a = ask(date(5, `null`, 0)); len(a.Dated) != 1 || string(a.Said[0].Value) != "null" {
		t.Errorf("a later one lost: %+v", a)
	}
}

// When a photo left the Trash is kept, an older program's Restore too.
func TestARestoreIsTimed(t *testing.T) {
	c, path := marking(t, 1)
	k := []Key{{hashed(0), "ron"}}
	if _, err := c.Mark(Trashed, k, true); err != nil {
		t.Fatal(err)
	}
	began := time.Now().UnixMilli()
	olderAt(t, path, copiesSince, `UPDATE library_photos SET trashed = 0`)
	var restored int64
	if err := c.db.QueryRow(`SELECT restored FROM library_photos`).Scan(&restored); err != nil || restored < began {
		t.Errorf("restored %d, before %d, %v", restored, began, err)
	}
}

// A photo filed into a library that has it already takes the copies and the
// date of the one filed, where theirs are the later; a date taken is due.
func TestAPhotoFiledTakesItsCopies(t *testing.T) {
	c, _ := marking(t, 1)
	addPhotos(t, c, "", 1)
	say := func(profile string, marks ...MarkAction) {
		t.Helper()
		s, _ := c.Shelf(profile)
		if _, err := c.Marks(profile, MarkQuestion{Shelf: s.ID, Install: "A" + profile, States: all, Marks: marks}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	date := act(3, true, 100)
	date.State, date.Value = Date, json.RawMessage(`{"taken":1715000000000,"zone":"+02:00"}`)
	say("ron", onCopy(edited(1, warm, 5000), "c1"))
	say("", onCopy(edited(1, bright, 10), "c1"), onCopy(act(2, true, 10), "c2"), date)
	if _, err := c.db.Exec(`UPDATE extra_marks SET due = 0`); err != nil {
		t.Fatal(err)
	}
	if err := c.refile(Key{hashed(0), ""}, "ron", "ron/0", true); err != nil {
		t.Fatal(err)
	}
	rows, err := c.db.Query(`SELECT copy, state, COALESCE(value, 'null'), due FROM extra_marks WHERE profile = 'ron' ORDER BY copy, state`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var copy, state, value string
		var due bool
		rows.Scan(&copy, &state, &value, &due)
		got = append(got, fmt.Sprint(copy, " ", state, " ", value, " ", due))
	}
	want := []string{` date {"taken":1715000000000,"zone":"+02:00"} true`, `c1 edit ` + bright + ` false`, `c2 favorite true false`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%q, want %q", got, want)
	}
}

// Where nothing can be written, a catalog from before copies takes no date
// and no copy's mark, and says so as it does of an edit.
func TestCopiesWhereNothingCanBeWritten(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" || os.Getenv("HOMEWEND_TEST_VFS") != "" {
		t.Skip("a mode takes no one's writing away")
	}
	c, path := marking(t, 1)
	c.Close()
	olderAt(t, path, linksSince, dropExtra)
	dir := filepath.Dir(path)
	os.Chmod(path, 0o444)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, _ := c.Shelf("ron")
	date := act(1, true, 0)
	date.State, date.Value = Date, json.RawMessage(`null`)
	for _, m := range []MarkAction{date, onCopy(act(2, true, 0), "c1")} {
		if _, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: "A", States: all, Marks: []MarkAction{m}}, time.Now()); err != ErrNoMarks {
			t.Errorf("%s: %v", m.State, err)
		}
	}
	if l, _, err := c.Listing("ron"); err != nil {
		t.Errorf("%+v %v", l, err)
	}
}
