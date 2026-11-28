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

// standsIn says whether file is a picture made while the one it should be
// made from is developed.
func standsIn(file string) bool { return strings.Contains(filepath.Base(file), cameraMark) }

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

// rawJob is a raw file to develop: the name its picture is kept by, the hash
// of its photo, its file (or the photo it is found by, when developed), and
// whether it is the whole of it.
type rawJob struct {
	name, hash, src string
	key             library.Key
	full            bool
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
	// failed are the jobs that did not develop this run, by what same
	// compares: not tried again until the app starts again.
	failed map[jobID]bool
	// recent are the hashes last developed, newest last.
	recent []string
	// checked are the library's RAFs whose old pictures were looked for this run.
	checked map[string]bool
	of      int
	started bool
	wake    chan struct{}
	// converts are the groups of negatives to print, in turn; converting
	// those of the group being printed not printed yet. convertOf are the
	// negatives asked since none was left, convertDone those of them done.
	converts               [][]library.Key
	converting             []library.Key
	convertOf, convertDone int
}

// recentKept is how many of the photos last developed are said: more than
// a window misses between two asks, a second apart.
const recentKept = 16

var noHelper sync.Once

// Developing is where the library's development is: Done of Of of its RAFs
// have their picture; Now are the hashes of the photos being developed for
// the window or a phone, or next; Recent of those last developed, newest last.
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
		now = append(now, j.hash)
	}
	for _, k := range slices.Concat(append(d.converts, d.converting)...) {
		now = append(now, k.Hash)
	}
	// The library's count is said while some of it is left: negatives
	// converted in a library all developed are 0 of 3, not 3 of 6.
	done, of := max(0, d.of-left), d.of
	if left == 0 {
		done, of = 0, 0
	}
	return Developing{Done: done + d.convertDone, Of: of + d.convertOf, Now: now, Recent: append([]string{}, d.recent...)}
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

