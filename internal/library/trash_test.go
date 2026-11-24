// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// trashing is a library with files at paths, each in the library of its
// first folder, with the content given.
type trashing struct {
	*testing.T
	root string
	c    *Catalog
}

func newTrashing(t *testing.T) *trashing {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, WorkDir), 0o755); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCatalog(filepath.Join(root, WorkDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &trashing{t, root, c}
}

// add files a photo, its file too unless it is there.
func (l *trashing) add(rel, content string) Key {
	l.Helper()
	if !exists(under(l.root, rel)) {
		write(l.T, l.root, rel, []byte(content))
	}
	profile, _, _ := strings.Cut(rel, "/")
	k := Key{mustHash(l.T, under(l.root, rel)), profile}
	if err := l.c.Put(Photo{Hash: k.Hash, Profile: profile, Path: rel, Name: path.Base(rel), Bytes: int64(len(content))}, "local/card"); err != nil {
		l.Fatal(err)
	}
	return k
}

func (l *trashing) mark(on bool, keys ...Key) {
	l.Helper()
	if done, err := l.c.Mark(Trashed, keys, on); err != nil || len(done) != len(keys) {
		l.Fatalf("marked %v %v, want %d", done, err, len(keys))
	}
}

func (l *trashing) tidy() []string {
	l.Helper()
	gone, err := TidyTrash(l.root, filepath.Join(l.root, WorkDir, "catalog.db"), func() (func(), error) { return func() {}, nil })
	if err != nil {
		l.Fatal(err)
	}
	return gone
}

// trash puts photos in the Trash, their files too.
func (l *trashing) trash(keys ...Key) {
	l.Helper()
	l.mark(true, keys...)
	l.tidy()
}

// empty empties keys and wants those left.
func (l *trashing) empty(keys []Key, left ...Key) (gone []string) {
	l.Helper()
	got, gone, err := EmptyTrash(l.root, l.c, keys)
	if err != nil || len(got) != len(left) {
		l.Fatalf("left %v %v, want %v", got, err, left)
	}
	for _, k := range left {
		if !slices.Contains(got, k) {
			l.Errorf("left %v, want %v", got, left)
		}
	}
	return gone
}

// at wants the photo k recorded at rel, with its file there.
func (l *trashing) at(k Key, rel string) {
	l.Helper()
	p, err := l.c.Photo(k)
	if err != nil || p.Path != rel {
		l.Errorf("recorded at %q %v, want %q", p.Path, err, rel)
	}
	l.file(rel, true)
}

func (l *trashing) file(rel string, want bool) {
	l.Helper()
	if exists(under(l.root, rel)) != want {
		l.Errorf("%s there: %v, want %v", rel, !want, want)
	}
}

func (l *trashing) exec(query string, args ...any) {
	l.Helper()
	if _, err := l.c.db.Exec(query, args...); err != nil {
		l.Fatal(err)
	}
}

// isErased wants k deleted for good: in no list, not to be restored, not
// missing, kept out of what comes in, and its marks as they were.
func (l *trashing) isErased(k Key) {
	l.Helper()
	for _, f := range []Filter{{Only: []Key{k}}, {Only: []Key{k}, Trashed: true}, {Only: []Key{k}, Archived: true}} {
		if n, err := l.c.Count(f); err != nil || n != 0 {
			l.Errorf("%+v lists %d %v, want none", f, n, err)
		}
	}
	if done, err := l.c.Mark(Trashed, []Key{k}, false); err != nil || len(done) != 0 {
		l.Errorf("restored %v %v", done, err)
	}
	if h, err := Holds(l.c, l.root, k); err != nil || !h.Known || !h.Out() || h.Trashed || !h.Deleted {
		l.Errorf("held %+v %v, want it deleted", h, err)
	}
	missing, err := Missing(filepath.Join(l.root, WorkDir, "catalog.db"), l.root)
	if err != nil || slices.ContainsFunc(missing, func(g Gone) bool { return g.Key == k }) {
		l.Errorf("missing %v %v", missing, err)
	}
}

// A photo put in the Trash has its file there, with what goes with it;
// restored, back where it was; emptied, nowhere, and nothing else is touched.
func TestAPhotoGoesInTheTrashAndOut(t *testing.T) {
	l := newTrashing(t)
	a, b := l.add("ron/2024/01/a.jpg", "a photo"), l.add("ron/2024/01/b.jpg", "another")
	write(t, l.root, "ron/2024/01/a.jpg.xmp", []byte("date"))
	write(t, l.root, WorkDir+"/metadata/ron/2024/01/a.jpg.json", []byte("google's"))
	l.trash(a)
	l.at(a, ".trash/ron/2024/01/a.jpg")
	l.file("ron/2024/01/a.jpg", false)
	l.file("ron/2024/01/a.jpg.xmp", false)
	l.file(".trash/ron/2024/01/a.jpg.xmp", true)
	l.file(WorkDir+"/metadata/.trash/ron/2024/01/a.jpg.json", true)
	l.at(b, "ron/2024/01/b.jpg")
	if h, err := Holds(l.c, l.root, a); err != nil || !h.Trashed || h.Deleted {
		t.Errorf("held %+v %v", h, err)
	}
	if by, err := l.c.BytesByKind(); err != nil || by[""] != 14 {
		t.Errorf("bytes %v %v, want the Trash's too", by, err)
	}
	l.mark(false, a)
	l.tidy()
	l.at(a, "ron/2024/01/a.jpg")
	l.file("ron/2024/01/a.jpg.xmp", true)
	l.file(WorkDir+"/metadata/ron/2024/01/a.jpg.json", true)
	l.file(".trash/ron/2024/01/a.jpg", false)

	l.trash(a)
	if gone := l.empty([]Key{a}); !slices.Equal(gone, []string{a.Hash}) {
		t.Errorf("gone %v", gone)
	}
	l.isErased(a)
	l.file(".trash", false)
	l.file(WorkDir+"/metadata/.trash/ron/2024/01/a.jpg.json", false)
	l.at(b, "ron/2024/01/b.jpg")
	if by, err := l.c.BytesByKind(); err != nil || by[""] != 7 {
		t.Errorf("bytes %v %v", by, err)
	}
	// Again, and one never in the Trash: nothing.
	l.empty([]Key{a, b, {"nothing", "ron"}})
	l.at(b, "ron/2024/01/b.jpg")
	if names, err := l.c.TrashedNames("ron"); err != nil || !slices.Equal(names, []string{"a.jpg"}) {
		t.Errorf("names %v %v", names, err)
	}
}

// Only what was shown is emptied: one put in the Trash meanwhile stays, and
// one restored meanwhile.
func TestEmptyTakesOnlyWhatWasShown(t *testing.T) {
	l := newTrashing(t)
	a, b, d := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third")
	l.trash(a, d)
	shown := []Key{a, d}
	l.trash(b)
	l.mark(false, d) // its file not brought back yet
	l.empty(shown)
	l.isErased(a)
	l.at(b, ".trash/ron/b.jpg")
	l.tidy()
	l.at(d, "ron/d.jpg")
}

// Restored after Empty looked at it, before it claimed it: it is not deleted.
func TestAPhotoRestoredAsEmptyLooks(t *testing.T) {
	l := newTrashing(t)
	a, b := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another")
	l.trash(a, b)
	rows, err := l.c.photosOf([]Key{a, b}, `out = 0 AND trashed = 1`)
	if err != nil {
		t.Fatal(err)
	}
	l.mark(false, a)
	done, _, err := erase(l.root, l.c, rows, map[Key]bool{a: true, b: true})
	if err != nil || len(done) != 1 || !done[b] {
		t.Errorf("%v %v, want b alone", done, err)
	}
	l.tidy()
	l.at(a, "ron/a.jpg")
	// Changed after it was read: it is not deleted either.
	l.trash(a)
	p, _ := l.c.Photo(a)
	was, err := deletable(l.root, l.c, p, nil)
	if err != nil {
		t.Fatal(err)
	}
	write(t, l.root, p.Path, []byte("an edit"))
	os.Chtimes(under(l.root, p.Path), time.Now(), time.Now().Add(time.Second))
	if err := remove(l.root, l.c, p, was, entries{}); err == nil {
		t.Error("deleted")
	}
	l.file(p.Path, true)
}

// A name taken meanwhile, in either direction: the other photo keeps it.
func TestAPlaceTakenMeanwhile(t *testing.T) {
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	l.trash(a)
	b := l.add("ron/a.jpg", "a new one there")
	l.trash(b)
	l.at(b, ".trash/ron/a-2.jpg")
	d := l.add("ron/a.jpg", "a third, kept")
	l.mark(false, a, b)
	l.tidy()
	l.at(d, "ron/a.jpg")
	for k, content := range map[Key]string{a: "a photo", b: "a new one there", d: "a third, kept"} {
		p, _ := l.c.Photo(k)
		if got, err := os.ReadFile(under(l.root, p.Path)); err != nil || string(got) != content || inTrash(p.Path) || p.Name != path.Base(p.Path) {
			t.Errorf("%+v holds %q %v, want %q", p, got, err, content)
		}
	}
}

// A run killed between the move and the record, at each point of it, on a disk
// with hard links or without: the photo is found, and is where it belongs.
func TestAMoveKilledIsFinished(t *testing.T) {
	for _, put := range []bool{true, false} {
		for name, kill := range map[string]func(from, to string) error{
			"before":    func(from, to string) error { return nil },
			"two names": os.Link,
			"moved":     os.Rename,
		} {
			t.Run(fmt.Sprint(put, name), func(t *testing.T) {
				l := newTrashing(t)
				a := l.add("ron/a.jpg", "a photo")
				from, to := "ron/a.jpg", ".trash/ron/a.jpg"
				if !put {
					l.trash(a)
					from, to = to, from
				}
				l.mark(put, a)
				os.MkdirAll(filepath.Dir(under(l.root, to)), 0o755)
				if err := kill(under(l.root, from), under(l.root, to)); err != nil {
					t.Fatal(err)
				}
				l.tidy()
				l.at(a, to)
				l.file(from, false)
				l.tidy()
				l.at(a, to)
				// Emptied at once, not tidied before, it goes all the same.
				if put {
					l.empty([]Key{a})
					l.isErased(a)
					l.file(to, false)
				}
			})
		}
	}
}

// Brought back by a run killed before it was recorded, then put in the Trash
// again: Empty finds the file.
func TestAPhotoKilledOnItsWayBackIsEmptied(t *testing.T) {
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	l.trash(a)
	os.MkdirAll(under(l.root, "ron"), 0o755)
	if err := os.Rename(under(l.root, ".trash/ron/a.jpg"), under(l.root, "ron/a.jpg")); err != nil {
		t.Fatal(err)
	}
	l.empty([]Key{a})
	l.isErased(a)
	l.file("ron/a.jpg", false)
	// Another photo there is not it.
	b := l.add("ron/b.jpg", "b's photo")
	l.trash(b)
	os.Remove(under(l.root, ".trash/ron/b.jpg"))
	write(t, l.root, "ron/b.jpg", []byte("not b's"))
	l.empty([]Key{b})
	l.isErased(b)
	l.file("ron/b.jpg", true)
}

// An Empty killed after its claim is finished, the file deleted or not yet;
// one whose file changed since is back in the Trash, untouched.
func TestAnEmptyKilledIsFinished(t *testing.T) {
	l := newTrashing(t)
	a, b, d := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third")
	l.trash(a, b, d)
	l.exec(`UPDATE library_photos SET out = 2`)
	os.Remove(under(l.root, ".trash/ron/a.jpg"))
	write(t, l.root, ".trash/ron/d.jpg", []byte("other bytes"))
	l.isErased(a) // what is claimed is out already
	if gone := l.tidy(); len(gone) != 2 {
		t.Errorf("gone %v", gone)
	}
	l.isErased(a)
	l.isErased(b)
	l.file(".trash/ron/b.jpg", false)
	if n, err := l.c.Count(Filter{Trashed: true}); err != nil || n != 1 {
		t.Errorf("in the Trash %d %v, want d", n, err)
	}
	l.file(".trash/ron/d.jpg", true)
	// Put back from a copy of the disk once all is done, a file is left alone.
	write(t, l.root, ".trash/ron/a.jpg", []byte("a photo"))
	l.tidy()
	l.empty([]Key{a})
	l.file(".trash/ron/a.jpg", true)
	l.isErased(a)
}

// What one does to a photo two libraries have is never done to the other's.
func TestTheOthersPhotoIsNotTouched(t *testing.T) {
	const content = "a photo of two"
	for name, do := range map[string]func(l *trashing, rons, anns Key){
		"one file": func(l *trashing, rons, anns Key) {
			l.trash(rons)
			l.empty([]Key{rons})
			l.isErased(rons)
			l.at(anns, "ann/a.jpg")
			l.trash(anns)
			l.empty([]Key{anns})
			l.isErased(anns)
			l.file("ann/a.jpg", false)
		},
		"two copies": func(l *trashing, rons, anns Key) {
			os.Remove(under(l.root, "ann/a.jpg"))
			write(l.T, l.root, "ann/a.jpg", []byte(content))
			l.trash(rons)
			l.empty([]Key{rons})
			l.isErased(rons)
			l.at(anns, "ann/a.jpg")
		},
		"her file changed, his is the last": func(l *trashing, rons, anns Key) {
			os.Remove(under(l.root, "ann/a.jpg"))
			write(l.T, l.root, "ann/a.jpg", []byte("an edit, hers "))
			l.trash(rons)
			l.empty([]Key{rons}, rons)
			l.at(rons, ".trash/ron/a.jpg")
		},
		"her file gone, his is the last": func(l *trashing, rons, anns Key) {
			os.Remove(under(l.root, "ann/a.jpg"))
			l.trash(rons)
			l.empty([]Key{rons}, rons)
			l.at(rons, ".trash/ron/a.jpg")
			l.mark(false, rons)
			l.tidy()
			l.at(rons, "ron/a.jpg")
		},
		"hers in the Trash, emptied with his": func(l *trashing, rons, anns Key) {
			os.Remove(under(l.root, "ann/a.jpg"))
			l.trash(rons, anns)
			if gone := l.empty([]Key{rons, anns}); len(gone) != 2 {
				l.Errorf("gone %v", gone)
			}
			l.isErased(rons)
			l.isErased(anns)
		},
		"hers in the Trash, not emptied": func(l *trashing, rons, anns Key) {
			l.trash(rons, anns)
			if gone := l.empty([]Key{rons}); len(gone) != 0 {
				l.Errorf("gone %v: ann has it", gone)
			}
			l.mark(false, anns)
			l.tidy()
			l.at(anns, "ann/a.jpg")
		},
		"one record's path is the other's": func(l *trashing, rons, anns Key) {
			l.exec(`UPDATE library_photos SET path = 'ann/a.jpg' WHERE profile = 'ron'`)
			l.mark(true, rons)
			l.tidy()
			l.empty([]Key{rons}, rons)
			l.at(anns, "ann/a.jpg")
		},
		"the same path on a disk that ignores capitals": func(l *trashing, rons, anns Key) {
			l.exec(`UPDATE library_photos SET path = 'RON/A.jpg' WHERE profile = 'ann'`)
			l.mark(true, rons)
			l.tidy()
			l.empty([]Key{rons}, rons)
			l.at(rons, "ron/a.jpg")
		},
		"her folder is a link into his Trash": func(l *trashing, rons, anns Key) {
			if err := os.RemoveAll(under(l.root, "ann")); err != nil {
				l.Fatal(err)
			}
			l.trash(rons)
			if err := os.Symlink(under(l.root, ".trash/ron"), under(l.root, "ann")); err != nil {
				l.Skip(err)
			}
			l.empty([]Key{rons}, rons)
			l.file("ann/a.jpg", true)
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := newTrashing(t)
			rons := l.add("ron/a.jpg", content)
			os.MkdirAll(under(l.root, "ann"), 0o755)
			if err := os.Link(under(l.root, "ron/a.jpg"), under(l.root, "ann/a.jpg")); err != nil {
				t.Skip(err)
			}
			do(l, rons, l.add("ann/a.jpg", content))
		})
	}
}

