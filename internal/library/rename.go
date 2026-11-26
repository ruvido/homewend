// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// A profile's name is its folder: renamed, the folder moves, and with it its
// part of the Trash and what is kept of its files in the work folder; the
// catalog, and what the caller keeps outside the library, follow.
//
// It is crash-safe by a journal, as the Trash's moves are: the rename is
// written in the library before anything moves. Until the folders have all
// moved it can still be undone, and is, when one of them cannot be (a file
// held open on Windows): nothing changed. From then on it only goes forward,
// and whoever takes the library's turn and finds the journal finishes it, each
// step done again being as once. Meanwhile readers find a file at either name
// (At), so the library shows every photo whatever the moment it was stopped.

// Renaming is a profile's rename, as its journal has it.
type Renaming struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Moved is written once every folder has moved, before the catalog:
	// a journal not moved whose new name has photos is another's name.
	Moved bool `json:"moved,omitempty"`
}

// ErrProfileTaken is a rename to a name that a profile of the library, or a
// folder of it, already has.
var ErrProfileTaken = errors.New("there is a profile of this name already")

// ErrNotMoved is a folder of the profile that would not move, most often for
// a file open in it on Windows: nothing was renamed.
var ErrNotMoved = errors.New("a folder of the profile would not move")

// metadataDir is where what is known of a file and not in it is kept, under the
// file's path in the library.
const metadataDir = WorkDir + "/metadata"

// ValidProfile is whether p can name a profile: a folder of the library,
// so a plain name, never one that leads out of it or into its work folder.
func ValidProfile(p string) bool {
	return p != "" && p != "." && p != ".." && !strings.ContainsAny(p, "/\\") && !strings.HasPrefix(p, ".") && p == strings.TrimSpace(p)
}

func renameJournal(root string) string { return filepath.Join(root, WorkDir, "rename.json") }

// renamedPath is rel with profile from's folder named to: in the library, in
// its Trash, and in the metadata kept of either; ok is whether it is under one.
func renamedPath(rel, from, to string) (string, bool) {
	head, rest := "", rel
	if r, ok := strings.CutPrefix(rest, metadataDir+"/"); ok {
		head, rest = metadataDir+"/", r
	}
	if r, ok := strings.CutPrefix(rest, TrashDir+"/"); ok {
		head, rest = head+TrashDir+"/", r
		// A name taken in the Trash: .trash/.2/<path>. No profile starts with a dot.
		if first, r, ok := strings.Cut(rest, "/"); ok && strings.HasPrefix(first, ".") {
			head, rest = head+first+"/", r
		}
	}
	if rest == from {
		return head + to, true
	}
	if r, ok := strings.CutPrefix(rest, from+"/"); ok {
		return head + to + "/" + r, true
	}
	return rel, false
}

// renamedFolders are the folders a rename moves, each with where it goes.
func renamedFolders(root string, r Renaming) [][2]string {
	var all [][2]string
	for _, head := range []string{"", metadataDir + "/"} {
		in := []string{head + r.From, head + TrashDir + "/" + r.From}
		entries, _ := os.ReadDir(under(root, head+TrashDir)) // none: no Trash
		for _, e := range entries {
			if e.IsDir() && strings.HasPrefix(e.Name(), ".") {
				in = append(in, head+TrashDir+"/"+e.Name()+"/"+r.From)
			}
		}
		for _, from := range in {
			to, _ := renamedPath(from, r.From, r.To)
			all = append(all, [2]string{from, to})
		}
	}
	return all
}

// moveFolder moves the folder from to to, both under root, unless it has
// moved already. One there under both names is the same folder on a disk that
// ignores capitals, renamed again for them. Both there, one was filed into
// after a kill (an older Homewend, another process): what is in from goes into
// to, a name taken in both staying where it is.
func moveFolder(root, from, to string) error {
	from, to = under(root, from), under(root, to)
	if !exists(from) {
		return nil
	}
	if exists(to) && !linked(from, to) {
		return mergeInto(from, to)
	}
	return os.Rename(from, to)
}

