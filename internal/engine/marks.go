// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"log"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// MarksAnswer is what a phone is answered, and whose library it is of.
type MarksAnswer struct {
	library.MarkAnswer
	Owner   string `json:"owner"`
	OwnerID string `json:"ownerId"`
}

// Marks takes the marks a phone says and answers what changed in its library.
func Marks(phone Phone, q library.MarkQuestion) (MarksAnswer, error) {
	then := time.Now()
	// A date moves its photo's file: in the library's turn, taken before
	// anything is applied, as the window's Change date.
	root := ""
	if slices.ContainsFunc(q.Marks, func(m library.MarkAction) bool { return m.State == library.Date }) {
		var err error
		if root, err = LibraryHere(); err != nil {
			return MarksAnswer{}, err
		}
		unlock, err := takeLibrary(context.Background(), root)
		if err != nil {
			return MarksAnswer{}, err
		}
		defer unlock()
	}
	c, err := openMarks()
	if err != nil {
		return MarksAnswer{}, err
	}
	defer c.Close()
	var a library.MarkAnswer
	answer := func() error {
		a, err = c.Marks(phone.Profile, q, then)
		return err
	}
	// A copy's mark may be a copy new to the list.
	if len(q.Marks) > 0 && !slices.ContainsFunc(q.Marks, func(m library.MarkAction) bool { return m.Copy != "" }) {
		err = heartsSet(c, answer)
	} else {
		err = answer()
	}
	for _, d := range a.Dated {
		// The mark is kept: what failed is in the log, as the window's own.
		if err := library.GiveMarkedDate(root, c, d); err != nil {
			log.Printf("a phone's date of %s: %v", d.Hash, err)
		}
	}
	if len(a.Said) > 0 {
		lookNow()
	}
	return MarksAnswer{a, phone.Profile, phone.OwnerID}, err
}

// ErrEmptyLibrary is asking the marks of a library with nothing in it yet:
// they begin with its first photo.
var ErrEmptyLibrary = errors.New("the library has nothing in it yet")

// openMarks is openHere, or ErrEmptyLibrary.
func openMarks() (*library.Catalog, error) {
	_, c, err := openHere(library.OpenCatalog)
	if err == nil && c == nil {
		err = ErrEmptyLibrary
	}
	return c, err
}

// How long a phone's question is held, and how often the library is asked
// meanwhile whether another process changed it.
var (
	marksHeld  = 55 * time.Second
	marksEvery = time.Second
)

// listRest is how long no list is worked out for the phones waiting after one
// was, or twenty times what it took: an import changes the library without pause.
var listRest = 2 * time.Second

// walking is the working out of lists for the phones waiting, beside the look.
var walking struct {
	sync.Mutex
	running bool
	next    time.Time
	fresh   []string // the profiles whose lists were, since the last look
}

// waiting are the phones told of the next change, one question each. While
// there are any, one goroutine looks for changes, at once when there is
// something in look. Never two: one held by a slow disk is not joined by others.
var waiting = struct {
	sync.Mutex
	phones   map[string]*waiter
	watching bool
	look     chan struct{}
}{phones: map[string]*waiter{}, look: make(chan struct{}, 1)}

// waiter is one phone's question, and where its answer goes.
type waiter struct {
	profile, shelf string
	after          int64
	states         []library.State
	list           *string // the tag of the list it has; nil, it does not ask
	told           chan told
}

type told struct {
	changed bool
	list    bool // its list is another
	err     error
}

// tell answers a question, once.
func (w *waiter) tell(t told) {
	select {
	case w.told <- t:
	default:
	}
}

// WaitForMarks waits until phone's library has marks of states past after, or its shelf
// is not the one named, or its list's tag is not list, when there is one;
// neither when none came in a while, the phone asked again or was unpaired, or
// ctx ended. The catalog is closed meanwhile.
func WaitForMarks(ctx context.Context, phone Phone, shelf string, after int64, states []library.State, list *string) (changed, listed bool, err error) {
	timer := time.NewTimer(marksHeld)
	defer timer.Stop()
	w := &waiter{profile: phone.Profile, shelf: shelf, after: after, states: states, list: list, told: make(chan told, 1)}
	waiting.Lock()
	if old := waiting.phones[phone.ID]; old != nil {
		old.tell(told{})
	}
	waiting.phones[phone.ID] = w
	if !waiting.watching {
		waiting.watching = true
		go watchMarks(time.NewTicker(marksEvery))
	}
	waiting.Unlock()
	defer func() {
		waiting.Lock()
		if waiting.phones[phone.ID] == w {
			delete(waiting.phones, phone.ID)
		}
		waiting.Unlock()
		lookNow() // with none left, it ends
	}()
	lookNow()
	select {
	case t := <-w.told:
		return t.changed, t.list, t.err
	case <-timer.C:
	case <-ctx.Done():
	}
	return false, false, nil
}

