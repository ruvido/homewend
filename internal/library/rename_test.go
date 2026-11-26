// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestRenamedPath(t *testing.T) {
	for rel, want := range map[string]string{
		"ron/2026/a.jpg":                         "ann marie/2026/a.jpg",
		"ron":                                    "ann marie",
		".trash/ron/2026/a.jpg":                  ".trash/ann marie/2026/a.jpg",
		".trash/.2/ron/a.jpg":                    ".trash/.2/ann marie/a.jpg",
		".homewend/metadata/ron/a.jpg.json":      ".homewend/metadata/ann marie/a.jpg.json",
		".homewend/metadata/.trash/.3/ron/a.jpg": ".homewend/metadata/.trash/.3/ann marie/a.jpg",
		"ronald/a.jpg":                           "",
		"ann/ron/a.jpg":                          "",
		".homewend/catalog.db":                   "",
	} {
		got, ok := renamedPath(rel, "ron", "ann marie")
		if want == "" && (ok || got != rel) || want != "" && (!ok || got != want) {
			t.Errorf("%s: %q %v, want %q", rel, got, ok, want)
		}
	}
}

// renameFixture is a library of two profiles, the one to rename with a photo in
// its folder, in an album, with a heart and an edit, one in the Trash, one in
// the Trash under a name taken, the metadata of one, a move of the Trash left
// in its journal.
func renameFixture(t *testing.T) *trashing {
	l := newTrashing(t)
	a := l.add("ron/2026/a.jpg", "a")
	b := l.add("ron/2025/b.jpg", "b")
	d := l.add("ron/2024/d.jpg", "d")
	l.add("ann/2026/c.jpg", "c")
	l.mark(true, b, d)
	l.tidy()
	if err := os.MkdirAll(under(l.root, ".trash/.2/ron/2024"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(under(l.root, ".trash/ron/2024/d.jpg"), under(l.root, ".trash/.2/ron/2024/d.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := l.c.setPath(d, ".trash/.2/ron/2024/d.jpg", "d.jpg"); err != nil {
		t.Fatal(err)
	}
	write(t, l.root, metadataDir+"/ron/2026/a.jpg.json", []byte("{}"))
	if err := os.MkdirAll(under(l.root, "ron/albums/Sea"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(under(l.root, "ron/2026/a.jpg"), under(l.root, "ron/albums/Sea/a.jpg")); err != nil {
		t.Fatal(err)
	}
	if err := l.c.addAlbum("Sea", a, "local/card"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.c.Mark(Favorite, []Key{a}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := l.c.db.Exec(`INSERT INTO edits (profile, hash, value, at, seq) VALUES ('ron', ?, '{}', 0, 0)`, a.Hash); err != nil {
		t.Fatal(err)
	}
	if err := keepMoves(l.root, []transit{{Key: a, From: "ron/2026/x.jpg", To: ".trash/ron/2026/x.jpg"}}); err != nil {
		t.Fatal(err)
	}
	return l
}

// everyFile fails for a photo whose file is not found where readers look.
func (l *trashing) everyFile() {
	l.Helper()
	photos, err := l.c.photos(``)
	if err != nil {
		l.Fatal(err)
	}
	if len(photos) != 4 {
		l.Fatalf("%d photos, want 4", len(photos))
	}
	for _, p := range photos {
		if !exists(under(l.root, At(l.root, p.Path))) {
			l.Errorf("%s of %s not found", p.Path, p.Profile)
		}
	}
}

// listsExactly is whether the folder above rel lists it, by its exact name.
func listsExactly(root, rel string) bool {
	entries, _ := os.ReadDir(filepath.Dir(under(root, rel)))
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == path.Base(rel) })
}

// renamed fails unless ron is to in every way, and nothing is left of ron.
func (l *trashing) renamed(to string) {
	l.Helper()
	l.everyFile()
	photos, _ := l.c.photos(``)
	for _, p := range photos {
		if p.Profile == "ron" || strings.Contains(p.Path, "ron/") || !exists(under(l.root, p.Path)) {
			l.Errorf("%+v", p)
		}
		if p.Profile == to && !strings.Contains(p.Path, to+"/") {
			l.Errorf("%s of %s", p.Path, to)
		}
	}
	// By the names the folders list: on a disk that ignores capitals, ron is
	// found where Ron is, and a rename to Ron not done would not show.
	for _, dir := range []string{"", ".trash/", ".trash/.2/", metadataDir + "/"} {
		if listsExactly(l.root, dir+"ron") || !listsExactly(l.root, dir+to) {
			l.Errorf("%sron is left, or %s%s not there", dir, dir, to)
		}
	}
	if exists(renameJournal(l.root)) {
		l.Error("the journal is left")
	}
	for _, file := range []string{to + "/albums/Sea/a.jpg", metadataDir + "/" + to + "/2026/a.jpg.json"} {
		if !exists(under(l.root, file)) {
			l.Errorf("%s is not there", file)
		}
	}
	var n int
	for _, q := range []string{
		`SELECT COUNT(*) FROM marks WHERE profile = ?1 AND value = 1 AND seq > (SELECT last FROM mark_seqs WHERE profile = 'ron')`,
		`SELECT COUNT(*) FROM edits WHERE profile = ?1`,
		`SELECT COUNT(*) FROM album_owners WHERE profile = ?1`,
		`SELECT 1 + COUNT(*) FROM marks WHERE profile = 'ron'`,
	} {
		if err := l.c.db.QueryRow(q, to).Scan(&n); err != nil || n != 1 {
			l.Errorf("%s: %d %v", q, n, err)
		}
	}
	if moves, err := movesOf(l.root); err != nil || len(moves) != 1 || moves[0].From != to+"/2026/x.jpg" || moves[0].To != ".trash/"+to+"/2026/x.jpg" || moves[0].Profile != to {
		l.Errorf("the Trash's journal: %+v %v", moves, err)
	}
}

func TestAProfileIsRenamed(t *testing.T) {
	for _, to := range []string{"Ron", "papà rossi"} {
		l := renameFixture(t)
		var followed int
		err := RenameProfile(l.root, l.db(), Renaming{From: "ron", To: to}, func(r Renaming) error {
			followed++
			if r.From != "ron" || r.To != to {
				t.Errorf("followed %+v", r)
			}
			return nil
		})
		if err != nil || followed != 1 {
			t.Fatal(err, followed)
		}
		l.renamed(to)
	}
}

// Killed after any step, the library shows every photo, and the next turn
// finishes the rename.
func TestARenameKilledIsFinished(t *testing.T) {
	r := Renaming{From: "ron", To: "papà"}
	steps := []func(l *trashing){
		func(l *trashing) { // the first folder moved
			f := renamedFolders(l.root, r)[0]
			if err := moveFolder(l.root, f[0], f[1]); err != nil {
				t.Fatal(err)
			}
		},
		func(l *trashing) { // every folder
			for _, f := range renamedFolders(l.root, r) {
				if err := moveFolder(l.root, f[0], f[1]); err != nil {
					t.Fatal(err)
				}
			}
		},
		func(l *trashing) { // the folders said moved
			moved := r
			moved.Moved = true
			if err := writeSynced(renameJournal(l.root), moved); err != nil {
				t.Fatal(err)
			}
		},
		func(l *trashing) { // the catalog
			if err := l.c.renameProfile(l.root, r); err != nil {
				t.Fatal(err)
			}
		},
		func(l *trashing) { // the rest, all but the journal's end
			moves, _ := movesOf(l.root)
			moves[0].From, _ = renamedPath(moves[0].From, r.From, r.To)
			if err := keepMoves(l.root, moves); err != nil {
				t.Fatal(err)
			}
		},
	}
	for at := 0; at <= len(steps); at++ {
		l := renameFixture(t)
		if err := writeSynced(renameJournal(l.root), r); err != nil {
			t.Fatal(err)
		}
		for _, step := range steps[:at] {
			step(l)
		}
		l.everyFile()
		var followed int
		if err := FinishRename(l.root, l.db(), func(Renaming) error { followed++; return nil }); err != nil || followed != 1 {
			t.Fatalf("after %d steps: %v, followed %d", at, err, followed)
		}
		l.renamed(r.To)
		// Nothing left to finish.
		if err := FinishRename(l.root, l.db(), func(Renaming) error { followed++; return nil }); err != nil || followed != 1 {
			t.Errorf("again: %v %d", err, followed)
		}
	}
}

// A name the library has, by a folder of it or a photo, is refused, and
// nothing changes.
func TestARenameToANameTakenIsRefused(t *testing.T) {
	for _, taken := range []string{"ann", ".trash/zoe", metadataDir + "/zoe", "catalog:zoe"} {
		l := renameFixture(t)
		to := filepath.Base(taken)
		if name, ok := strings.CutPrefix(taken, "catalog:"); ok {
			to = name
			l.add(name+"/z.jpg", "z")
		} else if err := os.MkdirAll(under(l.root, taken+"/x"), 0o755); err != nil {
			t.Fatal(err)
		}
		err := RenameProfile(l.root, l.db(), Renaming{From: "ron", To: to}, func(Renaming) error { t.Error("followed"); return nil })
		if !errors.Is(err, ErrProfileTaken) {
			t.Errorf("%s: %v", taken, err)
		}
		if !exists(under(l.root, "ron/2026/a.jpg")) || exists(renameJournal(l.root)) {
			t.Errorf("%s: changed", taken)
		}
	}
}

// A folder that will not move (on Windows, a file in it open) leaves the
// library as it was: those moved are moved back, the catalog is not touched.
func TestARenameThatCannotMoveChangesNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a folder that refuses a rename is made by its rights")
	}
	l := renameFixture(t)
	if err := os.Chmod(under(l.root, TrashDir), 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(under(l.root, TrashDir), 0o755)
	err := RenameProfile(l.root, l.db(), Renaming{From: "ron", To: "papà"}, func(Renaming) error { t.Error("followed"); return nil })
	if !errors.Is(err, ErrNotMoved) {
		t.Fatalf("renamed: %v", err)
	}
	if exists(under(l.root, "papà")) || exists(renameJournal(l.root)) {
		t.Error("half renamed")
	}
	photos, _ := l.c.photos(``)
	for _, p := range photos {
		if !exists(under(l.root, p.Path)) || p.Profile == "papà" {
			t.Errorf("%+v", p)
		}
	}
}

// Killed after the catalog, the old name filed into again (an older Homewend):
// finishing merges the old folder into the new one, a name taken in both and
// a photo the new name has too staying under the old, every file still found.
func TestARenameFinishedOverPhotosFiledUnderTheOldName(t *testing.T) {
	l := renameFixture(t)
	r := Renaming{"ron", "papà", true}
	if err := writeSynced(renameJournal(l.root), r); err != nil {
		t.Fatal(err)
	}
	for _, f := range renamedFolders(l.root, r) {
		if err := moveFolder(l.root, f[0], f[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.c.renameProfile(l.root, r); err != nil {
		t.Fatal(err)
	}
	fresh := l.add("ron/2027/new.jpg", "new")
	taken := l.add("ron/2026/a.jpg", "not a")
	again := l.add("ron/2023/a-again.jpg", "a") // papà has it
	if _, err := l.c.Mark(Favorite, []Key{again}, true); err != nil {
		t.Fatal(err)
	}
	if err := FinishRename(l.root, l.db(), func(Renaming) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if exists(renameJournal(l.root)) {
		t.Error("the journal is left")
	}
	want := map[Key]string{
		{fresh.Hash, "papà"}: "papà/2027/new.jpg",
		{taken.Hash, "papà"}: "ron/2026/a.jpg",
		again:                "papà/2023/a-again.jpg",
	}
	for k, path := range want {
		p, err := l.c.Photo(k)
		if err != nil || p.Path != path || !exists(under(l.root, p.Path)) {
			t.Errorf("%+v: %+v %v, want at %s", k, p, err, path)
		}
	}
	if p, _ := l.c.Photo(again); !p.Favorite {
		t.Error("its heart is gone")
	}
	photos, _ := l.c.photos(``)
	for _, p := range photos {
		if !exists(under(l.root, At(l.root, p.Path))) {
			t.Errorf("%s of %s not found", p.Path, p.Profile)
		}
	}
}

// A journal that does not read, or whose new name is another profile with
// nothing moved yet, is set aside, and the turn goes on.
func TestABadRenameJournalIsSetAside(t *testing.T) {
	for _, journal := range []string{`{"from": "ron"`, `{"from": "ron", "to": "ann"}`, `{"from": "ron", "to": "../x"}`, `{"from": "ron", "to": "ron"}`} {
		l := renameFixture(t)
		if err := os.WriteFile(renameJournal(l.root), []byte(journal), 0o644); err != nil {
			t.Fatal(err)
		}
		err := FinishRename(l.root, l.db(), func(Renaming) error { t.Error("followed"); return nil })
		if err != nil || exists(renameJournal(l.root)) || !exists(renameJournal(l.root)+".unread") {
			t.Errorf("%s: %v", journal, err)
		}
		if !exists(under(l.root, "ron/2026/a.jpg")) || !exists(under(l.root, "ann/2026/c.jpg")) {
			t.Errorf("%s: changed", journal)
		}
	}
}
