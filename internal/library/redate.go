// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Redate is a date given by the person to photos whose camera had it wrong:
// a camera whose clock was reset dates them 1 January of its first year
// (measured 09/11 on a Fujifilm card). Nobody can tell that from the photo,
// so nothing guesses it: it is said, before the import, by whoever knows.
type Redate struct {
	// Names are the files it is for: a name, in any case, or the path
	// below the folder when two of its folders hold the same name.
	Names []string `json:"names"`
	// When is the date and time of the first of them, by the camera's
	// clock: a clock, like EXIF, kept unmoved.
	When time.Time `json:"when"`
}

// UnknownNameError is a name given, to date or to bring back, that the
// folder does not have.
type UnknownNameError struct{ Name string }

func (e UnknownNameError) Error() string { return e.Name + " is not in the folder" }

// AmbiguousNameError is a name two files of the folder have: two cameras
// both name a photo DSCF0004.JPG. Which one is meant is said by its path.
type AmbiguousNameError struct {
	Name  string
	Paths []string // below the folder
}

func (e AmbiguousNameError) Error() string { return e.Name + " is in more than one folder" }

// given is the date each file of the redates gets, by its path: the first
// of them by the camera's own clock takes When, the others keep their
// distance from it — seventy seconds apart on the card are seventy seconds
// apart here. A file the camera gave no date takes When. A name the folder
// does not have, or has twice, is said before anything is copied.
func given(redates []Redate, f Folder) (map[string]time.Time, error) {
	dates := map[string]time.Time{}
	for _, r := range redates {
		var paths []string
		for _, name := range r.Names {
			found, err := named(name, f)
			if err != nil {
				return nil, err
			}
			paths = append(paths, found)
		}
		var first time.Time
		clocks := map[string]time.Time{}
		for _, p := range paths {
			if when, _, ok := cameraDate(p); ok {
				clocks[p] = when
				if first.IsZero() || when.Before(first) {
					first = when
				}
			}
		}
		for _, p := range paths {
			if clock, ok := clocks[p]; ok {
				dates[p] = r.When.Add(clock.Sub(first))
			} else {
				dates[p] = r.When
			}
		}
	}
	return dates, nil
}

