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
	"strings"
)

// TrashDir is the folder of a library that holds the files of its Trash, each
// under the path it had. The catalog's path is always where the file is, so a
// program that knows nothing of it still finds it.
const TrashDir = ".trash"

// What out says of a photo in the Trash: none of the two is in any list, and
// none can be restored. out = 1 with trashed = 1 is deleted for good.
const (
	// erasing: claimed by an Empty, its file still to go. A run killed there
	// is finished by the next, or undone.
	erasing = 2
	erased  = 1
)

func inTrash(rel string) bool { return strings.HasPrefix(rel, TrashDir+"/") }

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

// TidyTrash moves the files of the photos in the Trash into its folder, and
// those of the photos restored back to their place, and finishes an Empty that
// was killed. One that cannot be moved stays where it is, and the catalog says
// so. turn takes the library's turn for a few at a time. It says the contents
// no library has any more.
func TidyTrash(root, catalog string, turn func() (func(), error)) (gone []string, err error) {
	var keys []Key
	var shared map[string]bool
	err = with(catalog, func(c *Catalog) error {
		photos, err := c.photos(`WHERE ` + misplaced)
		if err != nil {
			return err
		}
		for _, p := range photos {
			keys = append(keys, p.Key())
		}
		// Once for them all: it reads every path there is.
		shared, err = c.sharedPaths(photos)
		return err
	})
	var failed []error
	const few = 200
	for len(keys) > 0 && err == nil {
		n := min(few, len(keys))
		var unlock func()
		if unlock, err = turn(); err != nil {
			return gone, err
		}
		err = with(catalog, func(c *Catalog) error {
			photos, err := c.photosOf(keys[:n], `out = 0 AND trashed <> (path GLOB '`+TrashDir+`/*')`)
			if err != nil {
				return err
			}
			failed = append(failed, tidy(root, c, photos, shared))
			// Claimed, and killed before the end.
			photos, err = c.photosOf(keys[:n], fmt.Sprint(`out = `, erasing))
			if err != nil || len(photos) == 0 {
				return err
			}
			_, more, err := erase(root, c, photos, map[Key]bool{})
			gone = append(gone, more...)
			return err
		})
		unlock()
		keys = keys[n:]
	}
	return gone, errors.Join(append(failed, err)...)
}

// misplaced is the SQL of the photos whose file is not where the Trash says,
// or is being erased: the few in the Trash, then the few by their path.
const misplaced = `id IN (SELECT id FROM library_photos WHERE trashed = 1 AND (out = 2 OR out = 0 AND path NOT GLOB '` + TrashDir + `/*')
	UNION ALL SELECT id FROM library_photos WHERE path >= '` + TrashDir + `/' AND path < '` + TrashDir + `0' AND out = 0 AND trashed = 0)`

// photosOf are those of keys that a condition keeps.
func (c *Catalog) photosOf(keys []Key, condition string) ([]Photo, error) {
	list, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	return c.photos(`WHERE (hash, profile) IN (SELECT value ->> 'hash', value ->> 'profile' FROM json_each(?)) AND `+condition, string(list))
}

