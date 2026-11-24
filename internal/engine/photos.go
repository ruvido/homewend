// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"cmp"
	"context"
	"errors"
	"fmt"
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

// GridPhoto is one tile of the window's grid: a photo two libraries have is
// two tiles.
type GridPhoto struct {
	Hash    string `json:"hash"`
	Profile string `json:"-"`
	// Of is Profile, by its place in the list's Profiles: on every tile a
	// name would be a tenth of the list. None is the first.
	Of int `json:"of,omitempty"`
	// Shared: another tile of the list is the same photo, in another library.
	Shared bool      `json:"shared,omitempty"`
	Name   string    `json:"name"`
	Taken  time.Time `json:"taken,omitzero"`
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
	// Favorite: given a heart.
	Favorite bool `json:"favorite,omitempty"`
	// Origin: the app it came through, or a screenshot; empty when it came
	// from a camera or nothing says.
	Origin library.Origin `json:"origin,omitempty"`
}

// Key is the photo's.
func (p GridPhoto) Key() library.Key { return library.Key{Hash: p.Hash, Profile: p.Profile} }

// PhotoList is a list of tiles, and the profiles theirs are among: those there
// are, in the overview's order, whatever is listed.
type PhotoList struct {
	Profiles []string    `json:"profiles"`
	Photos   []GridPhoto `json:"photos"`
}

// listOf is photos as a list: whose each is, and which two libraries have.
func listOf(photos []GridPhoto, profiles []string) PhotoList {
	place := map[string]int{}
	for i, p := range profiles {
		place[p] = i
	}
	// No longer an account's, a phone's or an import's, its photos are there.
	var gone []string
	count := map[string]int{}
	for i := range photos {
		p := photos[i].Profile
		if _, known := place[p]; !known && count[p] == 0 {
			gone = append(gone, p)
		}
		count[p]++
	}
	slices.Sort(gone)
	for _, p := range gone {
		place[p] = len(profiles)
		profiles = append(profiles, p)
	}
	most := ""
	for p, n := range count {
		if n > count[most] {
			most = p
		}
	}
	// The few of the smaller libraries are remembered, the many looked up.
	first := map[string]int{}
	for i := range photos {
		p := &photos[i]
		p.Of = place[p.Profile]
		if p.Profile == most {
			continue
		}
		if j, twice := first[p.Hash]; twice {
			p.Shared, photos[j].Shared = true, true
		} else {
			first[p.Hash] = i
		}
	}
	if len(first) > 0 {
		for i := range photos {
			if p := &photos[i]; p.Profile == most {
				if j, twice := first[p.Hash]; twice {
					p.Shared, photos[j].Shared = true, true
				}
			}
		}
	}
	return PhotoList{Profiles: profiles, Photos: photos}
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
	c, err := openAt(root, library.OpenCatalog)
	return c, root, err
}

// openAt opens the catalog of the library at root as open does, nil when
// nothing is in it.
func openAt(root string, open func(string) (*library.Catalog, error)) (*library.Catalog, error) {
	if _, err := os.Stat(catalogPath(root)); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return open(catalogPath(root))
}

// openHere is openAt of the chosen library, which is there (LibraryHere): one
// taken out as it is opened is gone, not one with nothing in it.
func openHere(open func(string) (*library.Catalog, error)) (string, *library.Catalog, error) {
	root, err := LibraryHere()
	if err != nil {
		return "", nil, err
	}
	c, err := openAt(root, open)
	if c == nil {
		if _, gone := LibraryHere(); gone != nil {
			return "", nil, gone
		}
	}
	return root, c, err
}

// The views of the library besides the grid, a year and an album.
const (
	ViewMissing = "missing" // the photos not on the disk
	ViewArchive = "archive"
	ViewTrash   = "trash"
)

