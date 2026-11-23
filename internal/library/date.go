// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/abema/go-mp4"
	"github.com/gen2brain/heic"
)

// DateSource says where a capture date came from. The user is shown it, because
// "we think this is from 2019" and "Google says this is from 2019" are different
// claims and only one of them is ours.
type DateSource string

const (
	FromSidecar DateSource = "sidecar" // Google's own answer
	FromEXIF    DateSource = "exif"    // the camera's answer: its clock, with no zone
	FromVideo   DateSource = "video"   // a video's header, read as the camera's clock
	FromGiven   DateSource = "given"   // the person's answer, given before an import: a clock, like the camera's
	FromFolder  DateSource = "folder"  // the year Takeout filed it under
	FromName    DateSource = "name"    // the day and time in the file's name: a guess, the file says nothing
	FromNameDay DateSource = "nameday" // only the day in the file's name, as WhatsApp writes it
	NoDate      DateSource = "none"    // and we do not invent one
)

// Guessed is how much of a date this source guesses: "year" (Google's
// folder), "day" or "time" (a file's name); "" when it is not a guess.
func (s DateSource) Guessed() string {
	switch s {
	case FromFolder:
		return "year"
	case FromNameDay:
		return "day"
	case FromName:
		return "time"
	}
	return ""
}

// Capture is when a photo was taken, how we know, and what made it.
type Capture struct {
	When   time.Time
	Source DateSource
	Origin Origin
	// clock: When is already a clock, as the catalog keeps every date; a
	// date read back from it is not moved again.
	clock bool
}

// Local is the capture's time on the clock it is filed by, in loc. Google's
// sidecar is an instant, moved into loc. A camera's clock — a photo's EXIF,
// a video's header as read — is already the clock of where it was taken,
// with no zone to move from: it is kept, or a photo of 23:30 on New Year's
// Eve in Rome, read as UTC and moved, lands in the next year. A folder's
// year has no clock at all.
func (c Capture) Local(loc *time.Location) time.Time {
	if c.Source == FromSidecar && !c.clock {
		return asClock(c.When.In(loc))
	}
	return c.When
}

// asClock is t's wall-clock time held in a UTC time: how the catalog keeps a
// date, and how the grid, its counts and the folders read it.
func asClock(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// DateOf answers "when was this taken?" in the order of how much the answer can
// be trusted, and stops at the first real one.
//
// **The file's modification time is never consulted, and that is deliberate.**
// Takeout sets it to the moment it built the archive — every file in the export
// we downloaded carries 24 September 2026 — so a program that trusts mtime files
// a whole life of photographs under the day it downloaded them. It is the most
// obvious source and the only one that is certainly wrong.
func DateOf(mediaPath, folder, sidecar string) Capture {
	var note sidecarNote
	if sidecar != "" {
		// One that cannot be read says nothing; the file still may.
		note, _ = readSidecar(sidecar)
	}
	name := filepath.Base(mediaPath)
	origin := OriginOf(note.AppSource.AndroidPackageName, name)
	if taken, ok := note.taken(); ok {
		google := asClock(taken.In(time.Local))
		if when, source, ok := uploadedLate(name, google); ok {
			return Capture{When: when, Source: source, Origin: origin}
		}
		if when, ok := elsewhere(mediaPath, google); ok {
			return Capture{When: when, Source: FromEXIF, Origin: origin}
		}
		return Capture{When: taken, Source: FromSidecar, Origin: origin}
	}
	if when, source, ok := cameraDate(mediaPath); ok {
		return Capture{When: when, Source: source, Origin: origin}
	}
	// "Photos from 2019" is not a date, but it is Google's own filing and it is
	// better than nothing: the photo lands in the right year, in a month folder
	// that says it is unknown.
	capture := Capture{Source: NoDate, Origin: origin}
	if match := yearFolder.FindStringSubmatch(folder); match != nil {
		if year, err := strconv.Atoi(match[1]); err == nil {
			capture.When, capture.Source = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC), FromFolder
		}
	}
	capture, _ = dateNamed(capture, name)
	return capture
}

// ofLivePhoto is a video's date when it is part of the Live Photo still: the
// camera's clock there too, or the two would be hours apart. Its header must
// say the same minute as Google's date, the still's: 1,483 of 1,500 in a real
// export, and the 17 others were a zone off. Any other video keeps its own.
func ofLivePhoto(c Capture, path, still string) (Capture, bool) {
	if still == "" || c.Source != FromSidecar {
		return c, false
	}
	google := c.Local(time.Local)
	ended, ok := videoDate(path)
	if !ok || ended.Sub(google).Abs() > time.Minute {
		return c, false
	}
	if when, ok := elsewhere(still, google); ok {
		c.When, c.Source = when, FromEXIF
	}
	return c, true
}

