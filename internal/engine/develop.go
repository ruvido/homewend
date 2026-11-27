// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/raw"
	"github.com/ruvido/homewend/internal/thumb"
)

// A RAF's pictures larger than a thumbnail, and any with an edit, are made
// from its developed picture, 2560 pixels long, kept in the cache's
// "developed" by the name its pictures have; an edit that keeps less of it
// than it draws is made from the whole, in "developed-full". The library's
// RAFs are developed in the background, one at a time, at the lowest
// priority, after the window's own pictures; one the window or a phone asks
// for goes first. Until it is there, a picture is made from the JPEG the
// camera embedded, under another name and not kept, so the developed one
// replaces it. The grid's own thumbnails stay the camera's, but a film
// negative's: its pictures, all of them, are of its print, named after the
// bounds it is printed with (printed), and its developed picture is that print.

// cameraMark is after the name of a picture made while the one it should be
// made from is developed, or where it cannot be.
const cameraMark = "-camera"

// positiveMark is between a name and its print's, all hexadecimal.
const positiveMark = "-positive-"

// printed is the name the pictures of name are kept by, of the print named
// positive; name itself when that is none.
func printed(name, positive string) string {
	if positive == "" {
		return name
	}
	return name + positiveMark + positive
}

// rawJob is a raw file to develop: the name its picture is kept by, its file
// (or the photo it is found by, when developed), and whether it is the whole
// of it.
type rawJob struct {
	name, src string
	key       library.Key
	full      bool
	// library is a job of the library's: counted in its progress.
	library bool
	// negative are the bounds of the print it is developed as; "" for none.
	negative string
}

func (j rawJob) dst() (string, error) {
	folder, err := picturesDir(developedDir(j.full))
	return thumb.Path(folder, j.name), err
}

// madeDirs are the cache's folders of pictures made to be shown, and
// developedDirs those of raw files developed, at 2560 pixels and whole.
var madeDirs, developedDirs = []string{"thumbs", "previews", "screens"}, []string{"developed", "developed-full"}

func developedDir(full bool) string {
	if full {
		return developedDirs[1]
	}
	return developedDirs[0]
}

// developer develops one raw file at a time: one takes every core, and a
// 40 MP one about a gigabyte between the helper and the engine.
var developer queue

type queue struct {
	sync.Mutex
	asked, library []rawJob
	now            *rawJob
	// failed are the jobs that did not develop this run: not tried again
	// until the app starts again.
	failed map[rawJob]bool
	// recent are the names last developed, newest last.
	recent []string
	// checked are the library's RAFs whose old pictures were looked for this run.
	checked map[string]bool
	of      int
	started bool
	wake    chan struct{}
	// converts are the groups of negatives to print, in turn; converting the
	// one being printed.
	converts   [][]library.Key
	converting []library.Key
}

// recentKept is how many of the names last developed are said: more than
// a window misses between two asks, a second apart.
const recentKept = 16

var noHelper sync.Once

// Developing is where the library's development is: Done of Of of its RAFs
// have their picture; Now are the names being developed for the window or
// a phone, or next; Recent the last developed, newest last.
type Developing struct {
	Done   int      `json:"done"`
	Of     int      `json:"of"`
	Now    []string `json:"now"`
	Recent []string `json:"recent"`
}

// DevelopingNow is where development is.
func DevelopingNow() Developing {
	d := &developer
	d.Lock()
	defer d.Unlock()
	left := len(d.library)
	now := []string{}
	for _, j := range append(slices.Clone(d.asked), d.nowOr()...) {
		if j.library {
			left++
		}
		now = append(now, j.name)
	}
	for _, k := range slices.Concat(append(d.converts, d.converting)...) {
		now = append(now, k.Hash)
	}
	return Developing{Done: max(0, d.of-left), Of: d.of, Now: now, Recent: append([]string{}, d.recent...)}
}

// nowOr is the job being developed, as a list.
func (d *queue) nowOr() []rawJob {
	if d.now == nil {
		return nil
	}
	return []rawJob{*d.now}
}

// developsFrom is whether a picture of src at size, drawn (with an edit, or
// printed from a negative) or not, is made from its developed picture.
func developsFrom(src string, size int, drawn bool) bool {
	return isRAF(src) && (size > thumb.Size || drawn)
}

func isRAF(name string) bool { return strings.EqualFold(filepath.Ext(name), ".raf") }

type aheadKey struct{}

// Ahead is ctx of a picture asked before it is wanted, as the viewer asks the
// photos either side: a RAF's is not developed sooner for it.
func Ahead(ctx context.Context) context.Context { return context.WithValue(ctx, aheadKey{}, true) }

