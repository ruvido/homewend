// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// ReadTakeout lists the media of an unpacked Takeout as items for Organize.
//
// Everything that is Takeout's own convention lives here and not in the
// organiser: the sidecar JSON, "Photos from 2019" being a year and not an
// album, every other folder being an album.
func ReadTakeout(root string) ([]Item, error) {
	files, err := mediaFiles(root)
	if err != nil {
		return nil, err
	}
	folders := map[string][]string{}
	for _, file := range files {
		dir := filepath.Dir(file)
		folders[dir] = append(folders[dir], filepath.Base(file))
	}
	sidecars := map[string]map[string]pairing{}
	for dir, media := range folders {
		sidecars[dir] = sidecarsOf(dir, media)
	}
	items := make([]Item, 0, len(files))
	for _, file := range files {
		dir := filepath.Dir(file)
		s := sidecars[dir][filepath.Base(file)]
		capture := DateOf(file, filepath.Base(dir), s.path)
		if c, ok := ofLivePhoto(capture, file, s.of); ok {
			capture, s.shared = c, false
		}
		items = append(items, Item{
			Path:    file,
			Capture: capture,
			Album:   albumOf(root, file),
			Sidecar: s.path,
			Shared:  s.shared,
		})
	}
	return items, nil
}

// albumOf returns the album a file sits in, or "" when it is in a year folder.
//
// Takeout's layout is Takeout/Google Photos/<folder>/<file>, where <folder> is
// either "Photos from 2019" or an album name.
func albumOf(srcDir, file string) string {
	relative, err := filepath.Rel(srcDir, file)
	if err != nil {
		return ""
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) < 2 {
		return ""
	}
	folder := parts[len(parts)-2]
	if folder == "" || yearFolder.MatchString(folder) {
		return ""
	}
	return folder
}

// mediaFiles lists every non-sidecar file under dir, in a stable order so that
// two runs place the same photo in the same place and suffix collisions the
// same way.
func mediaFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".html") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	return files, err
}