// uploadedLate is the date in a WhatsApp file's name, when Google's is more
// than a day after it, or in a screenshot's, when it is more than a minute.
// A screenshot's name has the second it was taken: in a real export 137 of
// 171 agree with Google's within minutes, 34 are later and none earlier.
// WhatsApp leaves no date inside, so Google has the day
// of the upload: in a real export 3,765 of 10,498, most of them months late.
// The name is the day the file came through the chat, and no picture is
// taken after it was sent: a date a person corrected is an earlier one, which
// stands. Signal's names say the same, but 2 late files are too few to rule on.
func uploadedLate(name string, google time.Time) (time.Time, DateSource, bool) {
	when, source, ok := nameDate(name)
	day := func(t time.Time) time.Time { return t.Truncate(24 * time.Hour) }
	late := false
	switch OriginOf("", name) {
	case OriginWhatsApp:
		late = day(google).Sub(day(when)) > 24*time.Hour
	case OriginScreenshot:
		late = google.Sub(when) > time.Minute
	}
	return when, source, ok && late
}

// elsewhere is the camera's clock in a photo, when it and Google's, on this
// computer's, are a time zone apart: taken travelling, 20:40 in New York is
// not 02:40 of the next day. Google has the camera's clock moved by a zone,
// so the two are whole quarters of an hour apart, to the second: 622 photos
// of a real export are, and the 18 that are seconds off were dated by a
// person in Google, or are collages it made. More than 14 hours is no zone.
// A video's header has no clock of its own place, and says nothing here.
func elsewhere(path string, google time.Time) (time.Time, bool) {
	if IsVideo(path) {
		return time.Time{}, false
	}
	when, _, ok := cameraDate(path)
	apart := when.Sub(google)
	return when, ok && apart != 0 && apart.Abs() <= 14*time.Hour && apart%(15*time.Minute) == 0
}

// dateNamed is c with the date name says, when c has none or only Google's
// year. The name stands against that year: in a real export all 44 that
// disagreed were WhatsApp files Google had filed under the year of their
// upload, and its year is UTC's, another one on New Year's night.
func dateNamed(c Capture, name string) (Capture, bool) {
	if c.Source != NoDate && c.Source != FromFolder {
		return c, false
	}
	when, source, ok := nameDate(name)
	if !ok {
		return c, false
	}
	c.When, c.Source = when, source
	return c, true
}

// cameraDate is when the camera says the photo or video was taken, and
// which kind of answer that is: the EXIF of a JPEG, a TIFF or a HEIC, the
// header of an MP4 or a MOV. A card or a folder has no sidecar, so this is
// all a file brought from one can say.
func cameraDate(path string) (time.Time, DateSource, bool) {
	if IsVideo(path) {
		when, ok := videoDate(path)
		return when, FromVideo, ok
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".heic" || ext == ".heif" {
		if when, ok := heicDate(path); ok {
			return when, FromEXIF, true
		}
		// A JPEG saved with the wrong name still has its EXIF where a JPEG
		// keeps it.
	}
	when, ok := exifDate(path)
	return when, FromEXIF, ok
}

// heicDate reads DateTimeOriginal out of a HEIC. Its EXIF is an item the meta
// box points at, not a block near the start as in a JPEG: the decoder the
// thumbnails use already finds it.
func heicDate(path string) (when time.Time, ok bool) {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer file.Close()
	// The decoder trusts the sizes a file declares: a damaged one can make it
	// panic (measured 09/11, a meta box claiming more than the file holds).
	// One damaged photo on a card has no date; it does not stop the rest.
	defer func() {
		if recover() != nil {
			when, ok = time.Time{}, false
		}
	}()
	exif, err := heic.DecodeExif(file)
	if err != nil {
		return time.Time{}, false
	}
	return cameraClock(exif.DateTimeOriginal)
}

// videoDate reads the creation time in an MP4's or a MOV's movie header. It
// is UTC, as the format says, and cameras write it so: measured 09/11 on a
// Samsung phone (11:57:38 for a file it named 125635, in Rome at +01:00) and
// a Fujifilm X-T3 (17:23:15 against its EXIF's 18:22:47 at +01:00). It is
// when the recording ended, not when it began: seconds later, the same day.
//
// It is returned as a camera's clock, like a photo's EXIF: the instant on
// this computer's clock, which is the zone a library is filed in by default
// (Options.Location). A video and the photos of the same minute then sort
// together, and the grid, which shows a camera's clock, shows its hour.
func videoDate(path string) (time.Time, bool) {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer file.Close()
	boxes, err := mp4.ExtractBoxWithPayload(file, nil, mp4.BoxPath{mp4.BoxTypeMoov(), mp4.BoxTypeMvhd()})
	if err != nil || len(boxes) == 0 {
		return time.Time{}, false
	}
	mvhd, ok := boxes[0].Payload.(*mp4.Mvhd)
	if !ok {
		return time.Time{}, false
	}
	seconds := uint64(mvhd.CreationTimeV0)
	if mvhd.GetVersion() == 1 {
		seconds = mvhd.CreationTimeV1
	}
	// Zero is a camera that wrote no date: 1 January 1904, not a day anyone
	// filmed.
	if seconds == 0 {
		return time.Time{}, false
	}
	when := time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(seconds) * time.Second)
	if when.Year() < 1970 {
		return time.Time{}, false
	}
	return asClock(when.In(time.Local)), true
}

