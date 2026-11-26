// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// lister is a phone that waits with the list it has.
type lister struct {
	t     *testing.T
	phone Phone
	tag   string
}

// listerOf is phone with its list read now.
func listerOf(t *testing.T, phone Phone) *lister {
	t.Helper()
	l := &lister{t: t, phone: phone}
	l.reads()
	return l
}

func (l *lister) reads() {
	l.t.Helper()
	tag, _, err := PhoneList(context.Background(), l.phone)
	if err != nil {
		l.t.Fatal(err)
	}
	l.tag = tag
}

func (l *lister) waits() <-chan told {
	l.t.Helper()
	// It has heard every mark.
	shelf := shelfOf(l.t, l.phone)
	tag, out := l.tag, make(chan told, 1)
	go func() {
		changed, list, err := WaitForMarks(context.Background(), l.phone, shelf.ID, shelf.Cursor, nil, &tag)
		out <- told{changed, list, err}
	}()
	return out
}

// told waits for the answer, which says the list is another.
func (l *lister) told(what string, got <-chan told, within time.Duration) {
	l.t.Helper()
	select {
	case a := <-got:
		if !a.list || a.err != nil {
			l.t.Errorf("%s: %+v, want the list", what, a)
		}
	case <-time.After(within):
		l.t.Errorf("%s: not told in %v", what, within)
	}
}

func quiet(t *testing.T, what string, got <-chan told) {
	t.Helper()
	select {
	case a := <-got:
		t.Errorf("%s: told %+v", what, a)
	case <-time.After(300 * time.Millisecond):
	}
}