// What is not surely the photo's own file is left where it is.
func TestAFileNotItsOwnIsLeft(t *testing.T) {
	outside := t.TempDir()
	for name, set := range map[string]func(l *trashing) (file string){
		"another file in its place": func(l *trashing) string {
			write(l.T, l.root, "ron/a.jpg", []byte("quite another photo"))
			return "ron/a.jpg"
		},
		"another of its size": func(l *trashing) string {
			write(l.T, l.root, "ron/a.jpg", []byte("b photo"))
			return ".trash/ron/a.jpg"
		},
		"its name a link": func(l *trashing) string {
			write(l.T, outside, "kept.jpg", []byte("a photo"))
			os.Remove(under(l.root, "ron/a.jpg"))
			if err := os.Symlink(filepath.Join(outside, "kept.jpg"), under(l.root, "ron/a.jpg")); err != nil {
				l.Skip(err)
			}
			return "ron/a.jpg"
		},
		"its folder a link out": func(l *trashing) string {
			write(l.T, outside, "ron/a.jpg", []byte("a photo"))
			os.RemoveAll(under(l.root, "ron"))
			if err := os.Symlink(filepath.Join(outside, "ron"), under(l.root, "ron")); err != nil {
				l.Skip(err)
			}
			return "ron/a.jpg"
		},
		"the Trash a link": func(l *trashing) string {
			if err := os.Symlink(outside, under(l.root, ".trash")); err != nil {
				l.Skip(err)
			}
			return "ron/a.jpg"
		},
		"a path out of the library": func(l *trashing) string {
			write(l.T, filepath.Dir(l.root), "out.jpg", []byte("a photo"))
			l.exec(`UPDATE library_photos SET path = '../out.jpg'`)
			return "../out.jpg"
		},
		"a path out of the Trash": func(l *trashing) string {
			l.exec(`UPDATE library_photos SET path = '.trash/../ron/a.jpg'`)
			return "ron/a.jpg"
		},
		"a whole path": func(l *trashing) string {
			l.exec(`UPDATE library_photos SET path = ?`, filepath.ToSlash(under(l.root, "ron/a.jpg")))
			return "ron/a.jpg"
		},
		"a folder": func(l *trashing) string {
			os.Remove(under(l.root, "ron/a.jpg"))
			write(l.T, l.root, "ron/a.jpg/in it.jpg", []byte("a photo"))
			return "ron/a.jpg/in it.jpg"
		},
	} {
		t.Run(name, func(t *testing.T) {
			l := newTrashing(t)
			a := l.add("ron/a.jpg", "a photo")
			file := set(l)
			l.mark(true, a)
			_, _ = TidyTrash(l.root, filepath.Join(l.root, WorkDir, "catalog.db"), func() (func(), error) { return func() {}, nil })
			l.empty([]Key{a}, a)
			l.file(file, true)
		})
	}
}

