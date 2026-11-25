// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
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

	"github.com/ruvido/homewend/internal/edit"
)

const (
	bright = `{"adjust":{"Exposure":12},"tools":2}`
	warm   = `{"adjust":{"Warmth":-8, "Grain":3}, "tools":2,"more":"<&>"}`
	newer  = `{"tools":3,"waves":[0.10,{"a":null}]}`
	upside = `{"adjust":{},"tools":2,"crop":{"turns":0,"flip":false,"angle":0,"x":0,"y":0,"w":1,"h":1,"shape":"Free"}}`
	// dropEdits makes a catalog one from before edits.
	dropEdits = `DROP TABLE edits; DROP TRIGGER edit_refiled; DROP TRIGGER edit_forgotten;`
)

var both = []State{Favorite, Edit}

// edited is an action of photo 0's edit; ago below -1 is no time.
func edited(n int64, value string, ago int64) MarkAction {
	a := act(n, true, ago)
	a.State, a.Value = Edit, json.RawMessage(value)
	return a
}

// tell says marks to ron's shelf, of both states, from after.
func tell(t testing.TB, c *Catalog, install string, after int64, marks ...MarkAction) MarkAnswer {
	t.Helper()
	s, err := c.Shelf("ron")
	if err != nil {
		t.Fatal(err)
	}
	a, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, After: after, Install: install, States: both, Marks: marks}, time.Now())
	if err != nil || a.Other {
		t.Fatalf("%+v %v", a, err)
	}
	return a
}

// editIs is photo 0's edit in the library of profile, "" being none.
func editIs(t testing.TB, c *Catalog, profile, want string) {
	t.Helper()
	for _, only := range [][]Key{nil, {{hashed(0), profile}}} {
		edits, err := c.Edits(only)
		if got := edits[Key{hashed(0), profile}]; err != nil || got != want {
			t.Errorf("%s's edit %q %v, want %q", profile, got, err, want)
		}
	}
}

// The last edit wins whole, as a heart; one with no time only fills.
func TestTheLastEditWins(t *testing.T) {
	const removed = `{"adjust":{"Exposure":0}, "tools":2}`
	phone := func(install string, marks ...MarkAction) func(*testing.T, *Catalog) {
		return func(t *testing.T, c *Catalog) { tell(t, c, install, 0, marks...) }
	}
	for name, c := range map[string]struct {
		does []func(*testing.T, *Catalog)
		want string
	}{
		"a first edit":                      {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 0))}, bright},
		"the later of two phones":           {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("B", edited(1, warm, 5000))}, bright},
		"the later, said last":              {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 5000)), phone("B", edited(1, warm, 100))}, warm},
		"removed":                           {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("A", edited(2, `null`, 50))}, ""},
		"removed, as nothing left":          {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("A", edited(2, removed, 50))}, ""},
		"removed before it was set":         {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("B", edited(2, `null`, 5000))}, bright},
		"a crop of nothing":                 {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("A", edited(2, upside, 50))}, upside},
		"no time over none":                 {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, noTime))}, bright},
		"no time over no time":              {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, noTime)), phone("B", edited(1, warm, noTime))}, bright},
		"no time over a crop of nothing":    {[]func(*testing.T, *Catalog){phone("A", edited(1, upside, noTime)), phone("B", edited(1, warm, noTime))}, upside},
		"no time over a timed":              {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("B", edited(1, warm, noTime))}, bright},
		"no time over a removed":            {[]func(*testing.T, *Catalog){phone("A", edited(1, `null`, 100)), phone("B", edited(1, warm, noTime))}, ""},
		"no time and null":                  {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, noTime)), phone("B", edited(1, `null`, noTime))}, bright},
		"no time and nothing left":          {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, noTime)), phone("B", edited(1, removed, noTime))}, bright},
		"a time before everything":          {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, noTime)), phone("A", edited(2, `null`, 1<<50))}, ""},
		"no time after a time, in one call": {[]func(*testing.T, *Catalog){phone("A", edited(2, bright, 100), edited(1, warm, noTime))}, bright},
		"said again after another's":        {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("A", edited(2, warm, 50)), phone("A", edited(1, bright, 0))}, warm},
		"one that waited, with a low count": {[]func(*testing.T, *Catalog){phone("A", act(9, true, 100)), phone("A", edited(1, bright, 50))}, bright},
		"a newer one, not known here":       {[]func(*testing.T, *Catalog){phone("A", edited(1, bright, 100)), phone("A", edited(2, newer, 0))}, newer},
	} {
		t.Run(name, func(t *testing.T) {
			cat, _ := marking(t, 1)
			for _, do := range c.does {
				do(t, cat)
				time.Sleep(2 * time.Millisecond)
			}
			editIs(t, cat, "ron", c.want)
			// And as it is said and heard.
			want := c.want
			if want == "" {
				want = "null"
			}
			a := tell(t, cat, "Z", 0, edited(1, `null`, noTime))
			if len(a.Said) != 1 || string(a.Said[0].Value) != want || a.Said[0].State != Edit || string(a.Marks[len(a.Marks)-1].Value) != want {
				t.Errorf("%s", a.Said)
			}
		})
	}
}