// developedFor is the file a picture of the RAF src, kept as name, at size
// with the edit e, is made from, and whether it is what it should be made
// from; "" while that is developed, or where it cannot be. Unless asked
// ahead, it is developed next.
func developedFor(ctx context.Context, src, name string, size int, e drawing) (from string, whole bool) {
	raise := ctx.Value(aheadKey{}) == nil
	from = developed(rawJob{name: name, src: src, negative: e.negative}, raise)
	if from == "" || e.edit.None() || !thumb.Short(from, size, e.edit) {
		return from, from != ""
	}
	if full := developed(rawJob{name: name, src: src, full: true, negative: e.negative}, raise); full != "" {
		return full, true
	}
	return from, false
}

// developed is the developed picture of j, or "" while it is made or when
// it cannot be; raised, it is made next.
func developed(j rawJob, raise bool) string {
	dst, err := j.dst()
	if err != nil {
		return ""
	}
	if _, err := os.Stat(dst); err == nil {
		return dst
	}
	if !raise {
		return ""
	}
	d := &developer
	d.Lock()
	defer d.Unlock()
	if d.failed[j] || slices.ContainsFunc(d.asked, j.same) || d.now != nil && j.same(*d.now) {
		return ""
	}
	if i := slices.IndexFunc(d.library, j.same); i >= 0 {
		j.library = true
		d.library = slices.Delete(d.library, i, i+1)
	}
	d.asked = append(d.asked, j)
	d.start()
	return ""
}

func (j rawJob) same(k rawJob) bool { return j.name == k.name && j.full == k.full }

// developLibrary has the library's RAFs, of photos in the library at root,
// developed in the background: those not yet, and not failed this run.
func developLibrary(root string, photos []library.Photo) {
	if !raw.Installed() {
		return
	}
	d := &developer
	d.Lock()
	checked := d.checked
	if checked == nil {
		checked = map[string]bool{}
		d.checked = checked
	}
	d.Unlock()
	var lack []rawJob
	of := 0
	for _, p := range photos {
		if !developsFrom(p.Name, thumb.PreviewSize, false) {
			continue
		}
		of++
		// A page of the library has no paths: the file is found when its turn comes.
		j := rawJob{name: printed(p.Hash, library.PositiveName(p.Negative)), key: p.Key(), library: true, negative: p.Negative}
		dst, err := j.dst()
		if err != nil {
			continue
		}
		_, err = os.Stat(dst)
		// Once a run: what an older version, or one cut short, left.
		if !checked[j.name] {
			if err == nil {
				forgetCamera(j.name)
			} else if p.Negative == "" {
				forgetUndeveloped(p.Hash, p.Edit)
			}
			checked[j.name] = true
		}
		if err != nil {
			lack = append(lack, j)
		}
	}
	d.Lock()
	defer d.Unlock()
	d.of = of
	d.library = d.library[:0]
	for _, j := range lack {
		if !d.failed[j] && !slices.ContainsFunc(d.asked, j.same) && (d.now == nil || !j.same(*d.now)) {
			d.library = append(d.library, j)
		}
	}
	if len(d.library) > 0 {
		d.start()
	}
}

// start starts the one that develops, if it is not, or wakes it; under the lock.
func (d *queue) start() {
	if d.started {
		select {
		case d.wake <- struct{}{}:
		default:
		}
		return
	}
	d.started, d.wake = true, make(chan struct{}, 1)
	if d.failed == nil {
		d.failed = map[rawJob]bool{}
	}
	go developAll()
}

// forgetUnfinished deletes what a development cut short, by the app's end,
// left half written.
func forgetUnfinished() {
	for _, dir := range developedDirs {
		if folder, err := picturesDir(dir); err == nil {
			files, _ := filepath.Glob(filepath.Join(folder, "*", "*.tmp"))
			for _, file := range files {
				os.Remove(file)
			}
		}
	}
}

// Convert has the photos, RAWs of one film's negatives, shown printed with
// the bounds of them all, in the background, first; or with on false shown
// as they came, at once. A photo that is no RAF is left as it is.
func Convert(photos []library.Key, on bool) error {
	if !on {
		return setNegatives(photos, "")
	}
	d := &developer
	d.Lock()
	defer d.Unlock()
	d.converts = append(d.converts, photos)
	d.start()
	return nil
}