// tidy moves the files of photos to where they belong, then records all of
// them at once: a run killed in between finds each again by its content
// (bringTo). Not recorded, they are moved back. One whose path is shared
// (sharedPaths) is another's too, and not this one's to move.
func tidy(root string, c *Catalog, photos []Photo, shared map[string]bool) error {
	type change struct {
		Photo
		to string
	}
	var done []change
	var failed []error
	for _, p := range photos {
		want, in := strings.CutPrefix(p.Path, TrashDir+"/")
		if !in {
			want = TrashDir + "/" + p.Path
		}
		if shared[strings.ToLower(p.Path)] {
			continue
		}
		to, err := bringTo(root, c, p, want)
		if err != nil {
			failed = append(failed, fmt.Errorf("%s: %w", p.Path, err))
		} else if to != "" {
			done = append(done, change{p, to})
		}
	}
	if len(done) == 0 {
		return errors.Join(failed...)
	}
	var lost []change
	err := c.inTx(func(tx *sql.Tx) error {
		// Restored, it has the name it is found under.
		set, err := tx.Prepare(`UPDATE library_photos SET path = ?1, name = iif(?2, name, ?3) WHERE hash = ?4 AND profile = ?5 AND path = ?6`)
		if err != nil {
			return err
		}
		for _, d := range done {
			res, err := set.Exec(d.to, inTrash(d.to), path.Base(d.to), d.Hash, d.Profile, d.Path)
			if err != nil {
				return err
			}
			if n, err := res.RowsAffected(); err != nil {
				return err
			} else if n == 0 {
				lost = append(lost, d)
			}
		}
		return nil
	})
	if err != nil {
		lost = done
	}
	for _, d := range lost {
		failed = append(failed, moveName(root, d.to, d.Path))
	}
	return errors.Join(append(failed, err)...)
}

// sharedPaths are the paths of photos, in lower case, that another photo is
// recorded at, on a disk that ignores capitals too.
func (c *Catalog) sharedPaths(photos []Photo) (map[string]bool, error) {
	if len(photos) == 0 {
		return nil, nil
	}
	list := make([]string, len(photos))
	for i, p := range photos {
		list[i] = p.Path
	}
	text, err := json.Marshal(list)
	if err != nil {
		return nil, err
	}
	paths, err := texts(c.db.Query(`SELECT path FROM library_photos WHERE NOT (out > 0 AND trashed = 1)
		AND lower(path) IN (SELECT lower(value) FROM json_each(?))`, string(text)))
	shared, seen := map[string]bool{}, map[string]bool{}
	for _, p := range paths {
		low := strings.ToLower(p)
		shared[low] = shared[low] || seen[low]
		seen[low] = true
	}
	return shared, err
}

// bringTo moves p's file to want, or beside it under another name when want is
// taken, and says where it is now: "" when it was left as it was. A run killed
// on the way left it there already, under one name or two.
func bringTo(root string, c *Catalog, p Photo, want string) (string, error) {
	if err := direct(root, want); err != nil {
		return "", err
	}
	info, err := regular(root, p.Path)
	if err != nil {
		return "", err
	}
	// Another file has taken its place, or an edit of it: it is not touched.
	if info != nil && inTrash(want) && info.Size() != p.Bytes {
		return "", nil
	}
	for n := 1; ; n++ {
		to := want
		if n > 1 {
			to = relativeTo(root, withSuffix(under(root, want), n))
		}
		taken, err := c.PathTaken(to, p.Key())
		if err != nil {
			return "", err
		}
		if taken {
			continue
		}
		there, err := os.Lstat(under(root, to))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if info == nil {
				return "", nil // gone
			}
			return to, moveName(root, p.Path, to)
		case err != nil:
			return "", err
		case info != nil && os.SameFile(info, there):
			return to, os.Remove(under(root, p.Path))
		case info == nil && there.Mode().IsRegular():
			if hash, err := hashOf(under(root, to)); err == nil && hash == p.Hash {
				return to, nil
			}
		}
	}
}

// besides are the files that go with the photo at rel: Google's sidecar, kept
// by the photo's path, and the .xmp of a date given.
func besides(rel string) []string {
	return []string{path.Join(WorkDir, "metadata", rel+".json"), rel + ".xmp"}
}

