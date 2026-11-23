// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
)

// How Takeout names the sidecar of a media file. The rules, and the names the
// tests hold, are the ones immich-go (adapters/googlePhotos/matchers.go) and
// GooglePhotosTakeoutHelper (json_extractor.dart, extras.dart) gathered from
// their users' exports:
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
	sidecarCut   = 46 // of a sidecar's name, in UTF-16 units; one less for a pair kept whole
)

var repeated = regexp.MustCompile(`^(.*)(\(\d+\))$`)

// editedSuffixes end the name of a copy edited in Google Photos, by the
// language of the account.
var editedSuffixes = []string{"-edited", "-effects", "-smile", "-mix", "-edytowane", "-edytowany", "-bearbeitet", "-bewerkt",
	"-編集済み", "-modificato", "-modifié", "-ha editado", "-editado", "-editat"}

// stillExts are the photo of a Live Photo, or the original of an edited copy.
var stillExts = []string{".HEIC", ".heic", ".JPG", ".jpg", ".JPEG", ".jpeg", ".PNG", ".png"}

// pairing is where a media file's date is. shared: it is another photo's,
// whose date this one has until its own arrives.
type pairing struct {
	path   string
	shared bool
}

// sidecarNote is what Homewend reads of a sidecar.
type sidecarNote struct {
	// The name the photo was uploaded with, whole.
	Title          string `json:"title"`
	PhotoTakenTime struct {
		Timestamp string `json:"timestamp"`
	} `json:"photoTakenTime"`
	// The app that produced the file, as an Android package name. Google's
	// own answer to "where did this come from", and far better than reading
	// it off the filename.
	AppSource struct {
		AndroidPackageName string `json:"androidPackageName"`
	} `json:"appSource"`
}

func readSidecar(path string) (note sidecarNote, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return note, err
	}
	err = json.Unmarshal(data, &note)
	return note, err
}

// taken is when the shutter fired. (creationTime, beside it, is when the
// photo reached Google: for a scan, decades later.) A scan of before 1970 is
// below zero; zero and 1904 are clocks nobody set, and a year before
// photography is no date.
func (n sidecarNote) taken() (time.Time, bool) {
	seconds, err := strconv.ParseInt(n.PhotoTakenTime.Timestamp, 10, 64)
	when := time.Unix(seconds, 0).UTC()
	real := err == nil && seconds != 0 && when.Year() >= 1826 && when.Year() <= 9999 &&
		!when.Equal(time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC))
	return when, real
}

// sidecarsOf pairs the media files of a folder with its sidecars, by name.
// A sidecar is one photo's own; a wrong date is worse than none, so a name
// cut short is believed only where the title inside agrees.
func sidecarsOf(dir string, media []string) map[string]pairing {
	there := map[string]bool{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			there[e.Name()] = true
		}
	}
	found, owner := map[string]pairing{}, map[string]string{}
	first := func(names []string) string {
		for _, n := range names {
			if there[n] && owner[n] == "" {
				return n
			}
		}
		return ""
	}
	// A photo before a video: a name with no extension is the photo's.
	media = slices.Clone(media)
	here := map[string]bool{}
	for _, m := range media {
		here[m] = true
	}
	slices.SortStableFunc(media, func(a, b string) int {
		return strings.Compare(strconv.FormatBool(IsVideo(a)), strconv.FormatBool(IsVideo(b)))
	})
	// The names kept whole, then the cut ones: by number, then by none.
	for _, names := range []func(string) []string{wholeNames, cutNumbered, cutBare} {
		claims := map[string][]string{}
		for _, m := range media {
			if _, ok := found[m]; ok {
				continue
			}
			if n := first(names(m)); n != "" && titled(dir, n, m) {
				claims[n] = append(claims[n], m)
			}
		}
		for n, by := range claims {
			// Two with the same claim: nobody's.
			if len(by) > 1 && !slices.Contains(wholeNames(by[0]), n) {
				continue
			}
			// A copy that lost its number may be reading the one of a photo
			// filed already: its own may still come.
			_, _, number := mediaName(by[0])
			lost := number != "" && !slices.Contains(wholeNames(by[0]), n) && !slices.Contains(cutNumbered(by[0]), n)
			owner[n], found[by[0]] = by[0], pairing{filepath.Join(dir, n), lost}
		}
	}
	for _, m := range media {
		if _, ok := found[m]; ok {
			continue
		}
		// A copy whose name lost its number where it was cut.
		for _, n := range cutBare(m) {
			if o := owner[n]; o != "" && sameName(o, m) {
				found[m] = pairing{filepath.Join(dir, n), true}
			}
		}
		for _, p := range partners(m) {
			if _, ok := found[m]; ok {
				break
			}
			if s, ok := found[p]; ok && !s.shared {
				found[m] = pairing{s.path, true}
				break
			}
			if here[p] {
				continue
			}
			// The partner is filed already, or in another part.
			for _, names := range []func(string) []string{wholeNames, cutNumbered} {
				if n := first(names(p)); n != "" && titled(dir, n, p) {
					found[m] = pairing{filepath.Join(dir, n), true}
					break
				}
			}
		}
	}
	return found
}

