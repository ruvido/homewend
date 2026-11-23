// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// GridPhoto is one tile of the window's grid.
type GridPhoto struct {
	Hash  string    `json:"hash"`
	Name  string    `json:"name"`
	Taken time.Time `json:"taken,omitzero"`
	// Guessed: how much of Taken is guessed ("year", "day", "time");
	// empty when the date is known.
	Guessed string `json:"guessed,omitempty"`
	Video   bool   `json:"video,omitempty"`
	// Missing: its file is gone from the library, deleted or moved by hand.
	Missing bool `json:"missing,omitempty"`
	// Turn: quarter turns anticlockwise the person gave it.
	Turn int `json:"turn,omitempty"`
	// Archived: in the Archive, shown in an album.
	Archived bool `json:"archived,omitempty"`
	// Origin: the app it came through, or a screenshot; empty when it came
	// from a camera or nothing says.
	Origin library.Origin `json:"origin,omitempty"`
}

// ErrNoLibrary is asking for photos before a library folder was chosen.
var ErrNoLibrary = errors.New("no library folder chosen yet")

// ChosenLibrary is the library folder, or ErrNoLibrary before one is chosen.
func ChosenLibrary() (string, error) {
	root, err := DefaultLibrary()
	if err == nil && root == "" {
		err = ErrNoLibrary
	}
	return root, err
}

// LibraryHere is the library folder to write into: chosen, and there. A
// library on a disk unplugged or a share not mounted is said
// (LibraryGoneError), never made again on the local disk.
func LibraryHere() (string, error) {
	root, err := ChosenLibrary()
	if err != nil {
		return "", err
	}
	s, err := LoadSettings()
	if err != nil {
		return "", err
	}
	if libraryAway(root, s.Filled) {
		return "", LibraryGoneError{Root: root}
	}
	return root, seenFilled(root, s)
}

// seenFilled remembers root as the library whose catalog was seen, once
// it has one, for libraryAway.
func seenFilled(root string, s Settings) error {
	if root == "" || s.Filled == root {
		return nil
	}
	if _, err := os.Stat(catalogPath(root)); err != nil {
		return nil
	}
	_, err := UpdateSettings(func(s *Settings) { s.Filled = root })
	return err
}

// libraryAway is the library folder not there, or there without the
// catalog it was seen with (filled): a disk unplugged, a share not mounted.
func libraryAway(root, filled string) bool {
	if _, err := os.Stat(root); err != nil {
		return true
	}
	_, err := os.Stat(catalogPath(root))
	return root == filled && errors.Is(err, fs.ErrNotExist)
}

func openLibrary() (*library.Catalog, string, error) {
	root, err := ChosenLibrary()
	if err != nil {
		return nil, "", err
	}
	if _, err := os.Stat(catalogPath(root)); errors.Is(err, fs.ErrNotExist) {
		return nil, root, nil
	}
	c, err := library.OpenCatalog(catalogPath(root))
	return c, root, err
}

// The views of the library besides the grid, a year and an album.
const (
	ViewMissing = "missing" // the photos not on the disk
	ViewArchive = "archive"
	ViewTrash   = "trash"
)

// Photos is a page of the grid, newest first: all of it, a year ("" for no
// date), an album, or a view.
// Only the photos of the accounts shown are in it.
func Photos(s Settings, year, album string, noDate bool, view string, offset, limit int) ([]GridPhoto, error) {
	c, root, err := openLibrary()
	if err != nil || c == nil {
		return []GridPhoto{}, err
	}
	defer c.Close()
	accounts, err := Accounts()
	if err != nil {
		return nil, err
	}
	shown := shownAddresses(accounts, s.Hidden)
	gone, err := shownMissing(c, root, shown)
	if err != nil {
		return nil, err
	}
	isGone := map[string]bool{}
	for _, g := range gone {
		isGone[g.Hash] = true
	}
	f := library.Filter{Year: year, Album: album, NoDate: noDate, Offset: offset, Limit: limit, Accounts: shown}
	// An archived photo is still in its album and still missing.
	f.Grid = album == "" && view == ""
	f.Archived = view == ViewArchive
	f.Trashed = view == ViewTrash
	if view == ViewMissing {
		f.Hashes = hashesOf(gone)
	}
	page, err := c.Page(f)
	if err != nil {
		return nil, err
	}
	out := make([]GridPhoto, len(page))
	for i, p := range page {
		out[i] = GridPhoto{Hash: p.Hash, Name: p.Name, Taken: p.Taken, Guessed: p.Source.Guessed(), Video: library.IsVideo(p.Name), Missing: isGone[p.Hash], Turn: p.Turn, Archived: p.Archived}
		if p.Origin != library.OriginCamera && p.Origin != library.OriginOther {
			out[i].Origin = p.Origin
		}
	}
	return out, nil
}

// The photos whose file is gone, as last looked at, and when.
var (
	missingMu   sync.Mutex
	missingRoot string
	missingAt   time.Time
	missingList []library.Gone
)

