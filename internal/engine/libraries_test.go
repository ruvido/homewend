// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// unread is a photo's body nobody may read: the computer has its bytes.
type unread struct{}

func (unread) Read([]byte) (int, error) { return 0, errors.New("the phone's bytes were read") }

// send is a photo sent by phone, and its hash.
func send(t *testing.T, phone Phone, name, body string) (Received, string) {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	got, err := ReceivePhoto(context.Background(), phone, hash, name, int64(len(body)), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return got, hash
}

func haveIs(t *testing.T, phone Phone, hash, want string) {
	t.Helper()
	held, err := Have(phone, []string{hash})
	got := strings.Repeat("have ", len(held.Have)) + strings.Repeat("out ", len(held.Out)) + strings.Repeat("archived", len(held.Archived))
	if got = strings.TrimSpace(got); err != nil || got != want {
		t.Errorf("%s's phone is told %q, %v; want %q", phone.Profile, got, err, want)
	}
}

// twoPhones is a library with two phones, ron's and ann's, and one photo of
// ron's.
func twoPhones(t *testing.T) (root string, ron, ann Phone, hash string, rons Received) {
	t.Helper()
	config(t)
	root = t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	ron, ann = Phone{ID: "p1", Name: "Pixel", Profile: "ron"}, Phone{ID: "p2", Name: "iPhone", Profile: "ann"}
	rons, hash = send(t, ron, "IMG_1.JPG", "a photo of two")
	return root, ron, ann, hash, rons
}

// A photo the computer has for one is not had for the other until her phone
// offers it: then it is hers too, under her phone's name, without a byte sent
// and without a second copy on the disk.
func TestAPhotoAnotherLibraryHasIsNotSentAgain(t *testing.T) {
	root, ron, ann, hash, rons := twoPhones(t)
	haveIs(t, ron, hash, "have")
	haveIs(t, ann, hash, "")
	haveIs(t, ann, hash, "") // asking brings nothing in
	got, err := ReceivePhoto(context.Background(), ann, hash, "IMG-HERS.JPG", 1<<60, unread{})
	if err != nil || !got.New || !strings.HasPrefix(got.Path, "ann/") || filepath.Base(got.Path) != "IMG-HERS.JPG" {
		t.Fatalf("ann's: %+v %v", got, err)
	}
	haveIs(t, ann, hash, "have")
	a, errA := os.Stat(filepath.Join(root, rons.Path))
	b, errB := os.Stat(filepath.Join(root, got.Path))
	if errA != nil || errB != nil || !os.SameFile(a, b) {
		t.Errorf("two files on the disk: %v %v", errA, errB)
	}
	if again, err := ReceivePhoto(context.Background(), ann, hash, "IMG-HERS.JPG", 14, unread{}); err != nil || again.New || again.Path != got.Path {
		t.Errorf("sent again: %+v %v", again, err)
	}
	for _, phone := range []Phone{ron, ann} {
		if l, err := LibraryOf(phone); err != nil || l.Photos != 1 || l.Bytes[""] != 14 {
			t.Errorf("%s's library %+v %v", phone.Profile, l, err)
		}
	}
	if list, err := Photos(Settings{}, "", "", "", false, "", 0); err != nil || len(list.Photos) != 1 || len(copiesIn(list)) != 2 {
		t.Errorf("%+v %v, want one tile of two", list.Photos, err)
	}
	if left, _ := os.ReadDir(filepath.Join(root, ".homewend", "phone")); len(left) > 0 {
		t.Errorf("left behind: %v", left)
	}
}

// What one does to a photo of two is not done to the other's: in the Trash,
// deleted, archived, taken out.
func TestWhatOneDoesToAPhotoIsNotDoneToTheOthers(t *testing.T) {
	root, ron, ann, hash, rons := twoPhones(t)
	// Already in ron's Trash, as ann's phone first offers it.
	if done, err := MarkOf(ron, library.Trashed, []string{hash}, true); err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	haveIs(t, ron, hash, "out")
	haveIs(t, ann, hash, "")
	anns, err := ReceivePhoto(context.Background(), ann, hash, "IMG_1.JPG", 14, unread{})
	if err != nil || !anns.New {
		t.Fatalf("%+v %v", anns, err)
	}
	haveIs(t, ann, hash, "have")
	if done, err := MarkOf(ann, library.Archived, []string{hash}, true); err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	haveIs(t, ann, hash, "have archived")
	if done, err := MarkOf(ron, library.Trashed, []string{hash}, false); err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	haveIs(t, ron, hash, "have")
	// From the window, by whose it is.
	if _, err := Mark(library.Trashed, []library.Key{{Hash: hash, Profile: "ann"}}, true); err != nil {
		t.Fatal(err)
	}
	haveIs(t, ann, hash, "out")
	haveIs(t, ron, hash, "have")
	if _, err := Mark(library.Trashed, []library.Key{{Hash: hash, Profile: "ann"}}, false); err != nil {
		t.Fatal(err)
	}
	if n, err := Turn(library.Key{Hash: hash, Profile: "ron"}, false); err != nil || n.Turn != 1 {
		t.Fatal(n, err)
	}
	if n, err := GiveDates([]library.Key{{Hash: hash, Profile: "ann"}}, time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if a, err := About(library.Key{Hash: hash, Profile: "ron"}); err != nil || a.Path != rons.Path || !slices.Equal(a.Local, []string{"Pixel"}) {
		t.Errorf("ron's %+v %v", a, err)
	}
	if a, err := About(library.Key{Hash: hash, Profile: "ann"}); err != nil || a.Path != "ann/2020/05/IMG_1.JPG" || !slices.Equal(a.Local, []string{"iPhone"}) {
		t.Errorf("ann's %+v %v", a, err)
	}
	// Ron deletes his file: ann's stays, and is what the thumbnail is made of.
	if err := os.Remove(filepath.Join(root, rons.Path)); err != nil {
		t.Fatal(err)
	}
	haveIs(t, ron, hash, "out")
	haveIs(t, ann, hash, "have archived")
	if back, err := ReceivePhoto(context.Background(), ron, hash, "IMG_1.JPG", 14, unread{}); err != nil || !back.Out {
		t.Errorf("deleted, sent again by ron: %+v %v", back, err)
	}
	if path, _, err := pictureOf(library.Key{Hash: hash, Profile: "ron"}, os.Stat); err != nil || path != filepath.Join(root, "ann", "2020", "05", "IMG_1.JPG") {
		t.Errorf("the picture is made from %s %v", path, err)
	}
	if _, err := lookForMissing(root); err != nil {
		t.Fatal(err)
	}
	if list, err := Photos(Settings{}, "", "", "", false, ViewMissing, 0); err != nil || len(list.Photos) != 1 || list.Photos[0].Profile != "ron" || !list.Photos[0].Missing {
		t.Errorf("missing %+v %v, want ron's", list, err)
	}
	if n, err := TakeOutMissing(&library.Key{Hash: hash, Profile: "ann"}, false); err != nil || n != 0 {
		t.Errorf("ann's taken out, not missing: %d %v", n, err)
	}
	if n, err := TakeOutMissing(nil, true); err != nil || n != 1 {
		t.Errorf("taken out %d %v, want ron's", n, err)
	}
	haveIs(t, ron, hash, "out")
	haveIs(t, ann, hash, "have archived")
}

// A photo ron deleted before ann's phone offered it is sent: there is no
// other copy, and it was never deleted from hers.
func TestAPhotoDeletedByOneIsStillTheOthersToKeep(t *testing.T) {
	root, ron, ann, hash, rons := twoPhones(t)
	if err := os.Remove(filepath.Join(root, rons.Path)); err != nil {
		t.Fatal(err)
	}
	haveIs(t, ann, hash, "")
	if got, _ := send(t, ann, "IMG_1.JPG", "a photo of two"); !got.New {
		t.Errorf("%+v, want it kept", got)
	}
	haveIs(t, ann, hash, "have")
	haveIs(t, ron, hash, "out")
}

// A file of ron's that another program changed is not ann's photo: her
// phone's bytes are taken.
func TestAChangedFileIsNotGivenToAPhone(t *testing.T) {
	root, _, ann, _, rons := twoPhones(t)
	if err := os.WriteFile(filepath.Join(root, rons.Path), []byte("changed since"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := send(t, ann, "IMG_1.JPG", "a photo of two")
	if b, err := os.ReadFile(filepath.Join(root, got.Path)); err != nil || string(b) != "a photo of two" {
		t.Errorf("ann's is %q %v", b, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, rons.Path)); string(b) != "changed since" {
		t.Errorf("ron's is now %q", b)
	}
}

// Two phones send the same photo at once: each has its own, one file.
func TestTwoPhonesSendTheSamePhotoAtOnce(t *testing.T) {
	root, ron, ann, _, _ := twoPhones(t)
	body := []byte(strings.Repeat("the same ", 100000))
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	type answer struct {
		Received
		err error
	}
	answers := make(chan answer, 2)
	for _, phone := range []Phone{ron, ann} {
		go func() {
			got, err := ReceivePhoto(context.Background(), phone, hash, "B.JPG", int64(len(body)), bytes.NewReader(body))
			answers <- answer{got, err}
		}()
	}
	var files []os.FileInfo
	for range 2 {
		a := <-answers
		info, err := os.Stat(filepath.Join(root, a.Path))
		if a.err != nil || !a.New || err != nil {
			t.Fatalf("%+v %v", a, err)
		}
		files = append(files, info)
	}
	if !os.SameFile(files[0], files[1]) {
		t.Error("two files on the disk")
	}
}

// Another process, a card imported from a terminal into ann's library, while
// the window marks ron's photos of the same card.
func TestAnImportInAnotherProcessIntoTheOtherLibrary(t *testing.T) {
	if card := os.Getenv("HOMEWEND_TEST_CARD"); card != "" {
		if _, err := Import(context.Background(), card, "ann", library.ImportOptions{}, nil); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := config(t)
	root, card := t.TempDir(), t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(card, "DCIM"), 0o755); err != nil {
		t.Fatal(err)
	}
	const photos = 40
	for i := range photos {
		if err := os.WriteFile(filepath.Join(card, "DCIM", fmt.Sprint("DSC", i, ".JPG")), bytes.Repeat([]byte{byte(i)}, 1000), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if done, err := Import(context.Background(), card, "ron", library.ImportOptions{}, nil); err != nil || done.Placed != photos {
		t.Fatalf("%+v %v", done, err)
	}
	other := exec.Command(os.Args[0], "-test.run=^TestAnImportInAnotherProcessIntoTheOtherLibrary$")
	other.Env = append(os.Environ(), "HOMEWEND_TEST_CARD="+card, "XDG_CONFIG_HOME="+filepath.Dir(dir))
	var said bytes.Buffer
	other.Stdout, other.Stderr = &said, &said
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	list, err := Photos(Settings{}, "", "", "", false, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Photos {
		if p.Profile != "ron" {
			continue
		}
		if done, err := Mark(library.Trashed, []library.Key{p.Key()}, true); err != nil || len(done) != 1 {
			t.Fatalf("%v %v", done, err)
		}
	}
	if err := other.Wait(); err != nil {
		t.Fatalf("%v\n%s", err, said.String())
	}
	list, err = Photos(Settings{}, "", "", "", false, "", 0)
	if err != nil || len(list.Photos) != photos || slices.ContainsFunc(list.Photos, func(p GridPhoto) bool { return p.Profile != "ann" }) {
		t.Errorf("%d in the grid %v, want ann's %d alone", len(list.Photos), err, photos)
	}
	if trash, err := Photos(Settings{}, "", "", "", false, ViewTrash, 0); err != nil || len(trash.Photos) != photos {
		t.Errorf("%d in the Trash %v, want ron's %d", len(trash.Photos), err, photos)
	}
	// Again into both: nothing comes in, ron's deleted stay out.
	for _, p := range []string{"ron", "ann"} {
		if done, err := Import(context.Background(), card, p, library.ImportOptions{}, nil); err != nil || done.Placed != 0 || done.InLibrary != photos {
			t.Errorf("into %s again: %+v %v", p, done, err)
		}
	}
}

// Two accounts of two people with the same photo: each account's library
// shows its own, the sidebar counts as the window shows, and a name an older
// program left in the second's folder is found by the engine.
func TestAnOlderProgramsSecondProfileIsFoundByTheEngine(t *testing.T) {
	root, _, ann, hash, rons := twoPhones(t)
	// As the older program left it: ann's name on the disk, no record.
	if err := os.MkdirAll(filepath.Join(root, "ann", "undated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, rons.Path), filepath.Join(root, "ann", "undated", "IMG_1.JPG")); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := UpdatePhones(func(p *Phones) error { p.Phones = []Phone{ann}; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := findLinksNow(root, nil, Settings{}); err != nil {
		t.Fatal(err)
	}
	haveIs(t, ann, hash, "have")
	// One tile, and one in the sidebar's count, of both.
	o, err := overview(root, nil, nil, "")
	if err != nil || o.Total != 1 {
		t.Errorf("overview %+v %v, want one", o, err)
	}
	if list, err := Photos(Settings{}, "", "", "", false, "", 0); err != nil || len(copiesIn(list)) != 2 {
		t.Errorf("%+v %v, want both", list.Photos, err)
	}
}

// copiesIn are the list's photos, each library's copy of a tile one.
func copiesIn(l PhotoList) []GridPhoto {
	var all []GridPhoto
	for _, p := range l.Photos {
		all = append(append(all, p), p.Also...)
	}
	return all
}

// A profile named like a year, or any number, keeps its photos and its files
// through the window's overview.
func TestAProfileNamedLikeAYearKeepsItsPhotos(t *testing.T) {
	for _, profile := range []string{"12345", "1985"} {
		t.Run(profile, func(t *testing.T) {
			dir := config(t)
			root := t.TempDir()
			if err := SetDefaultLibrary(root); err != nil {
				t.Fatal(err)
			}
			account := filepath.Join(dir, "accounts", "google", "ron@example.com")
			if err := os.MkdirAll(account, 0o755); err != nil {
				t.Fatal(err)
			}
			photo := profile + "/2019/07/a.jpg"
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, photo)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, photo), []byte("a photo"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, ".homewend"), 0o755); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", catalogPath(root))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`CREATE TABLE photos (hash TEXT PRIMARY KEY, path TEXT NOT NULL, name TEXT NOT NULL, bytes INTEGER NOT NULL, taken INTEGER, source TEXT NOT NULL, kind TEXT NOT NULL, origin TEXT NOT NULL);
				INSERT INTO photos VALUES ('h', '` + photo + `', 'a.jpg', 7, 1562345678, 'exif', 'taken', 'camera'); PRAGMA user_version = 1`); err != nil {
				t.Fatal(err)
			}
			db.Close()
			if _, err := UpdatePhones(func(p *Phones) error { p.Phones = []Phone{{ID: "p", Profile: profile}}; return nil }); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if o, err := LibraryOverview(Settings{}); err != nil || o.Total != 1 {
					t.Fatalf("%+v %v", o, err)
				}
			}
			if _, err := os.Stat(filepath.Join(root, photo)); err != nil {
				t.Errorf("its file: %v", err)
			}
			haveIs(t, Phone{Profile: profile}, "h", "have")
		})
	}
}

// A picture is made from a file that is still the photo, whoever's it is;
// another program's edit of one library's file is seen in that one alone.
func TestAnEditedFileIsNotTheOthersPicture(t *testing.T) {
	config(t)
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	root := t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	draw := func(w, h int) string {
		var b bytes.Buffer
		if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	ron, ann := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}, Phone{ID: "p2", Name: "iPhone", Profile: "ann"}
	rons, hash := send(t, ron, "A.PNG", draw(300, 100))
	anns, _ := send(t, ann, "A.PNG", draw(300, 100))
	// As editors save: written beside, then named over it.
	edited := filepath.Join(root, anns.Path)
	if err := os.WriteFile(edited+".tmp", []byte(draw(100, 300)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(edited+".tmp", edited); err != nil {
		t.Fatal(err)
	}
	tall := func(k library.Key) (bool, bool) {
		t.Helper()
		file, kept, err := Thumbnail(context.Background(), k, "", "")
		if err != nil {
			t.Fatal(err)
		}
		w, h := thumb.Dimensions(file)
		return h > w, kept
	}
	if tall, kept := tall(library.Key{Hash: hash, Profile: "ann"}); tall || !kept {
		t.Error("the content's thumbnail was made from the edited file")
	}
	if tall, _ := tall(library.Key{Hash: hash, Profile: "ron"}); tall {
		t.Error("ron is shown ann's edit")
	}
	for profile, want := range map[string]string{"ron": rons.Path, "ann": anns.Path} {
		if path, err := PathOf(library.Key{Hash: hash, Profile: profile}); err != nil || path != filepath.Join(root, want) {
			t.Errorf("%s's own is %s %v", profile, path, err)
		}
	}
	// None is the photo any more: each its own, kept by no content.
	if err := os.WriteFile(filepath.Join(root, rons.Path), []byte(draw(200, 50)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, size := range []string{"thumbs", "previews"} {
		folder, err := picturesDir(size)
		if err != nil {
			t.Fatal(err)
		}
		os.Remove(thumb.Path(folder, hash))
	}
	if tall, kept := tall(library.Key{Hash: hash, Profile: "ann"}); !tall || kept {
		t.Error("ann is not shown her own")
	}
	if tall, kept := tall(library.Key{Hash: hash, Profile: "ron"}); tall || kept {
		t.Error("ron is not shown his own")
	}
}

// A new profile that differs from one there is by its capitals alone is
// refused wherever one is made; those there are stay.
func TestAProfileOfOtherCapitalsIsRefused(t *testing.T) {
	dir := config(t)
	root := t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "accounts", "google", "ron@example.com"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "Ann"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := rememberProfile("Bob"); err != nil {
		t.Fatal(err)
	}
	var im Importing
	for _, p := range []string{"Ron", "ann", "bob"} {
		_, pairing := StartPairing(p)
		_, importing := Import(context.Background(), t.TempDir(), p, library.ImportOptions{}, nil)
		for what, err := range map[string]error{"pairing": pairing, "import": importing, "the window's import": im.Plan(ImportAsk{Dir: t.TempDir(), Profile: p})} {
			if !errors.Is(err, ErrProfileCase) {
				t.Errorf("%s into %s: %v", what, p, err)
			}
		}
	}
	for _, p := range []string{"ron", "Ann", "Bob", "Zoe"} {
		if err := CheckProfile(p); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if err := SetProfile("google/ron@example.com", "bob"); !errors.Is(err, ErrProfileCase) {
		t.Errorf("the account's: %v", err)
	}
	if err := SetProfile("google/ron@example.com", "Ron"); err != nil {
		t.Errorf("its own proposed name, in capitals: %v", err)
	}
}

// Each tile says whose it is by one list, the overview's whatever is shown, and
// the photo two libraries show is one tile, the first person's copy with the
// other's; one library's has none.
func TestWhoseATileIs(t *testing.T) {
	_, _, ann, hash, _ := twoPhones(t)
	if _, err := UpdatePhones(func(p *Phones) error {
		p.Phones = []Phone{{ID: "p1", Profile: "ron"}, {ID: "p2", Profile: "ann"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	list := func() PhotoList {
		t.Helper()
		l, err := Photos(Settings{}, "", "", "", false, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if l := list(); len(l.Photos) != 1 || l.Photos[0].Also != nil || l.Photos[0].Of != 1 {
		t.Errorf("ron's alone: %+v", l)
	}
	if _, err := ReceivePhoto(context.Background(), ann, hash, "IMG_1.JPG", 14, unread{}); err != nil {
		t.Fatal(err)
	}
	send(t, ann, "IMG_2.JPG", "ann's alone")
	l := list()
	if !slices.Equal(l.Profiles, []string{"ann", "ron"}) || len(l.Photos) != 2 {
		t.Fatalf("%+v", l)
	}
	for _, p := range l.Photos {
		if l.Profiles[p.Of] != p.Profile || (p.Hash == hash) != (len(p.Also) == 1) {
			t.Errorf("%+v", p)
		}
		if p.Hash == hash && (p.Profile != "ann" || p.Also[0].Profile != "ron" || p.Also[0].Of != 1) {
			t.Errorf("the tile is ann's, with ron's: %+v", p)
		}
	}
	// One of the two no longer shown, the other is as any photo.
	if _, err := Mark(library.Archived, []library.Key{{Hash: hash, Profile: "ann"}}, true); err != nil {
		t.Fatal(err)
	}
	if l := list(); len(l.Photos) != 2 || slices.ContainsFunc(l.Photos, func(p GridPhoto) bool { return p.Also != nil }) {
		t.Errorf("%+v: the other is not in the list", l.Photos)
	}
	if o, err := LibraryOverview(Settings{}); err != nil || !slices.Equal(o.Profiles, l.Profiles) {
		t.Errorf("the overview's %v %v, want the list's %v", o.Profiles, err, l.Profiles)
	}
}

// A library that cannot be written, with nothing to settle, is shown.
func TestALibraryThatCannotBeWrittenIsShown(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("a mode takes no one's writing away")
	}
	root, _, _, _, _ := twoPhones(t)
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, library.WorkDir)
	os.Remove(filepath.Join(work, "lock"))
	for _, p := range []string{catalogPath(root), work} {
		mode := os.FileMode(0o444)
		if p == work {
			mode = 0o555
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.Chmod(work, 0o755) })
	if o, err := LibraryOverview(s); err != nil || o.Total != 1 {
		t.Errorf("%+v %v", o, err)
	}
}

// A photo of two libraries is where its first person's copy is in the list,
// whatever date the other's has.
func TestATileOfTwoIsWhereTheFirstsCopyIs(t *testing.T) {
	l := listOf([]GridPhoto{{Hash: "a", Profile: "ron"}, {Hash: "b", Profile: "ann"}, {Hash: "a", Profile: "ann"}, {Hash: "c", Profile: "ron"}}, []string{"ann", "ron"})
	got := ""
	for _, p := range l.Photos {
		got += p.Profile + "/" + p.Hash
		for _, c := range p.Also {
			got += "+" + c.Profile
		}
		got += " "
	}
	if got != "ann/b ann/a+ron ron/c " {
		t.Errorf("%q", got)
	}
}
