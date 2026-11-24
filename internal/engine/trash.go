// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// tidying is the one run that moves files in and out of the Trash, the
// libraries asked of it since it began, and those done in this run.
var tidying = struct {
	sync.Mutex
	running     bool
	asked, done map[string]bool
}{asked: map[string]bool{}, done: map[string]bool{}}

// tidyTrash moves the files of photos just put in root's Trash, or restored,
// beside the request: the catalog has said it already, and a share takes its
// time.
func tidyTrash(root string) {
	tidying.Lock()
	run := !tidying.running
	tidying.running, tidying.asked[root] = true, true
	tidying.Unlock()
	if !run {
		return
	}
	beside(func() {
		for {
			tidying.Lock()
			now := slices.Collect(maps.Keys(tidying.asked))
			clear(tidying.asked)
			tidying.running = len(now) > 0
			tidying.Unlock()
			if len(now) == 0 {
				return
			}
			for _, root := range now {
				tidyTrashNow(root)
			}
		}
	})
}

// tidyTrashOnce is tidyTrash for what an earlier run left: the photos put in
// the Trash before it had a folder too. Not done while others placed photos,
// it is done the next time.
func tidyTrashOnce(root string) {
	tidying.Lock()
	done := tidying.done[root]
	tidying.Unlock()
	if !done {
		tidyTrash(root)
	}
}

func tidyTrashNow(root string) {
	if _, err := os.Stat(catalogPath(root)); root == "" || err != nil {
		return // none, or its disk is away: there is nothing to move
	}
	gone, err := library.TidyTrash(root, catalogPath(root), func() (func(), error) {
		// Its disk taken out since: never made again here.
		if _, err := os.Stat(catalogPath(root)); err != nil {
			return nil, err
		}
		return takeLibrary(context.Background(), root)
	})
	forgetPictures(gone)
	busy := errors.Is(err, ErrLibraryBusy)
	if err != nil && !busy {
		log.Printf("the Trash: %v", err)
	}
	tidying.Lock()
	defer tidying.Unlock()
	tidying.done[root] = !busy
}

// EmptyTrash deletes for good those of photos that are in the Trash, and says
// those it left there: the ones shown to the person who asked, no others.
func EmptyTrash(photos []library.Key) ([]library.Key, error) {
	root, err := LibraryHere()
	if err != nil {
		return nil, err
	}
	unlock, err := takeLibrary(context.Background(), root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, err := openAt(root, library.OpenCatalog)
	if err != nil || c == nil {
		return []library.Key{}, err
	}
	defer c.Close()
	left, gone, err := library.EmptyTrash(root, c, photos)
	forgetPictures(gone)
	if err != nil {
		// What it claimed and could not record is looked at again.
		tidying.Lock()
		delete(tidying.done, root)
		tidying.Unlock()
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