// exifDate reads DateTimeOriginal out of a JPEG or TIFF, and nothing else.
//
// Deliberately minimal: one tag, read-only, no dependency. We never write a
// byte of metadata — that is a product decision as much as an engineering one —
// so a full EXIF library would be a large surface for a single number.
func exifDate(path string) (time.Time, bool) {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer file.Close()

	// The APP1 segment lives near the front; a megabyte is far more than enough
	// and keeps this cheap on a library of tens of thousands of files.
	head := make([]byte, 1<<20)
	n, err := io.ReadFull(file, head)
	if err != nil && n == 0 {
		return time.Time{}, false
	}
	head = head[:n]

	offset, order, ok := exifHeader(head)
	if !ok {
		return time.Time{}, false
	}
	raw, ok := findDateTimeOriginal(head, offset, order)
	if !ok {
		return time.Time{}, false
	}
	return cameraClock(raw)
}

// cameraClock reads EXIF's "2019:07:14 18:22:05". It has no zone: the
// wall-clock time is all there is, kept in a UTC time and never moved (see
// Capture.Local).
func cameraClock(raw string) (time.Time, bool) {
	when, err := time.Parse("2006:01:02 15:04:05", strings.TrimRight(raw, "\x00 "))
	if err != nil || when.Year() < 1900 {
		return time.Time{}, false
	}
	return when, true
}

// exifHeader finds the TIFF header inside a JPEG's APP1 segment, or at the very
// start for a bare TIFF, and returns where it begins and its byte order.
func exifHeader(data []byte) (int, binary.ByteOrder, bool) {
	if len(data) > 4 {
		switch {
		case data[0] == 'I' && data[1] == 'I':
			return 0, binary.LittleEndian, true
		case data[0] == 'M' && data[1] == 'M':
			return 0, binary.BigEndian, true
		}
	}
	at := bytes.Index(data, []byte("Exif\x00\x00"))
	if at < 0 || at+6+4 > len(data) {
		return 0, nil, false
	}
	tiff := at + 6
	switch {
	case data[tiff] == 'I' && data[tiff+1] == 'I':
		return tiff, binary.LittleEndian, true
	case data[tiff] == 'M' && data[tiff+1] == 'M':
		return tiff, binary.BigEndian, true
	}
	return 0, nil, false
}

const (
	tagDateTimeOriginal = 0x9003
	tagExifIFD          = 0x8769
)

func findDateTimeOriginal(data []byte, tiff int, order binary.ByteOrder) (string, bool) {
	if tiff+8 > len(data) {
		return "", false
	}
	first := int(order.Uint32(data[tiff+4 : tiff+8]))

	// The tag lives in the Exif sub-IFD, which the main one points at. Both are
	// walked because some writers put it in either.
	for _, ifd := range []int{first, subIFD(data, tiff, first, order)} {
		if ifd <= 0 {
			continue
		}
		if value, ok := readTag(data, tiff, ifd, tagDateTimeOriginal, order); ok {
			return value, true
		}
	}
	return "", false
}

func subIFD(data []byte, tiff, ifd int, order binary.ByteOrder) int {
	value, ok := readTagOffset(data, tiff, ifd, tagExifIFD, order)
	if !ok {
		return 0
	}
	return value
}

func eachEntry(data []byte, tiff, ifd int, order binary.ByteOrder, visit func(tag uint16, kind uint16, count uint32, at int) bool) {
	start := tiff + ifd
	if start+2 > len(data) {
		return
	}
	count := int(order.Uint16(data[start : start+2]))
	for i := 0; i < count; i++ {
		at := start + 2 + i*12
		if at+12 > len(data) {
			return
		}
		if visit(order.Uint16(data[at:at+2]), order.Uint16(data[at+2:at+4]),
			order.Uint32(data[at+4:at+8]), at) {
			return
		}
	}
}

