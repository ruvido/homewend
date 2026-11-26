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

func (l *trashing) db() string { return filepath.Join(l.root, WorkDir, "catalog.db") }

func anyTurn() (func(), error) { return func() {}, nil }

func plainly(_ *Catalog, do func() error) error { return do() }

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
	gone, err := TidyTrash(l.root, l.db(), nil, anyTurn, plainly)
	if err != nil {
		l.Fatal(err)
	}
	return gone
}

// trash puts photos in the Trash, their files too.
func (l *trashing) trash(keys ...Key) {
	l.Helper()
	l.mark(true, keys...)
	if _, err := TidyTrash(l.root, l.db(), keys, anyTurn, plainly); err != nil {
		l.Fatal(err)
	}
}

// empty empties keys and wants those left.
func (l *trashing) empty(keys []Key, left ...Key) (gone []string) {
	l.Helper()
	got, gone, err := EmptyTrash(l.root, l.db(), keys, anyTurn, plainly, nil)
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
	missing, err := Missing(l.db(), l.root)
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

// Restored, or changed, after Empty read it and before it claimed it: it is
// not deleted, and neither is the last of a content that was to go with it.
func TestAPhotoRestoredAsEmptyReads(t *testing.T) {
	l := newTrashing(t)
	a, b, d := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third")
	l.add("ann/d.jpg", "a third")
	anns := Key{d.Hash, "ann"}
	l.trash(a, b, d, anns)
	os.Remove(under(l.root, ".trash/ann/d.jpg"))
	turns := 0
	left, _, err := EmptyTrash(l.root, l.db(), []Key{a, b, d, anns}, func() (func(), error) {
		if turns++; turns == 1 { // all were read
			l.mark(false, a, anns)
			write(t, l.root, ".trash/ron/b.jpg", []byte("an edit"))
			os.Chtimes(under(l.root, ".trash/ron/b.jpg"), time.Now(), time.Now().Add(time.Second))
		}
		return func() {}, nil
	}, plainly, nil)
	if err != nil || turns != 1 || len(left) != 4 {
		t.Fatalf("left %v %v in %d turns, want all", left, err, turns)
	}
	l.tidy()
	l.at(a, "ron/a.jpg")
	l.at(b, ".trash/ron/b.jpg")
	l.at(d, ".trash/ron/d.jpg")
}

// A name taken meanwhile, in either direction: the other photo keeps it.
func TestAPlaceTakenMeanwhile(t *testing.T) {
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	l.trash(a)
	b := l.add("ron/a.jpg", "a new one there")
	l.trash(b)
	l.at(b, ".trash/.2/ron/a.jpg")
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

// A run killed at each point of a move, in or out: until it is finished the
// photo is known where its file is, whatever the person does; then it is where
// it belongs with what goes with it.
func TestAMoveKilledIsFinished(t *testing.T) {
	const home, in = "ron/a.jpg", ".trash/ron/a.jpg"
	for _, where := range []string{"not moved", "two names", "moved", "what goes with it moved", "recorded"} {
		for _, then := range []string{"nothing", "restored", "put in the Trash", "emptied"} {
			for _, out := range []bool{false, true} {
				t.Run(fmt.Sprint(where, ", out ", out, ", then ", then), func(t *testing.T) {
					l := newTrashing(t)
					a := l.add(home, "a photo")
					write(t, l.root, home+".xmp", []byte("date"))
					from, to := home, in
					if out {
						l.trash(a)
						from, to = in, home
					}
					l.mark(!out, a)
					if err := keepMoves(l.root, []transit{{a, from, to}}); err != nil {
						t.Fatal(err)
					}
					os.MkdirAll(filepath.Dir(under(l.root, to)), 0o755)
					switch where {
					case "two names":
						os.Link(under(l.root, from), under(l.root, to))
					case "moved", "what goes with it moved", "recorded":
						os.Rename(under(l.root, from), under(l.root, to))
					}
					if where == "what goes with it moved" || where == "recorded" {
						moveBesides(l.root, from, to)
					}
					if where == "recorded" {
						l.exec(`UPDATE library_photos SET path = ?`, to)
					}
					trashed := !out
					switch then {
					case "restored":
						l.mark(false, a)
						trashed = false
					case "put in the Trash", "emptied":
						l.mark(true, a)
						trashed = true
					}
					// Before anything is finished.
					if h, err := Holds(l.c, l.root, a); err != nil || h.Deleted || h.Trashed != trashed {
						t.Errorf("held %+v %v", h, err)
					}
					if missing, err := Missing(l.db(), l.root); err != nil || len(missing) != 0 {
						t.Errorf("missing %v %v", missing, err)
					}
					p, _ := l.c.Photo(a)
					l.file(At(l.root, p.Path), true)
					if then == "emptied" {
						l.finish()
						l.empty([]Key{a})
						l.isErased(a)
						for _, rel := range []string{home, home + ".xmp", in, in + ".xmp"} {
							l.file(rel, false)
						}
						return
					}
					// By whoever takes the turn, or the look at the whole library.
					l.finish()
					l.tidy()
					want, other := home, in
					if trashed {
						want, other = in, home
					}
					l.at(a, want)
					l.file(want+".xmp", true)
					l.file(other, false)
					l.file(other+".xmp", false)
					l.file(WorkDir+"/trash-moves.json", false)
				})
			}
		}
	}
}

// finish is what the turn of the library does first.
func (l *trashing) finish() {
	l.Helper()
	if err := FinishMoves(l.root, l.db(), plainly); err != nil {
		l.Fatal(err)
	}
}

// A move whose folder is away is kept for when it is back, and its photo stays
// in the Trash meanwhile; one whose file is an edit is not deleted.
func TestAMoveOfAFolderAway(t *testing.T) {
	for _, out := range []bool{false, true} {
		l := newTrashing(t)
		a := l.add("ron/a.jpg", "a photo")
		from, to := "ron/a.jpg", ".trash/ron/a.jpg"
		if out {
			l.trash(a)
			from, to = to, from
		}
		l.mark(true, a)
		if err := keepMoves(l.root, []transit{{a, from, to}}); err != nil {
			t.Fatal(err)
		}
		folder := strings.Split(from, "/")[0]
		away := filepath.Join(t.TempDir(), "away")
		if err := os.Rename(under(l.root, folder), away); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			l.finish()
			l.empty([]Key{a}, a)
			l.file(WorkDir+"/trash-moves.json", true)
		}
		os.MkdirAll(under(l.root, folder), 0o755) // an empty folder in its place
		if err := os.Remove(under(l.root, folder)); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(away, under(l.root, folder)); err != nil {
			t.Fatal(err)
		}
		l.finish()
		l.empty([]Key{a})
		l.isErased(a)
	}
	// Killed, then edited at its home.
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	l.mark(true, a)
	if err := keepMoves(l.root, []transit{{a, "ron/a.jpg", ".trash/ron/a.jpg"}}); err != nil {
		t.Fatal(err)
	}
	write(t, l.root, "ron/a.jpg", []byte("an edit of it"))
	l.finish()
	l.empty([]Key{a}, a)
	l.at(a, "ron/a.jpg")
}

// A live photo whose place another file took is missing, in transit or not.
func TestAnotherFileInAPlaceInTransit(t *testing.T) {
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	l.trash(a)
	l.mark(false, a)
	os.Remove(under(l.root, ".trash/ron/a.jpg"))
	write(t, l.root, "ron/a.jpg", []byte("not it"))
	if missing, err := Missing(l.db(), l.root); err != nil || len(missing) != 1 {
		t.Errorf("missing %v %v, want it", missing, err)
	}
	if h, err := Holds(l.c, l.root, a); err != nil || !h.Deleted {
		t.Errorf("held %+v %v", h, err)
	}
}

// A folder of the Trash's own that is a link is not gone through.
func TestALinkInTheTrash(t *testing.T) {
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	l.trash(a)
	b := l.add("ron/a.jpg", "another")
	outside := t.TempDir()
	if err := os.Symlink(outside, under(l.root, ".trash/.2")); err != nil {
		t.Skip(err)
	}
	l.trash(b)
	l.at(b, "ron/a.jpg")
	l.empty([]Key{b}, b)
	l.at(b, "ron/a.jpg")
}

// An Empty killed after its claim is finished, the file deleted or not yet,
// or edited since; one whose file is now a link is back in the Trash, untouched.
func TestAnEmptyKilledIsFinished(t *testing.T) {
	l := newTrashing(t)
	a, b, d, e := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third"), l.add("ron/e.jpg", "a fourth")
	l.trash(a, b, d, e)
	l.exec(`UPDATE library_photos SET out = 2`)
	os.Remove(under(l.root, ".trash/ron/a.jpg"))
	write(t, l.root, ".trash/ron/e.jpg", []byte("other bytes"))
	os.Remove(under(l.root, ".trash/ron/d.jpg"))
	outside := t.TempDir()
	write(t, outside, "d.jpg", []byte("a third"))
	if err := os.Symlink(filepath.Join(outside, "d.jpg"), under(l.root, ".trash/ron/d.jpg")); err != nil {
		t.Skip(err)
	}
	l.isErased(a) // what is claimed is out already
	if gone := l.tidy(); len(gone) != 3 {
		t.Errorf("gone %v", gone)
	}
	l.isErased(a)
	l.isErased(b)
	l.isErased(e)
	l.file(".trash/ron/b.jpg", false)
	if n, err := l.c.Count(Filter{Trashed: true}); err != nil || n != 1 {
		t.Errorf("in the Trash %d %v, want d", n, err)
	}
	l.file(".trash/ron/d.jpg", true)
}

// An older program puts a file that comes in at an emptied photo's place: the
// next Empty deletes it, when it is the photo.
func TestAnOlderProgramsFileOfAnEmptiedPhoto(t *testing.T) {
	l := newTrashing(t)
	a, b, d := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third")
	l.trash(a, b, d)
	l.empty([]Key{a, b})
	write(t, l.root, ".trash/ron/a.jpg", []byte("a photo"))
	write(t, l.root, ".trash/ron/b.jpg", []byte("not it."))
	l.tidy()
	l.file(".trash/ron/a.jpg", true)
	l.empty([]Key{d})
	l.file(".trash/ron/a.jpg", false)
	l.file(".trash/ron/b.jpg", true)
	for _, k := range []Key{a, b, d} {
		l.isErased(k)
	}
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
		"both emptied, her file changed": func(l *trashing, rons, anns Key) {
			os.Remove(under(l.root, "ann/a.jpg"))
			write(l.T, l.root, "ann/a.jpg", []byte("an edit, hers "))
			l.trash(rons, anns)
			l.empty([]Key{rons, anns})
			l.isErased(rons)
			l.isErased(anns)
		},
		"both emptied, her edit cannot go: his is the last": func(l *trashing, rons, anns Key) {
			os.Remove(under(l.root, "ann/a.jpg"))
			write(l.T, l.root, "ann/a.jpg", []byte("an edit, hers "))
			l.trash(rons, anns)
			lock := under(l.root, ".trash/ann")
			if err := os.Chmod(lock, 0o555); err != nil || os.Geteuid() == 0 {
				l.Skip("a mode takes no one's writing away")
			}
			defer os.Chmod(lock, 0o755)
			l.empty([]Key{rons, anns}, rons, anns)
			l.at(rons, ".trash/ron/a.jpg")
		},
		"both emptied": func(l *trashing, rons, anns Key) {
			l.trash(rons, anns)
			l.empty([]Key{anns, rons})
			l.isErased(rons)
			l.isErased(anns)
			l.file(".trash", false)
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
			for _, name := range []string{"ann/a.jpg", "ann/É.jpg"} {
				os.Rename(under(l.root, "ann/a.jpg"), under(l.root, name))
				l.exec(`UPDATE library_photos SET path = ?`, name)
				l.mark(true, rons)
				l.tidy()
				l.empty([]Key{rons}, rons)
				l.at(anns, name)
			}
		},
		"the same path on a disk that ignores capitals": func(l *trashing, rons, anns Key) {
			l.exec(`UPDATE library_photos SET path = 'ron/A.jpg' WHERE profile = 'ann'`)
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
			_, _ = TidyTrash(l.root, l.db(), nil, anyTurn, plainly)
			l.empty([]Key{a}, a)
			l.file(file, true)
		})
	}
}

