// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf16"
)

// How Takeout names the sidecar of a media file. The rules, and the names the
// tests hold, are the ones immich-go and GooglePhotosTakeoutHelper gathered
// from their users' exports:
//
//   - IMG_1.jpg has IMG_1.jpg.supplemental-metadata.json, or, in an older
//     export, IMG_1.jpg.json;
//   - the name before ".json" is cut at 46: IMG_1.jpg.supplemental-met.json,
//     and a long photo's name is cut itself;
//   - the number of a repeated name moves: IMG_1(2).jpg has
//     IMG_1.jpg.supplemental-metadata(2).json;
//   - an edited copy and a Live Photo's video have none of their own, and
//     share the original's.

const (
	supplemental = ".supplemental-metadata"
	sidecarCut   = 46          // of a sidecar's name, in UTF-16 units, less one for a pair kept whole
	year1904     = -2082844800 // where a QuickTime clock never set begins
)

var repeated = regexp.MustCompile(`^(.*)(\(\d+\))$`)

// editedSuffixes end the name of a copy edited in Google Photos, by the
// language of the account.
var editedSuffixes = []string{"-edited", "-effects", "-smile", "-mix", "-edytowane", "-edytowany", "-bearbeitet", "-bewerkt",
	"-編集済み", "-modificato", "-modifié", "-ha editado", "-editado", "-editat"}

// stillExts are the photo of a Live Photo, or the original of an edited copy.
var stillExts = []string{".HEIC", ".heic", ".JPG", ".jpg", ".JPEG", ".jpeg", ".PNG", ".png"}

// findSidecar is the path of a media file's own sidecar, or "".
func findSidecar(mediaPath string) string {
	for _, candidate := range ownSidecars(mediaPath) {
		if exists(candidate) {
			return candidate
		}
	}
	return ""
}

// ownSidecars are the names a media file's sidecar can have, likeliest first.
func ownSidecars(mediaPath string) []string {
	dir, stem, ext, number := mediaName(mediaPath)
	paths := sidecarsOf(dir, stem+number, ext, "")
	if number != "" {
		paths = append(paths, sidecarsOf(dir, stem, ext, number)...)
	}
	return paths
}

// sharedSidecars are the sidecars of the photo a media file was made from or
// with: the original of an edited copy, the photo of a Live Photo's video.
// Never of a photo with another number: that is another photo.
func sharedSidecars(mediaPath string) []string {
	dir, stem, ext, number := mediaName(mediaPath)
	var paths []string
	still := func(stem string, exts []string) {
		for _, e := range exts {
			paths = append(paths, ownSidecars(filepath.Join(dir, stem+number+e))...)
		}
	}
	for _, suffix := range editedSuffixes {
		if original, ok := strings.CutSuffix(stem, suffix); ok {
			still(original, append([]string{ext}, stillExts...))
		}
	}
	if IsVideo(mediaPath) {
		still(stem, stillExts)
	}
	return paths
}

// mediaName splits IMG_1(2).jpg into IMG_1, .jpg and (2).
func mediaName(path string) (dir, stem, ext, number string) {
	dir, base := filepath.Split(path)
	ext = filepath.Ext(base)
	stem = strings.TrimSuffix(base, ext)
	if m := repeated.FindStringSubmatch(stem); m != nil {
		stem, number = m[1], m[2]
	}
	return dir, stem, ext, number
}

// sidecarsOf are the sidecar names of stem+ext with number, whole and cut.
func sidecarsOf(dir, stem, ext, number string) []string {
	whole := []rune(stem + ext + supplemental)
	media := len([]rune(stem + ext))
	var paths []string
	for n := len(whole); n > 0; n-- {
		// Shorter than the photo's own name only where Google cuts.
		if n < media && len(utf16.Encode(whole[:n])) < sidecarCut-1 {
			break
		}
		paths = append(paths, filepath.Join(dir, string(whole[:n])+number+".json"))
	}
	// Uploaded with no extension: Google gave one to the photo alone.
	for _, suffix := range []string{supplemental, ""} {
		paths = append(paths, filepath.Join(dir, stem+suffix+number+".json"))
	}
	return paths
}