// A heart and an edit of one photo are two things: each is answered for, the
// seqs are one count, and an unknown photo is said once.
func TestHeartsAndEditsAreHeard(t *testing.T) {
	c, _ := marking(t, 2)
	gone := edited(3, bright, 0)
	gone.Hash = hashed(9)
	goneHeart := act(4, true, 0)
	goneHeart.Hash = hashed(9)
	a := tell(t, c, "A", 0, edited(1, warm, 0), act(2, true, 0), gone, goneHeart)
	e := Mark{hashed(0), Edit, json.RawMessage(warm)}
	if want := []Mark{e, heart(0)}; !reflect.DeepEqual(a.Said, want) || !reflect.DeepEqual(a.Marks, want) || a.Cursor != 2 || !slices.Equal(a.Unknown, []string{hashed(9)}) {
		t.Errorf("%+v", a)
	}
	if a := tell(t, c, "A", 1); len(a.Marks) != 1 || !reflect.DeepEqual(a.Marks[0], heart(0)) {
		t.Errorf("%+v", a)
	}
	editIs(t, c, "ron", warm)
	heartIs(t, c, "ron", true)
	// Those no longer there are asked of none.
	if edits, err := c.Edits([]Key{}); err != nil || len(edits) != 0 {
		t.Errorf("%v %v", edits, err)
	}
}

// What is not an edit is refused by its count, and what is said beside it is
// applied.
func TestARefusedEditHoldsNothing(t *testing.T) {
	c, _ := marking(t, 2)
	long := `{"adjust":{},"tools":2,"x":"` + strings.Repeat("y", edit.MaxSize) + `"}`
	bad := []string{long, `[]`, `"x"`, `7`, `true`, `{}`, `{"adjust":{}}`, `{"adjust":{},"tools":1}`, `{"adjust":{},"tools":"2"}`, `{"tools":2}`,
		`{"adjust":{"Exposure":1.5},"tools":2}`, `{"adjust":{},"tools":2,"crop":{}}`, strings.Replace(upside, `"w":1`, `"w":2`, 1), "{\"adjust\":{},\"tools\":2,\"x\":\"\xff\"}", ``}
	var marks []MarkAction
	var want []int64
	for i, value := range bad {
		marks = append(marks, edited(int64(i+1), value, 0))
		want = append(want, int64(i+1))
	}
	good := edited(50, bright, 0)
	good.Hash = hashed(1)
	a := tell(t, c, "A", 0, append(marks, good, act(51, true, 0))...)
	if !slices.Equal(a.Refused, want) || len(a.Said) != 2 || a.Said[0].Hash != hashed(1) || len(a.Unknown) != 0 || a.Cursor != 2 {
		t.Errorf("%+v", a)
	}
	editIs(t, c, "ron", "")
	// Alone it writes nothing, and another shelf's refuses nothing.
	if a := tell(t, c, "A", 2, marks[1]); len(a.Refused) != 1 || a.Cursor != 2 || len(a.Said) != 0 {
		t.Errorf("%+v", a)
	}
	other, err := c.Marks("ron", MarkQuestion{Shelf: "x", Install: "A", States: both, Marks: []MarkAction{marks[1], good}}, time.Now())
	if err != nil || !other.Other || other.Refused == nil || len(other.Refused)+len(other.Said)+len(other.Marks) != 0 {
		t.Errorf("%+v %v", other, err)
	}
}