// mergeInto moves what is in the folder from into to, each entry by one
// rename, a folder there under both names merged in turn; from goes once empty.
func mergeInto(from, to string) error {
	entries, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	for _, e := range entries {
		f, t := filepath.Join(from, e.Name()), filepath.Join(to, e.Name())
		info, err := os.Lstat(t)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			err = os.Rename(f, t)
		case err == nil && e.IsDir() && info.IsDir():
			err = mergeInto(f, t)
		}
		if err != nil {
			return err
		}
	}
	os.Remove(from) // not empty: what was taken in both stays
	return nil
}

func isEmptyDir(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.ReadDir(1)
	return errors.Is(err, io.EOF)
}

// RenameProfile renames profile from to to, in the library at root whose
// catalog is at catalog, in the library's turn. follow changes what refers to
// the profile outside the library; it is run again after a kill, so doing it
// twice must be as once. A name the library has already, its folder or a
// photo of it, is ErrProfileTaken; a folder that would not move is said, and
// nothing changed.
func RenameProfile(root, catalog string, r Renaming, follow func(Renaming) error) error {
	if r.From == r.To {
		return nil
	}
	if exists(catalog) {
		err := with(catalog, func(c *Catalog) error {
			var taken bool
			err := c.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM library_photos WHERE profile = ?)`, r.To).Scan(&taken)
			if err == nil && taken {
				err = ErrProfileTaken
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	folders := renamedFolders(root, r)
	for _, f := range folders {
		if exists(under(root, f[0])) && exists(under(root, f[1])) && !linked(under(root, f[0]), under(root, f[1])) {
			return ErrProfileTaken
		}
	}
	if err := writeSynced(renameJournal(root), r); err != nil {
		return err
	}
	for i, f := range folders {
		err := moveFolder(root, f[0], f[1])
		if err == nil {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			if back := moveFolder(root, folders[j][1], folders[j][0]); back != nil {
				// Half moved and stuck: forward, at the next turn.
				return errors.Join(err, back)
			}
		}
		return errors.Join(ErrNotMoved, err, os.Remove(renameJournal(root)))
	}
	return finishRename(root, catalog, r, follow)
}

// FinishRename finishes the rename a killed run left, if any, in the
// library's turn: see RenameProfile.
func FinishRename(root, catalog string, follow func(Renaming) error) error {
	r, err := renaming(root)
	if err == nil && r != nil && !r.Moved && exists(catalog) {
		// Nothing of it moved, and the new name is a profile already: not
		// ours to merge.
		err = with(catalog, func(c *Catalog) error {
			var taken bool
			err := c.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM library_photos WHERE profile = ?)`, r.To).Scan(&taken)
			if err == nil && taken {
				err = ErrProfileTaken
			}
			return err
		})
	}
	if err != nil {
		// Set aside as the Trash's journal is, the turn going on: the library
		// stays as the catalog has it.
		log.Printf("the rename's journal, set aside: %v", err)
		return os.Rename(renameJournal(root), renameJournal(root)+".unread")
	}
	if r == nil {
		return nil
	}
	log.Printf("renaming %s to %s, begun before", r.From, r.To)
	return finishRename(root, catalog, *r, follow)
}

