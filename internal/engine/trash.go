// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// tidying is the one run that moves files in and out of the Trash, the photos
// of each library asked of it since it began (nil: all), and the libraries
// looked at whole.
var tidying = struct {
	sync.Mutex
	running bool
	asked   map[string][]library.Key
	done    map[string]bool
}{asked: map[string][]library.Key{}, done: map[string]bool{}}

// follow moves the files of photos just put in root's Trash, or restored,
// beside the requests: the catalog has said it, and a share takes its time. Not
// done while others place photos, it is by a look at the whole library.
func follow(root string, photos []library.Key) {
	tidying.Lock()
	run := !tidying.running
	tidying.running = true
	if asked, some := tidying.asked[root]; photos == nil || some && asked == nil {
		tidying.asked[root] = nil
	} else {
		tidying.asked[root] = append(asked, photos...)
	}
	tidying.Unlock()
	if !run {
		return
	}
	beside(func() {
		for {
			tidying.Lock()
			now := maps.Clone(tidying.asked)
			clear(tidying.asked)
			tidying.running = len(now) > 0
			tidying.Unlock()
			if len(now) == 0 {
				return
			}
			for root, photos := range now {
				gone, err := library.TidyTrash(root, catalogPath(root), photos, yielding(trashTurn(root)), unlisted)
				forgetPictures(gone)
				if errors.Is(err, ErrLibraryBusy) {
					lookAgain(root)
				} else if err != nil {
					log.Printf("the Trash: %v", err)
				}
			}
		}
	})
}

// trashTurn is root's turn, while it is there: never made again here.
func trashTurn(root string) library.Turn {
	return func() (func(), error) {
		if _, err := os.Stat(catalogPath(root)); err != nil {
			return nil, err
		}
		return takeLibrary(context.Background(), root)
	}
}

// yielding is turn, with a pause before each but the first: whoever waits asks.
func yielding(turn library.Turn) library.Turn {
	first := true
	return func() (func(), error) {
		if !first {
			time.Sleep(2 * lockPoll)
		}
		first = false
		return turn()
	}
}

func lookAgain(root string) {
	tidying.Lock()
	defer tidying.Unlock()
	delete(tidying.done, root)
}

// tidyTrashOnce looks at the whole library, once a run, for what an earlier
// one left: killed, or from before the Trash had a folder.
func tidyTrashOnce(root string) {
	if _, err := os.Stat(catalogPath(root)); root == "" || err != nil {
		return
	}
	tidying.Lock()
	done := tidying.done[root]
	tidying.done[root] = true
	tidying.Unlock()
	// Dates kept waiting by another program's turn.
	if _, waits := datesWaiting.Load(root); waits {
		giveDates(root)
	}
	if done {
		return
	}
	follow(root, nil)
}

// dating is the one run that gives photos the dates phones said, and whether
// it is asked again meanwhile; datesWaiting the libraries whose turn it did
// not get, given them as the turn is let go.
var (
	dating struct {
		sync.Mutex
		running, again bool
	}
	datesWaiting sync.Map
)

// giveDates gives root's photos the dates phones said, beside the requests,
// each in the library's turn.
func giveDates(root string) {
	dating.Lock()
	if dating.running {
		dating.again = true
		dating.Unlock()
		return
	}
	dating.running = true
	dating.Unlock()
	beside(func() {
		for {
			datesWaiting.Delete(root)
			err := library.GiveDueDates(root, catalogPath(root), yielding(trashTurn(root)))
			if errors.Is(err, ErrLibraryBusy) {
				datesWaiting.Store(root, true)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				log.Printf("the dates phones said: %v", err)
			}
			dating.Lock()
			if !dating.again {
				dating.running = false
				dating.Unlock()
				return
			}
			dating.again = false
			dating.Unlock()
		}
	})
}

// EmptyTrash deletes for good those of photos that are in the Trash, and says
// those it left there: the ones shown to the person who asked, no others.
func EmptyTrash(photos []library.Key) ([]library.Key, error) {
	return emptyTrash(photos, time.Now().UnixMilli())
}

