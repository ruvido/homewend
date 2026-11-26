// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// Deleted from a phone, emptied on the computer: its file and its pictures are
// gone, the phone is told it is out and cannot bring it in or restore it, and
// the same photo of another library keeps its own.
func TestThePhonesPhotoIsEmptied(t *testing.T) {
	for _, dir := range []string{"XDG_CACHE_HOME", "LocalAppData"} {
		t.Setenv(dir, t.TempDir())
	}
	root, ron, ann, hash, rons := twoPhones(t)
	anns, err := ReceivePhoto(context.Background(), ann, hash, "IMG_1.JPG", 14, unread{})
	if err != nil {
		t.Fatal(err)
	}
	mine, hers := library.Key{Hash: hash, Profile: "ron"}, library.Key{Hash: hash, Profile: "ann"}
	pictures := func() (files []string) {
		for _, dir := range []string{"thumbs", "previews"} {
			folder, err := picturesDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, thumb.Path(folder, hash), thumb.Path(folder, hash+"-e-1"))
		}
		return files
	}
	for _, f := range pictures() {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	there := func(want bool, files ...string) {
		t.Helper()
		for _, f := range files {
			if _, err := os.Stat(f); (err == nil) != want {
				t.Errorf("%s: %v, want it there: %v", f, err, want)
			}
		}
	}
	if done, err := MarkOf(ron, library.Trashed, []string{hash}, true); err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	there(true, filepath.Join(root, ".trash", rons.Path), filepath.Join(root, anns.Path))
	there(false, filepath.Join(root, rons.Path))
	// Hers is not in the Trash: asked for too, it is not touched.
	if left, err := EmptyTrash([]library.Key{mine, hers}); err != nil || len(left) != 0 {
		t.Fatal(left, err)
	}
	there(false, filepath.Join(root, ".trash", rons.Path))
	there(true, append(pictures(), filepath.Join(root, anns.Path))...)
	haveIs(t, ron, hash, "out")
	haveIs(t, ann, hash, "have")
	if back, err := ReceivePhoto(context.Background(), ron, hash, "IMG_1.JPG", 14, unread{}); err != nil || !back.Out {
		t.Errorf("sent again: %+v %v", back, err)
	}
	if done, err := MarkOf(ron, library.Trashed, []string{hash}, false); err != nil || len(done) != 0 {
		t.Errorf("restored %v %v", done, err)
	}
	if l, err := LibraryOf(ron); err != nil || l.Photos != 0 || l.Bytes[""] != 14 {
		t.Errorf("%+v %v", l, err)
	}
	// Marks of it are unknown, as those of one kept out.
	shelf := shelfOf(t, ron)
	a, err := Marks(ron, library.MarkQuestion{Shelf: shelf.ID, Install: "i", Marks: []library.MarkAction{{N: 1, Hash: hash, State: library.Favorite, Value: []byte("true")}}})
	if err != nil || len(a.Unknown) != 1 {
		t.Errorf("%+v %v", a, err)
	}
	if _, err := Mark(library.Trashed, []library.Key{hers}, true); err != nil {
		t.Fatal(err)
	}
	if left, err := EmptyTrash([]library.Key{hers}); err != nil || len(left) != 0 {
		t.Fatal(left, err)
	}
	there(false, append(pictures(), filepath.Join(root, anns.Path), filepath.Join(root, ".trash"))...)
	if list, err := Photos(Settings{}, "", "", "", false, ViewTrash, 0); err != nil || len(list.Photos) != 0 {
		t.Errorf("%+v %v", list, err)
	}
	if o, err := overview(root, nil, nil, ""); err != nil || o.Trashed != 0 || o.Total != 0 || len(o.Missing) != 0 {
		t.Errorf("%+v %v", o, err)
	}
}

