// homewend — Copyright (C) 2026 Ron Alder
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The look and the copy of the same folder end the same way: the line the
// command line and the window say is decided once.
func TestALookAndItsCopyEndAlike(t *testing.T) {
	card := t.TempDir()
	write(t, card, "DCIM/A.JPG", exifPhoto("2019:07:14 10:00:00", "a"))
	write(t, card, "DCIM/B.JPG", exifPhoto("2019:07:15 10:00:00", "b"))
	// A twin of A, and a file that cannot be read in the project that
	// stays out: counted alike, or the look says what the copy does not.
	write(t, card, "DCIM/Z/A2.JPG", exifPhoto("2019:07:14 10:00:00", "a"))
	write(t, card, "code/site/package.json", []byte("{}"))
	write(t, card, "code/site/logo.png", []byte("an icon"))
	if err := os.Chmod(filepath.Join(card, "code/site/logo.png"), 0); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	catalog, err := OpenCatalog(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	folder, err := ReadFolder(card)
	if err != nil {
		t.Fatal(err)
	}
	fits := func(int64) error { return nil }
	ends := func(want Outcome) {
		t.Helper()
		plan, err := PlanImport(context.Background(), card, folder, catalog, PlanOptions{Root: root, Profile: "ron"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		done, err := Import(context.Background(), folder, root, catalog, Options{Profile: "ron"}, ImportOptions{Fits: fits}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if plan.Outcome != want {
			t.Errorf("look %s, want %s", plan.Outcome, want)
		}
		if !reflect.DeepEqual(plan.Counts, done.Counts) {
			t.Errorf("look %+v\ncopy %+v", plan.Counts, done.Counts)
		}
	}
	ends(OutcomeNew)
	ends(OutcomeLeftOut) // all in; the project stays out, said first
	if err := os.Remove(filepath.Join(root, "ron", "2019", "07", "A.JPG")); err != nil {
		t.Fatal(err)
	}
	ends(OutcomeLeftOut)
	if err := os.RemoveAll(filepath.Join(card, "code")); err != nil {
		t.Fatal(err)
	}
	if folder, err = ReadFolder(card); err != nil {
		t.Fatal(err)
	}
	ends(OutcomeDeleted)
}
