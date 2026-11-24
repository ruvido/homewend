// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"cmp"
	"io/fs"
	"path/filepath"
	"strings"
)

// The files a library takes in, and their MIME types. Anything else in a
// folder — a camera's firmware, its database, a project's code — stays where
// it is and is said. The types are those of the freedesktop.org shared MIME
// database or its aliases, and Android's for SRW: the system's tables differ,
// and a month's tag would with them.
var (
	photoTypes = map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".heic": "image/heic", ".heif": "image/heif", ".png": "image/png", ".gif": "image/gif",
		".webp": "image/webp", ".tif": "image/tiff", ".tiff": "image/tiff", ".bmp": "image/bmp", ".avif": "image/avif",
		".dng": "image/x-adobe-dng", ".cr2": "image/x-canon-cr2", ".cr3": "image/x-canon-cr3", ".nef": "image/x-nikon-nef", ".arw": "image/x-sony-arw", ".raf": "image/x-fuji-raf",
		".orf": "image/x-olympus-orf", ".rw2": "image/x-panasonic-rw2", ".pef": "image/x-pentax-pef", ".srw": "image/x-samsung-srw",
	}
	videoTypes = map[string]string{
		".mp4": "video/mp4", ".mov": "video/quicktime", ".m4v": "video/mp4", ".3gp": "video/3gpp", ".avi": "video/x-msvideo", ".mkv": "video/x-matroska",
		".webm": "video/webm", ".mts": "video/mp2t", ".m2ts": "video/mp2t",
	}
)

// IsVideo is whether the file is a video, by its extension.
func IsVideo(path string) bool { return videoTypes[strings.ToLower(filepath.Ext(path))] != "" }

// IsMedia is whether the file is a photo or a video, by its extension.
func IsMedia(path string) bool { return typeOf(path) != "" }

// typeOf is the file's MIME type, by its extension, "" for what is no photo
// and no video.
func typeOf(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return cmp.Or(photoTypes[ext], videoTypes[ext])
}

// TypeOf is the type of the file at path as a phone is told it.
func TypeOf(path string) string { return cmp.Or(typeOf(path), "application/octet-stream") }

// systemNames are what an operating system writes on a disk for itself: a
// card read on a Mac carries .Spotlight-V100, .fseventsd and a "._" twin of
// every file (measured 09/11, 94 of 131 files on a camera's card). They are
// nobody's files, so they are not listed at all.
var systemNames = map[string]bool{
	"System Volume Information": true,
	"$RECYCLE.BIN":              true,
}

func isSystem(name string) bool { return strings.HasPrefix(name, ".") || systemNames[name] }

// Folder is what is under a folder: the photos and videos as items for
// Organize, the other files, and what could not be read. An item's date is
// not read yet: PlanImport reads it with the rest of the file.
type Folder struct {
	Root       string // the folder read
	Items      []Item
	Others     []string
	Unreadable int
}

// ReadFolder lists what is under dir. A file or folder that cannot be read
// is noted and passed over: one damaged file on a card must not hide the
// rest of it.
//
// A folder's conventions are not Takeout's: there is no sidecar, and no
// folder name is a year. The date is the camera's, or none.
func ReadFolder(dir string) (Folder, error) {
	f := Folder{Root: dir}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			f.Unreadable++
			return nil
		}
		if path != dir && isSystem(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !IsMedia(path) {
			f.Others = append(f.Others, path)
			return nil
		}
		f.Items = append(f.Items, Item{Path: path})
		return nil
	})
	return f, err
}