// Photos are the grid's, newest first: all of it, a year ("" for no date),
// an album of an account ("": not known), or a view; with a limit, the first of them.
// Only the photos of the accounts shown are in it.
func Photos(s Settings, year, album, albumOf string, noDate bool, view string, limit int) (PhotoList, error) {
	c, root, err := openLibrary()
	if err != nil || c == nil {
		return listOf([]GridPhoto{}, []string{}), err
	}
	defer c.Close()
	accounts, profiles, err := people(s)
	if err != nil {
		return PhotoList{}, err
	}
	shown := shownAddresses(accounts, s.Hidden)
	var all []library.Gone
	if view == ViewMissing {
		// It waits for the first look: none yet is not none missing.
		if all, err = firstMissing(root); err != nil {
			return PhotoList{}, err
		}
	} else {
		all = missingIn(root)
	}
	gone, err := shownOf(all, c, shown)
	if err != nil {
		return PhotoList{}, err
	}
	isGone := map[library.Key]bool{}
	for _, g := range gone {
		isGone[g.Key] = true
	}
	f := library.Filter{Year: year, Album: album, AlbumOf: albumOf, NoDate: noDate, Limit: limit, Accounts: shown}
	// An archived photo is still in its album and still missing.
	f.Grid = album == "" && view == ""
	f.Archived = view == ViewArchive
	f.Trashed = view == ViewTrash
	if view == ViewMissing {
		f.Only = keysOf(gone)
	}
	page, err := c.Page(f)
	if err != nil {
		return PhotoList{}, err
	}
	out := make([]GridPhoto, len(page))
	for i, p := range page {
		out[i] = GridPhoto{Hash: p.Hash, Profile: p.Profile, Name: p.Name, Taken: p.Taken, Guessed: p.Source.Guessed(), Video: library.IsVideo(p.Name), Missing: isGone[p.Key()], Turn: p.Turn, Archived: p.Archived, Favorite: p.Favorite}
		if p.Origin != library.OriginCamera && p.Origin != library.OriginOther {
			out[i].Origin = p.Origin
		}
	}
	return listOf(out, profiles), nil
}

// The photos whose file is gone, as last looked at. A look at every file
// takes seconds, minutes on a share: it runs beside the requests, which are
// answered with what the last one found.
var missing struct {
	sync.Mutex
	root    string
	at      time.Time // when the last look ended; zero: none did, or look again
	seen    bool      // a look of root ended
	list    []library.Gone
	taken   map[library.Key]bool // taken off the list by hand since the look running began
	running bool
	moves   int // the list changed: a part of LibraryChanges
}

// looking holds one look at a time.
var looking sync.Mutex

// beside runs a look beside the requests; in tests, at once.
var beside = func(look func()) { go look() }

// missingIn are the photos of the library at root whose file is gone, as
// last looked at: none before the first look ends. A look older than half
// a minute is taken again meanwhile.
func missingIn(root string) []library.Gone {
	missing.Lock()
	onLibrary(root)
	look := !missing.running && time.Since(missing.at) > 30*time.Second
	missing.running = missing.running || look
	missing.Unlock()
	if look {
		beside(func() {
			lookForMissing(root)
			missing.Lock()
			missing.running = false
			now := missing.root
			if now == root {
				missing.at = time.Now() // one that failed is not tried on every request
			}
			missing.Unlock()
			// Another library was chosen meanwhile.
			if now != root {
				missingIn(now)
			}
		})
	}
	missing.Lock()
	defer missing.Unlock()
	return missing.list
}

// onLibrary forgets another library's list; missing is held.
func onLibrary(root string) {
	if missing.root != root {
		missing.root, missing.at, missing.seen, missing.list = root, time.Time{}, false, nil
	}
}

// lookForMissing looks at every file of the library at root, now.
func lookForMissing(root string) ([]library.Gone, error) {
	looking.Lock()
	defer looking.Unlock()
	return look(root)
}

// firstMissing is missingIn, once a first look of root has ended.
func firstMissing(root string) ([]library.Gone, error) {
	looking.Lock()
	defer looking.Unlock()
	missing.Lock()
	onLibrary(root)
	seen, list := missing.seen, missing.list
	missing.Unlock()
	if seen {
		return list, nil
	}
	return look(root)
}

// look is lookForMissing, looking held. One whose library is no longer the
// chosen one changes nothing.
func look(root string) ([]library.Gone, error) {
	missing.Lock()
	missing.taken = map[library.Key]bool{}
	missing.Unlock()
	list, err := library.Missing(catalogPath(root), root)
	if err != nil {
		return nil, err
	}
	missing.Lock()
	defer missing.Unlock()
	list = without(list, missing.taken)
	if missing.root == root {
		if !slices.Equal(list, missing.list) {
			missing.moves++
		}
		missing.at, missing.seen, missing.list = time.Now(), true, list
	}
	return list, nil
}

func without(list []library.Gone, photos map[library.Key]bool) []library.Gone {
	return slices.DeleteFunc(slices.Clone(list), func(g library.Gone) bool { return photos[g.Key] })
}

// offMissing takes photos off the list, taken out or in the Trash.
func offMissing(photos []library.Key) {
	missing.Lock()
	defer missing.Unlock()
	if missing.taken == nil {
		missing.taken = map[library.Key]bool{}
	}
	for _, k := range photos {
		missing.taken[k] = true
	}
	missing.list = without(missing.list, missing.taken)
}

