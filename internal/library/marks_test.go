// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// hashed is a photo's hash made of n.
func hashed(n int) string { return fmt.Sprintf("%064x", n) }

// marking is a catalog at path with photos 0 to n-1 in ron's library.
func marking(t testing.TB, n int) (c *Catalog, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "catalog.db")
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	addPhotos(t, c, "ron", n)
	return c, path
}

func addPhotos(t testing.TB, c *Catalog, profile string, n int) {
	t.Helper()
	err := c.inTx(func(tx *sql.Tx) error {
		for i := range n {
			if _, err := tx.Exec(`INSERT INTO library_photos (hash, profile, path, name, bytes, source, kind, origin) VALUES (?, ?, ?, ?, 1, '', '', '')`,
				hashed(i), profile, fmt.Sprint(profile, "/", i), fmt.Sprint(i, ".jpg")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// heart is a photo's heart, as it is heard.
func heart(n int) Mark { return Mark{hashed(n), Favorite, json.RawMessage("true")} }

// act is an action of photo 0's heart; ago below -1 is no time.
func act(n int64, on bool, ago int64) MarkAction {
	a := MarkAction{N: n, Hash: hashed(0), State: Favorite, Value: json.RawMessage(fmt.Sprint(on))}
	if ago >= -1 {
		a.Ago = &ago
	}
	return a
}

const noTime = -2

// say says marks to ron's shelf from after 0.
func say(t testing.TB, c *Catalog, install string, marks ...MarkAction) MarkAnswer {
	t.Helper()
	s, err := c.Shelf("ron")
	if err != nil {
		t.Fatal(err)
	}
	a, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: install, Marks: marks}, time.Now())
	if err != nil || a.Other {
		t.Fatalf("%+v %v", a, err)
	}
	return a
}

// heartIs is whether photo 0 of profile has a heart, where the window reads
// it and in the mark alike.
func heartIs(t testing.TB, c *Catalog, profile string, want bool) {
	t.Helper()
	p, err := c.Photo(Key{hashed(0), profile})
	if err != nil {
		t.Fatal(err)
	}
	var mark bool
	if err := c.db.QueryRow(`SELECT value FROM marks WHERE hash = ? AND profile = ?`, hashed(0), profile).Scan(&mark); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if p.Favorite != want || mark != want {
		t.Errorf("%s's heart %v, its mark %v, want %v", profile, p.Favorite, mark, want)
	}
}

// The last action wins, by when it was made; one with no time only adds; one
// said twice, however late, is as once.
func TestTheLastHeartWins(t *testing.T) {
	window := func(on bool, ago int64) func(*testing.T, *Catalog) {
		return func(t *testing.T, c *Catalog) {
			for _, on := range []bool{!on, on} { // from what it was: the same makes no mark
				if _, err := c.Mark(Favorite, []Key{{hashed(0), "ron"}}, on); err != nil {
					t.Fatal(err)
				}
			}
			at := time.Now().UnixMilli() - ago
			if ago == noTime {
				at = 0
			}
			if _, err := c.db.Exec(`UPDATE marks SET at = ?`, at); err != nil {
				t.Fatal(err)
			}
		}
	}
	phone := func(install string, marks ...MarkAction) func(*testing.T, *Catalog) {
		return func(t *testing.T, c *Catalog) { say(t, c, install, marks...) }
	}
	for name, c := range map[string]struct {
		does []func(*testing.T, *Catalog)
		want bool
	}{
		"a first heart":                              {[]func(*testing.T, *Catalog){phone("A", act(1, true, 0))}, true},
		"later than the window's":                    {[]func(*testing.T, *Catalog){window(true, 5000), phone("A", act(1, false, 100))}, false},
		"before the window's":                        {[]func(*testing.T, *Catalog){window(true, 100), phone("A", act(1, false, 5000))}, true},
		"the window's after the phone's":             {[]func(*testing.T, *Catalog){phone("A", act(1, true, 0)), window(false, 0)}, false},
		"between two phones, the later":              {[]func(*testing.T, *Catalog){phone("A", act(1, true, 100)), phone("B", act(1, false, 5000))}, true},
		"no time over none":                          {[]func(*testing.T, *Catalog){phone("A", act(1, true, noTime))}, true},
		"no time over a timed off":                   {[]func(*testing.T, *Catalog){window(false, 5000), phone("A", act(1, true, noTime))}, false},
		"no time and false over a heart":             {[]func(*testing.T, *Catalog){window(true, noTime), phone("A", act(1, false, noTime))}, true},
		"a time over a heart with none":              {[]func(*testing.T, *Catalog){window(true, noTime), phone("A", act(1, false, 1<<50))}, false},
		"a time before everything":                   {[]func(*testing.T, *Catalog){window(true, noTime), phone("A", act(1, false, math.MaxInt64))}, false},
		"no time said after a time":                  {[]func(*testing.T, *Catalog){phone("A", act(2, false, 100)), phone("A", act(3, true, noTime))}, false},
		"no time before a time, in one call":         {[]func(*testing.T, *Catalog){phone("A", act(2, true, 100), act(1, true, noTime))}, true},
		"a time with a lower count than one without": {[]func(*testing.T, *Catalog){phone("A", act(3, true, noTime)), phone("A", act(2, false, 100))}, false},
		"an off where there was none, then no time":  {[]func(*testing.T, *Catalog){phone("A", act(1, false, 100)), phone("B", act(1, true, noTime))}, false},
		"an off where there was none, then an older": {[]func(*testing.T, *Catalog){phone("A", act(1, false, 100)), phone("B", act(1, true, 5000))}, false},
		"two of one call, in order":                  {[]func(*testing.T, *Catalog){phone("A", act(1, true, 100), act(2, false, 100))}, false},
		"said again after the window's":              {[]func(*testing.T, *Catalog){phone("A", act(7, true, 100)), window(false, 50), phone("A", act(7, true, 0))}, false},
		"a lower count of one install":               {[]func(*testing.T, *Catalog){phone("A", act(7, true, 100)), phone("A", act(6, false, 0))}, true},
		"the same count of another install":          {[]func(*testing.T, *Catalog){phone("A", act(7, true, 100)), phone("B", act(7, false, 0))}, false},
		"the greatest count there is":                {[]func(*testing.T, *Catalog){phone("A", act(math.MaxInt64, true, 100)), phone("B", act(1, false, 0))}, false},
		"an action from the future is now's":         {[]func(*testing.T, *Catalog){phone("A", act(1, true, -1)), phone("B", act(1, false, 0))}, false},
		"a window's of tomorrow, then one now":       {[]func(*testing.T, *Catalog){window(true, -86400e3), phone("A", act(1, false, 0))}, false},
		"a window's of tomorrow, then one before":    {[]func(*testing.T, *Catalog){window(true, -86400e3), phone("A", act(1, false, 300))}, false},
		"a window's of tomorrow, then no time":       {[]func(*testing.T, *Catalog){window(false, -86400e3), phone("A", act(1, true, noTime))}, false},
	} {
		t.Run(name, func(t *testing.T) {
			cat, _ := marking(t, 1)
			for _, do := range c.does {
				do(t, cat)
				time.Sleep(2 * time.Millisecond)
			}
			heartIs(t, cat, "ron", c.want)
		})
	}
}

// A heart set here while a phone's older one waited its turn is believed.
func TestAHeartSetWhileAPhoneWaited(t *testing.T) {
	c, _ := marking(t, 1)
	s, err := c.Shelf("ron")
	if err != nil {
		t.Fatal(err)
	}
	then := time.Now()
	time.Sleep(5 * time.Millisecond)
	if _, err := c.Mark(Favorite, []Key{{hashed(0), "ron"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: "A", Marks: []MarkAction{act(1, false, 0)}}, then); err != nil {
		t.Fatal(err)
	}
	heartIs(t, c, "ron", true)
}

// The window's action leaves the phone's count, and a phone's lost one is not
// kept as the last applied.
func TestWhoseCountIsKept(t *testing.T) {
	c, _ := marking(t, 2)
	say(t, c, "A", act(5, true, 100))
	if _, err := c.Mark(Favorite, []Key{{hashed(0), "ron"}}, false); err != nil {
		t.Fatal(err)
	}
	is := func(wantInstall string, wantN int64) {
		t.Helper()
		var install string
		var n int64
		if err := c.db.QueryRow(`SELECT install, n FROM marks WHERE hash = ?`, hashed(0)).Scan(&install, &n); err != nil || install != wantInstall || n != wantN {
			t.Errorf("%s %d %v, want %s %d", install, n, err, wantInstall, wantN)
		}
	}
	is("A", 5)
	say(t, c, "B", act(9, true, 60_000))
	is("A", 5)
	// Another photo's count is its own.
	other := act(1, true, 0)
	other.Hash = hashed(1)
	if a := say(t, c, "A", other); len(a.Said) != 1 || string(a.Said[0].Value) != "true" {
		t.Errorf("%+v", a)
	}
}

// What a question is answered: what the library holds of each hash said, once,
// what it does not have, and nothing of what it does not know.
func TestWhatIsSaidIsAnswered(t *testing.T) {
	c, _ := marking(t, 3)
	keys := []Key{{hashed(1), "ron"}, {hashed(2), "ron"}}
	if _, err := c.Mark(Trashed, keys[:1], true); err != nil {
		t.Fatal(err)
	}
	if err := c.TakeOut(keys[1:], true); err != nil {
		t.Fatal(err)
	}
	of := func(n int, count int64) MarkAction {
		a := act(count, true, 0)
		a.Hash = hashed(n)
		return a
	}
	edit := MarkAction{N: 9, Hash: hashed(0), State: "edit", Value: json.RawMessage(`{"crop":[1,2]}`)}
	gone := edit
	gone.Hash = hashed(77)
	a := say(t, c, "A", of(0, 1), of(1, 2), of(2, 3), of(9, 4), of(0, 5), of(9, 6), edit, gone)
	if want := []Mark{heart(0), heart(1)}; !reflect.DeepEqual(a.Said, want) {
		t.Errorf("said %v, want %v", a.Said, want)
	}
	if want := []string{hashed(2), hashed(9)}; !slices.Equal(a.Unknown, want) {
		t.Errorf("unknown %v, want %v", a.Unknown, want)
	}
	if len(a.Marks) != 2 || a.More {
		t.Errorf("heard %+v", a)
	}
	// Restored it has its heart; brought back, the one it had.
	if _, err := c.Mark(Trashed, keys[:1], false); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(Photo{Hash: hashed(2), Profile: "ron", Path: "ron/2", Name: "2.jpg"}); err != nil {
		t.Fatal(err)
	}
	a = say(t, c, "A", of(1, 2), of(2, 7))
	if want := []Mark{heart(1), heart(2)}; !reflect.DeepEqual(a.Said, want) {
		t.Errorf("said %v, want %v", a.Said, want)
	}
	// An unknown one and a state unknown alone write nothing.
	for _, marks := range [][]MarkAction{{of(9, 8)}, {edit}} {
		s, _ := c.Shelf("ron")
		if a := say(t, c, "A", marks...); a.Cursor != s.Cursor || len(a.Said) != 0 {
			t.Errorf("%+v", a)
		}
	}
}

// Another library's shelf, another catalog's, or a seq not reached: nothing is
// applied.
func TestAnotherShelf(t *testing.T) {
	c, _ := marking(t, 1)
	other, _ := marking(t, 1)
	addPhotos(t, c, "ann", 1)
	shelf := func(c *Catalog, profile string) string {
		s, err := c.Shelf(profile)
		if err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	mine := shelf(c, "ron")
	if mine == shelf(c, "ann") || mine == shelf(other, "ron") || mine != shelf(c, "ron") {
		t.Error("a shelf is a catalog's and a library's")
	}
	for name, q := range map[string]MarkQuestion{
		"ann's":                      {Shelf: shelf(c, "ann"), Marks: []MarkAction{act(1, true, 0)}},
		"another catalog's":          {Shelf: shelf(other, "ron"), Marks: []MarkAction{act(1, true, 0)}},
		"none, with marks":           {Marks: []MarkAction{act(1, true, 0)}},
		"a seq not reached":          {Shelf: mine, After: 1, Marks: []MarkAction{act(1, true, 0)}},
		"another's, a plain pull":    {Shelf: "x"},
		"a pull of a seq not here":   {Shelf: mine, After: math.MaxInt64},
		"none, a seq not reached":    {After: 4},
		"another's, and a big after": {Shelf: "x", After: 4},
	} {
		q.Install = "A"
		a, err := c.Marks("ron", q, time.Now())
		if err != nil || !a.Other || a.ID != mine || a.Cursor != 0 || a.More || len(a.Said)+len(a.Marks)+len(a.Unknown) != 0 || a.Said == nil || a.Marks == nil || a.Unknown == nil {
			t.Errorf("%s: %+v %v", name, a, err)
		}
	}
	if a, err := c.Marks("ron", MarkQuestion{}, time.Now()); err != nil || a.Other || a.ID != mine {
		t.Errorf("a plain pull of no shelf: %+v %v", a, err)
	}
	heartIs(t, c, "ron", false)
	// Ron's heart is not ann's to hear.
	say(t, c, "A", act(1, true, 0))
	heartIs(t, c, "ann", false)
	if a, err := c.Marks("ann", MarkQuestion{}, time.Now()); err != nil || len(a.Marks) != 0 || a.Cursor != 0 {
		t.Errorf("ann hears %+v %v", a, err)
	}
}

// What is not a question of marks is refused whole.
func TestWhatIsNotAQuestionOfMarks(t *testing.T) {
	c, _ := marking(t, 1)
	s, _ := c.Shelf("ron")
	with := func(change func(*MarkAction)) MarkQuestion {
		a := act(2, true, 0)
		change(&a)
		return MarkQuestion{Marks: []MarkAction{act(1, true, 0), a}}
	}
	long := MarkQuestion{Marks: make([]MarkAction, maxMarks+1)}
	for i := range long.Marks {
		long.Marks[i] = act(int64(i), true, 0)
	}
	for name, q := range map[string]MarkQuestion{
		"more than 500":     long,
		"a negative after":  {After: -1},
		"a negative count":  with(func(a *MarkAction) { a.N = -1 }),
		"a short hash":      with(func(a *MarkAction) { a.Hash = "ab" }),
		"an uppercase hash": with(func(a *MarkAction) { a.Hash = strings.Repeat("A", 64) }),
		"no hash":           with(func(a *MarkAction) { a.Hash = "" }),
		"no state":          with(func(a *MarkAction) { a.State = "" }),
		"no value":          with(func(a *MarkAction) { a.Value = nil }),
		"a null":            with(func(a *MarkAction) { a.Value = json.RawMessage("null") }),
		"a number":          with(func(a *MarkAction) { a.Value = json.RawMessage("1") }),
		"a long install":    {Install: strings.Repeat("x", maxInstall+1)},
	} {
		q.Shelf = s.ID
		if q.Install == "" {
			q.Install = "A"
		}
		if _, err := c.Marks("ron", q, time.Now()); !errors.Is(err, ErrNotMarks) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, Marks: []MarkAction{act(1, true, 0)}}, time.Now()); !errors.Is(err, ErrNotMarks) {
		t.Errorf("no install: %v", err)
	}
	heartIs(t, c, "ron", false)
}

// A library of hearts is heard 500 at a time, none missed or twice, one set
// again meanwhile heard at the end.
func TestALibraryIsHeardInPages(t *testing.T) {
	c, _ := marking(t, 1100)
	keys := make([]Key, 1100)
	for i := range keys {
		keys[i] = Key{hashed(i), "ron"}
	}
	if _, err := c.Mark(Favorite, keys, true); err != nil {
		t.Fatal(err)
	}
	if err := c.TakeOut(keys[1099:], false); err != nil { // the last seq is nobody's now
		t.Fatal(err)
	}
	heard := map[string]int{}
	q, calls := MarkQuestion{}, 0
	for {
		a, err := c.Marks("ron", q, time.Now())
		if err != nil || a.Other {
			t.Fatalf("%+v %v", a, err)
		}
		for _, m := range a.Marks {
			heard[m.Hash]++
		}
		if calls++; calls == 1 {
			if len(a.Marks) != 500 || !a.More || a.Cursor != 500 {
				t.Errorf("%d more %v cursor %d", len(a.Marks), a.More, a.Cursor)
			}
			if _, err := c.Mark(Favorite, keys[700:701], false); err != nil {
				t.Fatal(err)
			}
		}
		q.Shelf, q.After = a.ID, a.Cursor
		if !a.More {
			break
		}
	}
	if calls != 3 || len(heard) != 1099 || heard[hashed(700)] != 1 || q.After != 1101 {
		t.Errorf("%d calls, %d heard, cursor %d", calls, len(heard), q.After)
	}
	if differs, err := c.Differs("ron", q.Shelf, q.After, nil); differs || err != nil {
		t.Errorf("%v %v, want it all heard", differs, err)
	}
}

// A seq is never given again, whatever is forgotten, and a photo forgotten
// takes its mark with it.
func TestASeqIsNeverGivenAgain(t *testing.T) {
	c, _ := marking(t, 1)
	k := []Key{{hashed(0), "ron"}}
	for want := int64(1); want < 4; want++ {
		if _, err := c.Mark(Favorite, k, true); err != nil {
			t.Fatal(err)
		}
		if s, err := c.Shelf("ron"); err != nil || s.Cursor != want {
			t.Fatalf("%+v %v, want %d", s, err, want)
		}
		if err := c.TakeOut(k, false); err != nil {
			t.Fatal(err)
		}
		if a, err := c.Marks("ron", MarkQuestion{}, time.Now()); err != nil || len(a.Marks) != 0 || a.Cursor != want {
			t.Errorf("forgotten: %+v %v", a, err)
		}
		addPhotos(t, c, "ron", 1)
		heartIs(t, c, "ron", false)
	}
}

// A plain question asks no one who writes to wait, nor waits; what is said
// again, or is not known, writes nothing.
func TestAQuestionTakesNoLock(t *testing.T) {
	c, path := marking(t, 1)
	say(t, c, "A", act(1, true, 0))
	before := Changes(path)
	time.Sleep(20 * time.Millisecond)
	say(t, c, "A", act(1, true, 0))
	if Changes(path) != before {
		t.Error("said again, it was written")
	}
	other, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Where reading locks too (macOS), no one is asked to do without.
	if os.Getenv("HOMEWEND_TEST_VFS") != "" || runtime.GOOS == "darwin" {
		t.Skip("a reader waits for a writer here")
	}
	began := time.Now()
	edit := MarkAction{N: 1, Hash: hashed(0), State: "edit"}
	s, _ := c.Shelf("ron")
	for _, q := range []MarkQuestion{{}, {Shelf: s.ID, Install: "A", Marks: []MarkAction{edit}}, {Shelf: "x", Install: "A", Marks: []MarkAction{act(2, false, 0)}}} {
		if _, err := c.Marks("ron", q, time.Now()); err != nil {
			t.Error(err)
		}
	}
	if took := time.Since(began); took > time.Second {
		t.Errorf("it waited %v", took)
	}
}

// before is a catalog as a program from before marks left it, with a heart on
// each of ron's first two photos, ann's first, and one kept out.
func before(t *testing.T, version int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	fill := ""
	for i, p := range []string{"ron", "ron", "ron", "ann", "ron"} {
		fill += fmt.Sprintf(`INSERT INTO library_photos (hash, profile, path, name, bytes, source, kind, origin, favorite, out) VALUES ('%s', '%s', '%d', 'a.jpg', 1, '', '', '', %t, %t);`, hashed(i%3), p+strings.Repeat("x", i/4), i, i != 2, i == 1)
	}
	olderAt(t, path, version, schema+fill)
	return path
}

// The hearts of an older catalog are marks with no time, once, with no copy
// kept; one that looks for links still does, and no opening after the first
// writes.
func TestHeartsBecomeMarks(t *testing.T) {
	for version, want := range map[int]int{librariesSince: editsSince, linksBeforeMarks: linksSince} {
		path := before(t, version)
		for name, open := range map[string]func(string) (*Catalog, error){"to look": OpenCatalogReadOnly, "to write": OpenCatalog} {
			c, err := open(path)
			if err != nil {
				t.Fatal(name, err)
			}
			a, err := c.Marks("ron", MarkQuestion{}, time.Now())
			if err != nil || len(a.Marks) != 2 || a.Cursor != 2 || string(a.Marks[0].Value) != "true" {
				t.Errorf("%s: %+v %v", name, a, err)
			}
			if a, err := c.Marks("ann", MarkQuestion{}, time.Now()); err != nil || len(a.Marks) != 1 || a.Cursor != 1 {
				t.Errorf("%s: ann's %+v %v", name, a, err)
			}
			var now, timed int
			if err := c.db.QueryRow(`PRAGMA user_version`).Scan(&now); err != nil || now != want {
				t.Errorf("%s: version %d %v, want %d", name, now, err, want)
			}
			if err := c.db.QueryRow(`SELECT COUNT(*) FROM marks WHERE at <> 0 OR install <> '' OR n <> 0`).Scan(&timed); err != nil || timed != 0 {
				t.Errorf("%s: %d have a time, %v", name, timed, err)
			}
			c.Close()
		}
		if left, _ := filepath.Glob(path + "?*"); len(left) != 0 {
			t.Errorf("beside it: %v", left)
		}
		was := Changes(path)
		time.Sleep(20 * time.Millisecond)
		c, err := OpenCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		s, err := c.Shelf("ron")
		if c.Close(); err != nil || Changes(path) != was {
			t.Errorf("an opening wrote, %v", err)
		}
		// A program from before marks, finding links meanwhile, says its own
		// version: what is kept stays as it is.
		olderAt(t, path, linksBeforeMarks, "")
		if c, err = OpenCatalog(path); err != nil {
			t.Fatal(err)
		}
		again, err := c.Shelf("ron")
		if c.Close(); err != nil || again != s {
			t.Errorf("%+v %v, want %+v", again, err, s)
		}
		turns := 0
		err = FindLinks(t.TempDir(), path, nil, nil, func() (func(), error) { turns++; return func() {}, nil })
		if err != nil || turns != 0 {
			t.Errorf("links looked for %d times again, %v", turns, err)
		}
	}
	// Those not found yet are looked for, once.
	path := before(t, librariesSince)
	turns := 0
	for range 2 {
		if err := FindLinks(t.TempDir(), path, nil, func(string) string { return "" }, func() (func(), error) { turns++; return func() {}, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if turns != 1 {
		t.Errorf("links looked for %d times, want once", turns)
	}
}

// A program from before marks sets a heart, or forgets a photo: it is noted,
// and its mark is forgotten.
func TestAProgramFromBeforeMarks(t *testing.T) {
	c, path := marking(t, 2)
	olderAt(t, path, linksSince, `UPDATE library_photos SET favorite = 1 WHERE hash = '`+hashed(0)+`' AND profile = 'ron' AND out = 0`)
	heartIs(t, c, "ron", true)
	if a := say(t, c, "A", act(1, false, 60_000)); string(a.Said[0].Value) != "true" {
		t.Error("a heart set now was taken off by one a minute old")
	}
	// Its merge writes the heart as it was: no mark is made of none.
	olderAt(t, path, linksSince, `UPDATE library_photos SET favorite = max(favorite, 0) WHERE hash = '`+hashed(1)+`'`)
	if a, err := c.Marks("ron", MarkQuestion{}, time.Now()); err != nil || len(a.Marks) != 1 {
		t.Errorf("%+v %v, want no mark of a photo given none", a, err)
	}
	// Its relayout files a photo of no library in ron's.
	addPhotos(t, c, "", 3)
	old := Key{hashed(2), ""}
	if _, err := c.Mark(Favorite, []Key{old}, true); err != nil {
		t.Fatal(err)
	}
	s, _ := c.Shelf("ron")
	olderAt(t, path, linksSince, `UPDATE OR IGNORE library_photos SET profile = 'ron', path = 'ron/2' WHERE hash = '`+hashed(2)+`' AND profile = ''`)
	if a, err := c.Marks("ron", MarkQuestion{Shelf: s.ID, After: s.Cursor}, time.Now()); err != nil || len(a.Marks) != 1 || a.Marks[0].Hash != hashed(2) || string(a.Marks[0].Value) != "true" {
		t.Errorf("filed: %+v %v", a, err)
	}
	if a, err := c.Marks("", MarkQuestion{}, time.Now()); err != nil || len(a.Marks) != 0 {
		t.Errorf("left behind: %+v %v", a, err)
	}
	olderAt(t, path, linksSince, `DELETE FROM library_photos WHERE hash = '`+hashed(0)+`'`)
	if a, err := c.Marks("ron", MarkQuestion{}, time.Now()); err != nil || len(a.Marks) != 1 {
		t.Errorf("%+v %v", a, err)
	}
}

// A library that cannot be written, from before marks, is shown and looked
// at; it has none.
func TestHeartsWhereNothingCanBeWritten(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" || os.Getenv("HOMEWEND_TEST_VFS") != "" {
		t.Skip("a mode takes no one's writing away")
	}
	path := before(t, linksBeforeMarks)
	dir := filepath.Dir(path)
	os.Chmod(path, 0o444)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	for name, open := range map[string]func(string) (*Catalog, error){"to write": OpenCatalog, "to look": OpenCatalogReadOnly} {
		c, err := open(path)
		if err != nil {
			t.Fatal(name, err)
		}
		if n, err := c.Count(Filter{}); err != nil || n != 4 {
			t.Errorf("%s: %d %v", name, n, err)
		}
		if _, err := c.Shelf("ron"); !errors.Is(err, ErrNoMarks) {
			t.Errorf("%s: %v, want ErrNoMarks", name, err)
		}
		c.Close()
	}
	// One with marks takes no more, and says what it has.
	os.Chmod(path, 0o644)
	os.Chmod(dir, 0o755)
	c, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	os.Chmod(path, 0o444)
	os.Chmod(dir, 0o555)
	if c, err = OpenCatalog(path); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a, err := c.Marks("ron", MarkQuestion{}, time.Now())
	if err != nil || len(a.Marks) != 2 {
		t.Errorf("%+v %v", a, err)
	}
	if _, err := c.Marks("ron", MarkQuestion{Shelf: a.ID, Install: "A", Marks: []MarkAction{act(1, false, 0)}}, time.Now()); !errors.Is(err, ErrNoMarks) {
		t.Errorf("said: %v, want ErrNoMarks", err)
	}
}

// Two processes give an older catalog its marks at once, then both set hearts
// on it: one gives them, each heart has a seq of its own.
func TestTwoProcessesMark(t *testing.T) {
	const each = 100
	work := func(path, profile string) error {
		for i := range each {
			c, err := OpenCatalog(path)
			if err != nil {
				return err
			}
			_, err = c.Mark(Favorite, []Key{{hashed(i), "ann"}}, true)
			if err == nil {
				a := MarkAction{N: int64(i), Hash: hashed(i), State: Favorite, Value: json.RawMessage("true"), Ago: new(int64)}
				e := a
				e.State, e.Value = Edit, json.RawMessage(bright)
				var s Shelf
				if s, err = c.Shelf("ron"); err == nil {
					_, err = c.Marks("ron", MarkQuestion{Shelf: s.ID, Install: profile, States: both, Marks: []MarkAction{a, e}}, time.Now())
				}
			}
			c.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	if path := os.Getenv("HOMEWEND_TEST_MARKS"); path != "" {
		if err := work(path, "B"); err != nil {
			t.Fatal(err)
		}
		return
	}
	c, made := marking(t, each)
	addPhotos(t, c, "ann", each)
	if _, err := c.db.Exec(`DROP TABLE catalog_id; DROP TABLE marks; DROP TABLE mark_seqs; DROP TRIGGER mark_noted; DROP TRIGGER mark_refiled; DROP TRIGGER mark_forgotten; ` + dropEdits + ` PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	c.Close()
	other := exec.Command(os.Args[0], "-test.run=^TestTwoProcessesMark$")
	other.Env = append(os.Environ(), "HOMEWEND_TEST_MARKS="+made)
	var said bytes.Buffer
	other.Stdout, other.Stderr = &said, &said
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	if err := work(made, "A"); err != nil {
		t.Error(err)
	}
	if err := other.Wait(); err != nil {
		t.Errorf("the other process: %v\n%s", err, said.String())
	}
	c, err := OpenCatalog(made)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var whole string
	var ids, rows, seqs, apart int
	if err := c.db.QueryRow(`PRAGMA integrity_check`).Scan(&whole); err != nil || whole != "ok" {
		t.Errorf("integrity %q, %v", whole, err)
	}
	err = c.db.QueryRow(`SELECT (SELECT COUNT(*) FROM catalog_id), COUNT(*), COUNT(DISTINCT m.profile || m.seq), SUM(m.value <> p.favorite)
		FROM marks m JOIN library_photos p ON p.hash = m.hash AND p.profile = m.profile`).Scan(&ids, &rows, &seqs, &apart)
	if err != nil || ids != 1 || rows != 2*each || seqs != 2*each || apart != 0 {
		t.Errorf("%d ids, %d marks, %d seqs, %d not their photo's, %v", ids, rows, seqs, apart, err)
	}
	if err := c.db.QueryRow(`SELECT COUNT(DISTINCT seq) FROM (SELECT seq FROM edits UNION ALL SELECT seq FROM marks WHERE profile = 'ron')`).Scan(&seqs); err != nil || seqs != 2*each {
		t.Errorf("%d seqs of ron's hearts and edits, %v", seqs, err)
	}
	s, err := c.Shelf("ann")
	if err != nil || s.Cursor != 2*each {
		t.Errorf("%+v %v, want every heart counted", s, err)
	}
}

// A photo of no library filed in one takes its mark there, with a seq of that
// library; one that library has already, the last heart set on the two.
func TestAPhotoFiledTakesItsMark(t *testing.T) {
	for name, c := range map[string]struct {
		its, there string // the hearts' SQL of the one filed and of the one there
		want       bool
		at         int64
	}{
		"new there":             {"1, 100", "", true, 100},
		"no heart there":        {"1, 100", "0, -1", true, 100},
		"no time, none there":   {"1, 0", "0, -1", true, 0},
		"taken off there since": {"1, 100", "0, 200", false, 200},
		"taken off since":       {"0, 300", "1, 200", false, 300},
		"no time, off there":    {"1, 0", "0, 200", false, 200},
		"a heart there alone":   {"0, -1", "1, 200", true, 200},
	} {
		t.Run(name, func(t *testing.T) {
			cat, _ := marking(t, 0)
			for profile, heart := range map[string]string{"": c.its, "ron": c.there} {
				if heart == "" {
					continue
				}
				addPhotos(t, cat, profile, 1)
				var on, at int64
				fmt.Sscanf(heart, "%d, %d", &on, &at)
				if at < 0 {
					continue
				}
				for _, on := range []bool{on == 0, on == 1} {
					if _, err := cat.Mark(Favorite, []Key{{hashed(0), profile}}, on); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := cat.db.Exec(`UPDATE marks SET at = ?, install = 'A', n = ? WHERE profile = ?`, at, at, profile); err != nil {
					t.Fatal(err)
				}
			}
			if err := cat.refile(Key{hashed(0), ""}, "ron", "ron/0", true); err != nil {
				t.Fatal(err)
			}
			heartIs(t, cat, "ron", c.want)
			var at, seq, n, marks int64
			err := cat.db.QueryRow(`SELECT at, seq, n, (SELECT COUNT(*) FROM marks) FROM marks WHERE profile = 'ron'`).Scan(&at, &seq, &n, &marks)
			s, _ := cat.Shelf("ron")
			if err != nil || at != c.at || n != at || seq != s.Cursor || marks != 1 {
				t.Errorf("at %d, seq %d of %d, n %d, %d marks, %v", at, seq, s.Cursor, n, marks, err)
			}
		})
	}
}

// A photo put in a second library has no heart there, whatever the first's
// has; a new date leaves it.
func TestAPhotoSharedHasNoMark(t *testing.T) {
	c, _ := marking(t, 1)
	say(t, c, "A", act(1, true, 0))
	if err := c.inTx(func(tx *sql.Tx) error {
		_, err := share(tx, Key{hashed(0), "ron"}, "ann", "ann/0", nil, func(string) bool { return false })
		return err
	}); err != nil {
		t.Fatal(err)
	}
	heartIs(t, c, "ann", false)
	if err := c.setPath(Key{hashed(0), "ron"}, "ron/b", "b"); err != nil {
		t.Fatal(err)
	}
	if err := c.setDate(Key{hashed(0), "ron"}, Capture{When: time.Unix(1, 0), Source: FromGiven}); err != nil {
		t.Fatal(err)
	}
	heartIs(t, c, "ron", true)
}
