// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
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
	making  = turns{n: 2} // a phone's pictures
	// All the phones' together, "": the window is left a slot, where there are two.
	allMaking = turns{n: max(1, cap(thumbSlots)-1)}
	lists     sync.Map // profile → *phoneList
	settling  = 2 * time.Second
)

// phoneList is the last listing made of a profile's library, and the state
// of the library it is of.
type phoneList struct {
	changes string
	first   time.Time // when that state was first seen
	settled bool
	tag     string
	answer  []byte
}

// openForPhone is the chosen library and its catalog, to read, which the
// caller closes.
func openForPhone() (string, *library.Catalog, error) {
	root, c, err := lookAtLibrary()
	switch {
	case err != nil:
		err = orAway(err)
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
// for each state of the library, whoever asks.
func PhoneList(ctx context.Context, phone Phone) (tag string, answer []byte, err error) {
	root, err := LibraryHere()
	if err != nil {
		return "", nil, err
	}
	end, err := listing.take(ctx, phone.Profile)
	if err != nil {
		return "", nil, err
	}
	defer end()
	kept, _ := lists.LoadOrStore(phone.Profile, &phoneList{})
	l := kept.(*phoneList)
	// Read before the catalog is: what is written meanwhile is another state.
	now, changes := time.Now(), root+"\x00"+library.Changes(catalogPath(root))
	if l.changes != changes {
		l.first = now
	} else if l.settled {
		return l.tag, l.answer, nil
	}
	// A disk that keeps times by the second, two on FAT, shows two writes of
	// one as one state: it is believed once none can be of the same time, by
	// the file's clock or, that one ahead, by ours.
	info, err := os.Stat(catalogPath(root))
	settled := now.Sub(l.first) > settling || err == nil && now.Sub(info.ModTime()) > settling
	at, c, err := openForPhone()
	if err != nil {
		return "", nil, err
	}
	defer c.Close()
	list, err := c.Listing(phone.Profile)
	if err != nil {
		return "", nil, orAway(err)
	}
	tag, answer, err = list.Tag()
	// Another library chosen meanwhile is listed at the next question.
	if err == nil && at == root {
		*l = phoneList{changes: changes, first: l.first, settled: settled, tag: tag, answer: answer}
	}
	return tag, answer, err
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
	case errors.Is(err, ErrNoLibrary), errors.As(err, new(LibraryGoneError)), errors.As(err, new(syscall.Errno)) && !errors.Is(err, fs.ErrNotExist):
		return err
	case errors.Is(err, fs.ErrNotExist):
		return ErrFileGone
	}
	return otherwise
}

// PhoneThumbnail is Thumbnail of a photo of phone's library.
func PhoneThumbnail(ctx context.Context, phone Phone, hash string) (file string, kept bool, err error) {
	return phonePicture(ctx, phone, hash, "thumbs", thumb.Size)
}

// PhonePreview is Preview of a photo of phone's library.
func PhonePreview(ctx context.Context, phone Phone, hash string) (file string, kept bool, err error) {
	return phonePicture(ctx, phone, hash, "previews", thumb.PreviewSize)
}

func phonePicture(ctx context.Context, phone Phone, hash, dir string, size int) (file string, kept bool, err error) {
	root, p, err := phonePhoto(phone, hash)
	if err != nil {
		return "", false, err
	}
	end := func() {}
	defer func() { end() }()
	file, kept, err = cached(ctx, dir, size, hash, func() (string, string, error) {
		done, err := phoneTurn(ctx, phone)
		if err != nil {
			return "", "", err
		}
		end = done
		return pictureOf(p.Key(), func(path string) (fs.FileInfo, error) {
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
	f, _, err := openInside(root, p.Path)
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
}

// PhoneOriginal opens the file of a photo of phone's library.
func PhoneOriginal(phone Phone, hash string) (Original, error) {
	root, p, err := phonePhoto(phone, hash)
	if err != nil {
		return Original{}, err
	}
	f, info, err := openInside(root, p.Path)
	if err != nil {
		return Original{}, err
	}
	o := Original{File: f, Type: library.TypeOf(p.Name), Changed: info.ModTime(), Version: hash, Kept: info.Size() == p.Bytes}
	if !o.Kept {
		o.Version = versionOf(hash, info)
	}
	return o, nil
}