// A file gone is deleted for good only where the folder that should hold it is
// there to say so: one only away, a disk or a mount of it, is still the photo's.
func TestAFileGoneAlready(t *testing.T) {
	l := newTrashing(t)
	a, b, d := l.add("ron/a.jpg", "a photo"), l.add("ann/b.jpg", "another"), l.add("ron/d.jpg", "a third")
	os.Remove(under(l.root, "ron/a.jpg"))
	l.mark(true, a)
	l.tidy()
	l.empty([]Key{a})
	l.isErased(a)
	l.mark(true, b, d)
	away := func(rel string) (back func()) {
		to := filepath.Join(t.TempDir(), "away")
		if err := os.Rename(under(l.root, rel), to); err != nil {
			t.Fatal(err)
		}
		return func() {
			os.Remove(under(l.root, rel))
			if err := os.Rename(to, under(l.root, rel)); err != nil {
				t.Fatal(err)
			}
		}
	}
	back := away("ann")
	l.empty([]Key{b}, b)
	back()
	back = away("ann")
	os.Mkdir(under(l.root, "ann"), 0o755) // a mount's empty folder
	os.Remove(under(l.root, "ann"))
	l.empty([]Key{b}, b)
	back()
	l.tidy()
	for _, rel := range []string{".trash", ".trash/ann"} {
		back = away(rel)
		l.empty([]Key{b}, b)
		back()
		l.at(b, ".trash/ann/b.jpg")
	}
	l.empty([]Key{b, d})
	l.isErased(b)
	e := l.add("ron/e.jpg", "a fourth")
	l.trash(e)
	// Claimed, killed, and the Trash away as the next run looks.
	l.exec(`UPDATE library_photos SET out = 2 WHERE hash = ?`, e.Hash)
	back = away(".trash")
	l.tidy()
	back()
	l.at(e, ".trash/ron/e.jpg")
	if n, err := l.c.Count(Filter{Trashed: true}); err != nil || n != 1 {
		t.Errorf("%d in the Trash %v, want it back", n, err)
	}
	// The whole disk.
	whole := l.root + "-away"
	if err := os.Rename(l.root, whole); err != nil {
		t.Skip(err)
	}
	if left, _, err := EmptyTrash(l.root, l.db(), []Key{e}, anyTurn, plainly, nil); err == nil || len(left) != 1 {
		t.Errorf("emptied with the disk away: %v %v", left, err)
	}
	if err := os.Rename(whole, l.root); err != nil {
		t.Fatal(err)
	}
	l.at(e, ".trash/ron/e.jpg")
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
	l.at(b, ".trash/.2/ron/a.jpg")
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
	if _, err := TidyTrash(l.root, l.db(), nil, anyTurn, plainly); err == nil {
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
	if left, _, err := EmptyTrash(l.root, l.db(), []Key{d}, anyTurn, plainly, nil); err == nil || len(left) != 1 {
		t.Errorf("left %v %v", left, err)
	}
	l.at(d, ".trash/ron/d.jpg")
	l.mark(false, d)
	if _, err := TidyTrash(l.root, l.db(), nil, anyTurn, plainly); err == nil {
		t.Error("not said")
	}
	// Moved and not recorded, it is found, and recorded when it can be.
	p, _ := l.c.Photo(d)
	l.file(At(l.root, p.Path), true)
	l.exec(`DROP TRIGGER full`)
	l.finish()
	l.at(d, "ron/d.jpg")
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
// home, one it put in the Trash goes in, at the next look, Empty's too.
func TestAnOlderProgramsTrash(t *testing.T) {
	l := newTrashing(t)
	a, b := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another")
	l.trash(a)
	l.mark(false, a)
	l.at(a, ".trash/ron/a.jpg")
	if missing, err := Missing(l.db(), l.root); err != nil || len(missing) != 0 {
		t.Errorf("missing %v %v", missing, err)
	}
	l.mark(true, b)
	l.empty([]Key{a, b})
	l.isErased(b)
	l.file("ron/b.jpg", false)
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
		photos, err := c.photos(`WHERE ` + inTheTrash)
		if err == nil {
			var keys []Key
			for _, p := range photos {
				keys = append(keys, p.Key())
			}
			err = settle(root, c, keys, plainly)
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
		db := filepath.Join(root, WorkDir, "catalog.db")
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
			TidyTrash(root, db, keys, anyTurn, plainly)
			EmptyTrash(root, db, keys[:min(n, len(keys))], anyTurn, plainly, nil)
			c.Mark(Trashed, keys, false)
			TidyTrash(root, db, nil, anyTurn, plainly)
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

// Empty reads with nothing held and takes the library's turn for a few photos
// at a time: what others place waits for no whole Trash.
func TestEmptyTakesItsTurnForAFew(t *testing.T) {
	l := newTrashing(t)
	var keys []Key
	for i := range 2*fewErased + 1 {
		keys = append(keys, l.add(fmt.Sprintf("ron/%d.jpg", i), fmt.Sprint("photo ", i)))
	}
	l.trash(keys...)
	turns := 0
	left, gone, err := EmptyTrash(l.root, l.db(), keys, func() (func(), error) {
		if n, err := l.c.Count(Filter{Trashed: true}); err != nil || n != len(keys)-turns*fewErased {
			t.Errorf("turn %d: %d in the Trash %v", turns, n, err)
		}
		turns++
		return func() {}, nil
	}, plainly, nil)
	if err != nil || len(left) != 0 || len(gone) != len(keys) || turns != 3 {
		t.Errorf("left %v, %d gone, %d turns, %v", left, len(gone), turns, err)
	}
	// Others took the library for good after the first: the rest is in the Trash.
	keys = keys[:0]
	for i := range fewErased + 1 {
		keys = append(keys, l.add(fmt.Sprintf("ron/again%d.jpg", i), fmt.Sprint("again ", i)))
	}
	l.trash(keys...)
	turns = 0
	busy := errors.New("busy")
	left, _, err = EmptyTrash(l.root, l.db(), keys, func() (func(), error) {
		if turns++; turns > 1 {
			return nil, busy
		}
		return func() {}, nil
	}, plainly, nil)
	if !errors.Is(err, busy) || len(left) != 1 {
		t.Errorf("left %v %v, want one", left, err)
	}
	if n, err := l.c.Count(Filter{Trashed: true}); err != nil || n != 1 {
		t.Errorf("%d in the Trash %v, want one", n, err)
	}
}

// A library from before profiles, its photo killed on its way to the Trash and
// another library's hard-linked to it: its only file is not cleared away.
func TestAnOldPhotoInTransitKeepsItsFile(t *testing.T) {
	for _, moved := range []bool{false, true} {
		root, c, hashes := relaying(t, map[string]string{"2019/07/a.jpg": "a photo"})
		l := &trashing{t, root, c}
		old := Key{Hash: hashes["2019/07/a.jpg"]}
		os.MkdirAll(under(root, "ann/2019/07"), 0o755)
		if err := os.Link(under(root, "2019/07/a.jpg"), under(root, "ann/2019/07/a.jpg")); err != nil {
			t.Skip(err)
		}
		l.add("ann/2019/07/a.jpg", "a photo")
		l.mark(true, old)
		if err := keepMoves(root, []transit{{old, "2019/07/a.jpg", ".trash/2019/07/a.jpg"}}); err != nil {
			t.Fatal(err)
		}
		if moved {
			os.MkdirAll(under(root, ".trash/2019/07"), 0o755)
			os.Rename(under(root, "2019/07/a.jpg"), under(root, ".trash/2019/07/a.jpg"))
		}
		l.finish()
		if err := Relayout(root, c, nil, ronsAccount, "ron", time.UTC); err != nil {
			t.Fatal(err)
		}
		l.tidy()
		l.at(Key{old.Hash, "ron"}, ".trash/ron/2019/07/a.jpg")
		l.mark(false, Key{old.Hash, "ron"})
		l.tidy()
		l.at(Key{old.Hash, "ron"}, "ron/2019/07/a.jpg")
		l.file("ann/2019/07/a.jpg", true)
	}
}

// The look at the whole library finishes the journal before it picks what to
// move: a photo restored on its way in is home after one look.
func TestALookFinishesTheJournalFirst(t *testing.T) {
	l := newTrashing(t)
	a := l.add("ron/a.jpg", "a photo")
	if err := keepMoves(l.root, []transit{{a, "ron/a.jpg", ".trash/ron/a.jpg"}}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(under(l.root, ".trash/ron"), 0o755)
	os.Rename(under(l.root, "ron/a.jpg"), under(l.root, ".trash/ron/a.jpg"))
	l.tidy()
	l.at(a, "ron/a.jpg")
}

// What a journal says is recorded only when the file is the photo's, and no
// other photo's; an entry of a photo forgotten or filed elsewhere is dropped.
func TestAJournalIsNotBelieved(t *testing.T) {
	l := newTrashing(t)
	a, b, d, e := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third"), l.add("ron/e.jpg", "a fourth")
	l.mark(true, a)
	write(t, l.root, ".trash/ron/a.jpg", []byte("not it."))
	os.Remove(under(l.root, "ron/a.jpg"))
	if err := l.c.TakeOut([]Key{e}, false); err != nil {
		t.Fatal(err)
	}
	err := keepMoves(l.root, []transit{{a, "ron/a.jpg", ".trash/ron/a.jpg"}, {b, "ron/gone.jpg", "ron/d.jpg"}, {e, "ron/e.jpg", ".trash/ron/e.jpg"}, {b, "ron/b.jpg", "ron/d.jpg"}})
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(under(l.root, "ron/b.jpg"))
	l.finish()
	p, _ := l.c.Photo(a)
	if moves, err := movesOf(l.root); err != nil || len(moves) != 1 || moves[0].Key != a || p.Path != "ron/a.jpg" {
		t.Errorf("%+v %v, a at %s; want a's alone, not recorded", moves, err, p.Path)
	}
	l.at(d, "ron/d.jpg")
	if p, _ := l.c.Photo(b); p.Path != "ron/b.jpg" {
		t.Errorf("b recorded at another's %s", p.Path)
	}
	l.file("ron/e.jpg", true)
	l.empty([]Key{a}, a)
	// Its name is taken for the next.
	f := l.add("ron/a.jpg", "a fifth")
	os.Remove(under(l.root, ".trash/ron/a.jpg"))
	l.mark(true, f)
	if err := keepMoves(l.root, []transit{{a, "ann/away/a.jpg", ".trash/ron/a.jpg"}}); err != nil {
		t.Fatal(err)
	}
	l.exec(`UPDATE library_photos SET path = 'ann/away/a.jpg' WHERE hash = ?`, a.Hash)
	l.tidy()
	l.at(f, ".trash/.2/ron/a.jpg")
}

// A journal that does not read is set aside and the turn goes on; the look at
// the whole library finds what it held.
func TestAJournalThatDoesNotRead(t *testing.T) {
	for _, bytes := range []string{"", "{}", `[{"hash":"ab`} {
		l := newTrashing(t)
		a, b, d := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third")
		l.trash(b)
		l.mark(true, a, d)
		l.mark(false, b, d)
		// a on its way in, d too and restored, b on its way out.
		os.MkdirAll(under(l.root, "ron"), 0o755)
		for from, to := range map[string]string{"ron/a.jpg": ".trash/ron/a.jpg", "ron/d.jpg": ".trash/ron/d.jpg", ".trash/ron/b.jpg": "ron/b.jpg"} {
			if err := os.Rename(under(l.root, from), under(l.root, to)); err != nil {
				t.Fatal(err)
			}
		}
		write(t, l.root, WorkDir+"/trash-moves.json", []byte(bytes))
		l.finish()
		l.file(WorkDir+"/trash-moves.json", false)
		l.file(WorkDir+"/trash-moves.json.unread", true)
		l.tidy()
		l.at(a, ".trash/ron/a.jpg")
		l.at(b, "ron/b.jpg")
		l.at(d, "ron/d.jpg")
	}
}

// A file another program edited is the photo all the same, once Homewend has
// put it in the Trash's folder at its own path: the person deleted it. Not
// moved yet, at another photo's path, or where one deleted for good was, it stays.
func TestAnEditedPhotoIsEmptied(t *testing.T) {
	l := newTrashing(t)
	a, b, d := l.add("ron/a.jpg", "a photo"), l.add("ron/b.jpg", "another"), l.add("ron/d.jpg", "a third")
	l.trash(a, d)
	write(t, l.root, ".trash/ron/a.jpg", []byte("a photo, edited by another program"))
	write(t, l.root, "ron/b.jpg", []byte("b, edited before its Delete"))
	l.mark(true, b)
	l.tidy()
	l.at(b, "ron/b.jpg") // another size: not moved
	write(t, l.root, ".trash/ron/d.jpg", []byte("a third, edited"))
	l.add(".trash/ron/d.jpg", "a third, edited") // the same path is another photo's too
	var told []int
	left, _, err := EmptyTrash(l.root, l.db(), []Key{a, b, d}, anyTurn, plainly, func(n int) { told = append(told, n) })
	if err != nil || len(left) != 2 || slices.Contains(left, a) || !slices.Equal(told, []int{3}) {
		t.Errorf("left %v %v, told %v", left, err, told)
	}
	l.isErased(a)
	l.file(".trash/ron/a.jpg", false)
	l.at(b, "ron/b.jpg")
	l.file(".trash/ron/d.jpg", true)
	// Emptied, then an edit put back at its place.
	write(t, l.root, ".trash/ron/a.jpg", []byte("not it"))
	l.empty([]Key{b}, b)
	l.file(".trash/ron/a.jpg", true)
}
