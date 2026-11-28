// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// A phone reads its owner's library. Nothing here changes it, and the catalog
// is closed before an answer is sent: a slow phone holds no disk.

var (
	// ErrNotHere is a photo, or a month, the phone's library does not have.
	ErrNotHere = errors.New("not in this library")
	// ErrFileGone is a photo of the library whose file is not in its folder.
	ErrFileGone = errors.New("its file is not in the library")
	// ErrNoPicture is a photo no picture could be made of.
	ErrNoPicture = errors.New("no picture can be made of it")
)

// turns lets n at a time do a thing for each name.
type turns struct {
	n  int
	of sync.Map // name → chan struct{}
}

// take waits for name's turn; end, when there is no error, ends it.
func (t *turns) take(ctx context.Context, name string) (end func(), err error) {
	c, _ := t.of.LoadOrStore(name, make(chan struct{}, t.n))
	slot := c.(chan struct{})
	select {
	case slot <- struct{}{}:
		return func() { <-slot }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

var (
	listing = turns{n: 1} // of a profile's library
	reading = turns{n: 4} // a phone's months: those an app asks at once, and some it left
	sending = turns{n: 6} // a phone's originals: a player asks for two or three parts, and some it left
	making  = turns{n: 2} // a phone's pictures
	// All the phones' together, "": the window is left a slot, where there are two.
	allMaking = turns{n: max(1, cap(thumbSlots)-1)}
	lists     sync.Map // profile → *atomic.Pointer[phoneList]
)

// phoneList is the last listing made of a profile's library, and the state of
// the catalog it is of; hashes are the photos in it. It is not changed: a new
// one takes its place.
type phoneList struct {
	state  library.Written
	began  time.Time // when it began to be worked out
	tag    string
	answer []byte
	hashes map[library.Hash]string // each with its negative's bounds
}

func listOfProfile(profile string) *atomic.Pointer[phoneList] {
	l, _ := lists.LoadOrStore(profile, new(atomic.Pointer[phoneList]))
	return l.(*atomic.Pointer[phoneList])
}

// heartsSet is set, a write to c of marks alone: what was listed before it is
// still so after it, when nothing else was written meanwhile.
func heartsSet(c *library.Catalog, set func() error) error {
	return writing(c, set, func(_, _ library.Written) {})
}

// unlisted is set, a write to c the window's lists do not show either.
func unlisted(c *library.Catalog, set func() error) error {
	return writing(c, set, sameChanges)
}

// writing is set, a write to c after which what a phone was listed is still
// so; alone is told when nothing else was written meanwhile.
func writing(c *library.Catalog, set func() error, alone func(before, after library.Written)) error {
	before, err := c.Written()
	if err != nil {
		return set()
	}
	if err := set(); err != nil {
		return err
	}
	if after, err := c.Written(); err == nil && after.Next(before) {
		alone(before, after)
		lists.Range(func(_, of any) bool {
			kept := of.(*atomic.Pointer[phoneList])
			if l := kept.Load(); l != nil && l.state == before {
				now := *l
				now.state = after
				kept.CompareAndSwap(l, &now)
			}
			return true
		})
	}
	return nil
}

// openForPhone is the chosen library and its catalog, to read, which the
// caller closes.
func openForPhone() (string, *library.Catalog, error) {
	root, c, err := lookAtLibrary()
	switch {
	case err != nil:
	case root == "":
		err = ErrNoLibrary
	case c == nil:
		err = ErrEmptyLibrary
	}
	return root, c, err
}

// orAway is err, or that the library went away meanwhile.
func orAway(err error) error {
	if _, gone := LibraryHere(); gone != nil {
		return gone
	}
	return err
}

// PhoneList is the listing of phone's library and its tag, worked out once
// for each state of the library, whoever asks; those who asked before it began
// to be, share one.
func PhoneList(ctx context.Context, phone Phone) (tag string, answer []byte, err error) {
	asked := time.Now()
	end, err := listing.take(ctx, phone.Profile)
	if err != nil {
		return "", nil, err
	}
	defer end()
	_, c, err := openForPhone()
	if err != nil {
		return "", nil, err
	}
	defer c.Close()
	// Read before the photos are: what is written meanwhile is another state.
	state, err := c.Written()
	if err != nil {
		return "", nil, orAway(err)
	}
	kept := listOfProfile(phone.Profile)
	if l := kept.Load(); l != nil && (l.state == state || l.state.Path == state.Path && l.began.After(asked)) {
		return l.tag, l.answer, nil
	}
	began := time.Now()
	list, hashes, err := c.Listing(phone.Profile)
	if err != nil {
		return "", nil, orAway(err)
	}
	if tag, answer, err = list.Tag(); err == nil {
		kept.Store(&phoneList{state, began, tag, answer, hashes})
	}
	return tag, answer, err
}

// listedHere is whether the photo is in the last listing of phone's library,
// which is as it was then, the library, and the bounds it is printed with,
// if a negative: a small copy of one listed is given without the catalog.
// shortcut: by the files' times alone; a photo deleted within a tick of the
// disk's clock after another write is given until the library is listed.
func listedHere(phone Phone, hash string) (root, negative string, here bool, err error) {
	root, err = LibraryHere()
	if err != nil {
		return "", "", false, err
	}
	l := listOfProfile(phone.Profile).Load()
	var h library.Hash
	if l == nil || l.state.Path != catalogPath(root) || l.state.Changes != library.Changes(l.state.Path) {
		return root, "", false, nil
	}
	if _, err := hex.Decode(h[:], []byte(hash)); err == nil {
		negative, here = l.hashes[h]
	}
	return root, negative, here, nil
}

// PhoneMonth is a month of phone's library, its photos as JSON. Done, when
// there is no error, is called once it is sent: a few at a time are held for
// each phone.
func PhoneMonth(ctx context.Context, phone Phone, month string) (tag string, photos []byte, done func(), err error) {
	end, err := reading.take(ctx, phone.ID)
	if err != nil {
		return "", nil, nil, err
	}
	if tag, photos, err = monthOf(phone.Profile, month); err != nil {
		end()
		return "", nil, nil, err
	}
	return tag, photos, end, nil
}

func monthOf(profile, month string) (tag string, photos []byte, err error) {
	_, c, err := openForPhone()
	if err != nil {
		return "", nil, err
	}
	defer c.Close()
	if tag, photos, err = c.Month(profile, month); err != nil {
		return "", nil, orAway(err)
	}
	if photos == nil {
		err = ErrNotHere
	}
	return tag, photos, err
}

// phonePhoto is the photo of phone's library, and the library.
func phonePhoto(phone Phone, hash string) (root string, p library.Photo, err error) {
	root, c, err := openForPhone()
	if err != nil {
		return "", p, err
	}
	defer c.Close()
	p, here, err := c.Here(library.Key{Hash: hash, Profile: phone.Profile})
	if err == nil && here {
		p.Negative, err = c.Negative(p.Key())
	}
	if err != nil {
		return "", p, orAway(err)
	}
	if !here {
		err = ErrNotHere
	}
	return root, p, err
}

// openInside opens a file of the library at root, by its path in it, unless a
// link leads it out of there.
func openInside(root, path string) (f *os.File, info fs.FileInfo, err error) {
	r, err := os.OpenRoot(root)
	if err == nil {
		defer r.Close()
		// Looked at first: opening a pipe waits for who writes it.
		if info, err = r.Stat(filepath.FromSlash(path)); err == nil && !info.Mode().IsRegular() {
			err = ErrFileGone
		}
	}
	if err == nil {
		f, err = r.Open(filepath.FromSlash(path))
	}
	if err == nil {
		return f, info, nil
	}
	return nil, nil, failed(err, ErrFileGone)
}

// failed is why a photo's file, or its picture, is not given: the library is
// away, the file gone, a disk failed and may not the next time, or else
// otherwise.
func failed(err, otherwise error) error {
	err = orAway(err)
	switch {
	// A folder that is a file now, or a link to itself, has no file in it.
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR), errors.Is(err, syscall.ELOOP):
		return ErrFileGone
	case errors.Is(err, ErrNoLibrary), errors.As(err, new(LibraryGoneError)), errors.As(err, new(syscall.Errno)):
		return err
	}
	return otherwise
}

// PhoneThumbnail is Thumbnail of a photo of phone's library.
func PhoneThumbnail(ctx context.Context, phone Phone, hash string) (file string, kept bool, err error) {
	return phonePicture(ctx, phone, hash, thumbsDir)
}

// PhonePreview is Preview of a photo of phone's library.
func PhonePreview(ctx context.Context, phone Phone, hash string) (file string, kept bool, err error) {
	return phonePicture(ctx, phone, hash, previewsDir)
}

// PhoneScreen is a photo of phone's library at the size of a phone's screen,
// for the phone to keep and show with no computer in reach.
func PhoneScreen(ctx context.Context, phone Phone, hash string) (file string, kept bool, err error) {
	return phonePicture(ctx, phone, hash, screensDir)
}

func phonePicture(ctx context.Context, phone Phone, hash string, dir madeDir) (file string, kept bool, err error) {
	root, negative, here, err := listedHere(phone, hash)
	if err == nil && !here {
		var p library.Photo
		root, p, err = phonePhoto(phone, hash)
		negative = p.Negative
	}
	if err != nil {
		return "", false, err
	}
	end := func() {}
	defer func() { end() }()
	file, kept, err = cached(ctx, dir, hash, drawing{negative: negative}, func() (string, string, error) {
		done, err := phoneTurn(ctx, phone)
		if err != nil {
			return "", "", err
		}
		end = done
		return pictureOf(library.Key{Hash: hash, Profile: phone.Profile}, func(path string) (fs.FileInfo, error) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return nil, err
			}
			f, info, err := openInside(root, rel)
			if err == nil {
				f.Close()
			}
			return info, err
		})
	})
	switch {
	case err == nil:
	case ctx.Err() != nil:
		err = ctx.Err()
	default:
		err = failed(err, ErrNoPicture)
	}
	return file, kept, err
}