// walked waits for no list to be being worked out, and has the next not wait.
func walked(t *testing.T, rest time.Duration) {
	t.Helper()
	was := listRest
	listRest = rest
	settle := func() {
		for {
			walking.Lock()
			idle := !walking.running
			if idle {
				walking.next = time.Time{}
			}
			walking.Unlock()
			if idle {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	settle()
	t.Cleanup(func() { held(t, 0); settle(); listRest = was })
}

// A phone waiting with its list is told when it is another, whatever made it
// so, and by nothing else.
func TestAQuestionIsHeldUntilTheListIsAnother(t *testing.T) {
	if path := os.Getenv("HOMEWEND_TEST_TURN"); path != "" {
		c, err := library.OpenCatalog(path)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if _, _, err := c.Turn(library.Key{Hash: os.Getenv("HOMEWEND_TEST_HASH"), Profile: "ron"}, 1); err != nil {
			t.Fatal(err)
		}
		return
	}
	root, ron, ann, hash, _ := twoPhones(t)
	if _, err := ReceivePhoto(context.Background(), ann, hash, "IMG_1.JPG", 14, unread{}); err != nil {
		t.Fatal(err)
	}
	was := marksEvery
	marksEvery = 50 * time.Millisecond
	t.Cleanup(func() { marksEvery = was })
	walked(t, 0)
	stop := func(ids ...string) {
		for _, id := range ids {
			stopWaiting(id)
		}
		held(t, 0)
	}
	key := library.Key{Hash: hash, Profile: "ron"}
	rons, anns := listerOf(t, ron), listerOf(t, ann)
	r, a := rons.waits(), anns.waits()
	held(t, 2)
	quiet(t, "nothing", r)
	if runtime.GOOS == "linux" {
		for _, f := range openFiles(t) {
			if strings.HasPrefix(f, root) {
				t.Errorf("%s is open while phones wait", f)
			}
		}
	}
	if _, err := Turn(key, false); err != nil {
		t.Fatal(err)
	}
	rons.told("turned", r, 5*time.Second)
	// It says so until the phone reads it.
	rons.told("turned, between two questions", rons.waits(), 5*time.Second)
	rons.reads()
	r = rons.waits()
	quiet(t, "read", r)
	if _, err := Mark(library.Archived, []library.Key{key}, true); err != nil {
		t.Fatal(err)
	}
	rons.told("archived", r, 5*time.Second)
	rons.reads()
	r = rons.waits()
	held(t, 2)
	photo := "another of ron's"
	if _, err := ReceivePhoto(context.Background(), ron, hashText(photo), "IMG_2.JPG", int64(len(photo)), strings.NewReader(photo)); err != nil {
		t.Fatal(err)
	}
	rons.told("come in", r, 5*time.Second)
	quiet(t, "ann's, of ron's", a)
	// A heart is not in the list.
	rons.reads()
	r = rons.waits()
	held(t, 2)
	kept := listOfProfile("ron").Load()
	if _, err := Mark(library.Favorite, []library.Key{key}, true); err != nil {
		t.Fatal(err)
	}
	if got := <-r; !got.changed || got.list || got.err != nil {
		t.Errorf("a heart: %+v", got)
	}
	r = rons.waits()
	quiet(t, "a heart", r)
	if now := listOfProfile("ron").Load(); now.began != kept.began {
		t.Error("a heart had the list worked out")
	}
	other := exec.Command(os.Args[0], "-test.run=^TestAQuestionIsHeldUntilTheListIsAnother$")
	other.Env = append(os.Environ(), "HOMEWEND_TEST_TURN="+catalogPath(root), "HOMEWEND_TEST_HASH="+hash)
	if out, err := other.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	rons.told("another process", r, 5*time.Second)
	quiet(t, "ann's, all along", a)
	stop(ron.ID, ann.ID)
	// One that says no list is told of none.
	old := waits(context.Background(), ron, shelfOf(t, ron))
	held(t, 1)
	if _, err := Turn(key, false); err != nil {
		t.Fatal(err)
	}
	quiet(t, "no list asked of", old)
	stop(ron.ID)
	// No tag of any list.
	for _, tag := range []string{"", "x", `"` + rons.tag + `"`, rons.tag + "0", strings.Repeat("9", 4000), strings.Repeat("0", 32)} {
		rons.tag = tag
		rons.told("a bad tag", rons.waits(), 5*time.Second)
	}
	// Another library, with the same photo.
	rons.reads()
	r = rons.waits()
	held(t, 1)
	second := t.TempDir()
	if err := SetDefaultLibrary(second); err != nil {
		t.Fatal(err)
	}
	photo = "the second's"
	if _, err := ReceivePhoto(context.Background(), ron, hashText(photo), "IMG_3.JPG", int64(len(photo)), strings.NewReader(photo)); err != nil {
		t.Fatal(err)
	}
	if got := <-r; got.err == nil && !got.list && !got.changed {
		t.Errorf("another library: %+v", got)
	}
	rons.told("another library", rons.waits(), 5*time.Second)
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	// Its disk away.
	rons.reads()
	r = rons.waits()
	held(t, 1)
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Fatal(err)
	}
	if got := <-r; !errors.As(got.err, new(LibraryGoneError)) {
		t.Errorf("the disk away: %+v", got)
	}
	if err := os.Rename(root+"-away", root); err != nil {
		t.Fatal(err)
	}
	quiet(t, "the disk back", rons.waits())
	stop(ron.ID)
}

// While the library changes without pause its list is worked out now and then,
// and the last change is told.
func TestAnImportDoesNotWorkTheListOutAtEachPhoto(t *testing.T) {
	_, ron, _, hash, _ := twoPhones(t)
	was := marksEvery
	marksEvery = 20 * time.Millisecond
	t.Cleanup(func() { marksEvery = was })
	walked(t, 700*time.Millisecond)
	rons := listerOf(t, ron)
	walks, last := 0, listOfProfile("ron").Load().began
	r := rons.waits()
	held(t, 1)
	began := time.Now()
	for time.Since(began) < 2*time.Second {
		if _, err := Turn(library.Key{Hash: hash, Profile: "ron"}, false); err != nil {
			t.Fatal(err)
		}
		// A heart of another beside it has the list looked at as often.
		lookNow()
		if now := listOfProfile("ron").Load().began; now != last {
			walks, last = walks+1, now
			// Asked of with the last, it waits on: never read by the phone.
			rons.tag = listOfProfile("ron").Load().tag
			rons.told("as it changes", r, 5*time.Second)
			r = rons.waits()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if walks < 2 || walks > 4 {
		t.Errorf("worked out %d times in 2 s", walks)
	}
	stopWaiting(ron.ID)
	<-r
	rons.reads()
	r = rons.waits()
	held(t, 1)
	for range 3 {
		if _, err := Turn(library.Key{Hash: hash, Profile: "ron"}, false); err != nil {
			t.Fatal(err)
		}
	}
	rons.told("the last change", r, 3*time.Second)
	rons.reads()
	quiet(t, "read", rons.waits())
	stopWaiting(ron.ID)
	held(t, 0)
}

// A list worked out for a phone's own question, of a library that has changed
// since, has no other phone told: each would have the others ask without end.
func TestPhonesDoNotHaveEachOtherAskWithoutEnd(t *testing.T) {
	_, ron, _, hash, _ := twoPhones(t)
	walked(t, time.Hour)
	rons := listerOf(t, ron)
	has := rons.tag
	for range 2 {
		if _, err := Turn(library.Key{Hash: hash, Profile: "ron"}, false); err != nil {
			t.Fatal(err)
		}
		if has == rons.tag {
			rons.reads()
		}
	}
	shelf := shelfOf(t, ron)
	w := &waiter{profile: "ron", shelf: shelf.ID, after: shelf.Cursor, list: &has, told: make(chan told, 1)}
	if stale := lookFor([]*waiter{w}); len(w.told) != 0 || len(stale) != 1 {
		t.Errorf("told %d, to be worked out %v", len(w.told), stale)
	}
	rons.reads()
	if stale := lookFor([]*waiter{w}); len(w.told) != 1 || len(stale) != 0 {
		t.Errorf("the library's now: told %d, to be worked out %v", len(w.told), stale)
	}
}