func readTag(data []byte, tiff, ifd int, want uint16, order binary.ByteOrder) (string, bool) {
	var found string
	var ok bool
	eachEntry(data, tiff, ifd, order, func(tag, kind uint16, count uint32, at int) bool {
		if tag != want || kind != 2 || count == 0 { // 2 = ASCII
			return false
		}
		valueAt := tiff + int(order.Uint32(data[at+8:at+12]))
		end := valueAt + int(count)
		if valueAt < 0 || end > len(data) {
			return true
		}
		found, ok = string(data[valueAt:end]), true
		return true
	})
	return found, ok
}

func readTagOffset(data []byte, tiff, ifd int, want uint16, order binary.ByteOrder) (int, bool) {
	var found int
	var ok bool
	eachEntry(data, tiff, ifd, order, func(tag, kind uint16, count uint32, at int) bool {
		if tag != want {
			return false
		}
		found, ok = int(order.Uint32(data[at+8:at+12])), true
		return true
	})
	return found, ok
}

const tagOrientation = 0x0112

// Orientation is the EXIF orientation of a JPEG or TIFF, 1 to 8: how the
// pixels must be turned to be seen upright. 1, as is, when there is none.
func Orientation(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer file.Close()
	head := make([]byte, 1<<16)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	tiff, order, ok := exifHeader(head)
	if !ok || tiff+8 > len(head) {
		return 1
	}
	o := 1
	eachEntry(head, tiff, int(order.Uint32(head[tiff+4:tiff+8])), order, func(tag, kind uint16, count uint32, at int) bool {
		if tag != tagOrientation || kind != 3 { // 3 = SHORT, held in the entry itself
			return false
		}
		o = int(order.Uint16(head[at+8 : at+10]))
		return true
	})
	if o < 1 || o > 8 {
		return 1
	}
	return o
}

// millisName is a name that is only the milliseconds since 1970, maybe after
// the app's word, as apps that save an image name it: 1785963041597.png,
// FB_IMG_1785963041597.jpg. The library's own suffix for a name taken twice
// (withSuffix) is not part of it: 1785963041597-2.png.
var millisName = regexp.MustCompile(`^(?:[A-Za-z]+_)*(1\d{12})(?:-\d+)?$`)

// fileDate is what a file says of its own date: its camera's, else the one
// in its name. An import, a phone's backup and its plan all read it so.
func fileDate(path, name string) (time.Time, DateSource, bool) {
	if when, source, ok := cameraDate(path); ok {
		return when, source, true
	}
	return nameDate(name)
}

// nameDate is the date a file's name says (names, millisName), read as a
// clock like a camera's: a guess, since nothing inside the file confirms
// it. A name whose numbers are not a real day, or a day after tomorrow,
// says nothing.
func nameDate(name string) (time.Time, DateSource, bool) {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if m := millisName.FindStringSubmatch(stem); m != nil {
		ms, _ := strconv.ParseInt(m[1], 10, 64)
		when := asClock(time.UnixMilli(ms).In(time.Local))
		return when, FromName, plausible(when)
	}
	for _, n := range names {
		re := n.pattern
		if re.SubexpIndex("y") < 0 {
			continue
		}
		m := re.FindStringSubmatch(stem)
		if m == nil {
			continue
		}
		field := func(group string) int {
			v, _ := strconv.Atoi(m[re.SubexpIndex(group)])
			return v
		}
		y, mo, d := field("y"), field("m"), field("d")
		h, mi, s := 0, 0, 0
		timed := re.SubexpIndex("H") >= 0 && m[re.SubexpIndex("H")] != ""
		if timed {
			h, mi, s = field("H"), field("M"), field("S")
		}
		// A clock of 12 hours: 12 AM is midnight, 12 PM noon.
		if i := re.SubexpIndex("P"); i >= 0 && m[i] != "" && h >= 1 && h <= 12 {
			h %= 12
			if strings.EqualFold(m[i], "PM") {
				h += 12
			}
		}
		when := time.Date(y, time.Month(mo), d, h, mi, s, 0, time.UTC)
		real := when.Year() == y && int(when.Month()) == mo && when.Day() == d &&
			when.Hour() == h && when.Minute() == mi && when.Second() == s
		if !real || !plausible(when) {
			return time.Time{}, NoDate, false
		}
		if !timed {
			return when, FromNameDay, true
		}
		if re.SubexpIndex("U") >= 0 {
			when = asClock(when.In(time.Local))
		}
		return when, FromName, true
	}
	return time.Time{}, NoDate, false
}

// plausible is a day a digital photo can have been named on: not before
// digital cameras wrote dates in names, not after tomorrow.
func plausible(t time.Time) bool {
	return t.Year() >= 1990 && t.Before(time.Now().AddDate(0, 0, 2))
}
