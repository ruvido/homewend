// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import "testing"

// One write is told from two, whoever made them.
func TestAWriteIsToldFromTwo(t *testing.T) {
	c, path := marking(t, 2)
	other, err := OpenCatalog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	written := func() Written {
		t.Helper()
		w, err := c.Written()
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	heart := func(c *Catalog) {
		t.Helper()
		if _, err := c.Mark(Favorite, []Key{{hashed(0), "ron"}, {hashed(1), "ron"}}, true); err != nil {
			t.Fatal(err)
		}
	}
	before := written()
	if now := written(); now != before || now.Next(before) {
		t.Errorf("nothing written: %+v after %+v", now, before)
	}
	heart(c)
	if now := written(); !now.Next(before) || before.Next(now) {
		t.Errorf("written once: %+v after %+v", now, before)
	}
	heart(other)
	if now := written(); now.Next(before) {
		t.Errorf("written twice: %+v after %+v", now, before)
	}
}