// missingIn are the photos of the library at root whose file is gone,
// looked at again when the last look is older than half a minute.
// shortcut: every file is looked at on the request that finds the list
// old; seconds on a share of 100,000 photos; move it to the background
// when measured slow.
func missingIn(c *library.Catalog, root string) ([]library.Gone, error) {
	missingMu.Lock()
	defer missingMu.Unlock()
	if missingRoot == root && time.Since(missingAt) < 30*time.Second {
		return missingList, nil
	}
	list, err := c.Missing(root)
	if err != nil {
		return nil, err
	}
	missingRoot, missingAt, missingList = root, time.Now(), list
	return list, nil
}

// shownMissing are the photos whose file is gone, of the accounts shown
// (nil: all): the sidebar, the strip, the view and the take-out count the
// same ones.
func shownMissing(c *library.Catalog, root string, shown []string) ([]library.Gone, error) {
	gone, err := missingIn(c, root)
	if err != nil || shown == nil || len(gone) == 0 {
		return gone, err
	}
	page, err := c.Page(library.Filter{Hashes: hashesOf(gone), Accounts: shown})
	if err != nil {
		return nil, err
	}
	in := map[string]bool{}
	for _, p := range page {
		in[p.Hash] = true
	}
	var kept []library.Gone
	for _, g := range gone {
		if in[g.Hash] {
			kept = append(kept, g)
		}
	}
	return kept, nil
}

// hashesOf is never nil: to the catalog's filter nil is every photo.
func hashesOf(gone []library.Gone) []string {
	hashes := make([]string, 0, len(gone))
	for _, g := range gone {
		hashes = append(hashes, g.Hash)
	}
	return hashes
}

// TakeOutMissing takes out of the library the photo of hash whose file is
// gone, or with hash "" every one of the accounts shown: a clean-up, so that a card that still
// has them brings them in again, unless keepOut. It says how many.
func TakeOutMissing(hash string, keepOut bool) (int, error) {
	root, err := LibraryHere()
	if err != nil {
		return 0, err
	}
	unlock, err := takeLibrary(context.Background(), root)
	if err != nil {
		return 0, err
	}
	defer unlock()
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return 0, err
	}
	defer c.Close()
	s, err := LoadSettings()
	if err != nil {
		return 0, err
	}
	accounts, err := Accounts()
	if err != nil {
		return 0, err
	}
	missingMu.Lock()
	missingAt = time.Time{} // looked at again, now and after
	missingMu.Unlock()
	gone, err := shownMissing(c, root, shownAddresses(accounts, s.Hidden))
	if err != nil {
		return 0, err
	}
	var hashes []string
	for _, g := range gone {
		if hash == "" || g.Hash == hash {
			hashes = append(hashes, g.Hash)
		}
	}
	if err := c.TakeOut(hashes, keepOut); err != nil {
		return 0, err
	}
	missingMu.Lock()
	missingAt = time.Time{}
	missingMu.Unlock()
	return len(hashes), nil
}

// Mark sets state on the photos of hashes filed in profile's folder ("":
// any), or clears it, and says which those were.
func Mark(profile string, state library.State, hashes []string, on bool) ([]string, error) {
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return []string{}, err
	}
	defer c.Close()
	done, err := c.Mark(state, hashes, profile, on)
	// A photo in the Trash is not missing: the list is looked at again.
	missingMu.Lock()
	missingAt = time.Time{}
	missingMu.Unlock()
	return done, err
}

// TurnLeft gives a photo of the library a quarter turn anticlockwise, kept in
// the catalog, and says how many it has now.
func TurnLeft(hash string) (int, error) {
	c, _, err := openLibrary()
	if err != nil {
		return 0, err
	}
	if c == nil {
		return 0, fs.ErrNotExist
	}
	defer c.Close()
	return c.TurnLeft(hash)
}

// GiveDates gives photos of the library a date chosen in the window: the
// earliest of them when, the others keep their distance. Files move, so the
// library is held as an import holds it. It says how many moved.
func GiveDates(hashes []string, when time.Time) (int, error) {
	root, err := LibraryHere()
	if err != nil {
		return 0, err
	}
	unlock, err := takeLibrary(context.Background(), root)
	if err != nil {
		return 0, err
	}
	defer unlock()
	c, _, err := openLibrary()
	if err != nil {
		return 0, err
	}
	if c == nil {
		return 0, fs.ErrNotExist
	}
	defer c.Close()
	return library.GiveDates(root, c, hashes, when)
}

// thumbSlots keeps thumbnails from taking every core while a grid scrolls.
var thumbSlots = make(chan struct{}, max(1, runtime.NumCPU()-1))

// Thumbnail returns the file of a photo's thumbnail, making it the first time.
func Thumbnail(ctx context.Context, hash string) (string, error) {
	return thumbnailOf(ctx, hash, func() (string, error) { return Original(hash) })
}