// A phone that asks of hearts alone neither says, hears nor is woken by an
// edit; its cursor goes past them.
func TestAPhoneFromBeforeEdits(t *testing.T) {
	c, _ := marking(t, 1)
	s, err := c.Shelf("ron")
	if err != nil {
		t.Fatal(err)
	}
	tell(t, c, "A", 0, edited(1, bright, 0))
	differs := func(after int64, want bool, states ...State) {
		t.Helper()
		if got, err := c.Differs("ron", s.ID, after, states); err != nil || got != want {
			t.Errorf("after %d, %v: %v %v", after, states, got, err)
		}
	}
	differs(0, false)
	differs(0, false, Favorite, "x")
	differs(0, true, both...)
	differs(1, false, both...)
	differs(2, true)
	q := MarkQuestion{Shelf: s.ID, Install: "B", Marks: []MarkAction{edited(1, warm, 0), edited(2, `7`, 0)}}
	for _, states := range [][]State{nil, {}, {Favorite}, {"x"}} {
		q.States = states
		if a, err := c.Marks("ron", q, time.Now()); err != nil || len(a.Said)+len(a.Marks)+len(a.Unknown)+len(a.Refused) != 0 || a.Cursor != 1 {
			t.Errorf("%v: %+v %v", states, a, err)
		}
	}
	editIs(t, c, "ron", bright)
	tell(t, c, "A", 0, act(2, true, 0))
	differs(0, true)
	differs(1, true)
	differs(1, false, Edit)
	if a := say(t, c, "B"); len(a.Marks) != 1 || !reflect.DeepEqual(a.Marks[0], heart(0)) || a.Cursor != 2 {
		t.Errorf("%+v", a)
	}
}

