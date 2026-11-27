// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"strings"
	"testing"
	"time"
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
	if err := c.SetNegatives(append(ks, Key{"nowhere", "ron"}), someBounds); err != nil {
		t.Fatal(err)
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
	if err := c.SetNegatives(ks[:1], ""); err != nil {
		t.Fatal(err)
	}
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