// A photo whose file is gone already is emptied; with the disk gone, nothing is.
func TestAFileGoneAlready(t *testing.T) {
	l := newTrashing(t)
	a, b := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another")
	os.Remove(under(l.root, "ron/a.jpg"))
	l.mark(true, a)
	l.tidy()
	l.empty([]Key{a})
	l.isErased(a)
	l.trash(b)
	away := l.root + "-away"
	if err := os.Rename(l.root, away); err != nil {
		t.Skip(err)
	}
	if left, _, err := EmptyTrash(l.root, l.c, []Key{b}); err == nil && len(left) != 1 {
		t.Errorf("emptied with the disk away: %v %v", left, err)
	}
	if err := os.Rename(away, l.root); err != nil {
		t.Fatal(err)
	}
	l.at(b, ".trash/ron/b.jpg")
}

// Its names in its albums go with it, and no other's: not another photo's,
// nor the album of another library.
func TestAnAlbumsNamesGoToo(t *testing.T) {
	for _, links := range []bool{true, false} {
		l := newTrashing(t)
		a, b := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "b photo")
		for _, name := range []string{"ron/albums/Sea/a.jpg", "ron/albums/Sea/other name.jpg", "ann/albums/Sea/a.jpg"} {
			os.MkdirAll(filepath.Dir(under(l.root, name)), 0o755)
			if !links {
				write(t, l.root, name, []byte("a photo"))
			} else if err := os.Link(under(l.root, "ron/a.jpg"), under(l.root, name)); err != nil {
				t.Skip(err)
			}
		}
		write(t, l.root, "ron/albums/Sea/b.jpg", []byte("b photo"))
		write(t, l.root, "ron/albums/Hills/a.jpg", []byte("a photo")) // not in it
		for _, k := range []Key{a, b} {
			if err := l.c.addAlbum("Sea", k, ""); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.c.addAlbum("../../../x", a, ""); err != nil {
			t.Fatal(err)
		}
		l.trash(a)
		l.file("ron/albums/Sea/a.jpg", true)
		l.empty([]Key{a})
		l.isErased(a)
		for name, there := range map[string]bool{"ron/albums/Sea/a.jpg": false, "ron/albums/Sea/other name.jpg": false,
			"ron/albums/Sea/b.jpg": true, "ron/albums/Hills/a.jpg": true, "ann/albums/Sea/a.jpg": true} {
			l.file(name, there)
		}
		l.at(b, "ron/b.jpg")
	}
}