// developedFor is the file a picture of the RAF src, of the photo hash, kept
// as name, at size with the edit e, is made from, and whether it is what it should be made
// from; "" while that is developed, or where it cannot be. Unless asked
// ahead, it is developed next.
func developedFor(ctx context.Context, src, hash, name string, size int, e drawing) (from string, whole bool) {
	raise := ctx.Value(aheadKey{}) == nil
	from = developed(rawJob{name: name, hash: hash, src: src, negative: e.negative}, raise)
	if from == "" || e.edit.None() || !thumb.Short(from, size, e.edit) {
		return from, from != ""
	}
	if full := developed(rawJob{name: name, hash: hash, src: src, full: true, negative: e.negative}, raise); full != "" {
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
	if d.failed[j.id()] || slices.ContainsFunc(d.asked, j.same) || d.now != nil && j.same(*d.now) {
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

func (j rawJob) same(k rawJob) bool { return j.id() == k.id() }

// jobID is what tells a job from another: the picture it makes.
type jobID struct {
	name string
	full bool
}

func (j rawJob) id() jobID { return jobID{j.name, j.full} }

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
		j := rawJob{name: printed(p.Hash, library.PositiveName(p.Negative)), hash: p.Hash, key: p.Key(), library: true, negative: p.Negative}
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
		if !d.failed[j.id()] && !slices.ContainsFunc(d.asked, j.same) && (d.now == nil || !j.same(*d.now)) {
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
		d.failed = map[jobID]bool{}
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
		developer.unasked(photos)
		return printing(func(c *library.Catalog) error { return c.Unprint(photos) })
	}
	d := &developer
	d.Lock()
	defer d.Unlock()
	d.converts = append(d.converts, photos)
	d.convertOf += len(photos)
	d.start()
	return nil
}

// converted is k done with, printed or not.
func (d *queue) converted(k library.Key) {
	d.Lock()
	defer d.Unlock()
	if slices.Contains(d.converting, k) {
		d.convertDone++
	}
	d.converting = slices.DeleteFunc(d.converting, func(c library.Key) bool { return c == k })
	d.convertsLeft()
}

// unasked are photos to be shown as they came: not printed, if they are
// still to be.
func (d *queue) unasked(photos []library.Key) {
	d.Lock()
	defer d.Unlock()
	asked := func(k library.Key) bool { return slices.Contains(photos, k) }
	n := len(d.converting)
	d.converting = slices.DeleteFunc(d.converting, asked)
	d.convertOf -= n - len(d.converting)
	for i := range d.converts {
		n := len(d.converts[i])
		d.converts[i] = slices.DeleteFunc(d.converts[i], asked)
		d.convertOf -= n - len(d.converts[i])
	}
	d.converts = slices.DeleteFunc(d.converts, func(g []library.Key) bool { return len(g) == 0 })
	d.convertsLeft()
}

// convertsLeft counts the negatives asked from 0 again once none is left;
// under the lock.
func (d *queue) convertsLeft() {
	if len(d.converting) == 0 && len(d.converts) == 0 {
		d.convertOf, d.convertDone = 0, 0
	}
}

// convert prints the negatives photos: each one's bounds and frame read on
// a decode at half the size, once for copies of the same content, then they
// share the film's base (raw.SharedBase); each is shown printed, cropped to
// its frame, once its print is made, one after another, as a turn shows.
func convert(photos []library.Key) {
	type negative struct {
		copies []library.Key
		src    string
		frame  [4]float64
	}
	var negatives []negative
	var all []raw.Bounds
	for _, copies := range byHash(photos) {
		k := copies[0]
		src, _, err := pictureOf(k, os.Stat)
		if err == nil && !isRAF(src) {
			err = errNotRaw
		}
		var img *raw.Image
		var meta *raw.Meta
		if err == nil {
			img, meta, err = raw.Develop(context.Background(), src, raw.NegativeHalf, false)
		}
		var b raw.Bounds
		var frame [4]float64
		if err == nil {
			b, frame, err = raw.Analyse(img, meta)
		}
		if err != nil {
			if !errors.Is(err, errNotRaw) {
				log.Printf("reading the negative %s: %v", k.Hash, err)
			}
			for _, k := range copies {
				developer.converted(k)
			}
			continue
		}
		negatives = append(negatives, negative{copies, src, thumb.UprightShares(frame, library.Orientation(src))})
		all = append(all, b)
	}
	if len(all) == 0 {
		return
	}
	shared := raw.SharedBase(all)
	for i, n := range negatives {
		bounds := shared[i].String()
		for _, k := range n.copies {
			printNegative(k, rawJob{name: printed(k.Hash, library.PositiveName(bounds)), hash: k.Hash, src: n.src, negative: bounds}, n.frame)
			developer.converted(k)
		}
	}
}

// errNotRaw is a photo to print that is no raw file: left as it is.
var errNotRaw = errors.New("not a raw file")

// byHash are photos in groups of copies of the same content, in turn.
func byHash(photos []library.Key) [][]library.Key {
	var out [][]library.Key
	at := map[string]int{}
	for _, k := range photos {
		if i, ok := at[k.Hash]; ok {
			out[i] = append(out[i], k)
			continue
		}
		at[k.Hash] = len(out)
		out = append(out, []library.Key{k})
	}
	return out
}

// printNegative develops j, the print of k, and shows k printed, cropped to frame.
func printNegative(k library.Key, j rawJob, frame [4]float64) {
	dst, err := j.dst()
	if err != nil {
		return
	}
	if _, err := os.Stat(dst); err != nil {
		develop(j, false)
	}
	// Not printed, it stays as it came, not the camera's negative under the print's name.
	if _, err := os.Stat(dst); err != nil {
		return
	}
	// Under the lock: shown as it came meanwhile, it is not printed after.
	d := &developer
	d.Lock()
	defer d.Unlock()
	if !slices.Contains(d.converting, k) {
		return
	}
	if err := printing(func(c *library.Catalog) error { return c.Print(k, j.negative, frame) }); err != nil {
		log.Printf("printing the negative %s: %v", k.Hash, err)
	}
}

// printing is set, a write of prints to the library's catalog.
func printing(set func(*library.Catalog) error) error {
	c, _, err := openLibrary()
	if err != nil {
		return err
	}
	if c == nil {
		return fs.ErrNotExist
	}
	defer c.Close()
	err = set(c)
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
			group := d.converts[0]
			d.converting, d.converts = slices.Clone(group), d.converts[1:]
			d.Unlock()
			convert(group)
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
		d.recent = append(d.recent, j.hash)
		d.recent = d.recent[max(0, len(d.recent)-recentKept):]
	}
	if err != nil {
		d.failed[j.id()] = true
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
