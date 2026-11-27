// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/edit"
)

const someBounds = "1.2744 1.2720 1.6659 2.0183 2.1397 2.5716"

// A catalog of version 8 is given the negatives' table once, every row as
// it was; one stopped halfway is as it was, and done again.
func TestACatalogFromBeforeNegatives(t *testing.T) {
	c, path := marking(t, 3)
	tell(t, c, "A", 0, act(1, true, 100), edited(2, bright, 50))
	was := dumped(t, c)
	c.Close()
	const before = `DROP TRIGGER negative_forgotten; DROP TABLE negatives`
	// Killed halfway: a name taken makes the step fail after its first statement.
	olderAt(t, path, copiesSince, before+`; CREATE TRIGGER negative_forgotten AFTER DELETE ON library_photos BEGIN SELECT 1; END`)
	if c, err := OpenCatalog(path); err == nil {
		c.Close()
		t.Fatal("it opened")
	}
	ro, err := OpenCatalogReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if there, _ := hasTable(ro.db, "negatives"); there || dumped(t, ro) != was {
		t.Errorf("halfway: table %v", there)
	}
	// Read where the table is not: shown as they came.
	if photos, err := ro.Page(Filter{}); err != nil || len(photos) != 3 || photos[0].Negative != "" {
		t.Errorf("page %v %v", photos, err)
	}
	ro.Close()
	olderAt(t, path, copiesSince, `DROP TRIGGER negative_forgotten`)
	if c, err = OpenCatalog(path); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var version int
	if c.db.QueryRow(`PRAGMA user_version`).Scan(&version); version != negativesSince {
		t.Errorf("version %d", version)
	}
	if now := dumped(t, c); now != was {
		t.Errorf("rows changed:\n%s\nwant\n%s", now, was)
	}
	// Once: the next opening writes nothing.
	changes := Changes(path)
	time.Sleep(20 * time.Millisecond)
	again, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	again.Close()
	if Changes(path) != changes {
		t.Error("an opening wrote")
	}
}

// A negative printed is kept, listed, named to the phone, and back as it
// came; a photo deleted for good takes its bounds with it.
func TestNegatives(t *testing.T) {
	c, _ := marking(t, 3)
	ks := []Key{{hashed(0), "ron"}, {hashed(1), "ron"}}
	frame := [4]float64{0.1, 0.1, 0.9, 0.9}
	for _, k := range append(ks, Key{"nowhere", "ron"}) {
		if err := c.Print(k, someBounds, frame); err != nil {
			t.Fatal(err)
		}
	}
	// Cropped to the film's frame, as an edit: the phone crops it too.
	edits, _ := c.Edits([]Key{ks[0]})
	if e, err := edit.Parse([]byte(edits[ks[0]])); err != nil || e.None() {
		t.Errorf("edit %q %v", edits[ks[0]], err)
	}
	if b, err := c.Negative(ks[1]); err != nil || b != someBounds {
		t.Errorf("negative = %q %v", b, err)
	}
	photos, err := c.Page(Filter{})
	if err != nil {
		t.Fatal(err)
	}
	printed := 0
	for _, p := range photos {
		if p.Negative == someBounds {
			printed++
		}
	}
	if printed != 2 {
		t.Errorf("%d printed in the page", printed)
	}
	_, hashes, err := c.Listing("ron")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, b := range hashes {
		if b == someBounds {
			n++
		}
	}
	tagBefore, month, err := c.Month("ron", "none")
	if err != nil || n != 2 || strings.Count(string(month), `"positive":"`+PositiveName(someBounds)+`"`) != 2 {
		t.Errorf("listed %d, month %s %v", n, month, err)
	}
	if err := c.Unprint(ks[:1]); err != nil {
		t.Fatal(err)
	}
	editIs(t, c, "ron", "")
	if b, _ := c.Negative(ks[0]); b != "" {
		t.Errorf("turned back, still %q", b)
	}
	if tag, _, _ := c.Month("ron", "none"); tag == tagBefore {
		t.Error("the month's tag did not move")
	}
	if _, err := c.db.Exec(`DELETE FROM library_photos WHERE hash = ?`, hashed(1)); err != nil {
		t.Fatal(err)
	}
	var left int
	if c.db.QueryRow(`SELECT count(*) FROM negatives`).Scan(&left); left != 0 {
		t.Errorf("%d bounds left", left)
	}
}

// Shown as it came again, a photo keeps the person's own crop, and gets
// back the one it had; printed again, it is cropped once.
func TestUnprintKeepsThePersonsCrop(t *testing.T) {
	c, _ := marking(t, 2)
	frame := [4]float64{0.1, 0.1, 0.9, 0.9}
	k := Key{hashed(0), "ron"}
	if _, _, err := c.Turn(k, 1); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := c.Print(k, someBounds, frame); err != nil {
			t.Fatal(err)
		}
	}
	edits, _ := c.Edits([]Key{k})
	e, _ := edit.Parse([]byte(edits[k]))
	if w, h := e.Size(1000, 500); w != 400 || h != 800 {
		t.Errorf("printed twice, turned: %dx%d", w, h)
	}
	if err := c.Unprint([]Key{k}); err != nil {
		t.Fatal(err)
	}
	editIs(t, c, "ron", "")
	// The person crops it after the print: theirs stays.
	if err := c.Print(k, someBounds, frame); err != nil {
		t.Fatal(err)
	}
	theirs := `{"adjust":{},"tools":2,"crop":{"turns":0,"flip":false,"angle":6,"x":0,"y":0,"w":0.5,"h":0.5,"shape":"Free"}}`
	if err := c.inTx(func(tx *sql.Tx) error {
		_, was, err := unprinted(tx, k)
		if err == nil {
			_, err = setEdit(tx, k, was, theirs)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Unprint([]Key{k}); err != nil {
		t.Fatal(err)
	}
	editIs(t, c, "ron", theirs)
}
