// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// TrashDir is the folder of a library that holds the files of its Trash, each
// under the path it had; on a name taken, under a folder ".2" there, ".3"… So
// where a file came from is always read from its path (homeOf).
//
// A file on its way in is recorded there before it moves, one on its way out
// after: killed in between, a photo is recorded in the folder and its file is
// there or at its home, and is found by that alone (found).
const TrashDir = ".trash"

// What out is of a photo with trashed set, and the SQL of it.
const (
	erased  = 1 // deleted for good
	erasing = 2 // claimed by an Empty, its file still to go: not to be restored

	inTheTrash     = `(out = 0 AND trashed = 1)`
	deletedForGood = `(out > 0 AND trashed = 1)`
	beingErased    = `(out = 2 AND trashed = 1)`
	inItsFolder    = `(path >= '` + TrashDir + `/' AND path < '` + TrashDir + `0')`
	restoredThere  = `(out = 0 AND trashed = 0 AND ` + inItsFolder + `)`
)

func inTrash(rel string) bool { return strings.HasPrefix(rel, TrashDir+"/") }

// homeOf is the path a file in the Trash's folder came from.
func homeOf(rel string) string {
	rel = strings.TrimPrefix(rel, TrashDir+"/")
	if first, rest, ok := strings.Cut(rel, "/"); ok && strings.HasPrefix(first, ".") {
		return rest
	}
	return rel
}

// trashPath is where the file at home goes, n counting the tries.
func trashPath(home string, n int) string {
	if n == 1 {
		return TrashDir + "/" + home
	}
	return fmt.Sprintf("%s/.%d/%s", TrashDir, n, home)
}

func under(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

// errNotPlain is a path that leads out of the library, or through a link.
var errNotPlain = errors.New("not a plain path of the library")

// direct is whether rel is a path below root whose folders, those there are,
// are folders: through a link it could name a file that is also elsewhere.
func direct(root, rel string) error {
	if rel != path.Clean(rel) || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return errNotPlain
	}
	dir := root
	for _, part := range strings.Split(path.Dir(rel), "/") {
		dir = filepath.Join(dir, part)
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return errNotPlain
		}
	}
	return nil
}

