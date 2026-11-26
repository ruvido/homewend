// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
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
// The catalog's path is where the file is. A file moves after the catalog said
// its photo is in the Trash, or restored, a few in one turn of the library: the
// moves are written in a journal, made, and recorded, the journal then gone. Who
// takes the turn and finds one finishes it (FinishMoves), so it is only between
// the two that a file is not where it is recorded, and there At says where.
const TrashDir = ".trash"

// What out is of a photo with trashed set, and the SQL of it.
const (
	erased  = 1 // deleted for good
	erasing = 2 // claimed by an Empty, its file still to go: not to be restored

	inTheTrash     = `(out = 0 AND trashed = 1)`
	deletedForGood = `(out > 0 AND trashed = 1)`
	beingErased    = `(out = 2 AND trashed = 1)`
	inItsFolder    = `(path >= '` + TrashDir + `/' AND path < '` + TrashDir + `0')`
	notInItsFolder = `(` + inTheTrash + ` AND NOT ` + inItsFolder + `)`
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

// fewAtATime is how many photos are moved in one turn, and fewErased erased: a
// phone's photo or a date given waits for one.
const (
	fewAtATime = 100
	fewErased  = 50
)

// transit is a photo's file on its way, in or out, as the journal has it.
type transit struct {
	Key
	From, To string
}

func journal(root string) string { return filepath.Join(root, WorkDir, "trash-moves.json") }

// movesOf are the journal's: none when there is none.
func movesOf(root string) (moves []transit, err error) {
	data, err := os.ReadFile(journal(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil && json.Unmarshal(data, &moves) != nil {
		return nil, errJournal
	}
	return moves, err
}

// errJournal is a journal that does not read.
var errJournal = errors.New("the Trash's journal does not read")

// keepMoves writes the journal, on the disk before anything moves.
func keepMoves(root string, moves []transit) error {
	file := journal(root)
	if len(moves) == 0 {
		return os.Remove(file)
	}
	return writeSynced(file, moves)
}

// At is where the file recorded at rel is: there, or, while it is being moved,
// or its profile renamed, where it has gone.
func At(root, rel string) string {
	if exists(under(root, rel)) {
		return rel
	}
	moves, _ := movesOf(root)
	for _, m := range moves {
		if m.From == rel && exists(under(root, m.To)) {
			return m.To
		}
	}
	// A profile being renamed: its folder under either name.
	if r, _ := renaming(root); r != nil {
		for _, way := range [][2]string{{r.From, r.To}, {r.To, r.From}} {
			if p, ok := renamedPath(rel, way[0], way[1]); ok && exists(under(root, p)) {
				return p
			}
		}
	}
	return rel
}

// FinishMoves finishes the moves of a journal, the ones just made or a killed
// run's: what moved is recorded, with what goes with it, and what did not is as
// it was. One found at neither place, its folder away, is kept for later.
// write makes the record, the catalog's lists the same before and after. In the
// library's turn.
func FinishMoves(root, catalog string, write Write) error {
	if !exists(journal(root)) {
		return nil
	}
	return with(catalog, func(c *Catalog) error {
		moves, err := movesOf(root)
		if errors.Is(err, errJournal) {
			// What it held is found by the look at the whole library (lost).
			log.Printf("%v: set aside", err)
			return os.Rename(journal(root), journal(root)+".unread")
		}
		if err != nil {
			return err
		}
		return finishMoves(root, c, moves, nil, write)
	})
}

// Write does a write to c that changes no list.
type Write func(c *Catalog, do func() error) error

// moved are those this run just moved, which never writes over a file: what is
// at their To is theirs. Others' bytes are read.
func finishMoves(root string, c *Catalog, moves []transit, moved map[Key]bool, write Write) error {
	var kept, made []transit
	renamed := false
	for _, m := range moves {
		at, in, err := c.PathOf(m.Key)
		if err != nil {
			return err
		}
		if !in || at != m.From && at != m.To {
			continue // forgotten, or filed elsewhere since
		}
		from, errFrom := regular(root, m.From)
		to, errTo := regular(root, m.To)
		switch {
		case errFrom != nil || errTo != nil:
			kept = append(kept, m)
		case to != nil && (from == nil || os.SameFile(from, to)):
			other, err := c.PathTaken(m.To, m.Key)
			if err != nil {
				return err
			}
			if other {
				continue
			}
			if !moved[m.Key] {
				if hash, err := hashOf(under(root, m.To)); err != nil || hash != m.Hash {
					if from == nil {
						kept = append(kept, m) // not known for the photo: for a person to see
					}
					continue
				}
			}
			moveBesides(root, m.From, m.To)
			if from != nil && os.Remove(under(root, m.From)) != nil {
				kept = append(kept, m)
				continue
			}
			made = append(made, m)
			renamed = renamed || !inTrash(m.To) && path.Base(m.To) != path.Base(m.From)
		case from != nil:
			if to == nil {
				moveBesides(root, m.To, m.From)
			}
		case !proved(root, m.From) || !proved(root, m.To):
			kept = append(kept, m)
		}
	}
	record := func() error {
		return c.inTx(func(tx *sql.Tx) error {
			// Out of the Trash it has the name it is found under.
			set, err := tx.Prepare(`UPDATE library_photos SET path = ?1, name = iif(?2, name, ?3) WHERE hash = ?4 AND profile = ?5 AND path = ?6`)
			if err != nil {
				return err
			}
			for _, m := range made {
				if _, err := set.Exec(m.To, inTrash(m.To), path.Base(m.To), m.Hash, m.Profile, m.From); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if len(made) > 0 {
		if renamed {
			write = func(_ *Catalog, do func() error) error { return do() }
		}
		if err := write(c, record); err != nil {
			return err
		}
	}
	if len(kept) == len(moves) && len(moves) > 0 || len(kept) == 0 && !exists(journal(root)) {
		return nil
	}
	return keepMoves(root, kept)
}

// TidyTrash moves the files of the photos in the Trash into its folder, and
// those of the photos restored back to their place, and finishes an Empty that
// was killed: of keys, or of all with none, which reads every row. One that
// cannot move stays where it is. It says the contents no library has any more.
func TidyTrash(root, catalog string, keys []Key, turn Turn, write Write) (gone []string, err error) {
	var todo []Key
	var claimed []Photo
	var failed []error
	// What moved is where it is recorded before any is picked.
	if exists(journal(root)) {
		unlock, err := turn()
		if err != nil {
			return nil, err
		}
		failed = append(failed, FinishMoves(root, catalog, write))
		unlock()
	}
	some := false
	if keys == nil {
		err = with(catalog, func(c *Catalog) error {
			moves, err := lost(root, c)
			some = len(moves) > 0
			return err
		})
		if err != nil {
			return nil, errors.Join(append(failed, err)...)
		}
	}
	if some {
		unlock, err := turn()
		if err != nil {
			return nil, err
		}
		failed = append(failed, with(catalog, func(c *Catalog) error {
			moves, err := lost(root, c) // again, in its turn
			if err != nil {
				return err
			}
			kept, err := movesOf(root)
			if err != nil {
				return err
			}
			return finishMoves(root, c, append(kept, moves...), nil, write)
		}))
		unlock()
	}
	err = with(catalog, func(c *Catalog) error {
		pick := func(condition string) ([]Photo, error) {
			if keys == nil {
				return c.photos(`WHERE ` + condition)
			}
			return c.photosOf(keys, condition)
		}
		for _, condition := range []string{notInItsFolder, restoredThere} {
			photos, err := pick(condition)
			if err != nil {
				return err
			}
			for _, p := range photos {
				todo = append(todo, p.Key())
			}
		}
		claimed, err = pick(beingErased)
		return err
	})
	if err != nil {
		return nil, err
	}
	for few := range slices.Chunk(todo, fewAtATime) {
		unlock, err := turn()
		if err != nil {
			return nil, errors.Join(append(failed, err)...)
		}
		failed = append(failed, with(catalog, func(c *Catalog) error { return settle(root, c, few, write) }))
		unlock()
	}
	_, gone, err = erase(root, catalog, claimed, erasing, turn, nil)
	return gone, errors.Join(append(failed, err)...)
}

// lost are the moves nothing tells of any more, a journal that did not read:
// of a photo whose file is not where it is recorded, and is, its content read,
// in the Trash's folder at no photo's path, or at its home.
func lost(root string, c *Catalog) (moves []transit, err error) {
	is := func(p Photo, rel string) bool {
		info, err := regular(root, rel)
		if err != nil || info == nil || info.Size() != p.Bytes {
			return false
		}
		hash, err := hashOf(under(root, rel))
		return err == nil && hash == p.Hash
	}
	files, err := inFolder(root)
	if err != nil {
		return nil, err
	}
	recorded, err := c.photos(`WHERE ` + inItsFolder)
	if err != nil {
		return nil, err
	}
	at := map[string]bool{}
	for _, p := range recorded {
		at[p.Path] = true
		for n := 1; n <= homeNames && !exists(under(root, p.Path)); n++ {
			home := homeOf(p.Path)
			if n > 1 {
				home = relativeTo(root, withSuffix(under(root, home), n))
			}
			if is(p, home) {
				moves = append(moves, transit{p.Key(), p.Path, home})
				break
			}
		}
	}
	for _, rel := range files {
		if at[rel] {
			continue
		}
		photos, err := c.photos(`WHERE path = ?`, homeOf(rel))
		if err != nil {
			return nil, err
		}
		for _, p := range photos {
			if !exists(under(root, p.Path)) && is(p, rel) {
				moves = append(moves, transit{p.Key(), p.Path, rel})
				break
			}
		}
	}
	return moves, nil
}

// homeNames is how many names a photo's home is looked for under.
const homeNames = 20

// inFolder are the files in the Trash's folder.
func inFolder(root string) (files []string, err error) {
	if direct(root, trashPath("x", 1)) != nil {
		return nil, nil
	}
	err = filepath.WalkDir(filepath.Join(root, TrashDir), func(file string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files = append(files, relativeTo(root, file))
		}
		return nil
	})
	return files, err
}

// settle moves the files of photos to where they belong, in the library's turn.
func settle(root string, c *Catalog, photos []Key, write Write) error {
	in, err := c.photosOf(photos, notInItsFolder)
	if err != nil {
		return err
	}
	out, err := c.photosOf(photos, restoredThere)
	if err != nil {
		return err
	}
	shared, err := c.sharedPaths(in)
	if err != nil {
		return err
	}
	moves, err := movesOf(root) // those kept, their folder away
	if err != nil {
		return err
	}
	going, taken, moved := map[Key]bool{}, map[string]bool{}, map[Key]bool{}
	for _, m := range moves {
		going[m.Key], taken[m.To] = true, true
	}
	kept := len(moves)
	for _, p := range slices.Concat(in, out) {
		// Another file in its place by its size, or another photo's too: it
		// is not this one's to move.
		info, err := regular(root, p.Path)
		if err != nil || info == nil || going[p.Key()] || shared[p.Path] || !inTrash(p.Path) && info.Size() != p.Bytes {
			continue
		}
		to, err := free(root, c, p, taken)
		if err != nil {
			return err
		}
		if to != "" {
			moves, taken[to] = append(moves, transit{p.Key(), p.Path, to}), true
		}
	}
	if len(moves) == kept {
		return nil
	}
	if err := keepMoves(root, moves); err != nil {
		return err
	}
	var failed []error
	for _, m := range moves[kept:] {
		// Not told by os.SameFile: on Windows a FileInfo finds its file by the
		// path it was read at, which a move takes away.
		if err := rename(under(root, m.From), under(root, m.To)); err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", m.From, err))
		} else {
			moved[m.Key] = true
		}
	}
	return errors.Join(append(failed, finishMoves(root, c, moves, moved, write))...)
}

// free is the first free name p's file can go to, "" when a link is on the way.
func free(root string, c *Catalog, p Photo, taken map[string]bool) (string, error) {
	for n := 1; ; n++ {
		to := trashPath(p.Path, n)
		if inTrash(p.Path) {
			if to = homeOf(p.Path); n > 1 {
				to = relativeTo(root, withSuffix(under(root, to), n))
			}
		}
		if direct(root, to) != nil {
			return "", nil
		}
		other, err := c.PathTaken(to, p.Key())
		if err != nil {
			return "", err
		}
		if !other && !taken[to] && !exists(under(root, to)) {
			return to, nil
		}
	}
}

// sharedPaths are those of photos another photo is recorded at, be it in other
// capitals: its folder's are read once, not every path there is.
func (c *Catalog) sharedPaths(photos []Photo) (map[string]bool, error) {
	byDir := map[string][]string{}
	for _, p := range photos {
		byDir[path.Dir(p.Path)] = append(byDir[path.Dir(p.Path)], p.Path)
	}
	shared := map[string]bool{}
	for dir, paths := range byDir {
		list, err := json.Marshal(paths)
		if err != nil {
			return nil, err
		}
		found, err := texts(c.db.Query(`SELECT path FROM library_photos WHERE path >= ? AND path < ? AND lower(path) IN (SELECT lower(value) FROM json_each(?))`, dir+"/", dir+"0", string(list)))
		if err != nil {
			return nil, err
		}
		n := map[string]int{}
		for _, p := range found {
			n[strings.ToLower(p)]++
		}
		for _, p := range paths {
			shared[p] = n[strings.ToLower(p)] > 1
		}
	}
	return shared, nil
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
func EmptyTrash(root, catalog string, photos []Key, turn Turn, write Write, told func(done int)) (left []Key, gone []string, err error) {
	// Files not where they belong first.
	if gone, err = TidyTrash(root, catalog, photos, turn, write); err != nil && !exists(catalog) {
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
	_, more, err := erase(root, catalog, claimed, erasing, turn, nil)
	gone = append(gone, more...)
	if err != nil {
		return photos, gone, err
	}
	done, more, err := erase(root, catalog, rows, 0, turn, told)
	gone = append(gone, more...)
	left = []Key{}
	for _, p := range rows {
		if !done[p.Key()] {
			left = append(left, p.Key())
		}
	}
	if err == nil {
		_, more, err = erase(root, catalog, stray, erased, turn, nil)
		gone = append(gone, more...)
	}
	return left, gone, err
}

// strays are the photos deleted for good that have a file in the Trash's
// folder again: an older program takes one that comes in for a move left halfway.
func strays(root string, c *Catalog) ([]Photo, error) {
	files, err := inFolder(root)
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
	// edited: its file is not its content any more, changed by another program.
	edited bool
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
func erase(root, catalog string, rows []Photo, was int, turn Turn, told func(done int)) (done map[Key]bool, gone []string, err error) {
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
	handled := 0
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
		if handled += n; told != nil {
			told(handled)
		}
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
	var shared map[string]bool
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
		if taken, err = c.countAt(paths); err != nil {
			return err
		}
		shared, err = c.sharedPaths(rows)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	own := map[Key]doomed{}
	for _, p := range rows {
		// An edit goes only where nothing but its content says whose it is:
		// not put back where one was deleted for good, nor at a path another
		// photo has in other capitals.
		if d, err := ownFiles(root, p, albums[p.Key()], in); err == nil && taken[p.Path] == 1 && !(d.edited && (was == erased || shared[p.Path])) {
			own[p.Key()] = d
		}
	}
	for _, p := range rows {
		d, ok := own[p.Key()]
		for _, o := range copies {
			if !ok || o.Key() == p.Key() || o.Hash != p.Hash || d.files[0].info == nil || d.edited {
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
	// One on its way, its folder away, is whole somewhere.
	moves, err := movesOf(root)
	if err != nil {
		return nil, nil, err
	}
	going := map[Key]bool{}
	for _, m := range moves {
		going[m.Key] = true
	}
	for _, d := range read {
		if !going[d.Key()] && !slices.ContainsFunc(d.files, func(f seen) bool { return !f.still(root) }) && (d.files[0].info != nil || proved(root, d.Path)) {
			sure = append(sure, d.Photo)
			files[d.Key()] = d
		}
	}
	claimed, err := set(was, erasing, sure)
	if err != nil {
		return nil, nil, err
	}
	clear(going)
	for k := range claimed {
		// One that was to go with it was restored, or changed, with no file.
		going[k] = !slices.ContainsFunc(files[k].with, func(o Key) bool {
			d, ok := files[o]
			return !claimed[o] && (!ok || d.files[0].info == nil || d.edited)
		})
	}
	// One that goes with an edited one, which holds not its content, goes after it.
	after := func(k Key) bool {
		return slices.ContainsFunc(files[k].with, func(o Key) bool { return files[o].edited })
	}
	removed := map[Key]bool{}
	var stay []Photo
	for _, late := range []bool{false, true} {
		for _, p := range rows {
			k := p.Key()
			if after(k) != late {
				continue
			}
			if going[k] && (!late || !slices.ContainsFunc(files[k].with, func(o Key) bool { return files[o].edited && !removed[o] })) && remove(root, c, files[k]) == nil {
				ended, removed[k] = append(ended, p), true
			} else if claimed[k] || was == erasing {
				stay = append(stay, p)
			}
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
	// Homewend put it there and the person deleted it: an edit of it goes too.
	if !inTrash(p.Path) {
		return d, errNotIts
	}
	hash, err := hashOf(under(root, p.Path))
	if err != nil {
		return d, err
	}
	d.edited = hash != p.Hash
	for _, album := range albums {
		dir := path.Join(p.Profile, "albums", album)
		if direct(root, dir+"/x") != nil {
			continue
		}
		for _, name := range in.ofPhoto(under(root, dir), under(root, p.Path), p) {
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