// phoneTurn waits for phone's turn to have a picture made, or a file read.
func phoneTurn(ctx context.Context, phone Phone) (end func(), err error) {
	mine, err := making.take(ctx, phone.ID)
	if err != nil {
		return nil, err
	}
	all, err := allMaking.take(ctx, "")
	if err != nil {
		mine()
		return nil, err
	}
	return func() { all(); mine() }, nil
}

// PhoneInfo is what a photo's file says, as a phone is told it: its pixels, 0
// when not known, how long a video lasts, and its camera.
type PhoneInfo struct {
	W      int    `json:"w"`
	H      int    `json:"h"`
	MS     int64  `json:"ms,omitempty"`
	Camera string `json:"camera,omitempty"`
}

// PhoneInfoOf reads it from the file of a photo of phone's library.
func PhoneInfoOf(ctx context.Context, phone Phone, hash string) (PhoneInfo, error) {
	root, p, err := phonePhoto(phone, hash)
	if err != nil {
		return PhoneInfo{}, err
	}
	end, err := phoneTurn(ctx, phone)
	if err != nil {
		return PhoneInfo{}, err
	}
	defer end()
	f, _, err := openInside(root, library.At(root, p.Path))
	if err != nil {
		return PhoneInfo{}, err
	}
	f.Close()
	path := filepath.Join(root, filepath.FromSlash(p.Path))
	is := fileAt(path)
	out := PhoneInfo{W: is.Width, H: is.Height, Camera: is.Camera}
	if library.IsVideo(path) {
		out.MS = library.VideoLength(path).Milliseconds()
	}
	return out, nil
}

// Original is a photo's file, which the caller closes.
type Original struct {
	*os.File
	Type    string
	Changed time.Time
	// Version names these bytes: the photo's hash while the file is its
	// content, and Kept says it is.
	// shortcut: by its size, as pictureOf.
	Version string
	Kept    bool
	end     func()
}

// Close closes it, for the next of its phone's to be opened.
func (o Original) Close() error {
	defer o.end()
	return o.File.Close()
}

// PhoneOriginal opens the file of a photo of phone's library.
func PhoneOriginal(ctx context.Context, phone Phone, hash string) (Original, error) {
	end, err := sending.take(ctx, phone.ID)
	if err != nil {
		return Original{}, err
	}
	root, p, err := phonePhoto(phone, hash)
	var f *os.File
	var info fs.FileInfo
	if err == nil {
		f, info, err = openInside(root, library.At(root, p.Path))
	}
	if err != nil {
		end()
		return Original{}, err
	}
	o := Original{File: f, Type: library.TypeOf(p.Name), Changed: info.ModTime(), Version: hash, Kept: info.Size() == p.Bytes, end: end}
	if !o.Kept {
		o.Version = versionOf(hash, info)
	}
	return o, nil
}
