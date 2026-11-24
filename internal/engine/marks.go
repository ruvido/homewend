// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
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
		tellPhones()
	}
	return MarksAnswer{a, phone.Profile}, err
}

// openMarks is openHere, a library with nothing in it being none yet: its
// marks begin with its first photo.
func openMarks() (*library.Catalog, error) {
	c, err := openHere()
	if err == nil && c == nil {
		err = ErrNoLibrary
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
// there are any, one goroutine looks for changes; asking it to look now is a
// send on look.
var waiting struct {
	sync.Mutex
	phones map[string]*waiter
	look   chan struct{}
	stop   context.CancelFunc
}

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
	w := &waiter{profile: phone.Profile, shelf: shelf, after: after, told: make(chan told, 1)}
	seen, _ := LibraryChanges()
	waiting.Lock()
	if waiting.phones == nil {
		waiting.phones = map[string]*waiter{}
	}
	if old := waiting.phones[phone.ID]; old != nil {
		old.tell(false, nil)
	}
	waiting.phones[phone.ID] = w
	if waiting.stop == nil {
		var watching context.Context
		watching, waiting.stop = context.WithCancel(context.Background())
		waiting.look = make(chan struct{}, 1)
		go watchMarks(watching, waiting.look, time.NewTicker(marksEvery), seen)
	}
	waiting.Unlock()
	defer func() {
		waiting.Lock()
		defer waiting.Unlock()
		if waiting.phones[phone.ID] == w {
			delete(waiting.phones, phone.ID)
		}
		if len(waiting.phones) == 0 && waiting.stop != nil {
			waiting.stop()
			waiting.stop = nil
		}
	}()
	// What changes from here on is told; what had already is looked for.
	lookFor([]*waiter{w})
	timer := time.NewTimer(marksHeld)
	defer timer.Stop()
	select {
	case t := <-w.told:
		return t.changed, t.err
	case <-timer.C:
	case <-ctx.Done():
	}
	return false, nil
}

// watchMarks looks for the phones waiting when the library changes, until ctx
// ends: at once when told to, and by tick for what others do.
func watchMarks(ctx context.Context, look <-chan struct{}, tick *time.Ticker, seen string) {
	defer tick.Stop()
	for {
		asked := false
		select {
		case <-ctx.Done():
			return
		case <-look:
			asked = true
		case <-tick.C:
		}
		// One that cannot be told is looked at no more than one unchanged.
		now, err := LibraryChanges()
		if !asked && (err != nil || now == seen) {
			continue
		}
		seen = now
		waiting.Lock()
		var all []*waiter
		for _, w := range waiting.phones {
			all = append(all, w)
		}
		waiting.Unlock()
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

// tellPhones has the phones waiting told of a change made here, at once.
func tellPhones() {
	waiting.Lock()
	defer waiting.Unlock()
	if waiting.stop == nil {
		return
	}
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
