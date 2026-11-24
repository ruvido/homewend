// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Filed reports whether root holds photos filed before profiles: years,
// albums or undated straight under it. known are the profiles there are: a
// folder of one is its own, whatever its name looks like.
func Filed(root string, known []string) bool {
	tops, err := oldTops(root, known)
	return err == nil && len(tops) > 0
}

// oldTops are the folders straight under root of the layout before profiles.
func oldTops(root string, known []string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var tops []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() && (yearDir.MatchString(n) || oldTopDirs[n]) && !slices.Contains(known, n) {
			tops = append(tops, n)
		}
	}
	return tops, nil
}

var (
	yearDir    = regexp.MustCompile(`^[0-9]{4}$`)
	oldTopDirs = map[string]bool{"albums": true, "undated": true, "messaging": true}
	// datePath is a photo's path in a profile's folder, or in the layout
	// before profiles (folderOf).
	datePath = regexp.MustCompile(`^(messaging/)?([0-9]{4}/([0-9]{2}|unknown-month)|undated)/[^/]+$`)
)

// Relayout moves a library filed before profiles into them: each photo to
// its first source's profile, by year and month, a link to it in the library
// of every other source's, and its albums rebuilt beside it. A photo
// with no source goes in fallback's; with none, it stays. One in the folder of
// a profile there is (known, or any of the catalog's) is that profile's where
// it is. Nothing is copied and nothing is lost:
// files are renamed on the same disk, and an old album entry is removed only
// once it is the same file as a photo now in place. A run that stops halfway
// is finished by the next.
func Relayout(root string, catalog *Catalog, known []string, profileOf func(source string) string, fallback string, loc *time.Location) error {
	of, err := texts(catalog.db.Query(`SELECT DISTINCT profile FROM library_photos WHERE profile <> ''`))
	if err != nil {
		return err
	}
	known = append(of, known...)
	// Those in the Trash too: left behind, they would be no library's.
	photos, err := catalog.photos(`WHERE profile = ''`)
	if err != nil {
		return err
	}
	for _, p := range photos {
		if first, rest, _ := strings.Cut(p.Path, "/"); slices.Contains(known, first) && datePath.MatchString(rest) {
			old := filepath.Join(root, filepath.FromSlash(p.Path))
			err = fileIn(root, catalog, p, Options{Profile: first}, nil, old, old)
		} else {
			err = relayout(root, catalog, p, profileOf, fallback, loc)
		}
		if err != nil {
			return err
		}
	}
	tops, err := oldTops(root, known)
	if err != nil || len(tops) == 0 {
		return err
	}
	all, err := catalog.photos(``)
	if err != nil {
		return err
	}
	return clearOldTops(root, tops, all)
}

// relayout moves one photo.
func relayout(root string, catalog *Catalog, p Photo, profileOf func(source string) string, fallback string, loc *time.Location) error {
	albums, sources, err := catalog.About(p.Key())
	if err != nil {
		return err
	}
	profiles := profilesOf(sources, profileOf, fallback)
	if profiles[0] == "" {
		return nil
	}
	old := filepath.Join(root, filepath.FromSlash(p.Path))
	opt := Options{Location: loc, Profile: profiles[0], SeparateMessaging: true}
	if !exists(old) {
		// Received again since: that is the photo.
		there, has, err := catalog.PathOf(Key{p.Hash, opt.Profile})
		if err != nil {
			return err
		}
		if has && exists(filepath.Join(root, filepath.FromSlash(there))) {
			return catalog.refile(p.Key(), opt.Profile, there, false)
		}
		// Moved by a run that stopped before it was recorded, or gone.
		home, err := movedTo(root, catalog, p, opt)
		if err != nil || home == "" {
			return err
		}
		return catalog.refile(p.Key(), opt.Profile, relativeTo(root, home), true)
	}
	// The first's last: until then the photo is still to be moved.
	for i := len(profiles) - 1; i > 0; i-- {
		o := opt
		o.Profile = profiles[i]
		home, err := destination(old, Key{p.Hash, o.Profile}, root, p.capture(), o, catalog)
		if err != nil {
			return err
		}
		if err := linkAlbums(root, old, filepath.Base(home), albums, o); err != nil {
			return err
		}
		if _, err := linkInto(old, home); err != nil {
			return err
		}
		if err := catalog.inTx(func(tx *sql.Tx) error {
			_, err := share(tx, p.Key(), o.Profile, relativeTo(root, home), albums, func(s string) bool { return profileOf(s) == o.Profile })
			return err
		}); err != nil {
			return err
		}
	}
	home, err := destination(old, Key{p.Hash, opt.Profile}, root, p.capture(), opt, catalog)
	if err != nil {
		return err
	}
	return fileIn(root, catalog, p, opt, albums, old, home)
}