// convert prints the negatives photos: each one's bounds read on a decode at
// half the size, then the median of them all is theirs; each is shown
// printed once its print is made, one after another, as a turn shows.
func convert(photos []library.Key) {
	var keys []library.Key
	var srcs []string
	var all []raw.Bounds
	for _, k := range photos {
		src, _, err := pictureOf(k, os.Stat)
		if err == nil && !isRAF(src) {
			continue
		}
		var img *raw.Image
		if err == nil {
			img, _, err = raw.Develop(context.Background(), src, raw.NegativeHalf, false)
		}
		var b raw.Bounds
		if err == nil {
			b, err = raw.Analyse(img)
		}
		if err != nil {
			log.Printf("reading the negative %s: %v", k.Hash, err)
			continue
		}
		keys, srcs, all = append(keys, k), append(srcs, src), append(all, b)
	}
	if len(all) == 0 {
		return
	}
	bounds := raw.MedianBounds(all).String()
	for i, k := range keys {
		j := rawJob{name: printed(k.Hash, library.PositiveName(bounds)), src: srcs[i], negative: bounds}
		dst, err := j.dst()
		if err != nil {
			continue
		}
		if _, err := os.Stat(dst); err != nil {
			develop(j, false)
		}
		// Not printed, it stays as it came, not the camera's negative under the print's name.
		if _, err := os.Stat(dst); err != nil {
			continue
		}
		if err := setNegatives([]library.Key{k}, bounds); err != nil {
			log.Printf("printing the negative %s: %v", k.Hash, err)
		}
	}
}

// setNegatives has photos shown printed with bounds, or as they came.
func setNegatives(photos []library.Key, bounds string) error {
	c, _, err := openLibrary()
	if err != nil {
		return err
	}
	if c == nil {
		return fs.ErrNotExist
	}
	defer c.Close()
	err = c.SetNegatives(photos, bounds)
	lookNow()
	return err
}

// developAll develops what is asked, then the library's, for as long as the
// app runs.
func developAll() {
	forgetUnfinished()
	d := &developer
	for {
		d.Lock()
		if len(d.converts) > 0 {
			d.converting, d.converts = d.converts[0], d.converts[1:]
			d.Unlock()
			convert(d.converting)
			d.Lock()
			d.converting = nil
			d.Unlock()
			continue
		}
		var j *rawJob
		background := false
		switch {
		case len(d.asked) > 0:
			j = &d.asked[0]
		case len(d.library) > 0:
			j, background = &d.library[0], true
		}
		if j != nil {
			next := *j
			d.now = &next
			if background {
				d.library = d.library[1:]
			} else {
				d.asked = d.asked[1:]
			}
		}
		wake := d.wake
		d.Unlock()
		if j == nil {
			<-wake
			continue
		}
		develop(*d.now, background)
	}
}

// develop develops j; in the background, after the pictures the window waits for.
func develop(j rawJob, background bool) {
	for background && len(thumbSlots) > 0 {
		time.Sleep(50 * time.Millisecond)
	}
	long := thumb.PreviewSize
	if j.full {
		long = 0
	}
	dst, err := j.dst()
	if err == nil && j.src == "" {
		j.src, _, err = pictureOf(j.key, os.Stat)
	}
	var negative *raw.Bounds
	if err == nil && j.negative != "" {
		var b raw.Bounds
		b, err = raw.ParseBounds(j.negative)
		negative = &b
	}
	if err == nil {
		err = thumb.Develop(context.Background(), j.src, dst, long, negative, background)
	}
	d := &developer
	d.Lock()
	d.now = nil
	if err == nil {
		d.recent = append(d.recent, j.name)
		d.recent = d.recent[max(0, len(d.recent)-recentKept):]
	}
	if err != nil {
		d.failed[j] = true
		if errors.Is(err, raw.ErrNoHelper) {
			d.library = nil
		}
	}
	d.Unlock()
	switch {
	case err == nil:
		forgetCamera(j.name)
	case errors.Is(err, raw.ErrNoHelper):
		noHelper.Do(func() { log.Printf("raw files are shown as their cameras' JPEGs: %v", err) })
	default:
		log.Printf("developing %s: %v", filepath.Base(j.src), err)
	}
}

// forgetCamera deletes the pictures of name made while it was developed.
func forgetCamera(name string) {
	for _, dir := range madeDirs {
		if folder, err := picturesDir(dir); err == nil {
			files, _ := filepath.Glob(thumb.Path(folder, name+cameraMark+"*"))
			for _, file := range files {
				os.Remove(file)
			}
		}
	}
}

// forgetUndeveloped deletes the pictures of a RAF, with the edit named edit,
// that an older version made from the camera's JPEG under the name the
// developed ones take: found there, they would be shown for ever.
func forgetUndeveloped(name, edit string) {
	for _, dir := range madeDirs {
		folder, err := picturesDir(dir)
		if err != nil {
			return
		}
		if dir != "thumbs" {
			os.Remove(thumb.Path(folder, name))
		}
		if edit != "" {
			os.Remove(thumb.Path(folder, thumb.Edited(name, edit)))
		}
	}
}
