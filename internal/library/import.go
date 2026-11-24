// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/ruvido/homewend/internal/progress"
)

// ErrNoLink is a hard link the disk refused: the folder is on another disk
// than the library, or on a disk that has none.
var ErrNoLink = errors.New("the disk refused a hard link")

// Imported is what an import did.
type Imported struct {
	Placed  int   `json:"placed"`  // photos and videos now in the library
	Bytes   int64 `json:"bytes"`   // they take, counted once each
	Undated int   `json:"undated"` // of them, with no date the camera wrote
	// Of them, a camera's raw files and the videos: the rest are photos.
	Raw    int `json:"raw"`
	Videos int `json:"videos"`
	Counts
}

// redate gives a photo of the library its date, and counts it.
func (done *Imported) redate(root string, catalog *Catalog, k Key, when time.Time, opt Options) error {
	if err := redateInLibrary(root, catalog, k, Capture{When: when, Source: FromGiven}, opt); err != nil {
		return err
	}
	done.Redated++
	return nil
}

// ImportOptions are how an import brings files in.
type ImportOptions struct {
	// Link gives each file a second name in the library instead of a copy:
	// no space, the same disk needed.
	Link bool
	// Fits is asked before each copy whether the library's disk has room.
	Fits func(size int64) error
	// What the person said, as the look before it heard it.
	Asked
	// Hashes are what a look before read of the folder, when it did: the
	// dates that decide a group are not read again.
	Hashes Hashes
}

// arrivals is where a file from a folder waits, copied and read back, for
// Organize to place it: inside the library, so placing it is a rename.
func arrivals(root string) string { return filepath.Join(root, WorkDir, "import") }