// Preview is the picture the viewer shows a photo of the library with, at
// the size of a screen: kept like a thumbnail, so the original, on a slow
// disk or a share, is read once.
//
// shortcut: no limit on the cache's size (about 0.5–1 MB a photo looked at),
// prune by age when a library's previews grow past a few GB.
func Preview(ctx context.Context, hash string) (string, error) {
	return cached(ctx, "previews", thumb.PreviewSize, hash, func() (string, error) { return Original(hash) })
}

// thumbnailOf is the thumbnail of the content hash, kept by it: made the
// first time from the file source finds, a photo of the library or one on
// a card being looked at.
func thumbnailOf(ctx context.Context, hash string, source func() (string, error)) (string, error) {
	return cached(ctx, "thumbs", thumb.Size, hash, source)
}

// cached is the picture of the content hash at size, kept by it in the
// cache's folder dir.
func cached(ctx context.Context, dir string, size int, hash string, source func() (string, error)) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dst := thumb.Path(filepath.Join(cache, "homewend", dir), hash)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	src, err := source()
	if err != nil {
		return "", err
	}
	select {
	case thumbSlots <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-thumbSlots }()
	if _, err := os.Stat(dst); err == nil { // made while this one waited
		return dst, nil
	}
	return dst, thumb.Make(ctx, src, dst, size)
}

// Original is the file of a photo in the library.
func Original(hash string) (string, error) {
	c, root, err := openLibrary()
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", fs.ErrNotExist
	}
	defer c.Close()
	rel, ok, err := c.PathOf(hash)
	if err != nil || !ok {
		return "", errors.Join(err, fs.ErrNotExist)
	}
	return filepath.Join(root, rel), nil
}

// Folder is where a place of the library is on disk: a profile, a year, the
// photos with no date, or an album, as Organize files them. The profile is
// the account's, when one is named, else the one profile the window shows;
// with several shown and none named, a place is in each, and the library
// itself is the folder.
func Folder(s Settings, account, year, album string, noDate bool) (string, error) {
	root, err := ChosenLibrary()
	if err != nil {
		return "", err
	}
	accounts, err := Accounts()
	if err != nil {
		return "", err
	}
	return folder(root, accounts, s.Hidden, account, year, album, noDate), nil
}

func folder(root string, accounts []AccountInfo, hidden []string, account, year, album string, noDate bool) string {
	var profiles []string
	for _, a := range accounts {
		named := account != "" && a.ID == account
		shown := account == "" && a.Email != "" && !slices.Contains(hidden, a.ID)
		if (named || shown) && !slices.Contains(profiles, a.Profile) {
			profiles = append(profiles, a.Profile)
		}
	}
	if len(profiles) != 1 {
		return root
	}
	home := filepath.Join(root, profiles[0])
	switch {
	case album != "":
		return filepath.Join(home, "albums", album)
	case noDate:
		return filepath.Join(home, "undated")
	case year != "":
		return filepath.Join(home, year)
	}
	return home
}

// ProfileFolder is the folder of a profile in the library.
func ProfileFolder(profile string) (string, error) {
	if !ValidProfile(profile) {
		return "", ErrBadProfile
	}
	root, err := ChosenLibrary()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, profile), nil
}

// Latest are the n photos that arrived last.
func Latest(n int) ([]GridPhoto, error) {
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return []GridPhoto{}, err
	}
	defer c.Close()
	list, err := c.Latest(n)
	out := make([]GridPhoto, len(list))
	for i, p := range list {
		out[i] = GridPhoto{Hash: p.Hash, Name: p.Name, Video: library.IsVideo(p.Name), Turn: p.Turn}
	}
	return out, err
}

// PhotoAbout is what the viewer says beside a photo's name.
type PhotoAbout struct {
	Albums   []string `json:"albums"`
	Accounts []string `json:"accounts"` // the accounts', by id
	Local    []string `json:"local"`    // the cards' and folders', by name
	// Dated: where its date comes from (library.DateSource).
	Dated library.DateSource `json:"dated"`
	Bytes int64              `json:"bytes"`
	Path  string             `json:"path"` // in the library
}

// About is what the viewer says of a photo: its albums, where it came from,
// its date's source, its size and its place.
func About(hash string) (PhotoAbout, error) {
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return PhotoAbout{Albums: []string{}, Accounts: []string{}, Local: []string{}}, err
	}
	defer c.Close()
	var sources []string
	about := PhotoAbout{Accounts: []string{}, Local: []string{}}
	about.Albums, sources, err = c.About(hash)
	for _, s := range sources {
		if name, ok := strings.CutPrefix(s, library.LocalSource); ok {
			about.Local = append(about.Local, name)
		} else {
			about.Accounts = append(about.Accounts, s)
		}
	}
	if err != nil {
		return about, err
	}
	p, err := c.Photo(hash)
	about.Dated, about.Bytes, about.Path = p.Source, p.Bytes, p.Path
	return about, err
}