// shownOf are those of gone that are of the accounts shown (nil: all): the
// sidebar, the strip, the view and the take-out count the same ones.
func shownOf(gone []library.Gone, c *library.Catalog, shown []string) ([]library.Gone, error) {
	if shown == nil || len(gone) == 0 {
		return gone, nil
	}
	page, err := c.Page(library.Filter{Only: keysOf(gone), Accounts: shown})
	if err != nil {
		return nil, err
	}
	in := map[library.Key]bool{}
	for _, p := range page {
		in[p.Key()] = true
	}
	return slices.DeleteFunc(slices.Clone(gone), func(g library.Gone) bool { return !in[g.Key] }), nil
}

// keysOf is never nil: to the catalog's filter nil is every photo.
func keysOf(gone []library.Gone) []library.Key {
	keys := make([]library.Key, 0, len(gone))
	for _, g := range gone {
		keys = append(keys, g.Key)
	}
	return keys
}

// TakeOutMissing takes out of its library the photo one whose file is
// gone, or with none every one of the accounts shown: a clean-up, so that a card that still
// has them brings them in again, unless keepOut. It says how many.
func TakeOutMissing(one *library.Key, keepOut bool) (int, error) {
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
	// What is taken out is what is gone now.
	gone, err := lookForMissing(root)
	if err != nil {
		return 0, err
	}
	gone, err = shownOf(gone, c, shownAddresses(accounts, s.Hidden))
	if err != nil {
		return 0, err
	}
	var keys []library.Key
	for _, g := range gone {
		if one == nil || g.Key == *one {
			keys = append(keys, g.Key)
		}
	}
	if err := c.TakeOut(keys, keepOut); err != nil {
		return 0, err
	}
	offMissing(keys)
	return len(keys), nil
}

// Mark sets state on photos, or clears it, and says which are in their
// library; with the library away, none.
func Mark(state library.State, photos []library.Key, on bool) ([]library.Key, error) {
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return []library.Key{}, err
	}
	return markIn(c, state, photos, on)
}

// markIn is Mark in the catalog c, which it closes.
func markIn(c *library.Catalog, state library.State, photos []library.Key, on bool) ([]library.Key, error) {
	defer c.Close()
	done, err := c.Mark(state, photos, on)
	if state == library.Favorite {
		lookNow()
	}
	// A photo in the Trash is not missing.
	if state == library.Trashed && on {
		offMissing(done)
	} else if state == library.Trashed {
		// One restored may be missing: looked at again.
		missing.Lock()
		missing.at = time.Time{}
		missing.Unlock()
	}
	return done, err
}

// TurnLeft gives a photo of the library a quarter turn anticlockwise, kept in
// the catalog, and says how many it has now.
func TurnLeft(k library.Key) (int, error) {
	c, _, err := openLibrary()
	if err != nil {
		return 0, err
	}
	if c == nil {
		return 0, fs.ErrNotExist
	}
	defer c.Close()
	return c.TurnLeft(k)
}

// GiveDates gives photos of the library a date chosen in the window: the
// earliest of them when, the others keep their distance. Files move, so the
// library is held as an import holds it. It says how many moved.
func GiveDates(photos []library.Key, when time.Time) (int, error) {
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
	return library.GiveDates(root, c, photos, when)
}

// thumbSlots keeps thumbnails from taking every core while a grid scrolls.
var thumbSlots = make(chan struct{}, max(1, runtime.NumCPU()-1))

// Thumbnail returns the file of a photo's thumbnail, making it the first time;
// kept says it is the content's, which never changes.
func Thumbnail(ctx context.Context, k library.Key) (file string, kept bool, err error) {
	return cached(ctx, "thumbs", thumb.Size, k.Hash, func() (string, string, error) { return pictureOf(k) })
}

// Preview is the picture the viewer shows a photo of the library with, at
// the size of a screen: kept like a thumbnail, so the original, on a slow
// disk or a share, is read once.
//
// shortcut: no limit on the cache's size (about 0.5–1 MB a photo looked at),
// prune by age when a library's previews grow past a few GB.
func Preview(ctx context.Context, k library.Key) (file string, kept bool, err error) {
	return cached(ctx, "previews", thumb.PreviewSize, k.Hash, func() (string, string, error) { return pictureOf(k) })
}