// fileIn records p, at old, in its library, at home.
func fileIn(root string, catalog *Catalog, p Photo, opt Options, albums []string, old, home string) error {
	// Its library has it already, imported since: that is the photo. Its
	// bytes there, this file is one too many, whether a second name or a copy
	// read to be the same. Others there, a file changed since: nothing is
	// touched, and both stay recorded.
	there, has, err := catalog.PathOf(Key{p.Hash, opt.Profile})
	if err != nil {
		return err
	}
	if its := filepath.Join(root, filepath.FromSlash(there)); has && exists(its) && exists(old) {
		if !same(its, old) {
			return nil
		}
		if err := linkAlbums(root, its, filepath.Base(its), albums, opt); err != nil {
			return err
		}
		return leaveOld(root, catalog, p, opt, its, old)
	}
	if home == old || !exists(old) {
		return catalog.refile(p.Key(), opt.Profile, relativeTo(root, home), !has || !exists(filepath.Join(root, filepath.FromSlash(there))))
	}
	if err := linkAlbums(root, old, filepath.Base(home), albums, opt); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(home), 0o755); err != nil {
		return err
	}
	// Two names, then the record, then one: stopped in between, the photo is
	// still where the catalog says. On a disk with no hard links it is moved,
	// and a run stopped there is finished by movedTo.
	if !exists(home) && os.Link(old, home) != nil {
		if err := move(old, home); err != nil {
			return err
		}
	}
	return leaveOld(root, catalog, p, opt, home, old)
}

// linkAlbums gives a photo at file its entries, named name, in the albums of
// a profile's folder.
func linkAlbums(root, file, name string, albums []string, opt Options) error {
	for _, album := range albums {
		if _, err := linkInto(file, filepath.Join(profileRoot(root, opt), "albums", album, name)); err != nil {
			return err
		}
	}
	return nil
}

// leaveOld records that p is at home, and takes away its old name and its
// sidecar's: unless another photo is recorded there.
func leaveOld(root string, catalog *Catalog, p Photo, opt Options, home, old string) error {
	meta := func(path string) string {
		return filepath.Join(root, WorkDir, "metadata", relativeTo(root, path)+".json")
	}
	if exists(meta(old)) && !exists(meta(home)) {
		if err := os.MkdirAll(filepath.Dir(meta(home)), 0o755); err != nil {
			return err
		}
		if err := keepSidecar(meta(old), meta(home)); err != nil {
			return err
		}
	}
	if err := catalog.refile(p.Key(), opt.Profile, relativeTo(root, home), true); err != nil {
		return err
	}
	if taken, err := catalog.PathTaken(p.Path, Key{}); err != nil || taken || !spare(old, home) {
		return err
	}
	os.Remove(meta(old))
	return os.Remove(old)
}

// spare is whether old may go, home being the same photo under a name of its
// own: not a link to it, nor the same name reached another way, through a
// linked folder or on a disk that ignores capitals.
func spare(old, home string) bool {
	at, err := os.Lstat(home)
	if err != nil || !at.Mode().IsRegular() || !exists(old) {
		return false
	}
	return !linked(filepath.Dir(old), filepath.Dir(home)) || !strings.EqualFold(filepath.Base(old), filepath.Base(home))
}

// Unsettled is whether root has anything for Relayout, read alone.
func Unsettled(root string, catalog *Catalog, known []string) (bool, error) {
	of, err := texts(catalog.db.Query(`SELECT DISTINCT profile FROM library_photos`))
	return slices.Contains(of, "") || Filed(root, append(of, known...)), err
}

// movedTo is where a photo no longer at its old path was moved by a run that
// stopped before it was recorded: a file of its content, no photo's, at the
// names destination would have given it. "" when there is none.
func movedTo(root string, catalog *Catalog, p Photo, opt Options) (string, error) {
	name := filepath.Join(folderOf(root, p.capture(), opt), filepath.Base(p.Path))
	for n := 1; ; n++ {
		target := name
		if n > 1 {
			target = withSuffix(name, n)
		}
		if !exists(target) {
			return "", nil
		}
		taken, err := catalog.PathTaken(relativeTo(root, target), p.Key())
		if err != nil {
			return "", err
		}
		if hash, err := hashOf(target); !taken && err == nil && hash == p.Hash {
			return target, nil
		}
	}
}

// profilesOf are the profiles of a photo's sources, the first source's first.
func profilesOf(sources []string, profileOf func(string) string, fallback string) []string {
	sort.Strings(sources)
	var out []string
	seen := map[string]bool{}
	for _, s := range sources {
		if p := profileOf(s); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{fallback}
	}
	return out
}

// clearOldTops removes what is left of the old layout in tops: entries that
// are the same file as a photo in place (one of photos) and no photo's own
// path, then the folders left empty. Anything else stays where it is.
func clearOldTops(root string, tops []string, photos []Photo) error {
	recorded := make(map[string]bool, len(photos))
	// By name: an old album entry keeps its photo's, and os.SameFile says
	// whether it is that photo.
	named := map[string][]Photo{}
	for _, p := range photos {
		recorded[p.Path] = true
		named[path.Base(p.Path)] = append(named[path.Base(p.Path)], p)
	}
	for _, name := range tops {
		top := filepath.Join(root, name)
		err := filepath.WalkDir(top, func(file string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || recorded[relativeTo(root, file)] {
				return err
			}
			for _, p := range named[d.Name()] {
				if at := filepath.Join(root, filepath.FromSlash(p.Path)); linked(file, at) && spare(file, at) {
					return os.Remove(file)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("clearing %s: %w", name, err)
		}
		removeEmpty(top)
	}
	return nil
}

// removeEmpty removes dir and the folders under it that hold nothing.
func removeEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			removeEmpty(filepath.Join(dir, e.Name()))
		}
	}
	os.Remove(dir) // a folder that still holds something stays
}