// moveName moves a photo's file, and first what goes with it, so that a run
// killed leaves nothing behind.
func moveName(root, from, to string) error {
	beside := func(from, to []string) {
		for i, b := range from {
			if info, err := regular(root, b); err == nil && info != nil && direct(root, to[i]) == nil {
				_ = rename(under(root, b), under(root, to[i])) // it stays with the old name
			}
		}
	}
	beside(besides(from), besides(to))
	err := rename(under(root, from), under(root, to))
	if err != nil {
		beside(besides(to), besides(from))
	}
	return err
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
// library has, stays in the Trash with it. It says those left there, and the
// contents no library has any more.
func EmptyTrash(root string, c *Catalog, photos []Key) (left []Key, gone []string, err error) {
	const here = `out = 0 AND trashed = 1`
	rows, err := c.photosOf(photos, here)
	if err != nil {
		return nil, nil, err
	}
	var out []Photo
	for _, p := range rows {
		if !inTrash(p.Path) {
			out = append(out, p)
		}
	}
	shared, err := c.sharedPaths(out)
	if err != nil {
		return nil, nil, err
	}
	_ = tidy(root, c, out, shared) // what could not be moved is looked at below
	if rows, err = c.photosOf(photos, here); err != nil {
		return nil, nil, err
	}
	asked := map[Key]bool{}
	for _, p := range rows {
		asked[p.Key()] = true
	}
	done, gone, err := erase(root, c, rows, asked)
	left = []Key{}
	for _, p := range rows {
		if !done[p.Key()] {
			left = append(left, p.Key())
		}
	}
	return left, gone, err
}

// erase deletes the files of rows, in the Trash or claimed before, and says
// whose it did. Looked at first, then claimed in one write, so that none can
// be restored any more, then deleted, then recorded: a run killed leaves them
// claimed, and TidyTrash comes back to them. asked are those deleted together.
func erase(root string, c *Catalog, rows []Photo, asked map[Key]bool) (done map[Key]bool, gone []string, err error) {
	done = map[Key]bool{}
	files := map[Key]fs.FileInfo{}
	for _, p := range rows {
		if info, err := deletable(root, c, p, asked); err == nil {
			files[p.Key()] = info
		}
	}
	mark := func(out int, photos []Photo, also string) (changed []Photo, err error) {
		err = c.inTx(func(tx *sql.Tx) error {
			set, err := tx.Prepare(`UPDATE library_photos SET out = ? WHERE hash = ? AND profile = ? AND path = ? AND trashed = 1 AND ` + also)
			if err != nil {
				return err
			}
			for _, p := range photos {
				res, err := set.Exec(out, p.Hash, p.Profile, p.Path)
				if err != nil {
					return err
				}
				if n, err := res.RowsAffected(); err != nil {
					return err
				} else if n > 0 {
					changed = append(changed, p)
				}
			}
			return nil
		})
		return changed, err
	}
	var sure, back []Photo
	for _, p := range rows {
		if _, ok := files[p.Key()]; ok {
			sure = append(sure, p)
		} else {
			back = append(back, p)
		}
	}
	if sure, err = mark(erasing, sure, `out IN (0, 2)`); err != nil {
		return done, nil, err
	}
	var ended []Photo
	in := entries{}
	for _, p := range sure {
		if err := remove(root, c, p, files[p.Key()], in); err != nil {
			back = append(back, p)
		} else {
			ended = append(ended, p)
		}
	}
	if _, err = mark(0, back, `out = 2`); err != nil {
		return done, nil, err
	}
	if _, err = mark(erased, ended, `out = 2`); err != nil {
		return done, nil, err
	}
	for _, p := range ended {
		done[p.Key()] = true
		var held bool
		if err := c.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM library_photos WHERE hash = ? AND out = 0)`, p.Hash).Scan(&held); err != nil {
			return done, gone, err
		}
		if !held {
			gone = append(gone, p.Hash)
		}
	}
	if direct(root, TrashDir+"/x") == nil {
		removeEmpty(filepath.Join(root, TrashDir))
	}
	return done, gone, nil
}

var (
	errNotIts = errors.New("the file is not the photo's, or is another's too")
	errLast   = errors.New("the file is the last of a photo another library has")
)

// deletable is the file of p, in the Trash, that may be deleted: nil when it
// is gone already. Its bytes are read, whole: nothing else says it is p's.
func deletable(root string, c *Catalog, p Photo, asked map[Key]bool) (fs.FileInfo, error) {
	info, err := regular(root, p.Path)
	if err != nil {
		return nil, err
	}
	if info == nil && inTrash(p.Path) {
		// Put back by a run killed before it was recorded.
		home := p
		home.Path = strings.TrimPrefix(p.Path, TrashDir+"/")
		there, err := its(root, c, home)
		if err != nil && !errors.Is(err, errNotIts) { // errNotIts: taken since by another photo
			return nil, err
		}
		if there != nil {
			if err := moveName(root, home.Path, p.Path); err != nil {
				return nil, err
			}
			info = there
		}
	}
	if info == nil {
		// Its disk taken out, nothing is gone.
		_, err := os.Stat(filepath.Join(root, WorkDir))
		return nil, err
	}
	if !inTrash(p.Path) {
		return nil, errNotIts
	}
	if info, err = its(root, c, p); err != nil || info == nil {
		return nil, errors.Join(err, errNotIts)
	}
	others, err := c.photos(`WHERE hash = ? AND profile <> ? AND out = 0`, p.Hash, p.Profile)
	if err != nil {
		return nil, err
	}
	for _, o := range others {
		if !asked[o.Key()] && !holds(root, o, p, info) {
			return nil, errLast
		}
	}
	return info, nil
}

// its is the file at p's path, its content read to be p's and the path no
// other photo's: nil when there is none.
func its(root string, c *Catalog, p Photo) (fs.FileInfo, error) {
	info, err := regular(root, p.Path)
	if err != nil || info == nil {
		return nil, err
	}
	taken, err := c.PathTaken(p.Path, p.Key())
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, errNotIts
	}
	hash, err := hashOf(under(root, p.Path))
	if err != nil {
		return nil, err
	}
	if hash != p.Hash {
		return nil, errNotIts
	}
	return info, nil
}

// holds is whether o has the content it shares with p in a file of its own,
// whatever becomes of p's: another name of the same file, or a copy, read.
func holds(root string, o, p Photo, file fs.FileInfo) bool {
	if strings.EqualFold(o.Path, p.Path) {
		return false
	}
	info, err := os.Stat(under(root, o.Path))
	if err != nil {
		return false
	}
	if os.SameFile(info, file) {
		// Through a link it could be p's own name.
		_, err := regular(root, o.Path)
		return err == nil
	}
	hash, err := hashOf(under(root, o.Path))
	return err == nil && hash == o.Hash
}

// remove deletes p's file, looked at as was, with its names in p's albums and
// what goes with it. It stops before the file if one of them cannot go.
func remove(root string, c *Catalog, p Photo, was fs.FileInfo, in entries) error {
	file := under(root, p.Path)
	info, err := regular(root, p.Path)
	if err != nil {
		return err
	}
	if (info == nil) != (was == nil) || info != nil && (!os.SameFile(info, was) || info.Size() != was.Size() || !info.ModTime().Equal(was.ModTime())) {
		return errNotIts // changed since it was read
	}
	if info != nil {
		names, err := albumNames(root, c, p, in)
		if err != nil {
			return err
		}
		for i, name := range names {
			if err = os.Remove(name); err != nil {
				names = names[:i]
				break
			}
		}
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
	for _, b := range besides(p.Path) {
		if info, err := regular(root, b); inTrash(p.Path) && err == nil && info != nil {
			os.Remove(under(root, b))
		}
	}
	return nil
}

// albumNames are the files of p's albums, in its library's folder, that are
// p: second names of its file, or copies, read.
func albumNames(root string, c *Catalog, p Photo, in entries) ([]string, error) {
	albums, _, err := c.About(p.Key())
	if err != nil {
		return nil, err
	}
	var names []string
	for _, album := range albums {
		dir := path.Join(p.Profile, "albums", album)
		if direct(root, dir+"/x") != nil {
			continue
		}
		for _, name := range in.of(under(root, dir), under(root, p.Path)) {
			taken, err := c.PathTaken(relativeTo(root, name), Key{})
			if err != nil {
				return nil, err
			}
			if !taken {
				names = append(names, name)
			}
		}
	}
	return names, nil
}
