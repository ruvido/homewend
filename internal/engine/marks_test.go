// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// waits is a phone's question held, and its answer when it comes.
func waits(ctx context.Context, phone Phone, shelf library.Shelf) <-chan told {
	out := make(chan told, 1)
	go func() {
		changed, err := WaitForMarks(ctx, phone, shelf.ID, shelf.Cursor)
		out <- told{changed, err}
	}()
	return out
}

// held waits until the phones of n questions wait, their first look done.
func held(t *testing.T, n int) {
	t.Helper()
	for range 500 {
		waiting.Lock()
		got := len(waiting.phones)
		waiting.Unlock()
		if got == n {
			time.Sleep(50 * time.Millisecond)
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("not %d waiting", n)
}

func answer(t *testing.T, what string, got <-chan told, changed bool, within time.Duration) error {
	t.Helper()
	select {
	case a := <-got:
		if a.changed != changed {
			t.Errorf("%s: changed %v, want %v", what, a.changed, changed)
		}
		return a.err
	case <-time.After(within):
		t.Errorf("%s: not answered in %v", what, within)
		return nil
	}
}

func shelfOf(t *testing.T, phone Phone) library.Shelf {
	t.Helper()
	h, err := Have(phone, nil)
	if err != nil || h.Shelf == nil {
		t.Fatalf("%+v %v", h, err)
	}
	return *h.Shelf
}

// A question held is told of a heart of its library as it is set here, and
// not of another's; ended, nothing looks any more.
func TestAQuestionIsHeldUntilAHeart(t *testing.T) {
	if path := os.Getenv("HOMEWEND_TEST_HEART"); path != "" {
		c, err := library.OpenCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, err := c.Mark(library.Favorite, []library.Key{{Hash: os.Getenv("HOMEWEND_TEST_HASH"), Profile: "ron"}}, false); err != nil {
			t.Fatal(err)
		}
		return
	}
	root, ron, ann, hash, _ := twoPhones(t)
	if _, err := ReceivePhoto(context.Background(), ann, hash, "IMG_1.JPG", 14, unread{}); err != nil {
		t.Fatal(err)
	}
	was := marksEvery
	marksEvery = time.Hour // the window's own does not wait for a look
	t.Cleanup(func() { marksEvery = was })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rons, anns := waits(ctx, ron, shelfOf(t, ron)), waits(ctx, ann, shelfOf(t, ann))
	held(t, 2)
	if runtime.GOOS == "linux" {
		for _, fd := range openFiles(t) {
			if strings.HasPrefix(fd, root) {
				t.Errorf("%s is open while phones wait: its disk could not be ejected", fd)
			}
		}
	}
	key := []library.Key{{Hash: hash, Profile: "ron"}}
	if _, err := Mark(library.Favorite, key, true); err != nil {
		t.Fatal(err)
	}
	answer(t, "a heart of ron's", rons, true, time.Second)
	select {
	case a := <-anns:
		t.Errorf("ann's phone was told of ron's heart: %+v", a)
	case <-time.After(50 * time.Millisecond):
	}
	// A second phone of ron's says one: his first hears of it at once.
	shelf := shelfOf(t, ron)
	rons = waits(ctx, ron, shelf)
	held(t, 2)
	ago := int64(0)
	second := ron
	second.ID = "p3"
	a, err := Marks(second, library.MarkQuestion{Shelf: shelf.ID, After: shelf.Cursor, Install: "C",
		Marks: []library.MarkAction{{N: 1, Hash: hash, State: library.Favorite, Value: json.RawMessage("false"), Ago: &ago}}})
	if err != nil || len(a.Said) != 1 || a.Said[0].Value || a.Owner != "ron" {
		t.Errorf("%+v %v", a, err)
	}
	answer(t, "a heart from another phone", rons, true, time.Second)
	cancel()
	answer(t, "ended", anns, false, time.Second)
	held(t, 0)
	waiting.Lock()
	if waiting.watching {
		t.Error("it still looks with no phone waiting")
	}
	waiting.Unlock()

	// What another process does is seen by the next look.
	marksEvery = 50 * time.Millisecond
	rons = waits(context.Background(), ron, shelfOf(t, ron))
	anns = waits(context.Background(), ann, shelfOf(t, ann))
	held(t, 2)
	other := exec.Command(os.Args[0], "-test.run=^TestAQuestionIsHeldUntilAHeart$")
	other.Env = append(os.Environ(), "HOMEWEND_TEST_HEART="+catalogPath(root), "HOMEWEND_TEST_HASH="+hash)
	if out, err := other.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	answer(t, "another process's heart", rons, true, 5*time.Second)
	held(t, 1) // the look his leaving asks for is over: on Windows an open catalog stays where it is
	// The disk taken out while ann's waits.
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	if err := answer(t, "the disk away", anns, false, 5*time.Second); !errors.As(err, new(LibraryGoneError)) {
		t.Errorf("the disk away: %v", err)
	}
	if _, err := WaitForMarks(context.Background(), ann, "", 0); !errors.As(err, new(LibraryGoneError)) {
		t.Errorf("asked with the disk away: %v", err)
	}
	if err := os.Rename(root+"-away", root); err != nil {
		t.Fatal(err)
	}
	held(t, 0)
}

func openFiles(t *testing.T) []string {
	t.Helper()
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, fd := range fds {
		if name, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name())); err == nil {
			out = append(out, name)
		}
	}
	return out
}

