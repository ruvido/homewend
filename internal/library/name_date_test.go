// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
		{"PXL_20260826_152150123.jpg", at(2026, 8, 26, 17, 21, 50), FromName},
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
		{"1785963041597-2.png", at(2026, 8, 5, 22, 50, 41), FromName},
		{"WhatsApp Image 2026-08-26 at 15.21.50.jpeg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"WhatsApp Image 2026-08-26 at 15.21.50-2.jpeg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"WhatsApp Image 2026-08-26 at 3.21.50 PM.jpeg", at(2026, 8, 26, 15, 21, 50), FromName},
		{"WhatsApp Image 2026-08-26 at 12.21.50 AM.jpeg", at(2026, 8, 26, 0, 21, 50), FromName},
		{"Screenshot_2019-04-16-11-19-37-232_com.google.a.jpg", at(2019, 4, 16, 11, 19, 37), FromName},
		{"2016_01_30_11_49_15.mp4", at(2016, 1, 30, 11, 49, 15), FromName},
		{"00004XTR_00004_BURST20190216172030.jpg", at(2019, 2, 16, 17, 20, 30), FromName},
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
	// The name stands against Google's year; one that is no day does not.
	for _, name := range []string{"IMG-20161229-WA0010.jpg", "IMG_20211345_101558.jpg"} {
		path := "ron/2017/unknown-month/" + name
		write(t, root, path, []byte(name))
		if err := catalog.Put(Photo{Hash: name, Path: path, Name: name,
			Taken: time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC), Source: FromFolder, Kind: DateGuessed}); err != nil {
			t.Fatal(err)
		}
	}
	// One that cannot move is said, and the others still move.
	write(t, root, "ron/undated/20230505_101010.jpg", []byte("stuck"))
	if err := catalog.Put(Photo{Hash: "stuck", Path: "ron/undated/20230505_101010.jpg", Name: "20230505_101010.jpg", Source: NoDate, Kind: DateUnknown}); err != nil {
		t.Fatal(err)
	}
	write(t, root, "ron/2023", []byte("a file where the year's folder goes"))
	// One whose file is gone stays as it is.
	if err := catalog.Put(Photo{Hash: "gone", Path: "ron/undated/20240101_101010.jpg", Name: "20240101_101010.jpg", Source: NoDate, Kind: DateUnknown}); err != nil {
		t.Fatal(err)
	}

	turns := 0
	photos, err := NamedPhotos(catalog)
	if err != nil {
		t.Fatal(err)
	}
	err = DateByName(root, catalog, photos, func() (func(), error) { turns++; return func() { turns-- }, nil })
	if err == nil || !strings.Contains(err.Error(), "20230505_101010.jpg") || turns != 0 {
		t.Fatalf("%v, %d turns kept; want the stuck one said", err, turns)
	}
	mustExist(t, filepath.Join(root, "ron", "2016", "12", "IMG-20161229-WA0010.jpg"))
	mustExist(t, filepath.Join(root, "ron", "2017", "unknown-month", "IMG_20211345_101558.jpg"))
	mustExist(t, filepath.Join(root, "ron", "undated", "20230505_101010.jpg"))
	mustExist(t, filepath.Join(root, "ron", "2026", "08", "20260826_152150.jpg"))
	mustExist(t, filepath.Join(root, "ron", "2025", "01", "IMG-20250101-WA0003.jpg"))
	mustExist(t, filepath.Join(root, "ron", "2019", "03", "IMG_20190312_101558.jpg"))
	mustExist(t, filepath.Join(root, "ron", "undated", "IMG_1922.jpg"))
	if gone, err := catalog.Photo("gone"); err != nil || gone.Source != NoDate {
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

func TestANameStandsAgainstGooglesYear(t *testing.T) {
	dir := t.TempDir()
	for name, want := range map[string]string{
		"IMG-20161229-WA0010.jpg": "2016-12-29 nameday", // filed under its upload's year
		"IMG_20170312_101558.jpg": "2017-03-12 name",
		"IMG_20991231_101558.jpg": "2017-01-01 folder", // a day not yet come
		"IMG_20211345_101558.jpg": "2017-01-01 folder", // no day at all
	} {
		write(t, dir, "Photos from 2017/"+name, []byte("no date inside"))
		c := DateOf(filepath.Join(dir, "Photos from 2017", name), "Photos from 2017", "")
		if got := c.When.Format("2006-01-02 ") + string(c.Source); got != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
}

func TestAWhatsAppFileUploadedLateTakesItsNamesDay(t *testing.T) {
	dir := t.TempDir()
	at := func(y int, m time.Month, d, h, min int) int64 {
		return time.Date(y, m, d, h, min, 0, 0, time.Local).Unix()
	}
	for _, c := range []struct {
		name   string
		google int64
		want   string
	}{
		{"IMG-20150407-WA0006.jpg", at(2016, 9, 19, 16, 47), "2015-04-07 nameday guessed"},
		{"WhatsApp Image 2016-04-07 at 15.21.50.jpeg", at(2016, 9, 19, 16, 47), "2016-04-07 name guessed"},
		// Two days after, then the day after in all but two minutes.
		{"IMG-20170505-WA0001.jpg", at(2017, 5, 7, 0, 1), "2017-05-05 nameday guessed"},
		{"IMG-20170505-WA0000.jpg", at(2017, 5, 6, 23, 59), "2017-05-06 sidecar received"},
		{"IMG-20191231-WA0002.jpg", at(2020, 1, 1, 0, 2), "2020-01-01 sidecar received"},
		// Google's is earlier: a person's, or the camera's.
		{"VID-20161225-WA0005.mp4", at(2015, 12, 19, 0, 51), "2015-12-19 sidecar received"},
		// Not WhatsApp's.
		{"signal-2021-07-17-181447.jpg", at(2021, 7, 20, 12, 11), "2021-07-20 sidecar received"},
		{"Screenshot_20191112-112004.png", at(2020, 1, 14, 11, 51), "2019-11-12 name guessed"},
		{"Screenshot_20191112-112004.png", at(2019, 11, 12, 11, 20), "2019-11-12 sidecar received"},
		// Nothing says a screenshot is after Google's date.
		{"Screenshot_20191112-112005.png", at(2019, 11, 1, 11, 20), "2019-11-01 sidecar received"},
	} {
		path := "Photos from 2016/" + c.name
		write(t, dir, path, []byte("no date inside"))
		sidecar(t, dir, path, c.google)
		got := DateOf(filepath.Join(dir, path), "Photos from 2016", filepath.Join(dir, path+".supplemental-metadata.json"))
		if s := got.Local(time.Local).Format("2006-01-02 ") + string(got.Source) + " " + string(got.Kind()); s != c.want {
			t.Errorf("%s: %s, want %s", c.name, s, c.want)
		}
	}
	// By its name, not by the app Google says: no name, no day to take.
	write(t, dir, "20150407_101010.jpg", []byte("sent through a chat"))
	write(t, dir, "20150407_101010.jpg.json", []byte(`{"photoTakenTime":{"timestamp":"1474296453"},"appSource":{"androidPackageName":"com.whatsapp"}}`))
	if c := DateOf(filepath.Join(dir, "20150407_101010.jpg"), "", filepath.Join(dir, "20150407_101010.jpg.json")); c.Source != FromSidecar {
		t.Errorf("a camera's name: %s, want the sidecar's", c.Source)
	}
}

// Only an app Google names and no badge knows is another app: a camera's
// file whose name no app imposes comes from nowhere known.
func TestAnAppIsNotAnUnknownFile(t *testing.T) {
	for _, c := range []struct {
		app, name string
		want      Origin
	}{
		{"com.instagram.android", "IMG_20190401_101010_123.jpg", OriginInstagram},
		{"com.canva.editor", "design.png", OriginApp},
		{"com.google.android.apps.photos", "20260826_152150.jpg", OriginOther},
		{"com.google.android.apps.photos", "IMG-20190714-WA0001.jpg", OriginWhatsApp},
		{"", "20260826_152150.jpg", OriginOther},
	} {
		if got := OriginOf(c.app, c.name); got != c.want {
			t.Errorf("%s %s: %q, want %q", c.app, c.name, got, c.want)
		}
	}
}
