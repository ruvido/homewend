// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/progress"
)

// ErrLibraryBusy is another run placing photos in the library: an import,
// or a download, in this program or in another one (the window and the
// command line are two). Two at once would each move files the other is
// counting on.
var ErrLibraryBusy = errors.New("another import or download is placing photos in this library")

// lockLibrary takes the library's turn to place photos, or says at once that
// another run has it. The turn is a lock on a file in the library, held by
// the system: it goes with a program that dies.
func lockLibrary(root string) (unlock func(), err error) {
	dir := filepath.Join(root, library.WorkDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	switch err := tryLock(file); {
	case errors.Is(err, errLocked):
		file.Close()
		return nil, ErrLibraryBusy
	case err != nil:
		// A disk that cannot lock at all (some network shares): the run
		// goes on unguarded rather than not at all.
		file.Close()
		return func() {}, nil
	}
	unlock = func() { unlockFile(file); file.Close() }
	// Nobody else is moving: what a killed run left is finished first, a
	// rename before the Trash's moves it may have renamed.
	err = library.FinishRename(root, catalogPath(root), followRename(root))
	if err == nil {
		err = library.FinishMoves(root, catalogPath(root), unlisted)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// lockPoll is how often a run waiting for the library's turn asks for it.
const lockPoll = 100 * time.Millisecond

// takeLibrary takes the library's turn, waiting a moment for a run that
// holds it for one photo: a date read from a name, a phone's photo placed.
// One that holds it longer is busy.
func takeLibrary(ctx context.Context, root string) (unlock func(), err error) {
	soon, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	unlock, err = waitLibrary(soon, root, nil)
	if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
		err = ErrLibraryBusy
	}
	return unlock, err
}

// waitLibrary waits for the library's turn, asking again every lockPoll,
// or for ctx to end. A download in the background waits for an
// import to finish placing rather than failing.
func waitLibrary(ctx context.Context, root string, emit progress.Func) (unlock func(), err error) {
	for said := false; ; said = true {
		unlock, err := lockLibrary(root)
		if !errors.Is(err, ErrLibraryBusy) {
			return unlock, err
		}
		// Said once, so the download does not look stuck.
		if !said {
			emit.Emit(progress.Event{Stage: progress.Busy})
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockPoll):
		}
	}
}

// errLocked is a lock another run holds.
var errLocked = errors.New("locked")