// On a disk with no hard links files are moved, and never over another.
func TestTheTrashOfADiskWithNoHardLinks(t *testing.T) {
	was := hardLink
	hardLink = func(_, _ string) error { return errors.ErrUnsupported }
	t.Cleanup(func() { hardLink = was })
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	l.trash(a)
	b := l.add("ron/a.jpg", "another")
	l.trash(b)
	l.at(a, ".trash/ron/a.jpg")
	l.at(b, ".trash/ron/a-2.jpg")
	l.mark(false, a)
	l.tidy()
	l.at(a, "ron/a.jpg")
	l.empty([]Key{b})
	l.isErased(b)
	if err := rename(under(l.root, "ron/a.jpg"), under(l.root, WorkDir+"/catalog.db")); err == nil {
		t.Error("moved over a file")
	}
	l.at(a, "ron/a.jpg")
}

// Where nothing can be written nothing is lost: what could not be moved or
// deleted is still recorded where it is, in the Trash.
func TestAFolderThatCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 || runtime.GOOS == "windows" {
		t.Skip("a mode takes no one's writing away")
	}
	l := newTrashing(t)
	a, b := l.add("ron/a.jpg", "a photo"), l.add("ann/b.jpg", "another")
	write(t, l.root, "ron/a.jpg.xmp", []byte("date"))
	l.trash(b)
	lock := func(rel string, mode os.FileMode) {
		if err := os.Chmod(under(l.root, rel), mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(under(l.root, rel), 0o755) })
	}
	lock("ron", 0o555)
	lock(".trash/ann", 0o555)
	l.mark(true, a)
	if _, err := TidyTrash(l.root, filepath.Join(l.root, WorkDir, "catalog.db"), func() (func(), error) { return func() {}, nil }); err == nil {
		t.Error("not said")
	}
	l.at(a, "ron/a.jpg")
	l.file("ron/a.jpg.xmp", true)
	l.file(".trash/ron/a.jpg", false)
	l.empty([]Key{a, b}, a, b)
	l.at(b, ".trash/ann/b.jpg")
	lock("ron", 0o755)
	lock(".trash/ann", 0o755)
	l.empty([]Key{a, b})
	l.isErased(a)
	l.isErased(b)
	// The catalog full or not to be written: nothing moves, nothing goes.
	d := l.add("ron/d.jpg", "a third")
	l.trash(d)
	l.exec(`CREATE TRIGGER full BEFORE UPDATE OF out, path ON library_photos BEGIN SELECT RAISE(ABORT, 'full'); END`)
	if left, _, err := EmptyTrash(l.root, l.c, []Key{d}); err == nil || len(left) != 1 {
		t.Errorf("left %v %v", left, err)
	}
	l.at(d, ".trash/ron/d.jpg")
	l.mark(false, d)
	if _, err := TidyTrash(l.root, filepath.Join(l.root, WorkDir, "catalog.db"), func() (func(), error) { return func() {}, nil }); err == nil {
		t.Error("not said")
	}
	l.at(d, ".trash/ron/d.jpg")
	l.file("ron/d.jpg", false)
}