// regular is the file at rel: nil when there is none, an error when that
// cannot be told or it is no plain file.
func regular(root, rel string) (fs.FileInfo, error) {
	if err := direct(root, rel); err != nil {
		return nil, err
	}
	info, err := os.Lstat(under(root, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil && !info.Mode().IsRegular() {
		err = errNotPlain
	}
	return info, err
}

// Turn takes the library's turn to move files, and gives it back.
type Turn func() (func(), error)

// fewAtATime is how many photos are moved in one turn, and fewer erased: a
// phone's photo or a date given waits for one.
const (
	fewAtATime = 200
	fewErased  = 50
)

// moving is a photo's file on its way into the Trash's folder, as it was seen.
type moving struct {
	Photo
	to   string
	file fs.FileInfo
}

// PutInTrash is Mark of Trashed, with the files' places in the same write:
// moving them is MoveIn's, beside. In the library's turn.
func PutInTrash(root string, c *Catalog, photos []Key) (done []Key, moves []moving, err error) {
	rows, err := c.photosOf(photos, `out = 0`)
	if err != nil {
		return []Key{}, nil, err
	}
	if moves, err = planIn(root, c, rows); err != nil {
		return []Key{}, nil, err
	}
	err = c.inTx(func(tx *sql.Tx) error {
		if done, err = setState(tx, Trashed, photos, true); err != nil {
			return err
		}
		return recordIn(tx, moves)
	})
	return done, moves, err
}

// planIn is where the files of rows, not in the Trash's folder, go in it. Left
// where it is: one gone, one that is not the photo's by its size, one another
// photo is recorded at, be it in other capitals, one reached through a link.
func planIn(root string, c *Catalog, rows []Photo) ([]moving, error) {
	var moves []moving
	dirs, seen := map[string]error{}, map[string]int{}
	var want []string
	all := make([]string, len(rows))
	for i, p := range rows {
		all[i] = p.Path
	}
	list, err := json.Marshal(all)
	if err != nil {
		return nil, err
	}
	for _, p := range rows {
		if inTrash(p.Path) {
			continue
		}
		dir := path.Dir(p.Path)
		err, known := dirs[dir]
		if !known {
			if err = direct(root, p.Path); err == nil {
				err = direct(root, trashPath(p.Path, 1))
			}
			dirs[dir] = err
			if err == nil {
				paths, err := texts(c.db.Query(`SELECT path FROM library_photos WHERE path >= ? AND path < ? AND lower(path) IN (SELECT lower(value) FROM json_each(?))`, dir+"/", dir+"0", string(list)))
				if err != nil {
					return nil, err
				}
				for _, other := range paths {
					seen[strings.ToLower(other)]++
				}
			}
		}
		if err != nil || p.Path != path.Clean(p.Path) || seen[strings.ToLower(p.Path)] > 1 {
			continue
		}
		info, err := os.Lstat(under(root, p.Path))
		if err != nil || !info.Mode().IsRegular() || info.Size() != p.Bytes {
			continue
		}
		moves = append(moves, moving{p, "", info})
		want = append(want, trashPath(p.Path, 1))
	}
	taken, err := c.countAt(want)
	if err != nil {
		return nil, err
	}
	for i := range moves {
		for n := 1; ; n++ {
			to := trashPath(moves[i].Path, n)
			if n > 1 {
				other, err := c.PathTaken(to, moves[i].Key())
				if err != nil {
					return nil, err
				}
				if other {
					continue
				}
			}
			if taken[to] == 0 && !exists(under(root, to)) {
				moves[i].to = to
				taken[to]++
				break
			}
		}
	}
	return moves, nil
}

func recordIn(tx *sql.Tx, moves []moving) error {
	set, err := tx.Prepare(`UPDATE library_photos SET path = ? WHERE hash = ? AND profile = ? AND path = ? AND ` + inTheTrash)
	if err != nil {
		return err
	}
	for _, m := range moves {
		if _, err := set.Exec(m.to, m.Hash, m.Profile, m.Path); err != nil {
			return err
		}
	}
	return nil
}

// MoveIn moves the files PutInTrash recorded. One that cannot move is recorded
// where it is again. Those not moved, for what follows to find, are said.
func MoveIn(root string, c *Catalog, moves []moving, turn Turn) (left []Key, err error) {
	for few := range slices.Chunk(moves, fewAtATime) {
		unlock, busy := turn()
		if busy != nil {
			err = errors.Join(err, busy)
			for _, m := range few {
				left = append(left, m.Key())
			}
			continue
		}
		err = errors.Join(err, moveIn(root, c, few))
		unlock()
	}
	return left, err
}

func moveIn(root string, c *Catalog, moves []moving) error {
	keys := make([]Key, len(moves))
	for i, m := range moves {
		keys[i] = m.Key()
	}
	rows, err := c.photosOf(keys, inTheTrash)
	if err != nil {
		return err
	}
	at := map[Key]string{}
	for _, p := range rows {
		at[p.Key()] = p.Path
	}
	var failed []error
	var stay []moving
	for _, m := range moves {
		if at[m.Key()] != m.to {
			continue // restored, or not recorded
		}
		info, err := regular(root, m.Path)
		if err == nil && info == nil {
			continue // moved by another run
		}
		if err == nil {
			if !os.SameFile(info, m.file) {
				err = errNotIts
			} else {
				err = moveName(root, m.Path, m.to)
			}
		}
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", m.Path, err))
			stay = append(stay, m)
		}
	}
	if len(stay) > 0 {
		failed = append(failed, c.inTx(func(tx *sql.Tx) error {
			for _, m := range stay {
				if _, err := tx.Exec(`UPDATE library_photos SET path = ? WHERE hash = ? AND profile = ? AND path = ?`, m.Path, m.Hash, m.Profile, m.to); err != nil {
					return err
				}
			}
			return nil
		}))
	}
	return errors.Join(failed...)
}

// TidyTrash moves the files of the photos in the Trash into its folder, and
// those of the photos restored back to their place, finds those of a run that
// was killed, and finishes its Empty: of keys, or of all with none, which
// reads every row. One that cannot move stays, recorded where it is. It says
// the contents no library has any more.
func TidyTrash(root, catalog string, keys []Key, turn Turn) (gone []string, err error) {
	var todo []Key
	var claimed []Photo
	err = with(catalog, func(c *Catalog) error {
		pick := func(condition string) ([]Photo, error) {
			if keys == nil {
				return c.photos(`WHERE ` + condition)
			}
			return c.photosOf(keys, condition)
		}
		in, err := pick(inTheTrash)
		if err != nil {
			return err
		}
		for _, p := range in {
			if !inTrash(p.Path) || !exists(under(root, p.Path)) || leftAt(root, homeOf(p.Path)) {
				todo = append(todo, p.Key())
			}
		}
		back, err := pick(restoredThere)
		if err != nil {
			return err
		}
		for _, p := range back {
			todo = append(todo, p.Key())
		}
		claimed, err = pick(beingErased)
		return err
	})
	if err != nil {
		return nil, err
	}
	var failed []error
	for few := range slices.Chunk(todo, fewAtATime) {
		unlock, err := turn()
		if err != nil {
			return nil, errors.Join(append(failed, err)...)
		}
		failed = append(failed, with(catalog, func(c *Catalog) error { return settle(root, c, few) }))
		unlock()
	}
	_, gone, err = erase(root, catalog, claimed, erasing, turn)
	return gone, errors.Join(append(failed, err)...)
}

// settle brings the files of photos to where they belong, whatever a run
// killed left, in the library's turn.
func settle(root string, c *Catalog, photos []Key) error {
	in, err := c.photosOf(photos, inTheTrash)
	if err != nil {
		return err
	}
	back, err := c.photosOf(photos, restoredThere)
	if err != nil {
		return err
	}
	moves, err := planIn(root, c, in)
	if err != nil {
		return err
	}
	var failed []error
	if len(moves) > 0 {
		if err := c.inTx(func(tx *sql.Tx) error { return recordIn(tx, moves) }); err != nil {
			return err
		}
		failed = append(failed, moveIn(root, c, moves))
	}
	for _, p := range in {
		if !inTrash(p.Path) {
			continue
		}
		at, err := found(root, c, p)
		if err == nil && at != "" && at != p.Path {
			err = moveName(root, at, p.Path)
		}
		if err == nil && at != "" {
			err = gather(root, c, p, p.Path)
		}
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", p.Path, err))
		}
	}
	for i, p := range back {
		at, err := found(root, c, p)
		if err == nil && at == p.Path {
			at, err = bringBack(root, c, p)
		}
		if err == nil && at != "" {
			err = gather(root, c, p, at)
		}
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", p.Path, err))
		}
		back[i].Path, back[i].Name = at, p.Path // "" when it was not brought back
	}
	if len(back) == 0 {
		return errors.Join(failed...)
	}
	err = c.inTx(func(tx *sql.Tx) error {
		set, err := tx.Prepare(`UPDATE library_photos SET path = ?, name = ? WHERE hash = ? AND profile = ? AND path = ?`)
		if err != nil {
			return err
		}
		for _, p := range back {
			if p.Path == "" {
				continue
			}
			if _, err := set.Exec(p.Path, path.Base(p.Path), p.Hash, p.Profile, p.Name); err != nil {
				return err
			}
		}
		return nil
	})
	for _, p := range back {
		if err != nil && p.Path != "" {
			failed = append(failed, moveName(root, p.Path, p.Name))
		}
	}
	return errors.Join(append(failed, err)...)
}