// Import brings f's photos and videos into the library at root, one at a
// time. Each is read once to know its content: one the library already
// holds is not copied again. A new one is copied into the library and read
// back — or, with link, given a second name there, which costs no space and
// needs the same disk — then placed by Organize, which files it by its date.
// Nothing in the folder is changed.
//
// One at a time, so that a card taken out halfway, or a stop asked through
// ctx, leaves in the library every photo already in, and the next run brings
// the rest. fits is asked before each copy whether the library's disk has
// room for it.
func Import(ctx context.Context, f Folder, root string, catalog *Catalog, opt Options, how ImportOptions, emit progress.Func) (Imported, error) {
	done := Imported{Counts: Counts{Unreadable: f.Unreadable}}
	// A name the folder does not have, or has twice, is said before
	// anything is copied.
	dates, err := given(how.Redates, f)
	if err != nil {
		return done, err
	}
	back, err := how.bringsBack(f)
	if err != nil {
		return done, err
	}
	// What a stopped run left here was never placed: it starts again.
	if err := os.RemoveAll(arrivals(root)); err != nil {
		return done, err
	}
	defer os.RemoveAll(arrivals(root))
	// A project's icons, an app's previews: left where they are, as the
	// look said, unless chosen. A date is read only where it decides a
	// group, a card's DCIM never; once stopped, none: the copy below
	// stops before its first file.
	groups, err := classify(f, func(path string) bool { return ctx.Err() == nil && how.Hashes.dated(path) }, how.Choice, emit)
	if err != nil {
		return done, err
	}
	// Stopped while dates were read: the groups are not what the look said.
	if err := ctx.Err(); err != nil {
		return done, err
	}
	in := chosen(f, groups, how.Choice)
	done.LeftOut = leftOut(groups, how.Choice)
	items := slices.DeleteFunc(slices.Clone(f.Items), func(item Item) bool { return !in[item.Path] })
	seen := map[string]bool{}
	// The run's pace: its files' bytes gone through, each read once to be
	// known and, when new, once to be copied, half of its size for each.
	sizes := make([]int64, len(items))
	var all, handled int64
	for i, item := range items {
		sizes[i] = sizeOf(item.Path)
		all += sizes[i]
	}
	moved := func(n int64) { emit.Emit(progress.Event{Stage: progress.Moved, Done: n, Total: all}) }
	for i, item := range items {
		if i > 0 {
			handled += sizes[i-1]
		}
		moved(handled)
		if err := ctx.Err(); err != nil {
			return done, err
		}
		// A file of a few GB is minutes on a card, read once to know it and
		// once to copy it: its bytes are told, at most a few times a second.
		// A photo done within the first quarter of a second tells none: a
		// card of small files would flash its first bytes at each one.
		last, told, copying := time.Now(), false, false
		size := sizes[i]
		tell := func(n int64) {
			if now := time.Now(); now.Sub(last) > 250*time.Millisecond || (told && n == size) {
				last, told = now, true
				// The pace first: the line said with the Copy carries it.
				if copying {
					moved(handled + size/2 + n/2)
				} else {
					moved(handled + n/2)
				}
				emit.Emit(progress.Event{Stage: progress.Copy, N: i + 1, Of: len(items), Name: filepath.Base(item.Path), Done: n, Total: size})
			}
		}
		emit.Emit(progress.Event{Stage: progress.Copy, N: i + 1, Of: len(items), Name: filepath.Base(item.Path)})
		hash := item.Hash
		if hash == "" {
			var err error
			if hash, err = hashReading(item.Path, tell); err != nil {
				done.Unreadable++
				continue
			}
		}
		when, dated := dates[item.Path]
		key := Key{hash, opt.Profile}
		held, err := Holds(catalog, root, key)
		if err != nil {
			return done, err
		}
		v := done.judge(seen, hash, f.Root, item.Path, held, back(item.Path))
		if v == staysOut {
			continue
		}
		if v == inLibrary {
			// Already in this library, and the card is a source too: hiding
			// the account it came from first does not hide it.
			if err := catalog.addOwner(key, opt.Account); err != nil {
				return done, err
			}
		}
		// A date given to a photo already in, or to a twin under its other
		// name: the likeliest case, seen as 2018 in the library after a
		// first import.
		if v != comesIn {
			if dated {
				if err := done.redate(root, catalog, key, when, opt); err != nil {
					return done, err
				}
			}
			continue
		}
		// Brought back, it is filed as new. The arrivals were emptied, so it
		// is no move left halfway either.
		arrival := Item{Capture: Capture{Source: NoDate, Origin: OriginOf("", filepath.Base(item.Path))}, Again: held.Deleted, Arrival: true}
		if dated {
			arrival.Capture.When, arrival.Capture.Source = when, FromGiven
		}
		copying = true
		// In another library, it is not copied again: its file there is
		// given a second name, or a copy where the disk has no hard links.
		src, link := item.Path, how.Link
		shared := ""
		if held.Elsewhere {
			if shared, err = Shared(catalog, root, hash, item.Path); err != nil {
				return done, err
			}
		}
		if shared != "" {
			src, link = shared, true
		}
		placed, err := bringIn(src, filepath.Base(item.Path), hash, i, root, catalog, opt, link, how.Fits, arrival, tell)
		if shared != "" && errors.Is(err, ErrNoLink) {
			placed, err = bringIn(src, filepath.Base(item.Path), hash, i, root, catalog, opt, false, how.Fits, arrival, tell)
		}
		if err != nil {
			return done, err
		}
		done.Placed += placed.Placed
		done.Bytes += placed.Bytes
		done.Undated += placed.Undated
		// What it is, said once: for the counts and for the year it is filed in.
		kind := kindOf(item.Path)
		switch kind {
		case "video":
			done.Videos += placed.Placed
		case "raw":
			done.Raw += placed.Placed
		}
		filed, err := catalog.Photo(key)
		if err != nil {
			return done, err
		}
		// A date given goes beside the copy too, for the photo apps that
		// read it there.
		if dated {
			if err := writeXMP(filepath.Join(root, filepath.FromSlash(filed.Path)), when); err != nil {
				return done, err
			}
			done.Redated++
		}
		// Its month, for the window that counts what came in by month: as
		// it was filed, the catalog's date. bringIn dates it as the look
		// does (the date given, else the camera's), so the two agree.
		e := progress.Event{Stage: progress.Filed, Note: kind}
		if !filed.Taken.IsZero() {
			e.Name, e.N = strconv.Itoa(filed.Taken.Year()), int(filed.Taken.Month())
		}
		emit.Emit(e)
	}
	moved(all)
	done.end(done.Placed)
	return done, nil
}

// kindOf is what a file is beside a photo: "video", "raw", or "".
func kindOf(path string) string {
	switch {
	case IsVideo(path):
		return "video"
	case IsRaw(path):
		return "raw"
	}
	return ""
}