// watchMarks looks for the phones waiting, while there are any: at once when
// told to, and by tick when the library changed or a list is to be worked out.
func watchMarks(tick *time.Ticker) {
	defer tick.Stop()
	seen, stale := "", []string(nil)
	for {
		asked := false
		select {
		case <-waiting.look:
			asked = true
		case <-tick.C:
		}
		waiting.Lock()
		all := slices.Collect(maps.Values(waiting.phones))
		if len(all) == 0 {
			waiting.watching = false
			waiting.Unlock()
			return
		}
		waiting.Unlock()
		// One that cannot be told is looked at no more than one unchanged.
		now, err := LibraryChanges()
		if asked || err == nil && now != seen {
			seen = now
			stale = lookFor(all)
		}
		if len(stale) > 0 && walkLists(stale) {
			stale = nil // it looks again
		}
	}
}

// lookFor tells those of waiters that have something to hear, or why it
// cannot be known, and says whose lists are to be worked out to know.
func lookFor(waiters []*waiter) (stale []string) {
	c, err := openMarks()
	if c != nil {
		defer c.Close()
	}
	var now library.Written
	if err == nil && slices.ContainsFunc(waiters, func(w *waiter) bool { return w.list != nil }) {
		now, err = c.Written()
	}
	walking.Lock()
	fresh := walking.fresh
	walking.fresh = nil
	walking.Unlock()
	for _, w := range waiters {
		var t told
		if err == nil {
			t.changed, err = c.Differs(w.profile, w.shelf, w.after, w.states)
		}
		t.err = err
		if err == nil && w.list != nil {
			kept := listOfProfile(w.profile).Load()
			here := kept != nil && kept.state.Path == now.Path
			// One worked out for a phone's own question is told to the others
			// while it is the library's: as it changes, each would have the
			// others ask, one after the other, without end.
			known := here && (kept.state == now || slices.Contains(fresh, w.profile))
			t.list = !library.IsTag(*w.list) || known && kept.tag != *w.list
			if !t.list && (!here || kept.state != now) && !slices.Contains(stale, w.profile) {
				stale = append(stale, w.profile)
			}
		}
		if t != (told{}) {
			w.tell(t)
		}
	}
	return stale
}

// walkLists works out the lists of profiles and has the phones waiting looked
// for again; false when it is too early to. The answers wait for none of it.
func walkLists(profiles []string) bool {
	walking.Lock()
	defer walking.Unlock()
	if walking.running || time.Now().Before(walking.next) {
		return false
	}
	walking.running = true
	go func() {
		began := time.Now()
		for _, p := range profiles {
			// One that fails is worked out again after the rest; why it did, the
			// look says.
			_, _, _ = PhoneList(context.Background(), Phone{Profile: p})
		}
		walking.Lock()
		walking.running, walking.fresh, walking.next = false, profiles, time.Now().Add(max(listRest, 20*time.Since(began)))
		walking.Unlock()
		lookNow()
	}()
	return true
}

// lookNow has the phones waiting looked for, at once.
func lookNow() {
	select {
	case waiting.look <- struct{}{}:
	default:
	}
}

// renamedWaiting tells the phones waiting on profile that it changed: renamed,
// it is another shelf.
func renamedWaiting(profile string) {
	waiting.Lock()
	defer waiting.Unlock()
	for _, w := range waiting.phones {
		if w.profile == profile {
			w.tell(told{changed: true})
		}
	}
}

// stopWaiting ends the question of the phone id.
func stopWaiting(id string) {
	waiting.Lock()
	defer waiting.Unlock()
	if w := waiting.phones[id]; w != nil {
		w.tell(told{})
	}
}