// renaming is the rename under way in the library at root, nil when none is.
func renaming(root string) (*Renaming, error) {
	data, err := os.ReadFile(renameJournal(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var r Renaming
	if err == nil {
		err = json.Unmarshal(data, &r)
	}
	if err == nil && (!ValidProfile(r.From) || !ValidProfile(r.To) || r.From == r.To) {
		err = errors.New("the rename's journal names no profile")
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// finishRename does every step of r from the folders on, each as many times
// as asked: the folders, the catalog, the Trash's journal, follow; then the
// journal goes. One that fails leaves it, for the next turn.
func finishRename(root, catalog string, r Renaming, follow func(Renaming) error) error {
	for _, f := range renamedFolders(root, r) {
		if err := moveFolder(root, f[0], f[1]); err != nil {
			return err
		}
	}
	if !r.Moved {
		r.Moved = true
		if err := writeSynced(renameJournal(root), r); err != nil {
			return err
		}
	}
	if exists(catalog) {
		if err := with(catalog, func(c *Catalog) error { return c.renameProfile(root, r) }); err != nil {
			return err
		}
	}
	moves, err := movesOf(root)
	if err != nil {
		return err
	}
	if len(moves) > 0 {
		for i, m := range moves {
			moves[i].From, _ = renamedPath(m.From, r.From, r.To)
			moves[i].To, _ = renamedPath(m.To, r.From, r.To)
			if m.Profile == r.From {
				moves[i].Profile = r.To
			}
		}
		if err := keepMoves(root, moves); err != nil {
			return err
		}
	}
	if err := follow(r); err != nil {
		return err
	}
	return os.Remove(renameJournal(root))
}

// renameProfile gives the profile's photos, their paths, marks, edits and
// albums the new name, in one transaction; again, it changes nothing. A photo
// the new name has too (filed under the old one after a kill) keeps the old,
// with its marks; a path whose file stayed under the old folder, its name
// taken in the new one (moveFolder), keeps it. The files are under root.
func (c *Catalog) renameProfile(root string, r Renaming) error {
	marked, err := hasTable(c.db, "mark_seqs") // none before marks
	if err != nil {
		return err
	}
	edited, err := hasTable(c.db, "edits")
	if err != nil {
		return err
	}
	return c.inTx(func(tx *sql.Tx) error {
		var steps []string
		if marked {
			// The marks moved by the triggers take seqs after any either had:
			// a seq is never given twice.
			steps = append(steps, `INSERT INTO mark_seqs SELECT ?2, last FROM mark_seqs WHERE profile = ?1
				ON CONFLICT DO UPDATE SET last = max(last, excluded.last)`)
		}
		steps = append(steps, `UPDATE OR IGNORE library_photos SET profile = ?2 WHERE profile = ?1`)
		// What the triggers did not carry, a row with no photo, follows unless
		// the new name has one of its own; a photo left under the old name keeps its.
		const orphan = ` WHERE profile = ?1 AND hash NOT IN (SELECT hash FROM library_photos WHERE profile = ?1)`
		for _, table := range []string{"marks", "edits"} {
			if table == "marks" && !marked || table == "edits" && !edited {
				continue
			}
			steps = append(steps, `UPDATE OR IGNORE `+table+` SET profile = ?2`+orphan, `DELETE FROM `+table+orphan)
		}
		steps = append(steps, `INSERT OR IGNORE INTO album_owners SELECT account, album, ?2 FROM album_owners WHERE profile = ?1`,
			`DELETE FROM album_owners WHERE profile = ?1 AND NOT EXISTS (SELECT 1 FROM library_photos WHERE profile = ?1)`)
		for _, step := range steps {
			if _, err := tx.Exec(step, r.From, r.To); err != nil {
				return err
			}
		}
		// The paths, of whichever profile: a file is where its folder is; one
		// that stayed under the old name (moveFolder) keeps its path. One
		// statement for each folder: a row at a time took seconds at 100,000.
		heads, err := texts(tx.Query(`SELECT DISTINCT substr(path, 1, 8 + instr(substr(path, 9), '/'))
			FROM library_photos WHERE substr(path, 1, 8) = ?`, TrashDir+"/."))
		if err != nil {
			return err
		}
		kept, err := json.Marshal(stayed(root, r))
		if err != nil {
			return err
		}
		for _, head := range append([]string{"", TrashDir + "/"}, heads...) {
			_, err := tx.Exec(`UPDATE library_photos SET path = ?2 || substr(path, length(?1) + 1)
				WHERE substr(path, 1, length(?1)) = ?1 AND path NOT IN (SELECT value FROM json_each(?3))`,
				head+r.From+"/", head+r.To+"/", string(kept))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// stayed are the files left under r's old folders, each a name the new one has
// too (moveFolder), as the catalog has their paths.
func stayed(root string, r Renaming) []string {
	files := []string{}
	for _, f := range renamedFolders(root, r) {
		from, to := under(root, f[0]), under(root, f[1])
		if !exists(from) || linked(from, to) {
			continue
		}
		filepath.WalkDir(from, func(path string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				files = append(files, relativeTo(root, path))
			}
			return nil
		})
	}
	return files
}

// writeSynced writes v as JSON to file whole, on the disk before it returns:
// a journal, read after a kill.
func writeSynced(file string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.Create(file + ".tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		return err
	}
	return os.Rename(file+".tmp", file)
}