// Edits are heard 256 KB at a time, one at least, none missed.
func TestEditsAreHeardInPages(t *testing.T) {
	c, _ := marking(t, 20)
	big := func(n int) string {
		head := `{"tools":3,"x":"`
		return head + strings.Repeat("y", n-len(head)-2) + `"}`
	}
	// Four are as much as a page takes, one less than five.
	fits := maxPage/4 - markBytes - 64 - len(Edit)
	for i := range 20 {
		a := edited(int64(i+1), big(fits), 0)
		a.Hash = hashed(i)
		tell(t, c, "A", 0, a)
	}
	var sizes []int
	q := MarkQuestion{States: both}
	heard := map[string]bool{}
	for {
		a, err := c.Marks("ron", q, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		all, _ := json.Marshal(a.Marks)
		if len(all) > maxPage+2 {
			t.Errorf("a page of %d bytes", len(all))
		}
		sizes = append(sizes, len(a.Marks))
		for _, m := range a.Marks {
			heard[m.Hash] = true
		}
		q.Shelf, q.After = a.ID, a.Cursor
		if !a.More {
			break
		}
	}
	if !slices.Equal(sizes, []int{4, 4, 4, 4, 4}) || len(heard) != 20 || q.After != 20 {
		t.Errorf("%v, %d heard, cursor %d", sizes, len(heard), q.After)
	}
}

// An edit is its library's, as its photo is: it stays in the Trash and kept
// out, unknown to a phone then; forgotten, it goes, its seq never another's.
func TestAnEditGoesWithItsPhoto(t *testing.T) {
	c, _ := marking(t, 1)
	addPhotos(t, c, "ann", 1)
	k := []Key{{hashed(0), "ron"}}
	tell(t, c, "A", 0, edited(1, bright, 0))
	editIs(t, c, "ann", "")
	if a, err := c.Marks("ann", MarkQuestion{States: both}, time.Now()); err != nil || len(a.Marks) != 0 || a.Cursor != 0 {
		t.Errorf("ann hears %+v %v", a, err)
	}
	if _, err := c.Mark(Trashed, k, true); err != nil {
		t.Fatal(err)
	}
	if a := tell(t, c, "A", 0, edited(2, warm, 0)); len(a.Said) != 1 || string(a.Said[0].Value) != warm {
		t.Errorf("in the Trash: %+v", a)
	}
	if _, err := c.Mark(Trashed, k, false); err != nil {
		t.Fatal(err)
	}
	editIs(t, c, "ron", warm)
	if err := c.TakeOut(k, true); err != nil {
		t.Fatal(err)
	}
	if a := tell(t, c, "A", 0, edited(3, bright, 0)); len(a.Said) != 0 || len(a.Unknown) != 1 {
		t.Errorf("kept out: %+v", a)
	}
	if err := c.Put(Photo{Hash: hashed(0), Profile: "ron", Path: "ron/0", Name: "0.jpg"}); err != nil {
		t.Fatal(err)
	}
	editIs(t, c, "ron", warm)
	// A second library's is another; a new path and date leave it.
	if err := c.inTx(func(tx *sql.Tx) error {
		_, err := share(tx, k[0], "sam", "sam/0", nil, func(string) bool { return false })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	editIs(t, c, "sam", "")
	if err := c.setPath(k[0], "ron/b", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.setDate(k[0], Capture{When: time.Unix(1, 0), Source: FromGiven}); err != nil {
		t.Fatal(err)
	}
	editIs(t, c, "ron", warm)
	if err := c.TakeOut(k, false); err != nil {
		t.Fatal(err)
	}
	addPhotos(t, c, "ron", 1)
	editIs(t, c, "ron", "")
	if a := tell(t, c, "A", 0); len(a.Marks) != 0 || a.Cursor != 2 {
		t.Errorf("forgotten: %+v", a)
	}
	if a := tell(t, c, "A", 0, edited(4, bright, 0)); a.Cursor != 3 {
		t.Errorf("%+v", a)
	}
}

// A photo of no library filed in one takes its edit there; one that library
// has already, the last of the two.
func TestAPhotoFiledTakesItsEdit(t *testing.T) {
	set := func(t *testing.T, c *Catalog, profile, value string, at int64) {
		s, err := c.Shelf(profile)
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Marks(profile, MarkQuestion{Shelf: s.ID, Install: "A", States: both, Marks: []MarkAction{edited(at, value, 0)}}, time.Now())
		if err == nil {
			_, err = c.db.Exec(`UPDATE edits SET at = ? WHERE profile = ?`, at, profile)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		there string
		at    int64
		want  string
	}{"new there": {"", 0, bright}, "none there": {"none", 0, bright}, "an older there": {warm, 50, bright}, "a later there": {warm, 200, warm}, "removed there since": {"null", 200, ""}} {
		t.Run(name, func(t *testing.T) {
			cat, _ := marking(t, 0)
			addPhotos(t, cat, "", 1)
			set(t, cat, "", bright, 100)
			if c.there != "" {
				addPhotos(t, cat, "ron", 1)
			}
			if c.at != 0 {
				set(t, cat, "ron", c.there, c.at)
			}
			if err := cat.refile(Key{hashed(0), ""}, "ron", "ron/0", true); err != nil {
				t.Fatal(err)
			}
			editIs(t, cat, "ron", c.want)
			var seq, rows int64
			s, _ := cat.Shelf("ron")
			if err := cat.db.QueryRow(`SELECT seq, (SELECT COUNT(*) FROM edits) FROM edits WHERE profile = 'ron'`).Scan(&seq, &rows); err != nil || seq != s.Cursor || seq == 0 || rows != 1 {
				t.Errorf("seq %d of %d, %d rows, %v", seq, s.Cursor, rows, err)
			}
		})
	}
}

// A program from before edits refiles a photo, or forgets it: its edit too. It
// reads the hearts as ever, and its version going back loses none.
func TestAProgramFromBeforeEdits(t *testing.T) {
	c, path := marking(t, 2)
	tell(t, c, "A", 0, edited(1, bright, 0))
	if _, err := c.db.Exec(`UPDATE edits SET profile = ''; UPDATE library_photos SET profile = '' WHERE hash = ?`, hashed(0)); err != nil {
		t.Fatal(err)
	}
	s, _ := c.Shelf("ron")
	olderAt(t, path, linksBeforeEdits, `UPDATE OR IGNORE library_photos SET profile = 'ron', path = 'ron/x' WHERE profile = ''`)
	editIs(t, c, "ron", bright)
	if a := tell(t, c, "A", s.Cursor); len(a.Marks) != 1 || a.Cursor != s.Cursor+1 {
		t.Errorf("filed: %+v", a)
	}
	again, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	editIs(t, again, "ron", bright)
	var version int
	if err := again.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != linksSince {
		t.Errorf("version %d %v", version, err)
	}
	var numbers bool
	if err := c.db.QueryRow(`SELECT NOT EXISTS (SELECT 1 FROM marks WHERE typeof(value) <> 'integer')`).Scan(&numbers); err != nil || !numbers {
		t.Errorf("an older program could not read the marks, %v", err)
	}
	olderAt(t, path, linksBeforeEdits, `DELETE FROM library_photos WHERE hash = '`+hashed(0)+`'`)
	editIs(t, c, "ron", "")
}

// An older catalog is given its edits' table once, its hearts and shelf as
// they were, and still looks for the links it had to.
func TestACatalogFromBeforeEdits(t *testing.T) {
	for version, want := range map[int]int{marksSince: editsSince, linksBeforeEdits: linksSince} {
		c, path := marking(t, 1)
		say(t, c, "A", act(1, true, 0))
		was, _ := c.Shelf("ron")
		c.Close()
		olderAt(t, path, version, dropEdits)
		for name, open := range map[string]func(string) (*Catalog, error){"to look": OpenCatalogReadOnly, "to write": OpenCatalog} {
			c, err := open(path)
			if err != nil {
				t.Fatal(name, err)
			}
			now, err := c.Shelf("ron")
			if err != nil || now != was {
				t.Errorf("%s: %+v %v, want %+v", name, now, err, was)
			}
			heartIs(t, c, "ron", true)
			editIs(t, c, "ron", "")
			if err := c.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != want {
				t.Errorf("%s: version %d %v, want %d", name, version, err, want)
			}
			c.Close()
		}
		if left, _ := filepath.Glob(path + "?*"); len(left) != 0 {
			t.Errorf("beside it: %v", left)
		}
		before := Changes(path)
		time.Sleep(20 * time.Millisecond)
		c, err := OpenCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		if Changes(path) != before {
			t.Error("an opening wrote")
		}
		for i, looked := range []int{want, linksSince} {
			turns := 0
			if err := FindLinks(t.TempDir(), path, nil, func(string) string { return "" }, func() (func(), error) { turns++; return func() {}, nil }); err != nil {
				t.Fatal(err)
			}
			if turns != linksSince-looked {
				t.Errorf("links looked for %d times, look %d of %d", turns, i, want)
			}
		}
	}
}

// Where nothing can be written, a catalog from before edits says its hearts to
// a phone that asks for both, and has no edits.
func TestEditsWhereNothingCanBeWritten(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" || os.Getenv("HOMEWEND_TEST_VFS") != "" {
		t.Skip("a mode takes no one's writing away")
	}
	c, path := marking(t, 1)
	say(t, c, "A", act(1, true, 0))
	c.Close()
	olderAt(t, path, linksBeforeEdits, dropEdits)
	dir := filepath.Dir(path)
	os.Chmod(path, 0o444)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	editIs(t, c, "ron", "")
	if a := tell(t, c, "A", 0); len(a.Marks) != 1 || !reflect.DeepEqual(a.Marks[0], heart(0)) {
		t.Errorf("%+v", a)
	}
	s, _ := c.Shelf("ron")
	if differs, err := c.Differs("ron", s.ID, 0, both); err != nil || !differs {
		t.Errorf("%v %v", differs, err)
	}
	if _, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: "A", States: both, Marks: []MarkAction{edited(1, bright, 0)}}, time.Now()); err != ErrNoMarks {
		t.Error(fmt.Sprint(err))
	}
}