// While others place photos, a photo is put in the Trash and restored all the
// same, its file follows later; Empty says it cannot.
func TestTheTrashWhileTheLibraryIsBusy(t *testing.T) {
	root, _, _, hash, rons := twoPhones(t)
	k := []library.Key{{Hash: hash, Profile: "ron"}}
	unlock, err := lockLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if done, err := Mark(library.Trashed, k, true); err != nil || len(done) != 1 {
		t.Fatal(done, err)
	}
	if _, err := os.Stat(filepath.Join(root, rons.Path)); err != nil {
		t.Errorf("moved under another's turn: %v", err)
	}
	if _, err := EmptyTrash(k); !errors.Is(err, ErrLibraryBusy) {
		t.Errorf("%v, want busy", err)
	}
	unlock()
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if o, err := LibraryOverview(s); err != nil || o.Trashed != 1 {
		t.Errorf("%+v %v", o, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".trash", rons.Path)); err != nil {
		t.Errorf("not moved in its turn: %v", err)
	}
	if err := os.Rename(root, root+"-away"); err != nil {
		t.Skip(err)
	}
	if _, err := EmptyTrash(k); !errors.As(err, new(LibraryGoneError)) {
		t.Errorf("%v, want it away", err)
	}
}

// Emptied in another process while this one restores: a photo said to be
// restored has its file, and one that has none was said not to be.
func TestEmptiedInAnotherProcessWhileRestored(t *testing.T) {
	if keys := os.Getenv("HOMEWEND_TEST_EMPTY"); keys != "" {
		var all []library.Key
		for _, h := range strings.Fields(keys) {
			all = append(all, library.Key{Hash: h, Profile: "ron"})
		}
		for {
			_, err := EmptyTrash(all)
			if err == nil {
				return
			}
			if !errors.Is(err, ErrLibraryBusy) {
				t.Fatal(err)
			}
		}
	}
	dir := config(t)
	root := t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	ron := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}
	var keys []library.Key
	var hashes []string
	for i := range 60 {
		_, hash := send(t, ron, fmt.Sprint(i, ".JPG"), fmt.Sprint("photo ", i))
		keys, hashes = append(keys, library.Key{Hash: hash, Profile: "ron"}), append(hashes, hash)
	}
	if done, err := Mark(library.Trashed, keys, true); err != nil || len(done) != len(keys) {
		t.Fatal(len(done), err)
	}
	other := exec.Command(os.Args[0], "-test.run=^TestEmptiedInAnotherProcessWhileRestored$")
	other.Env = append(os.Environ(), "HOMEWEND_TEST_EMPTY="+strings.Join(hashes, " "), "XDG_CONFIG_HOME="+filepath.Dir(dir))
	var said bytes.Buffer
	other.Stdout, other.Stderr = &said, &said
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	restored := map[library.Key]bool{}
	for i := len(keys) - 1; i >= 0; i-- {
		done, err := Mark(library.Trashed, keys[i:i+1], false)
		if err != nil {
			t.Fatal(err)
		}
		restored[keys[i]] = len(done) == 1
	}
	if err := other.Wait(); err != nil {
		t.Fatalf("%v\n%s", err, said.String())
	}
	if _, err := library.TidyTrash(root, catalogPath(root), nil, trashTurn(root), unlisted); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		path, err := PathOf(k)
		if err != nil {
			t.Fatal(err)
		}
		_, err = os.Stat(path)
		if restored[k] != (err == nil) || restored[k] && strings.Contains(path, library.TrashDir) {
			t.Errorf("restored %v, at %s: %v", restored[k], path, err)
		}
	}
}

// A photo put in the Trash by a version that had no folder for it goes into it
// as the window opens.
func TestAnEarlierTrashGoesIntoTheFolder(t *testing.T) {
	root, ron, _, hash, rons := twoPhones(t)
	c, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		t.Fatal(err)
	}
	c.Mark(library.Trashed, []library.Key{{Hash: hash, Profile: "ron"}}, true) // the catalog's alone
	c.Close()
	s, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if o, err := LibraryOverview(s); err != nil || o.Trashed != 1 {
			t.Errorf("%+v %v", o, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".trash", rons.Path)); err != nil {
		t.Error(err)
	}
	haveIs(t, ron, hash, "out")
	if list, err := lookForMissing(root); err != nil || len(list) != 0 {
		t.Errorf("%v %v", list, err)
	}
}

