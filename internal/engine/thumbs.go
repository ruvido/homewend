// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// Thumbs is how many photos of the library have been given a thumbnail, or
// cannot be.
type Thumbs struct {
	Done int `json:"done"`
	Of   int `json:"of"`
}

var thumbs struct {
	sync.Mutex
	Thumbs
}

// ThumbsMade is where KeepThumbnails is.
func ThumbsMade() Thumbs {
	thumbs.Lock()
	defer thumbs.Unlock()
	return thumbs.Thumbs
}

// thumbsEvery is how often the library is asked whether it changed.
var thumbsEvery = 2 * time.Second

// KeepThumbnails makes the thumbnails the library lacks, newest photo first,
// one at a time, until ctx ends: seen for the first time, a photo has its own.
// It looks again when the library has changed. While an import changes it
// without pause, listing it takes no more than a twentieth of the time.
func KeepThumbnails(ctx context.Context) {
	// Tried and not made, a file no decoder reads: not again this run.
	failed := map[string]bool{}
	seen := ""
	for {
		rest := thumbsEvery
		if now, err := LibraryChanges(); err == nil && now != seen {
			listing, ok := makeThumbnails(ctx, now, failed)
			if rest = max(rest, 20*listing); ok {
				seen = now
			} else {
				// No library, or its disk taken out: the same, back, is looked at.
				seen = ""
				setThumbs(Thumbs{})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(rest):
		}
	}
}

func setThumbs(t Thumbs) {
	thumbs.Lock()
	thumbs.Thumbs = t
	thumbs.Unlock()
}

// makeThumbnails makes them for the library as it is at changes, and says
// how long listing it took and whether it was there to the end. It stops, for
// the caller to begin again, when the library changes: another chosen, a
// newer photo.
func makeThumbnails(ctx context.Context, changes string, failed map[string]bool) (listing time.Duration, ok bool) {
	began := time.Now()
	root, err := DefaultLibrary()
	if err != nil || root == "" {
		return 0, false
	}
	s, err := LoadSettings()
	if err != nil || libraryAway(root, s.Filled) {
		return 0, false
	}
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return 0, false
	}
	photos, err := c.Page(library.Filter{})
	c.Close()
	if err != nil {
		return 0, false
	}
	folder, err := picturesDir("thumbs")
	if err != nil {
		return 0, false
	}
	kept := thumb.Kept(folder)
	var todo []library.Photo
	// As it came, for a phone; with its edit, for the window.
	lacks := func(p library.Photo, edit string) bool {
		name := thumb.Edited(p.Hash, edit)
		return !kept[name] && !failed[name]
	}
	for _, p := range photos {
		if lacks(p, "") || lacks(p, p.Edit) {
			todo = append(todo, p)
		}
	}
	set := func(done int) { setThumbs(Thumbs{Done: done, Of: len(photos)}) }
	listing = time.Since(began)
	again := time.Now().Add(max(thumbsEvery, 20*listing))
	for i, p := range todo {
		set(len(photos) - len(todo) + i)
		// Those on the screen first: it works while no one else does.
		for len(thumbSlots) > 0 && ctx.Err() == nil {
			time.Sleep(50 * time.Millisecond)
		}
		if ctx.Err() != nil {
			return listing, true
		}
		if time.Now().After(again) {
			if now, err := LibraryChanges(); err == nil && now != changes {
				return listing, true
			}
			again = time.Now().Add(thumbsEvery)
		}
		for _, edit := range slices.Compact([]string{p.Edit, ""}) {
			if !lacks(p, edit) {
				continue
			}
			// With an edit, what is not kept was drawn as it came.
			if _, made, err := Thumbnail(ctx, p.Key(), edit); err != nil || edit != "" && !made {
				// The disk taken out: none of the rest is there to be tried.
				if libraryAway(root, s.Filled) {
					return listing, false
				}
				failed[thumb.Edited(p.Hash, edit)] = true
			}
		}
	}
	set(len(photos))
	return listing, true
}
