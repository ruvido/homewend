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

// A photo whose file was deleted by hand is missing. Taken out, it is a
// clean-up: gone from every list, and the card brings it in again. Taken
// out and kept out, an import leaves it out as deleted. Its file back, it
// is in the lists again.
func TestAPhotoNotOnTheDiskIsTakenOutOrKeptOut(t *testing.T) {
	card := cameraCard(t)
	root := t.TempDir()
	importCard(t, card, root, false)
	catalog, err := OpenCatalog(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if err := os.MkdirAll(filepath.Join(root, WorkDir), 0o755); err != nil {
		t.Fatal(err)
	}
	photo := filepath.Join(root, "ron", "2018", "01", "DSCF0002.JPG")
	missing := func(want int) []string {
		t.Helper()
		gone, err := catalog.Missing(root)
		if err != nil || len(gone) != want {
			t.Fatalf("missing %v, %v; want %d", gone, err, want)
		}
		var hashes []string
		for _, g := range gone {
			hashes = append(hashes, g.Hash)
		}
		return hashes
	}
	years := func() int {
		t.Helper()
		counts, err := catalog.CountsByYear(nil)
		if err != nil {
			t.Fatal(err)
		}
		return counts["2018"]
	}
	missing(0)
	if err := os.Remove(photo); err != nil {
		t.Fatal(err)
	}
	gone := missing(1)
	if err := catalog.TakeOut(gone, false); err != nil {
		t.Fatal(err)
	}
	missing(0)
	if years() != 0 {
		t.Errorf("2018 counts %d after the clean-up, want 0", years())
	}
	if done := importCard(t, card, root, false); done.Placed != 1 || done.Deleted != 0 {
		t.Errorf("%+v; after a clean-up, want the card's copy in again", done)
	}
	if err := os.Remove(photo); err != nil {
		t.Fatal(err)
	}
	if err := catalog.TakeOut(missing(1), true); err != nil {
		t.Fatal(err)
	}
	missing(0)
	if years() != 0 {
		t.Errorf("2018 counts %d kept out, want 0", years())
	}
	if done := importCard(t, card, root, false); done.Placed != 0 || done.Deleted != 1 {
		t.Errorf("%+v; kept out, want it left out as deleted", done)
	}
	write(t, root, "ron/2018/01/DSCF0002.JPG", exifPhoto("2018:01:01 00:01:36", "two"))
	missing(0)
	if years() != 1 {
		t.Errorf("2018 counts %d with its file back, want 1", years())
	}
}