// Held is what a library knows of a photo: where it holds it, below the
// library's folder; Known, listed in the catalog; Deleted, listed and gone
// from the disk, or deleted for good from the Trash; Trashed, in the Trash,
// its file still there. Elsewhere, not
// in this library and in another, its file there.
type Held struct {
	Path                              string
	Known, Deleted, Trashed, Archived bool
	Elsewhere                         bool
}

// Out is whether the photo is kept out of what comes in: deleted, or in
// the Trash.
func (h Held) Out() bool { return h.Deleted || h.Trashed }

// Holds is what the library of k's profile knows of the photo k; with no
// profile, what any library of them all does, one that has its file first. A
// disk that answers late is not a deletion. The look and the copy judge a
// photo here, alike.
func Holds(catalog *Catalog, root string, k Key) (Held, error) {
	held, err := catalog.held()
	if err != nil {
		return Held{}, fmt.Errorf("reading the catalog: %w", err)
	}
	rows, err := held.Query(k.Hash)
	if err != nil {
		return Held{}, fmt.Errorf("reading the catalog: %w", err)
	}
	defer rows.Close()
	var all []Held
	var profiles []string
	for rows.Next() {
		h := Held{Known: true}
		var profile string
		if err := rows.Scan(&profile, &h.Path, &h.Trashed, &h.Archived, &h.Deleted); err != nil {
			return Held{}, err
		}
		all, profiles = append(all, h), append(profiles, profile)
	}
	if err := rows.Err(); err != nil {
		return Held{}, fmt.Errorf("reading the catalog: %w", err)
	}
	rows.Close() // the disk is asked with no row open
	var mine Held
	elsewhere := false
	for i, h := range all {
		_, gone := os.Lstat(filepath.Join(root, filepath.FromSlash(h.Path)))
		missing := errors.Is(gone, fs.ErrNotExist)
		if missing {
			// Moved as it was looked at, it is recorded elsewhere, or being moved.
			now, in, err := catalog.PathOf(Key{k.Hash, profiles[i]})
			if err != nil {
				return Held{}, err
			}
			if in {
				h.Path = now
			}
			missing = !exists(under(root, At(root, h.Path)))
		}
		h.Deleted = h.Deleted || !h.Trashed && missing
		switch {
		case profiles[i] != k.Profile && k.Profile != "":
			elsewhere = elsewhere || !missing
		case !mine.Known || mine.Out():
			mine = h
		}
	}
	mine.Elsewhere = elsewhere && (!mine.Known || mine.Deleted)
	return mine, nil
}

// bringIn copies or links one file into the arrivals, under name, and places
// it as item, which holds all but its path. A capture
// with no date is read from the copy, the camera's own.
func bringIn(src, name, hash string, i int, root string, catalog *Catalog, opt Options, link bool, fits func(int64) error, item Item, copied func(int64)) (Organized, error) {
	// A folder of its own for each file: two cameras both name a photo
	// DSCF0001.JPG. Emptied once the file is placed.
	arrived := filepath.Join(arrivals(root), fmt.Sprint(i), name)
	defer os.RemoveAll(filepath.Dir(arrived))
	if err := os.MkdirAll(filepath.Dir(arrived), 0o755); err != nil {
		return Organized{}, err
	}
	if link {
		// The file a link points at, not the link: a second name for a
		// link would be a link into the folder.
		target, err := filepath.EvalSymlinks(src)
		if err != nil {
			return Organized{}, err
		}
		if err := os.Link(target, arrived); err != nil {
			return Organized{}, fmt.Errorf("%w: %v", ErrNoLink, err)
		}
		// Another name for what was read to know it.
		item.Hash = hash
	} else {
		if err := fits(sizeOf(src)); err != nil {
			return Organized{}, err
		}
		if _, err := copyFile(src, arrived, copied); err != nil {
			return Organized{}, fmt.Errorf("copying %s: %w", src, err)
		}
	}
	item.Path = arrived
	if item.Capture.Source == NoDate {
		if when, source, ok := fileDate(arrived, name); ok {
			item.Capture.When, item.Capture.Source = when, source
		}
	}
	return Organize([]Item{item}, root, catalog, opt, nil)
}
