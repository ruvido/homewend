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

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// tidying is the one run that moves files in and out of the Trash, what each
// library was asked since it began, and the libraries looked at whole.
var tidying = struct {
	sync.Mutex
	running bool
	asked   map[string][]func() error
	done    map[string]bool
}{asked: map[string][]func() error{}, done: map[string]bool{}}

// markTidy is Catalog.Mark, and for the Trash the files follow beside the
// request: a share takes its time. The catalog c is closed. A photo put in the
// Trash is written once, with its file's new place, unless others place
// photos now: then its file goes later, as a restored one's does.
func markTidy(c *library.Catalog, state library.State, photos []library.Key, on bool) ([]library.Key, error) {
	root, err := ChosenLibrary()
	if state != library.Trashed || err != nil {
		defer c.Close()
		return c.Mark(state, photos, on)
	}
	catalog, turn := catalogPath(root), trashTurn(root)
	if unlock, err := lockLibrary(root); on && err == nil {
		done, moves, err := library.PutInTrash(root, c, photos)
		unlock()
		if err != nil || len(moves) == 0 {
			c.Close()
		} else {
			follow(root, func() error {
				left, err := library.MoveIn(root, c, moves, turn)
				c.Close()
				if len(left) > 0 {
					_, more := library.TidyTrash(root, catalog, left, turn)
					err = errors.Join(err, more)
				}
				return err
			})
		}
		return done, err
	} else if err == nil {
		unlock()
	}
	done, err := c.Mark(state, photos, on)
	c.Close()
	if err == nil && len(done) > 0 {
		follow(root, func() error {
			_, err := library.TidyTrash(root, catalog, done, turn)
			return err
		})
	}
	return done, err
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

// follow runs do beside the requests, after what was asked before it. What it
// could not do, others placing photos, is found by a look at the whole library.
func follow(root string, do func() error) {
	tidying.Lock()
	run := !tidying.running
	tidying.running = true
	tidying.asked[root] = append(tidying.asked[root], do)
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
			for root, all := range now {
				for _, do := range all {
					if err := do(); errors.Is(err, ErrLibraryBusy) {
						lookAgain(root)
					} else if err != nil {
						log.Printf("the Trash: %v", err)
					}
				}
			}
		}
	})
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
	follow(root, func() error {
		gone, err := library.TidyTrash(root, catalogPath(root), nil, trashTurn(root))
		forgetPictures(gone)
		return err
	})
}

// EmptyTrash deletes for good those of photos that are in the Trash, and says
// those it left there: the ones shown to the person who asked, no others.
func EmptyTrash(photos []library.Key) ([]library.Key, error) {
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
	left, gone, err := library.EmptyTrash(root, catalogPath(root), photos, turn)
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