// leftAt is whether a run killed may have left something at home.
func leftAt(root, home string) bool {
	return exists(under(root, home)) || slices.ContainsFunc(besides(home), func(b string) bool { return exists(under(root, b)) })
}

// gather takes to at, where p's file is, what a run killed left of it at its
// other names: a second name of the file, and what goes with it where no
// other file is.
func gather(root string, c *Catalog, p Photo, at string) error {
	file, err := regular(root, at)
	if err != nil || file == nil {
		return err
	}
	names := []string{p.Path}
	for n := 1; n <= homeNames; n++ {
		name := homeName(root, homeOf(p.Path), n)
		if !leftAt(root, name) {
			break // the first free name was taken, so none is beyond it
		}
		names = append(names, name)
	}
	for _, name := range names {
		info, err := regular(root, name)
		if name == at || err != nil || info != nil && !os.SameFile(info, file) || info == nil && !leftAt(root, name) {
			continue
		}
		if taken, err := c.PathTaken(name, p.Key()); err != nil || taken {
			continue
		}
		moveBesides(root, name, at)
		if info != nil {
			if err := os.Remove(under(root, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// homeNames is how many names a photo's home is looked for under.
const homeNames = 20

// homeName is the nth name of a photo that comes back to home.
func homeName(root, home string, n int) string {
	if n == 1 {
		return home
	}
	return relativeTo(root, withSuffix(under(root, home), n))
}

// found is where the file of p, recorded in the Trash's folder, is: there, or
// at its home, its content read, when a run was killed. "" when nowhere.
func found(root string, c *Catalog, p Photo) (string, error) {
	info, err := regular(root, p.Path)
	if err != nil || info != nil {
		return p.Path, err
	}
	home := p
	for n := 1; n <= homeNames; n++ {
		home.Path = homeName(root, homeOf(p.Path), n)
		info, err := regular(root, home.Path)
		if err != nil && !errors.Is(err, errNotPlain) {
			return "", err
		}
		if err != nil || info == nil || info.Size() != p.Bytes {
			continue
		}
		taken, err := c.PathTaken(home.Path, p.Key())
		if err != nil {
			return "", err
		}
		if taken {
			continue
		}
		if hash, err := hashOf(under(root, home.Path)); err != nil {
			return "", err
		} else if hash == p.Hash {
			return home.Path, nil
		}
	}
	return "", nil
}

// bringBack moves the file of p home, or beside it when its name is taken. The
// second name of a run killed is its own place.
func bringBack(root string, c *Catalog, p Photo) (string, error) {
	home := homeOf(p.Path)
	if err := direct(root, home); err != nil {
		return "", err
	}
	info, err := regular(root, p.Path)
	if err != nil {
		return "", err
	}
	for n := 1; n <= homeNames; n++ {
		to := homeName(root, home, n)
		taken, err := c.PathTaken(to, p.Key())
		if err != nil {
			return "", err
		}
		there, err := os.Lstat(under(root, to))
		switch {
		case taken:
		case errors.Is(err, fs.ErrNotExist):
			return to, moveName(root, p.Path, to)
		case err != nil:
			return "", err
		case os.SameFile(info, there):
			return to, os.Remove(under(root, p.Path))
		}
	}
	return "", errNotIts
}

// photosOf are those of keys that a condition keeps.
func (c *Catalog) photosOf(keys []Key, condition string) ([]Photo, error) {
	list, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	return c.photos(`WHERE (hash, profile) IN (SELECT value ->> 'hash', value ->> 'profile' FROM json_each(?)) AND `+condition, string(list))
}

// besides are the files that go with the photo at rel: Google's sidecar, kept
// by the photo's path, and the .xmp of a date given.
func besides(rel string) []string {
	return []string{path.Join(WorkDir, "metadata", rel+".json"), rel + ".xmp"}
}

// moveName moves a photo's file, and first what goes with it, so that a run
// killed leaves nothing behind.
func moveName(root, from, to string) error {
	moveBesides(root, from, to)
	err := rename(under(root, from), under(root, to))
	if err != nil {
		moveBesides(root, to, from)
	}
	return err
}

// moveBesides moves what goes with the photo at from; what cannot, stays.
func moveBesides(root, from, to string) {
	into := besides(to)
	for i, b := range besides(from) {
		if info, err := regular(root, b); err == nil && info != nil && direct(root, into[i]) == nil {
			_ = rename(under(root, b), under(root, into[i]))
		}
	}
}

// hardLink is os.Link; in tests, of a disk that has none.
var hardLink = os.Link

// rename is os.Rename that never writes over a file: by a second name, or on
// a disk that has none by looking first.
func rename(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	err := hardLink(from, to)
	if err == nil {
		if err = os.Remove(from); err != nil {
			os.Remove(to)
		}
		return err
	}
	if exists(to) {
		return err
	}
	return os.Rename(from, to)
}

// EmptyTrash deletes for good the files of photos, those of them in the Trash
// now, and keeps them as deleted: in no list, skipped by every import. One
// whose file is not surely its own, or is the last of a content another
// library has, stays in the Trash with it, and so does one whose file is not
// there with nothing to say it is not only away. It says those left there, and
// the contents no library has any more.
func EmptyTrash(root, catalog string, photos []Key, turn Turn) (left []Key, gone []string, err error) {
	// Files not where they belong first.
	if gone, err = TidyTrash(root, catalog, photos, turn); err != nil && !exists(catalog) {
		return photos, gone, err
	}
	var rows, stray, claimed []Photo
	err = with(catalog, func(c *Catalog) error {
		if rows, err = c.photosOf(photos, inTheTrash); err != nil {
			return err
		}
		if claimed, err = c.photos(`WHERE ` + beingErased); err != nil {
			return err
		}
		stray, err = strays(root, c)
		return err
	})
	if err != nil {
		return photos, gone, err
	}
	// Before any folder that proves them gone goes.
	_, more, err := erase(root, catalog, claimed, erasing, turn)
	gone = append(gone, more...)
	if err != nil {
		return photos, gone, err
	}
	done, more, err := erase(root, catalog, rows, 0, turn)
	gone = append(gone, more...)
	left = []Key{}
	for _, p := range rows {
		if !done[p.Key()] {
			left = append(left, p.Key())
		}
	}
	if err == nil {
		_, more, err = erase(root, catalog, stray, erased, turn)
		gone = append(gone, more...)
	}
	return left, gone, err
}

// strays are the photos deleted for good that have a file in the Trash's
// folder again: an older program takes one that comes in for a move left halfway.
func strays(root string, c *Catalog) ([]Photo, error) {
	if direct(root, trashPath("x", 1)) != nil {
		return nil, nil
	}
	files := []string{}
	err := filepath.WalkDir(filepath.Join(root, TrashDir), func(file string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, relativeTo(root, file))
		}
		return nil
	})
	if err != nil || len(files) == 0 {
		return nil, err
	}
	list, err := json.Marshal(files)
	if err != nil {
		return nil, err
	}
	return c.photos(`WHERE path IN (SELECT value FROM json_each(?)) AND out = ? AND trashed = 1`, string(list), erased)
}

// doomed is a photo read and found to be deletable: its file (nil: gone, and
// proved so), its names in its albums, and the files that hold its content for
// the other libraries, all to be as they were when it is deleted.
type doomed struct {
	Photo
	files []seen
	// with are the photos that are no holder only as they go with it.
	with []Key
}

// seen is a file as it was read. The photo's own is the first, then its names.
type seen struct {
	rel  string
	info fs.FileInfo
	keep bool // another library's: not deleted
}

// still is whether the file is the one that was seen.
func (f seen) still(root string) bool {
	info, err := os.Stat(under(root, f.rel))
	if f.info == nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	if err == nil && !f.keep {
		info, err = regular(root, f.rel)
	}
	return err == nil && info != nil && os.SameFile(info, f.info) && info.Size() == f.info.Size() && info.ModTime().Equal(f.info.ModTime())
}

// erase deletes the files of rows, whose out is was, and says whose it did. A
// few at a time: read with nothing held, then, in the library's turn, seen to
// be as they were, claimed in one write, deleted, and recorded. One restored
// before the claim is not deleted; after it, it cannot be restored. A run
// killed leaves them claimed, and TidyTrash comes back to them.
func erase(root, catalog string, rows []Photo, was int, turn Turn) (done map[Key]bool, gone []string, err error) {
	done = map[Key]bool{}
	if len(rows) == 0 {
		return done, nil, nil
	}
	// One content is never in two groups: each photo's verdict is the others'.
	slices.SortFunc(rows, func(a, b Photo) int { return strings.Compare(a.Hash, b.Hash) })
	var albums map[Key][]string
	if err := with(catalog, func(c *Catalog) (err error) { albums, err = c.albumNames(rows); return err }); err != nil {
		return done, nil, err
	}
	in := entries{}
	folders := map[string]bool{}
	for len(rows) > 0 {
		n := min(fewErased, len(rows))
		for n < len(rows) && rows[n].Hash == rows[n-1].Hash {
			n++
		}
		few, forgotten, err := eraseFew(root, catalog, rows[:n], was, albums, in, turn)
		if err != nil {
			return done, gone, err
		}
		gone = append(gone, forgotten...)
		for _, p := range few {
			done[p.Key()] = true
			folders[path.Dir(p.Path)] = true
		}
		rows = rows[n:]
	}
	// The folders that held them, as far as they are empty.
	for dir := range folders {
		for ; inTrash(dir+"/") && direct(root, dir+"/x") == nil && os.Remove(under(root, dir)) == nil; dir = path.Dir(dir) {
		}
	}
	return done, gone, nil
}

func eraseFew(root, catalog string, rows []Photo, was int, albums map[Key][]string, in entries, turn Turn) (ended []Photo, gone []string, err error) {
	var read []doomed
	var copies []Photo
	var taken map[string]int
	err = with(catalog, func(c *Catalog) error {
		var hashes, paths []string
		for _, p := range rows {
			hashes, paths = append(hashes, p.Hash), append(paths, p.Path)
		}
		list, err := json.Marshal(hashes)
		if err != nil {
			return err
		}
		if copies, err = c.photos(`WHERE out = 0 AND hash IN (SELECT value FROM json_each(?))`, string(list)); err != nil {
			return err
		}
		taken, err = c.countAt(paths)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	own := map[Key]doomed{}
	for _, p := range rows {
		if d, err := ownFiles(root, p, albums[p.Key()], in); err == nil && taken[p.Path] == 1 {
			own[p.Key()] = d
		}
	}
	for _, p := range rows {
		d, ok := own[p.Key()]
		for _, o := range copies {
			if !ok || o.Key() == p.Key() || o.Hash != p.Hash || d.files[0].info == nil {
				continue
			}
			// Going too, its file its own: nobody is left without.
			if _, too := own[o.Key()]; too {
				d.with = append(d.with, o.Key())
				continue
			}
			var h seen
			if h, ok = holds(root, o, p, d.files[0].info); ok {
				d.files = append(d.files, h)
			}
		}
		if ok {
			read = append(read, d)
		}
	}
	if len(read) == 0 && was != erasing {
		return nil, nil, nil
	}
	unlock, err := turn()
	if err != nil {
		return nil, nil, err
	}
	defer unlock()
	err = with(catalog, func(c *Catalog) error {
		ended, gone, err = eraseRead(root, c, rows, read, was)
		return err
	})
	return ended, gone, err
}

// eraseRead is the part of eraseFew in the library's turn.
func eraseRead(root string, c *Catalog, rows []Photo, read []doomed, was int) (ended []Photo, gone []string, err error) {
	set := func(from, to int, photos []Photo) (changed map[Key]bool, err error) {
		changed = map[Key]bool{}
		if len(photos) == 0 {
			return changed, nil
		}
		err = c.inTx(func(tx *sql.Tx) error {
			stmt, err := tx.Prepare(`UPDATE library_photos SET out = ? WHERE hash = ? AND profile = ? AND path = ? AND trashed = 1 AND out = ?`)
			if err != nil {
				return err
			}
			for _, p := range photos {
				res, err := stmt.Exec(to, p.Hash, p.Profile, p.Path, from)
				if err != nil {
					return err
				}
				if n, err := res.RowsAffected(); err != nil {
					return err
				} else if n > 0 {
					changed[p.Key()] = true
				}
			}
			return nil
		})
		return changed, err
	}
	var sure []Photo
	files := map[Key]doomed{}
	for _, d := range read {
		if !slices.ContainsFunc(d.files, func(f seen) bool { return !f.still(root) }) && (d.files[0].info != nil || proved(root, d.Path)) {
			sure = append(sure, d.Photo)
			files[d.Key()] = d
		}
	}
	claimed, err := set(was, erasing, sure)
	if err != nil {
		return nil, nil, err
	}
	going := map[Key]bool{}
	for k := range claimed {
		// One that was to go with it was restored, or changed, with no file.
		going[k] = !slices.ContainsFunc(files[k].with, func(o Key) bool {
			d, ok := files[o]
			return !claimed[o] && (!ok || d.files[0].info == nil)
		})
	}
	var stay []Photo
	for _, p := range rows {
		k := p.Key()
		if going[k] && remove(root, c, files[k]) == nil {
			ended = append(ended, p)
		} else if claimed[k] || was == erasing {
			stay = append(stay, p)
		}
	}
	back := was
	if was == erasing {
		back = 0 // its Empty is over: it is in the Trash
	}
	if _, err := set(erasing, back, stay); err != nil {
		return nil, nil, err
	}
	if _, err := set(erasing, erased, ended); err != nil {
		return nil, nil, err
	}
	for _, p := range ended {
		var held bool
		if err := c.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM library_photos WHERE hash = ? AND out = 0)`, p.Hash).Scan(&held); err != nil {
			return ended, gone, err
		}
		if !held {
			gone = append(gone, p.Hash)
		}
	}
	return ended, gone, nil
}

var errNotIts = errors.New("the file is not the photo's, or is another's too")

// proved is whether the photo at rel, not there, is gone and not only away: the
// folder that should hold it is there, and the library's own.
func proved(root, rel string) bool {
	if direct(root, rel) != nil {
		return false
	}
	for _, dir := range []string{path.Dir(rel), WorkDir} {
		if info, err := os.Stat(under(root, dir)); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// ownFiles are the files to delete of p: its own, in the Trash's folder, read
// whole as nothing else says it is p's, and its names in its albums. Gone, it
// is one file of no info.
func ownFiles(root string, p Photo, albums []string, in entries) (doomed, error) {
	info, err := regular(root, p.Path)
	if err != nil {
		return doomed{}, err
	}
	d := doomed{Photo: p, files: []seen{{rel: p.Path, info: info}}}
	if info == nil {
		if !proved(root, p.Path) {
			return d, errNotIts
		}
		return d, nil
	}
	if !inTrash(p.Path) || info.Size() != p.Bytes {
		return d, errNotIts
	}
	if hash, err := hashOf(under(root, p.Path)); err != nil || hash != p.Hash {
		return d, errors.Join(err, errNotIts)
	}
	for _, album := range albums {
		dir := path.Join(p.Profile, "albums", album)
		if direct(root, dir+"/x") != nil {
			continue
		}
		for _, name := range in.of(under(root, dir), under(root, p.Path)) {
			rel := relativeTo(root, name)
			if info, err := regular(root, rel); err != nil {
				return d, err
			} else if info != nil {
				d.files = append(d.files, seen{rel: rel, info: info})
			}
		}
	}
	return d, nil
}

// holds is the file of o, which has the content it shares with p whatever
// becomes of p's: another name of the same file, or a copy, read.
func holds(root string, o, p Photo, file fs.FileInfo) (seen, bool) {
	if strings.EqualFold(o.Path, p.Path) {
		return seen{}, false
	}
	info, err := os.Stat(under(root, o.Path))
	if err != nil {
		return seen{}, false
	}
	if os.SameFile(info, file) {
		// Through a link it could be p's own name.
		_, err = regular(root, o.Path)
		return seen{o.Path, info, true}, err == nil
	}
	hash, err := hashOf(under(root, o.Path))
	return seen{o.Path, info, true}, err == nil && hash == o.Hash
}

// remove deletes the files of d, its own the last, with what goes with it. It
// stops before its own if a name of it cannot go.
func remove(root string, c *Catalog, d doomed) error {
	if d.files[0].info != nil {
		var names []string
		for _, f := range d.files[1:] {
			if f.keep {
				continue
			}
			taken, err := c.PathTaken(f.rel, Key{})
			if err != nil {
				return err
			}
			if !taken {
				names = append(names, under(root, f.rel))
			}
		}
		var err error
		for i, name := range names {
			if err = os.Remove(name); err != nil {
				names = names[:i]
				break
			}
		}
		file := under(root, d.Path)
		if err == nil {
			err = os.Remove(file)
		}
		if err != nil {
			for _, name := range names {
				linkInto(file, name)
			}
			return err
		}
	}
	for _, b := range besides(d.Path) {
		if info, err := regular(root, b); inTrash(d.Path) && err == nil && info != nil {
			os.Remove(under(root, b))
		}
	}
	return nil
}

// countAt is how many photos are recorded at each of paths.
func (c *Catalog) countAt(paths []string) (map[string]int, error) {
	list, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	found, err := texts(c.db.Query(`SELECT path FROM library_photos WHERE path IN (SELECT value FROM json_each(?))`, string(list)))
	at := map[string]int{}
	for _, p := range found {
		at[p]++
	}
	return at, err
}

// albumNames are the albums of each of photos, read once for them all.
func (c *Catalog) albumNames(photos []Photo) (map[Key][]string, error) {
	keys := make([]Key, len(photos))
	for i, p := range photos {
		keys[i] = p.Key()
	}
	list, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := c.db.Query(`SELECT p.hash, p.profile, m.album FROM library_photos p JOIN album_members m ON m.photo = p.id
		WHERE m.photo IN (SELECT id FROM library_photos WHERE (hash, profile) IN (SELECT value ->> 'hash', value ->> 'profile' FROM json_each(?)))`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	albums := map[Key][]string{}
	for rows.Next() {
		var k Key
		var album string
		if err := rows.Scan(&k.Hash, &k.Profile, &album); err != nil {
			return nil, err
		}
		albums[k] = append(albums[k], album)
	}
	return albums, rows.Err()
}