// pictureOf is the file a photo's picture is made from: its own, or else
// another library's, that is still the photo, by its size. With none, its own
// as it is now, and version, the name its picture is kept by: another
// program's edit of one library's file is not the content's picture, which
// every library is shown.
// shortcut: an edit that leaves the size as it was is not seen; reading the
// file to know would be reading each original once more.
func pictureOf(k library.Key) (file, version string, err error) {
	c, root, err := openLibrary()
	if err != nil {
		return "", "", err
	}
	if c == nil {
		return "", "", fs.ErrNotExist
	}
	copies, err := c.Copies(k.Hash)
	c.Close()
	if err != nil {
		return "", "", err
	}
	// Its own first.
	if i := slices.IndexFunc(copies, func(p library.Photo) bool { return p.Key() == k }); i > 0 {
		copies[0], copies[i] = copies[i], copies[0]
	}
	for _, p := range copies {
		at := filepath.Join(root, filepath.FromSlash(p.Path))
		info, err := os.Stat(at)
		switch {
		case err != nil:
		case info.Size() == p.Bytes:
			return at, "", nil
		case p.Key() == k:
			file, version = at, fmt.Sprintf("%s-%x-%x", k.Hash, info.Size(), info.ModTime().UnixNano())
		}
	}
	if file == "" {
		return "", "", fs.ErrNotExist
	}
	return file, version, nil
}

// picturesDir is where the cache keeps the pictures of kind dir.
func picturesDir(dir string) (string, error) {
	cache, err := os.UserCacheDir()
	return filepath.Join(cache, "homewend", dir), err
}

// cached is the picture of the content hash at size, kept by it in the
// cache's folder dir; or, when source says the file it found is not that content
// any more, of the version source names, and kept is false.
func cached(ctx context.Context, dir string, size int, hash string, source func() (file, version string, err error)) (dst string, kept bool, err error) {
	folder, err := picturesDir(dir)
	if err != nil {
		return "", false, err
	}
	there := func(name string) bool {
		dst = thumb.Path(folder, name)
		_, err := os.Stat(dst)
		return err == nil
	}
	if there(hash) {
		return dst, true, nil
	}
	src, version, err := source()
	if err != nil {
		return "", false, err
	}
	name := cmp.Or(version, hash)
	if kept = version == ""; there(name) {
		return dst, kept, nil
	}
	select {
	case thumbSlots <- struct{}{}:
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
	defer func() { <-thumbSlots }()
	if there(name) { // made while this one waited
		return dst, kept, nil
	}
	return dst, kept, thumb.Make(ctx, src, dst, size)
}

// PathOf is where a photo is on the disk, or was.
func PathOf(k library.Key) (string, error) {
	c, root, err := openLibrary()
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", fs.ErrNotExist
	}
	defer c.Close()
	rel, ok, err := c.PathOf(k)
	if err != nil || !ok {
		return "", errors.Join(err, fs.ErrNotExist)
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
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
func Latest(n int) (PhotoList, error) {
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return listOf([]GridPhoto{}, []string{}), err
	}
	defer c.Close()
	s, err := LoadSettings()
	if err != nil {
		return PhotoList{}, err
	}
	_, profiles, err := people(s)
	if err != nil {
		return PhotoList{}, err
	}
	list, err := c.Latest(n)
	out := make([]GridPhoto, len(list))
	for i, p := range list {
		out[i] = GridPhoto{Hash: p.Hash, Profile: p.Profile, Name: p.Name, Video: library.IsVideo(p.Name), Turn: p.Turn}
	}
	return listOf(out, profiles), err
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

// PhotoFile is what a photo's file says of itself: its pixels, 0 when not
// known, and the camera that took it, "" when it does not say.
type PhotoFile struct {
	Width  int    `json:"width"`
	Height int    `json:"height"`
	Camera string `json:"camera"`
}

// FileOf reads them from the photo's file: asked only when they are shown.
func FileOf(k library.Key) (PhotoFile, error) {
	path, err := PathOf(k)
	if err == nil {
		_, err = os.Stat(path)
	}
	if err != nil {
		return PhotoFile{}, err
	}
	var f PhotoFile
	f.Width, f.Height = thumb.Dimensions(path)
	f.Camera = library.CameraOf(path)
	return f, nil
}

// About is what the viewer says of a photo: its albums, where it came from,
// its date's source, its size and its place.
func About(k library.Key) (PhotoAbout, error) {
	c, _, err := openLibrary()
	if err != nil || c == nil {
		return PhotoAbout{Albums: []string{}, Accounts: []string{}, Local: []string{}}, err
	}
	defer c.Close()
	var sources []string
	about := PhotoAbout{Accounts: []string{}, Local: []string{}}
	about.Albums, sources, err = c.About(k)
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
	p, err := c.Photo(k)
	about.Dated, about.Bytes, about.Path = p.Source, p.Bytes, p.Path
	return about, err
}