// An emptied photo is skipped by a Takeout and by a card, and comes back only
// asked for by name; its mark was kept as a photo kept out keeps it.
func TestAnEmptiedPhotoIsSkipped(t *testing.T) {
	l := newTrashing(t)
	takeout, card := t.TempDir(), t.TempDir()
	write(t, takeout, "Takeout/Google Photos/Photos from 2019/A.jpg", []byte("a photo"))
	write(t, takeout, "Takeout/Google Photos/Sea/A.jpg", []byte("a photo"))
	sidecar(t, takeout, "Takeout/Google Photos/Photos from 2019/A.jpg", 1563098400)
	write(t, card, "DCIM/100/A.JPG", []byte("a photo"))
	opt := Options{Profile: "ron", Location: time.UTC, Account: "google/ron@x"}
	items, err := ReadTakeout(takeout)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Organize(items, l.root, l.c, opt, nil); err != nil {
		t.Fatal(err)
	}
	page, _ := l.c.Page(Filter{})
	if len(page) != 1 {
		t.Fatalf("%v", page)
	}
	a := page[0].Key()
	if _, err := l.c.Mark(Favorite, []Key{a}, true); err != nil {
		t.Fatal(err)
	}
	l.trash(a)
	l.empty([]Key{a})
	l.isErased(a)
	var files []string
	filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() && IsMedia(p) {
			files = append(files, p)
		}
		return nil
	})
	if len(files) != 0 {
		t.Errorf("left on the disk: %v", files)
	}
	// The same export again, and what a stopped run had left of it.
	write(t, takeout, "Takeout/Google Photos/Photos from 2019/A.jpg", []byte("a photo"))
	if items, err = ReadTakeout(takeout); err != nil {
		t.Fatal(err)
	}
	if done, err := Organize(items, l.root, l.c, opt, nil); err != nil || done.Placed != 0 || done.Skipped != len(items) || done.Linked+done.Copied != 0 {
		t.Errorf("%+v %v", done, err)
	}
	l.isErased(a)
	folder, err := ReadFolder(card)
	if err != nil {
		t.Fatal(err)
	}
	how := ImportOptions{Fits: func(int64) error { return nil }}
	if done, err := Import(context.Background(), folder, l.root, l.c, opt, how, nil); err != nil || done.Placed != 0 || done.Deleted != 1 {
		t.Errorf("%+v %v, want it left out as deleted", done, err)
	}
	if plan, err := PlanImport(context.Background(), card, folder, l.c, PlanOptions{Root: l.root, Profile: "ron"}, nil); err != nil || plan.Deleted != 1 || plan.Photos != 0 {
		t.Errorf("%+v %v", plan.Counts, err)
	}
	l.isErased(a)
	how.BringBack = []string{"A.JPG"}
	if done, err := Import(context.Background(), folder, l.root, l.c, opt, how, nil); err != nil || done.Placed != 1 || done.BroughtBack != 1 {
		t.Errorf("%+v %v, want it back", done, err)
	}
	p, err := l.c.Photo(a)
	if err != nil || !onDisk(l.root, p) || inTrash(p.Path) || !p.Favorite {
		t.Errorf("%+v %v", p, err)
	}
	if n, _ := l.c.Count(Filter{}); n != 1 {
		t.Errorf("%d listed, want it back", n)
	}
}

