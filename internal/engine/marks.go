// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// MarksAnswer is what a phone is answered, and whose library it is of.
type MarksAnswer struct {
	library.MarkAnswer
	Owner string `json:"owner"`
}

// Marks takes the marks a phone says and answers what changed in its library.
func Marks(phone Phone, q library.MarkQuestion) (MarksAnswer, error) {
	then := time.Now()
	c, err := openMarks()
	if err != nil {
		return MarksAnswer{}, err
	}
	defer c.Close()
	a, err := c.Marks(phone.Profile, q, then)
	if len(a.Said) > 0 {
		lookNow()
	}
	return MarksAnswer{a, phone.Profile}, err
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
	told           chan told
}

type told struct {
	changed bool
	err     error
}

// tell answers a question, once.
func (w *waiter) tell(changed bool, err error) {
	select {
	case w.told <- told{changed, err}:
	default:
	}
}

// WaitForMarks waits until phone's library has marks past after, or its shelf
// is not the one named; false when none came in a while, the phone asked
// again or was unpaired, or ctx ended. The catalog is closed meanwhile.
func WaitForMarks(ctx context.Context, phone Phone, shelf string, after int64) (bool, error) {
	timer := time.NewTimer(marksHeld)
	defer timer.Stop()
	w := &waiter{profile: phone.Profile, shelf: shelf, after: after, told: make(chan told, 1)}
	waiting.Lock()
	if old := waiting.phones[phone.ID]; old != nil {
		old.tell(false, nil)
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
		return t.changed, t.err
	case <-timer.C:
	case <-ctx.Done():
	}
	return false, nil
}

// watchMarks looks for the phones waiting, while there are any: at once when
// told to, and by tick when the library changed.
func watchMarks(tick *time.Ticker) {
	defer tick.Stop()
	seen := ""
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
		if !asked && (err != nil || now == seen) {
			continue
		}
		seen = now
		lookFor(all)
	}
}

// lookFor tells those of waiters that have something to hear, or why it
// cannot be known.
func lookFor(waiters []*waiter) {
	c, err := openMarks()
	if c != nil {
		defer c.Close()
	}
	for _, w := range waiters {
		var shelf library.Shelf
		if err == nil {
			shelf, err = c.Shelf(w.profile)
		}
		if err != nil || shelf.Differs(w.shelf, w.after) {
			w.tell(err == nil, err)
		}
	}
}

// lookNow has the phones waiting looked for, at once.
func lookNow() {
	select {
	case waiting.look <- struct{}{}:
	default:
	}
}

// stopWaiting ends the question of the phone id.
func stopWaiting(id string) {
	waiting.Lock()
	defer waiting.Unlock()
	if w := waiting.phones[id]; w != nil {
		w.tell(false, nil)
	}
}
