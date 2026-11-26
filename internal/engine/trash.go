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
	if done {
		return
	}
	follow(root, nil)
}

// EmptyTrash deletes for good those of photos that are in the Trash, and says
// those it left there: the ones shown to the person who asked, no others.
func EmptyTrash(photos []library.Key) ([]library.Key, error) {
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
	left, gone, err := library.EmptyTrash(root, catalogPath(root), photos, turn, unlisted, func(n int) { setEmptying(Emptying{n, len(photos)}) })
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
	for _, dir := range []string{"thumbs", "previews"} {
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
