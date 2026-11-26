// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ruvido/homewend/internal/raw"
	"github.com/ruvido/homewend/internal/thumb"
)

// A RAF's pictures larger than a thumbnail, and any with an edit, are made
// from its developed picture, kept in the cache's "developed" by the name its
// pictures have. It is made the first time one is asked for, in the
// background: a minute of the helper's at most. Until then they are made
// from the JPEG the camera embedded, under another name and not kept, so the
// developed one replaces them when it is there. The grid's own thumbnails
// stay the camera's: a thousand raw files are not developed to be scrolled.

// cameraMark is after the name of a picture made from the camera's JPEG
// while the raw file is developed, or where it cannot be.
const cameraMark = "-camera"

// developing are the names being developed, or that failed this run: not
// tried again until the app starts again.
var developing sync.Map

// developSlot lets one raw file be developed at a time: one takes every
// core, and a 40 MP one about a gigabyte between the helper and the engine.
var developSlot = make(chan struct{}, 1)

var noHelper sync.Once

// developedCount grows each time a raw file has been developed: a window
// showing one asks for its picture again.
var developedCount atomic.Int64

// Developed is how many raw files have been developed since the app started.
func Developed() int64 { return developedCount.Load() }

// developsFrom is whether a picture of src at size, with an edit or not, is
// made from its developed picture.
func developsFrom(src string, size int, edited bool) bool {
	return strings.EqualFold(filepath.Ext(src), ".raf") && (size > thumb.Size || edited)
}

// developed is the developed picture of the raw file src, kept as name, or ""
// while it is made, or when it cannot be.
func developed(src, name string) string {
	folder, err := picturesDir("developed")
	if err != nil {
		return ""
	}
	dst := thumb.Path(folder, name)
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	if _, busy := developing.LoadOrStore(name, true); busy {
		return ""
	}
	go func() {
		developSlot <- struct{}{}
		defer func() { <-developSlot }()
		err := thumb.Develop(context.Background(), src, dst)
		switch {
		case err == nil:
			developing.Delete(name)
			forgetCamera(name)
			developedCount.Add(1)
		case errors.Is(err, raw.ErrNoHelper):
			noHelper.Do(func() { log.Printf("raw files are shown as their cameras' JPEGs: %v", err) })
		default:
			log.Printf("developing %s: %v", filepath.Base(src), err)
		}
	}()
	return ""
}

// forgetCamera deletes the pictures of name made from the camera's JPEG
// while it was developed.
func forgetCamera(name string) {
	for _, dir := range []string{"thumbs", "previews", "screens"} {
		if folder, err := picturesDir(dir); err == nil {
			files, _ := filepath.Glob(thumb.Path(folder, name+cameraMark+"*"))
			for _, file := range files {
				os.Remove(file)
			}
		}
	}
}

// forgetUndeveloped deletes the pictures of a RAF, with the edit named edit,
// that an older version made from the camera's JPEG under the name the
// developed ones take: found there, they would be shown for ever.
func forgetUndeveloped(name, edit string) {
	if folder, err := picturesDir("developed"); err != nil {
		return
	} else if _, err := os.Stat(thumb.Path(folder, name)); err == nil {
		return
	}
	for _, dir := range []string{"thumbs", "previews", "screens"} {
		folder, err := picturesDir(dir)
		if err != nil {
			return
		}
		if dir != "thumbs" {
			os.Remove(thumb.Path(folder, name))
		}
		if edit != "" {
			os.Remove(thumb.Path(folder, thumb.Edited(name, edit)))
		}
	}
}
