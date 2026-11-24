// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
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

// KeepThumbnails makes the thumbnails the library lacks, newest photo first,
// one at a time, until ctx ends: seen for the first time, a photo has its own.
// It looks again when the library has changed.
func KeepThumbnails(ctx context.Context) {
	// Tried and not made, a file no decoder reads: not again this run.
	failed := map[string]bool{}
	seen := ""
	for {
		// Set before the look: what changes during it is looked at again.
		if now, err := LibraryChanges(); err == nil && now != seen {
			seen = now
			makeThumbnails(ctx, failed)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

func makeThumbnails(ctx context.Context, failed map[string]bool) {
	root, err := DefaultLibrary()
	if err != nil || root == "" {
		return
	}
	s, err := LoadSettings()
	if err != nil || libraryAway(root, s.Filled) {
		return
	}
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return
	}
	photos, err := c.Page(library.Filter{})
	c.Close()
	if err != nil {
		return
	}
	made, err := thumbsKept()
	if err != nil {
		return
	}
	var todo []library.Photo
	for _, p := range photos {
		if !made[p.Hash] && !failed[p.Hash] {
			todo = append(todo, p)
		}
	}
	set := func(done int) {
		thumbs.Lock()
		thumbs.Thumbs = Thumbs{Done: done, Of: len(photos)}
		thumbs.Unlock()
	}
	for i, p := range todo {
		set(len(photos) - len(todo) + i)
		// Those on the screen first: it works while no one else does.
		for len(thumbSlots) > 0 && ctx.Err() == nil {
			time.Sleep(50 * time.Millisecond)
		}
		if ctx.Err() != nil {
			return
		}
		file := filepath.Join(root, filepath.FromSlash(p.Path))
		if _, err := thumbnailOf(ctx, p.Hash, func() (string, error) { return file, nil }); err != nil {
			// The disk taken out: none of the rest is there to be tried.
			if libraryAway(root, s.Filled) {
				break
			}
			failed[p.Hash] = true
		}
	}
	set(len(photos))
}

// thumbsKept are the photos that have a thumbnail, by hash: read from the
// folders, not asked one by one.
func thumbsKept() (map[string]bool, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(cache, "homewend", "thumbs")
	folders, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	kept := map[string]bool{}
	for _, f := range folders {
		files, _ := os.ReadDir(filepath.Join(dir, f.Name()))
		for _, file := range files {
			if hash, ok := strings.CutSuffix(file.Name(), ".jpg"); ok {
				kept[hash] = true
			}
		}
	}
	return kept, nil
}