// emptyTrash is EmptyTrash, of those not restored after before (ms).
func emptyTrash(photos []library.Key, before int64) ([]library.Key, error) {
	setEmptying(Emptying{0, len(photos)})
	defer setEmptying(Emptying{})
	root, err := LibraryHere()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(catalogPath(root)); errors.Is(err, fs.ErrNotExist) {
		return []library.Key{}, nil
	}
	// Busy now is said before anything is read.
	turn := trashTurn(root)
	unlock, err := turn()
	if err != nil {
		return nil, err
	}
	unlock()
	left, gone, err := library.EmptyTrash(root, catalogPath(root), photos, before, turn, unlisted, func(n int) { setEmptying(Emptying{n, len(photos)}) })
	forgetPictures(gone)
	if err != nil {
		lookAgain(root) // what it claimed and could not record
	}
	// Others placing photos came in between: the rest is left.
	if errors.Is(err, ErrLibraryBusy) && len(left) < len(photos) {
		err = nil
	}
	return left, err
}

// Emptying is how many of the photos an Empty was asked for it has done; Of is
// 0 while none runs.
type Emptying struct {
	Done int `json:"done"`
	Of   int `json:"of"`
}

var emptying struct {
	sync.Mutex
	Emptying
}

// EmptyingNow is where the Empty under way is.
func EmptyingNow() Emptying {
	emptying.Lock()
	defer emptying.Unlock()
	return emptying.Emptying
}

func setEmptying(e Emptying) {
	emptying.Lock()
	emptying.Emptying = e
	emptying.Unlock()
}

// forgetPictures deletes the pictures kept of contents no library has, those
// of a changed file too, which are named after it.
func forgetPictures(hashes []string) {
	for _, dir := range []string{"thumbs", "previews", "developed"} {
		folder, err := picturesDir(dir)
		if err != nil {
			return
		}
		for _, hash := range hashes {
			os.Remove(thumb.Path(folder, hash))
			changed, _ := filepath.Glob(thumb.Path(folder, hash+"-*"))
			for _, file := range changed {
				os.Remove(file)
			}
		}
	}
}

// SeenInTrash is a photo a phone saw in the Trash Ago ms before it asked.
type SeenInTrash struct {
	Hash string `json:"hash"`
	Ago  int64  `json:"ago"`
}

// EmptyTrashOf empties, of phone's library, the photos it saw in the Trash
// and that were not restored since, by the window's Empty and its checks. It
// says those deleted for good, those before too, and those kept: restored
// since. One not of the library, or left in the Trash, is in neither.
func EmptyTrashOf(phone Phone, seen []SeenInTrash) (emptied, kept []string, err error) {
	then := time.Now().UnixMilli()
	// Said twice: since the first time it was seen.
	ago := map[string]int64{}
	for _, s := range seen {
		ago[s.Hash] = max(ago[s.Hash], s.Ago, 0)
	}
	hashes := slices.Collect(maps.Keys(ago))
	states, err := trashSeen(phone, hashes)
	if err != nil {
		return []string{}, []string{}, err
	}
	var empty []library.Key
	for hash, s := range states {
		if s.InTrash && s.Restored <= then-ago[hash] {
			empty = append(empty, library.Key{Hash: hash, Profile: phone.Profile})
		}
	}
	if len(empty) > 0 {
		// One restored after then, read or not, is not deleted: told in the
		// library's turn.
		if _, err := emptyTrash(empty, then); err != nil {
			return []string{}, []string{}, err
		}
		// As they are now: one restored meanwhile is not deleted.
		if states, err = trashSeen(phone, hashes); err != nil {
			return []string{}, []string{}, err
		}
	}
	emptied, kept = []string{}, []string{}
	for hash, s := range states {
		switch {
		case s.Erased:
			emptied = append(emptied, hash)
		case s.Back || s.Restored > then-ago[hash]:
			kept = append(kept, hash)
		}
	}
	return emptied, kept, nil
}

// trashSeen is where the photos hashes of phone's library stand.
func trashSeen(phone Phone, hashes []string) (map[string]library.Seen, error) {
	_, c, err := openHere(library.OpenCatalog)
	if err != nil || c == nil {
		return nil, err
	}
	defer c.Close()
	return c.TrashSeen(phone.Profile, hashes)
}