// A Delete, or a Restore, has the window read its lists once: the file follows
// with a write that shows in none. Another's write meanwhile does.
func TestAFileThatFollowsIsNoChange(t *testing.T) {
	root, ron, _, hash, rons := twoPhones(t)
	was := beside
	var later []func()
	beside = func(do func()) { later = append(later, do) }
	defer func() { beside = was }()
	k := []library.Key{{Hash: hash, Profile: "ron"}}
	for _, on := range []bool{true, false} {
		later = nil
		tag, _, err := PhoneList(context.Background(), ron)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Mark(library.Trashed, k, on); err != nil {
			t.Fatal(err)
		}
		marked, err := LibraryChanges()
		if err != nil {
			t.Fatal(err)
		}
		raw := library.Changes(catalogPath(root))
		for _, do := range later {
			do()
		}
		if _, err := os.Stat(filepath.Join(root, ".trash", rons.Path)); (err == nil) != on || library.Changes(catalogPath(root)) == raw {
			t.Errorf("on %v: not moved and recorded: %v", on, err)
		}
		if now, err := LibraryChanges(); err != nil || now != marked {
			t.Errorf("on %v: the lists are read again: %s, %s %v", on, marked, now, err)
		}
		if after, _, err := PhoneList(context.Background(), ron); err != nil || after == tag {
			t.Errorf("the phone's list is not another: %v", err)
		}
		if _, err := Mark(library.Favorite, k, on); err != nil {
			t.Fatal(err)
		}
		if now, err := LibraryChanges(); err != nil || now == marked {
			t.Errorf("a heart is no change: %v", err)
		}
	}
}

// Between a file's move and its record its picture is found, and a phone has it.
func TestAPhotoInTransit(t *testing.T) {
	root, ron, _, hash, rons := twoPhones(t)
	k := library.Key{Hash: hash, Profile: "ron"}
	work := filepath.Join(root, library.WorkDir, "trash-moves.json")
	if err := os.WriteFile(work, []byte(`[{"hash":"`+hash+`","profile":"ron","From":"`+rons.Path+`","To":".trash/`+rons.Path+`"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(root, ".trash", rons.Path)
	os.MkdirAll(filepath.Dir(to), 0o755)
	if err := os.Rename(filepath.Join(root, rons.Path), to); err != nil {
		t.Fatal(err)
	}
	haveIs(t, ron, hash, "have")
	if at, err := PathOf(k); err != nil || at != to {
		t.Errorf("%s %v", at, err)
	}
	if at, _, err := pictureOf(k, os.Stat); err != nil || at != to {
		t.Errorf("%s %v", at, err)
	}
	// The next to take the turn finishes it: the photo, live, is home.
	unlock, err := lockLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	s, _ := LoadSettings()
	if _, err := LibraryOverview(s); err != nil {
		t.Fatal(err)
	}
	if at, err := PathOf(k); err != nil || at != filepath.Join(root, rons.Path) {
		t.Errorf("%s %v", at, err)
	}
	if _, err := os.Stat(filepath.Join(root, rons.Path)); err != nil {
		t.Error(err)
	}
}

// While an Empty runs, how far it is is told; none runs, nothing is.
func TestHowFarAnEmptyIs(t *testing.T) {
	root, _, _, hash, _ := twoPhones(t)
	k := []library.Key{{Hash: hash, Profile: "ron"}, {Hash: "none", Profile: "ron"}}
	if _, err := Mark(library.Trashed, k[:1], true); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() { _, err := EmptyTrash(k); done <- err }()
	time.Sleep(300 * time.Millisecond) // waiting for its turn
	if e := EmptyingNow(); e != (Emptying{0, 2}) {
		t.Errorf("%+v", e)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if e := EmptyingNow(); e != (Emptying{}) {
		t.Errorf("%+v after", e)
	}
}
