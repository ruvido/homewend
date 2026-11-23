// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheDateInAName(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip(err)
	}
	local := time.Local
	time.Local = rome
	defer func() { time.Local = local }()
	at := func(y int, m time.Month, d, h, min, s int) time.Time {
		return time.Date(y, m, d, h, min, s, 0, time.UTC)
	}
	for _, c := range []struct {
		name   string
		when   time.Time
		source DateSource
	}{
		{"20260826_152150.jpg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"IMG_20261121_121457_504.jpg", at(2026, 11, 21, 12, 14, 57), FromName},
		{"PXL_20260826_152150123.jpg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"Screenshot_20260826-152150.png", at(2026, 8, 26, 15, 21, 50), FromName},
		{"IMG-20260826-WA0001.jpg", at(2026, 8, 26, 0, 0, 0), FromNameDay},
		{"signal-2026-08-26-152150.jpg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"signal-2026-08-26.jpg", at(2026, 8, 26, 0, 0, 0), FromNameDay},
		{"signal-2026-08-26-15-21-50-123.jpg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"STK-20260826-WA0002.webp", at(2026, 8, 26, 0, 0, 0), FromNameDay},
		{"photo_2026-08-26_15-21-50.jpg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"photo_12@26-08-2026.jpg", at(2026, 8, 26, 0, 0, 0), FromNameDay},
		// 1785963041597 ms is 2026-08-05 20:50:41 UTC: 22:50:41 in Rome.
		{"1785963041597.png", at(2026, 8, 5, 22, 50, 41), FromName},
		{"FB_IMG_1785963041597.jpg", at(2026, 8, 5, 22, 50, 41), FromName},
	} {
		when, source, ok := nameDate(c.name)
		if !ok || !when.Equal(c.when) || source != c.source {
			t.Errorf("%s: %v %s %v, want %v %s", c.name, when, source, ok, c.when, c.source)
		}
	}
	// Not a date: a counter, a hash, a month 13, a day not yet come.
	for _, name := range []string{"IMG_1922.jpg", "DSCF0004.JPG", "1f3440a69f544ffbcac2ab190ec4b717.jpg",
		"logo-100.jpg", "20261340_101010.jpg", "20990101_101010.jpg", "Plan 2 - View 1.png"} {
		if when, _, ok := nameDate(name); ok {
			t.Errorf("%s: dated %v", name, when)
		}
	}
}

func TestAPhotoWithNoDateInsideTakesTheOneInItsName(t *testing.T) {
	card := t.TempDir()
	write(t, card, "DCIM/20260826_152150.jpg", []byte("no exif here"))
	root := t.TempDir()

	importCard(t, card, root, false)

	mustExist(t, filepath.Join(root, "ron", "2026", "08", "20260826_152150.jpg"))
	if _, err := os.Stat(filepath.Join(root, "ron", "2026", "08", "20260826_152150.jpg.xmp")); !os.IsNotExist(err) {
		t.Errorf("a guessed date was written in a .xmp: %v", err)
	}
	catalog, err := OpenCatalog(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	all, err := catalog.Page(Filter{})
	if err != nil || len(all) != 1 {
		t.Fatalf("%d photos, %v", len(all), err)
	}
	if p := all[0]; p.Source != FromName || p.Kind != DateGuessed || p.Source.Guessed() != "time" {
		t.Errorf("source %s, kind %s; want a guessed time from the name", p.Source, p.Kind)
	}
}

func TestPhotosAlreadyInWithNoDateTakeTheOneInTheirName(t *testing.T) {
	root := t.TempDir()
	catalog, err := OpenCatalog(filepath.Join(root, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	for i, name := range []string{"20260826_152150.jpg", "IMG_1922.jpg", "IMG-20250101-WA0003.jpg"} {
		path := "ron/undated/" + name
		write(t, root, path, []byte{byte(i)})
		if err := catalog.Put(Photo{Hash: name, Path: path, Name: name, Source: NoDate, Kind: DateUnknown, Origin: OriginOther}); err != nil {
			t.Fatal(err)
		}
	}
	// Only the year, from Google's folder: the name says the month too.
	write(t, root, "ron/2019/unknown-month/IMG_20190312_101558.jpg", []byte("folder"))
	if err := catalog.Put(Photo{Hash: "folder", Path: "ron/2019/unknown-month/IMG_20190312_101558.jpg", Name: "IMG_20190312_101558.jpg",
		Taken: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC), Source: FromFolder, Kind: DateGuessed, Origin: OriginCamera}); err != nil {
		t.Fatal(err)
	}
	// One whose file is gone stays as it is.
	if err := catalog.Put(Photo{Hash: "gone", Path: "ron/undated/20240101_101010.jpg", Name: "20240101_101010.jpg", Source: NoDate, Kind: DateUnknown}); err != nil {
		t.Fatal(err)
	}

	if err := DateByName(root, catalog); err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(root, "ron", "2026", "08", "20260826_152150.jpg"))
	mustExist(t, filepath.Join(root, "ron", "2025", "01", "IMG-20250101-WA0003.jpg"))
	mustExist(t, filepath.Join(root, "ron", "2019", "03", "IMG_20190312_101558.jpg"))
	mustExist(t, filepath.Join(root, "ron", "undated", "IMG_1922.jpg"))
	if gone, err := catalog.photo("gone"); err != nil || gone.Source != NoDate {
		t.Errorf("the one whose file is gone: %s, %v", gone.Source, err)
	}
}

func TestThePlanFilesANameDateAsTheCopyWill(t *testing.T) {
	card := t.TempDir()
	write(t, card, "DCIM/Screenshot_20260826-152150.png", []byte("no exif here"))
	folder, err := ReadFolder(card)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanImport(context.Background(), card, folder, nil, PlanOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files[0].Taken.IsZero() {
		t.Fatalf("files %+v, want one dated by its name", plan.Files)
	}
	if len(plan.Years) != 1 || plan.Years[0].Year != "2026" {
		t.Errorf("years %+v, want 2026", plan.Years)
	}
}