// A question ends when the phone asks again, is unpaired, waits its while, or
// another library is chosen; one with something to hear is not held.
func TestHowAQuestionEnds(t *testing.T) {
	_, ron, ann, hash, _ := twoPhones(t)
	every, held55 := marksEvery, marksHeld
	marksEvery = 50 * time.Millisecond
	t.Cleanup(func() { marksEvery, marksHeld = every, held55 })
	ctx := context.Background()
	shelf := shelfOf(t, ron)
	first := waits(ctx, ron, shelf)
	held(t, 1)
	second := waits(ctx, ron, shelf)
	answer(t, "asked again", first, false, time.Second)
	if err := Unpair(ron.ID, "computer"); err != nil {
		t.Fatal(err)
	}
	answer(t, "unpaired", second, false, time.Second)
	held(t, 0)
	marksHeld = 100 * time.Millisecond
	if changed, err := WaitForMarks(ctx, ron, shelf.ID, shelf.Cursor); changed || err != nil {
		t.Errorf("nothing came: %v %v", changed, err)
	}
	marksHeld = held55
	for what, s := range map[string]library.Shelf{"another shelf": {ID: "x"}, "no shelf": {}, "a seq not reached": {ID: shelf.ID, Cursor: 9}} {
		answer(t, what, waits(ctx, ron, s), true, time.Second)
	}
	if _, err := Mark(library.Favorite, []library.Key{{Hash: hash, Profile: "ron"}}, true); err != nil {
		t.Fatal(err)
	}
	answer(t, "a heart there already", waits(ctx, ron, shelf), true, time.Second)
	// Another library chosen, with a photo of ann's.
	anns := waits(ctx, ann, shelfOf(t, ann))
	held(t, 1)
	other := t.TempDir()
	if err := SetDefaultLibrary(other); err != nil {
		t.Fatal(err)
	}
	if err := answer(t, "a library with nothing in it", anns, false, time.Second); !errors.Is(err, ErrEmptyLibrary) {
		t.Errorf("%v", err)
	}
	send(t, ann, "IMG_2.JPG", "another")
	anns = waits(ctx, ann, shelfOf(t, ann))
	held(t, 1)
	third := t.TempDir()
	c := filepath.Join(third, library.WorkDir)
	if err := os.MkdirAll(c, 0o755); err != nil {
		t.Fatal(err)
	}
	cat, err := library.OpenCatalog(filepath.Join(c, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	cat.Close()
	if err := SetDefaultLibrary(third); err != nil {
		t.Fatal(err)
	}
	answer(t, "another library", anns, true, time.Second)
}

// Many phones ask and are told at once, over and over.
func TestManyPhonesWait(t *testing.T) {
	_, ron, _, hash, _ := twoPhones(t)
	const phones = 8
	done := make(chan error, phones)
	for i := range phones {
		phone := ron
		phone.ID = fmt.Sprint("p", i)
		go func() {
			s := library.Shelf{}
			for range 20 {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				_, err := WaitForMarks(ctx, phone, s.ID, s.Cursor)
				cancel()
				if err != nil {
					done <- err
					return
				}
				a, err := Marks(phone, library.MarkQuestion{})
				if err != nil {
					done <- err
					return
				}
				s = a.Shelf
			}
			done <- nil
		}()
	}
	for i, left := 0, phones; left > 0; i++ {
		if _, err := Mark(library.Favorite, []library.Key{{Hash: hash, Profile: "ron"}}, i%2 == 0); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if left--; err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Millisecond):
		}
	}
	held(t, 0)
}

// A catalog another keeps locked holds no question past its while, nor one
// given up, and a window's mark with the disk away is none.
func TestAQuestionOfALockedCatalog(t *testing.T) {
	root, ron, _, hash, _ := twoPhones(t)
	shelf := shelfOf(t, ron)
	db, err := sql.Open("sqlite", catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	was := marksHeld
	marksHeld = 300 * time.Millisecond
	answer(t, "its while", waits(context.Background(), ron, shelf), false, 3*time.Second)
	marksHeld = was
	ctx, cancel := context.WithCancel(context.Background())
	given := waits(ctx, ron, shelf)
	held(t, 1)
	cancel()
	answer(t, "given up", given, false, 3*time.Second)
	db.Close()
	// Let go of, the next is told as ever.
	answer(t, "let go of", waits(context.Background(), ron, library.Shelf{}), true, 40*time.Second)
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	if done, err := Mark(library.Favorite, []library.Key{{Hash: hash, Profile: "ron"}}, true); err != nil || len(done) != 0 {
		t.Errorf("the window, the disk away: %v %v", done, err)
	}
	if _, err := MarkOf(ron, library.Archived, []string{hash}, true); !errors.As(err, new(LibraryGoneError)) {
		t.Errorf("a phone, the disk away: %v", err)
	}
	if err := os.Rename(root+"-away", root); err != nil {
		t.Fatal(err)
	}
}

// A disk taken out between the look for the library and the catalog's opening
// is away: the library is not one with nothing in it, nor the error another.
func TestADiskTakenOutAsItIsOpened(t *testing.T) {
	root, _, _, _, _ := twoPhones(t)
	for what, fails := range map[string]error{"none there": nil, "not opened": errors.New("unable to open database file")} {
		_, c, err := openHere(func(string) (*library.Catalog, error) {
			if err := os.Rename(root, root+"-away"); err != nil {
				t.Fatal(err)
			}
			return nil, fails
		})
		if c != nil || !errors.As(err, new(LibraryGoneError)) {
			t.Errorf("%s: %v", what, err)
		}
		if err := os.Rename(root+"-away", root); err != nil {
			t.Fatal(err)
		}
	}
}