// named is the one file of f that name means: its name, or its path below
// the folder, in any case — a card's FAT keeps names as the camera wrote
// them, and nobody types them so.
func named(name string, f Folder) (string, error) {
	match := func(name string) []string {
		want := filepath.ToSlash(filepath.Clean(name))
		var found []string
		for _, item := range f.Items {
			rel, err := filepath.Rel(f.Root, item.Path)
			if err != nil {
				continue
			}
			if strings.EqualFold(filepath.ToSlash(rel), want) {
				found = append(found, item.Path)
			}
		}
		// A bare name is the path of a file at the folder's top first,
		// then any file's name: A.JPG beside edited/A.JPG is the top one.
		if len(found) > 0 || strings.Contains(want, "/") {
			return found
		}
		for _, item := range f.Items {
			if strings.EqualFold(filepath.Base(item.Path), want) {
				found = append(found, item.Path)
			}
		}
		return found
	}
	found := match(name)
	// Not below the folder as typed: a path the shell completed — whole,
	// or from where the command runs — when it leads into the folder.
	if len(found) == 0 && strings.ContainsAny(name, `/\`) {
		if rel, ok := Below(name, f.Root); ok {
			found = match(rel)
		}
	}
	switch len(found) {
	case 0:
		return "", UnknownNameError{Name: name}
	case 1:
		return found[0], nil
	}
	e := AmbiguousNameError{Name: name}
	for _, p := range found {
		rel, _ := filepath.Rel(f.Root, p)
		e.Paths = append(e.Paths, filepath.ToSlash(rel))
	}
	return "", e
}

// RealPath is p whole, its links followed: one folder, however it was
// named — from here, from /, through a link.
func RealPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p, err
	}
	return filepath.EvalSymlinks(abs)
}

// Below is path, as typed, relative to root and with /, when it leads into
// root: as it is, or through the links of its folders. Its own name is kept
// as typed, for its case to be matched as names are.
func Below(path, root string) (string, bool) {
	if r, err := RealPath(root); err == nil {
		root = r
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	if !within(abs, root) {
		dir, err := RealPath(filepath.Dir(abs))
		if err != nil {
			return "", false
		}
		abs = filepath.Join(dir, filepath.Base(abs))
	}
	if !within(abs, root) {
		return "", false
	}
	rel, err := filepath.Rel(root, abs)
	return filepath.ToSlash(rel), err == nil
}

// redateInLibrary gives a date to a photo the library already holds: it
// moves to the folder of that date, on the same disk, and the catalog says
// the date; a date the person gave is said in a .xmp beside it too, a
// guessed one is not. The file is not written. The same photo in another
// library keeps its own place and date.
func redateInLibrary(root string, catalog *Catalog, k Key, capture Capture, opt Options) error {
	record, err := catalog.Photo(k)
	if err != nil {
		return err
	}
	from := filepath.Join(root, filepath.FromSlash(record.Path))
	capture.Origin = record.Origin
	to, err := destination(from, k, root, capture, opt, catalog)
	if err != nil {
		return err
	}
	if to != from {
		// The catalog first: the new name is the photo's before anything
		// moves, and a move that fails gives the old one back.
		if err := catalog.setPath(k, relativeTo(root, to), filepath.Base(to)); err != nil {
			return err
		}
		if err := move(from, to); err != nil {
			catalog.setPath(k, record.Path, record.Name)
			return err
		}
		// Google's sidecar, kept under the library's metadata folder by the
		// photo's path, goes with it.
		meta := func(p string) string { return filepath.Join(root, WorkDir, "metadata", relativeTo(root, p)+".json") }
		if exists(meta(from)) {
			if err := move(meta(from), meta(to)); err != nil {
				return err
			}
		}
		// A .xmp of an earlier date goes too, and is written again below.
		os.Remove(from + ".xmp")
		// The month and the year it leaves, if nothing else is in them: an
		// empty 2018 would say a year the camera never saw. Remove fails on
		// a folder that is not empty, which is the check.
		// Never the profile's own folder: undated/ is right under it.
		month := filepath.Dir(from)
		if os.Remove(month) == nil && filepath.Dir(month) != profileRoot(root, opt) {
			os.Remove(filepath.Dir(month))
		}
	}
	if err := catalog.setDate(k, capture); err != nil {
		return err
	}
	if capture.Source != FromGiven {
		return nil
	}
	return writeXMP(to, capture.When)
}

// GiveDates gives photos of the library at root a date, chosen in the
// library: the earliest of them is taken at when, the others keep their
// distance from it, as dates given before an import do; one with no date
// takes when. Each stays in its own library. A photo whose file is gone is
// left out, before anything moves: a run half done, done again, would move
// the first ones twice. It says how many moved.
func GiveDates(root string, catalog *Catalog, photos []Key, when time.Time) (int, error) {
	records := make([]Photo, 0, len(photos))
	var earliest time.Time
	for _, k := range photos {
		p, err := catalog.Photo(k)
		if err != nil {
			return 0, err
		}
		if !onDisk(root, p) {
			continue
		}
		records = append(records, p)
		if !p.Taken.IsZero() && (earliest.IsZero() || p.Taken.Before(earliest)) {
			earliest = p.Taken
		}
	}
	for i, p := range records {
		at := when
		if !p.Taken.IsZero() {
			at = when.Add(p.Taken.Sub(earliest))
		}
		if err := redateFiled(root, catalog, p, Capture{When: at, Source: FromGiven}); err != nil {
			return i, err
		}
	}
	return len(records), nil
}

// NamedPhotos are the photos of the library that take their name's date
// (dateNamed): those that came in before names were read.
func NamedPhotos(catalog *Catalog) ([]Photo, error) {
	all, err := catalog.photos(`WHERE NOT ` + away)
	return slices.DeleteFunc(all, func(p Photo) bool {
		_, ok := dateNamed(p.capture(), p.Name)
		return !ok
	}), err
}

// DateByName files photos under the date their name says. turn takes the
// library's turn for one photo, so nobody waits for all of them. One that
// cannot move stays as it was and the rest still move; what went wrong is
// returned.
func DateByName(root string, catalog *Catalog, photos []Photo, turn func() (func(), error)) error {
	var failed []error
	for _, p := range photos {
		if !onDisk(root, p) {
			continue
		}
		unlock, err := turn()
		if err != nil {
			return err
		}
		// Read again in its turn: a date given meanwhile stays.
		if p, err = catalog.Photo(p.Key()); err == nil {
			if capture, ok := dateNamed(p.capture(), p.Name); ok {
				err = redateFiled(root, catalog, p, capture)
			}
		}
		unlock()
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", p.Path, err))
		}
	}
	return errors.Join(failed...)
}

// onDisk is whether p's file is in the library: one gone is left as it is.
func onDisk(root string, p Photo) bool {
	_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p.Path)))
	return err == nil
}

// redateFiled gives p the date of capture in the profile it is filed in.
func redateFiled(root string, catalog *Catalog, p Photo, capture Capture) error {
	return redateInLibrary(root, catalog, p.Key(), capture, Options{Profile: p.Profile, SeparateMessaging: true})
}

// writeXMP puts a given date in an XMP file beside the photo, named as
// Immich, digiKam and darktable look for it first: the photo's whole name
// and .xmp. The photo itself is never written. The date is the clock given,
// with this computer's offset at that date: without one, Immich reads it as
// UTC. A time the clock skipped (the hour lost to summer time) keeps the
// time given, with the offset just after it.
func writeXMP(photo string, when time.Time) error {
	offset := time.Date(when.Year(), when.Month(), when.Day(), when.Hour(), when.Minute(), when.Second(), 0, time.Local).Format("-07:00")
	stamp := when.Format("2006-01-02T15:04:05") + offset
	body := "<?xpacket begin=\"\ufeff\" id=\"W5M0MpCehiHzreSzNTczkc9d\"?>\n" + `<x:xmpmeta xmlns:x="adobe:ns:meta/">
 <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">
  <rdf:Description rdf:about=""
    xmlns:exif="http://ns.adobe.com/exif/1.0/"
    xmlns:xmp="http://ns.adobe.com/xap/1.0/"
    exif:DateTimeOriginal="` + stamp + `"
    xmp:CreateDate="` + stamp + `"/>
 </rdf:RDF>
</x:xmpmeta>
<?xpacket end="w"?>
`
	tmp := photo + ".xmp.tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, photo+".xmp")
}