// What was deleted from the Trash came: an export's count says it is here, and
// the Trash's folder is not walked.
func TestAnExportsCountKnowsTheTrash(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "2019/07/a.jpg", []byte("a"))
	write(t, dir, ".trash/ron/b.jpg", []byte("b"))
	m := Manifest{Media: []Entry{{Name: "a.jpg", Folder: "Photos from 2019"}, {Name: "b.jpg", Folder: "Photos from 2019"}, {Name: "c.jpg", Folder: "Photos from 2019"}}}
	v, err := Verify(dir, m, nil)
	if err != nil || v.Present != 1 {
		t.Errorf("%+v %v", v, err)
	}
	if v, err = Verify(dir, m, []string{"b.jpg", "not of this export.jpg"}); err != nil || v.Present != 2 || len(v.Unexpected) != 0 || !slices.Equal(v.Missing, []string{"c.jpg"}) {
		t.Errorf("%+v %v", v, err)
	}
}

// An older program leaves the files where they are: one it restored is brought
// home, one it put in the Trash goes in, and Empty asks neither before.
func TestAnOlderProgramsTrash(t *testing.T) {
	l := newTrashing(t)
	a, b := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another")
	l.trash(a)
	l.mark(false, a)
	l.at(a, ".trash/ron/a.jpg")
	if missing, err := Missing(filepath.Join(l.root, WorkDir, "catalog.db"), l.root); err != nil || len(missing) != 0 {
		t.Errorf("missing %v %v", missing, err)
	}
	l.mark(true, b)
	l.empty([]Key{a, b})
	l.isErased(b)
	l.file("ron/b.jpg", false)
	l.at(a, ".trash/ron/a.jpg")
	l.tidy()
	l.at(a, "ron/a.jpg")
	// A date given to it meanwhile files it, and the Trash takes it again.
	l.trash(a)
	l.mark(false, a)
	if n, err := GiveDates(l.root, l.c, []Key{a}, time.Date(2020, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	l.at(a, "ron/2020/05/a.jpg")
	l.trash(a)
	l.at(a, ".trash/ron/2020/05/a.jpg")
}

// A library from before profiles is moved into them, its Trash and what was
// emptied from it too.
func TestAnOldLibrarysTrashFolder(t *testing.T) {
	root, c, hashes := relaying(t, map[string]string{"2019/07/a.jpg": "a photo", "2019/07/b.jpg": "another"})
	l := &trashing{t, root, c}
	os.MkdirAll(filepath.Join(root, WorkDir), 0o755)
	a, b := Key{Hash: hashes["2019/07/a.jpg"]}, Key{Hash: hashes["2019/07/b.jpg"]}
	tidyAll := func() {
		photos, err := c.photos(`WHERE ` + misplaced)
		if err == nil {
			err = tidy(root, c, photos, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	l.mark(true, a, b)
	tidyAll()
	l.at(a, ".trash/2019/07/a.jpg")
	l.empty([]Key{b})
	if err := Relayout(root, c, nil, ronsAccount, "ron", time.UTC); err != nil {
		t.Fatal(err)
	}
	a.Profile = "ron"
	tidyAll()
	l.at(a, ".trash/ron/2019/07/a.jpg")
	if n, err := c.Count(Filter{Trashed: true}); err != nil || n != 1 {
		t.Errorf("%d in the Trash %v", n, err)
	}
	if n, err := c.Count(Filter{}); err != nil || n != 0 {
		t.Errorf("%d listed %v", n, err)
	}
}

// Killed at any moment, a run that trashes, restores and empties loses no
// photo: each is in the library, or in the Trash, with its file, or was
// emptied, and then is in nobody's way.
func TestKilledAtAnyMoment(t *testing.T) {
	const photos = 30
	if root := os.Getenv("HOMEWEND_TEST_TRASH"); root != "" {
		c, err := OpenCatalog(filepath.Join(root, WorkDir, "catalog.db"))
		if err != nil {
			t.Fatal(err)
		}
		all, err := c.photos(``)
		if err != nil {
			t.Fatal(err)
		}
		var keys []Key
		for _, p := range all {
			keys = append(keys, p.Key())
		}
		for n := 0; ; n++ {
			c.Mark(Trashed, keys, true)
			TidyTrash(root, filepath.Join(root, WorkDir, "catalog.db"), func() (func(), error) { return func() {}, nil })
			EmptyTrash(root, c, keys[:min(n, len(keys))])
			c.Mark(Trashed, keys, false)
			TidyTrash(root, filepath.Join(root, WorkDir, "catalog.db"), func() (func(), error) { return func() {}, nil })
		}
	}
	for round := range 12 {
		l := newTrashing(t)
		var keys []Key
		for i := range photos {
			keys = append(keys, l.add(fmt.Sprintf("ron/%d.jpg", i), fmt.Sprint("photo ", i)))
		}
		other := exec.Command(os.Args[0], "-test.run=^TestKilledAtAnyMoment$")
		other.Env = append(os.Environ(), "HOMEWEND_TEST_TRASH="+l.root)
		if err := other.Start(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Duration(30+rand.Intn(200)) * time.Millisecond)
		other.Process.Kill()
		other.Wait()
		l.tidy()
		on := 0
		for _, k := range keys {
			p, err := l.c.Photo(k)
			if err != nil {
				t.Fatal(err)
			}
			gone, err := l.c.erased(k)
			if err != nil {
				t.Fatal(err)
			}
			info, _ := regular(l.root, p.Path)
			if gone == (info != nil) {
				t.Errorf("round %d: %s erased %v, its file %v", round, p.Path, gone, info != nil)
			}
			if !gone {
				on++
				if hash, err := hashOf(under(l.root, p.Path)); err != nil || hash != k.Hash {
					t.Errorf("round %d: %s holds another's bytes %v", round, p.Path, err)
				}
			}
		}
		files := 0
		filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() && IsMedia(p) {
				files++
			}
			return nil
		})
		if files != on {
			t.Errorf("round %d: %d files for %d photos", round, files, on)
		}
	}
}