// partners are the photos media was made from or with: the original of an
// edited copy, the photo of a Live Photo's video. Never with another number:
// that is another photo.
func partners(media string) []string {
	stem, ext, number := mediaName(media)
	var names []string
	for _, suffix := range editedSuffixes {
		if original, ok := strings.CutSuffix(stem, suffix); ok {
			for _, e := range append([]string{ext}, stillExts...) {
				names = append(names, original+number+e)
			}
		}
	}
	if IsVideo(media) {
		for _, e := range stillExts {
			names = append(names, stem+number+e)
		}
	}
	return names
}

// mediaName splits IMG_1(2).jpg into IMG_1, .jpg and (2).
func mediaName(base string) (stem, ext, number string) {
	ext = filepath.Ext(base)
	stem = strings.TrimSuffix(base, ext)
	if m := repeated.FindStringSubmatch(stem); m != nil {
		stem, number = m[1], m[2]
	}
	return stem, ext, number
}

func sameName(a, b string) bool {
	as, ae, _ := mediaName(a)
	bs, be, _ := mediaName(b)
	return as == bs && ae == be
}

// wholeNames are the sidecar names that hold the media's whole name.
func wholeNames(media string) []string {
	stem, ext, number := mediaName(media)
	var names []string
	add := func(name, number string) {
		for n := len(supplemental); n >= 0; n-- {
			names = append(names, name+supplemental[:n]+number+".json")
		}
	}
	add(stem+ext, number)
	if number != "" {
		add(stem+number+ext, "")
	}
	// Uploaded with no extension: Google gave one to the photo alone.
	for _, suffix := range []string{supplemental, ""} {
		names = append(names, stem+suffix+number+".json")
	}
	return names
}

// cutNumbered and cutBare are the names cut into the media's own, its number
// moved after them or lost.
func cutNumbered(media string) []string {
	_, _, number := mediaName(media)
	return cutNames(media, number)
}

func cutBare(media string) []string { return cutNames(media, "") }

func cutNames(media, number string) []string {
	stem, ext, _ := mediaName(media)
	name := []rune(stem + ext)
	var names []string
	for n := len(name) - 1; n > 0; n-- {
		units := len(utf16.Encode(name[:n]))
		if units < sidecarCut-1 {
			break
		}
		if units <= sidecarCut {
			names = append(names, string(name[:n])+number+".json")
		}
	}
	return names
}

// titled is whether the sidecar called name can be media's: one that holds
// its whole name is; a cut one, or one with no extension, is if the title
// inside begins as media's name does, with its extension.
func titled(dir, name, media string) bool {
	stem, ext, _ := mediaName(media)
	if strings.HasPrefix(name, stem+ext) || strings.HasPrefix(name, media) {
		return true
	}
	note, err := readSidecar(filepath.Join(dir, name))
	if err != nil {
		return false
	}
	if note.Title == "" {
		return true
	}
	e := filepath.Ext(note.Title)
	return strings.HasPrefix(plain(note.Title), plain(stem)) && (e == "" || strings.EqualFold(e, ext) || !IsMedia(note.Title))
}

// plain is a name as Google may have rewritten it: & and ' become _.
func plain(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return '_'
	}, name)
}
