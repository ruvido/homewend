// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Filed reports whether root holds photos filed before profiles: years,
// albums or undated straight under it.
func Filed(root string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && (yearDir.MatchString(e.Name()) || oldTopDirs[e.Name()]) {
			return true
		}
	}
	return false
}

var (
	yearDir    = regexp.MustCompile(`^[0-9]{4}$`)
	oldTopDirs = map[string]bool{"albums": true, "undated": true, "messaging": true}
)

// Relayout moves a library filed before profiles into them: each photo to
// its first source's profile, by year and month, a link to it in the library
// of every other source's, and its albums rebuilt beside it. A photo
// with no source goes in fallback's. Nothing is copied and nothing is lost:
// files are renamed on the same disk, and an old album entry is removed only
// once it is the same file as a photo now in place. A run that stops halfway
// is finished by the next: a photo leaves the old layout last.
func Relayout(root string, catalog *Catalog, profileOf func(source string) string, fallback string, loc *time.Location) error {
	photos, err := catalog.Page(Filter{})
	if err != nil {
		return err
	}
	// The photos now in place, by name: an old album entry keeps its photo's
	// name, and os.SameFile says whether it is that photo.
	moved := map[string][]fs.FileInfo{}
	for _, p := range photos {
		home := filepath.Join(root, filepath.FromSlash(p.Path))
		if !exists(home) {
			continue
		}
		if p.Profile == "" {
			if home, err = relayout(root, catalog, p, home, profileOf, fallback, loc); err != nil {
				return err
			}
		}
		if info, err := os.Lstat(home); err == nil {
			moved[filepath.Base(home)] = append(moved[filepath.Base(home)], info)
		}
	}
	return clearOldTops(root, moved)
}

// relayout moves one photo, at old, and says where it is now.
func relayout(root string, catalog *Catalog, p Photo, old string, profileOf func(source string) string, fallback string, loc *time.Location) (string, error) {
	albums, sources, err := catalog.About(p.Key())
	if err != nil {
		return "", err
	}
	profiles := profilesOf(sources, profileOf, fallback)
	// The first's last: until then the photo is still to be moved.
	for i := len(profiles) - 1; ; i-- {
		opt := Options{Location: loc, Profile: profiles[i], SeparateMessaging: true}
		home, err := destination(old, Key{p.Hash, opt.Profile}, root, p.capture(), opt, catalog)
		if err != nil {
			return "", err
		}
		for _, album := range albums {
			if _, err := linkInto(old, filepath.Join(profileRoot(root, opt), "albums", album, filepath.Base(home))); err != nil {
				return "", err
			}
		}
		if i == 0 {
			if err := move(old, home); err != nil {
				return "", err
			}
			if err := catalog.refile(p.Key(), opt.Profile, relativeTo(root, home)); err != nil {
				return "", err
			}
			sidecar := filepath.Join(root, WorkDir, "metadata", filepath.FromSlash(p.Path)+".json")
			if exists(sidecar) {
				err = move(sidecar, filepath.Join(root, WorkDir, "metadata", relativeTo(root, home)+".json"))
			}
			return home, err
		}
		if _, err := linkInto(old, home); err != nil {
			return "", err
		}
		if err := catalog.inTx(func(tx *sql.Tx) error {
			return share(tx, p.Key(), opt.Profile, relativeTo(root, home), albums, func(s string) bool { return profileOf(s) == opt.Profile })
		}); err != nil {
			return "", err
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

// clearOldTops removes what is left of the old layout: album entries that are
// the same file as a photo now filed, then the folders left empty. Anything
// else stays where it is.
func clearOldTops(root string, moved map[string][]fs.FileInfo) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || !(yearDir.MatchString(e.Name()) || oldTopDirs[e.Name()]) {
			continue
		}
		top := filepath.Join(root, e.Name())
		err := filepath.WalkDir(top, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			for _, m := range moved[d.Name()] {
				if os.SameFile(info, m) {
					return os.Remove(path)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("clearing %s: %w", e.Name(), err)
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
