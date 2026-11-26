// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/ruvido/homewend/internal/library"
)

// RenameProfile renames the profile from to to: its folder in the library
// moves, and everything that names it follows at once (library.RenameProfile,
// followRename). Refused while an import runs, or a download for one of its
// accounts: either holds the name it began with.
func RenameProfile(from, to string, jobs *Jobs, im *Importing) error {
	if !library.ValidProfile(to) {
		return ErrBadProfile
	}
	root, err := LibraryHere()
	if err == nil && root == "" {
		err = ErrNoLibrary
	}
	if err != nil {
		return err
	}
	if im.State().Running {
		return ErrLibraryBusy
	}
	accounts, err := Accounts()
	if err != nil {
		return err
	}
	for _, a := range accounts {
		if a.Profile == from && jobs.State(a.ID).Running {
			return ErrLibraryBusy
		}
	}
	unlock, err := lockLibrary(root)
	if err != nil {
		return err
	}
	defer unlock()
	if err := checkRename(from, to); err != nil {
		return err
	}
	// Every path of the catalog is written again: a disk without room for
	// its journal would stop it halfway.
	if info, err := os.Stat(catalogPath(root)); err == nil {
		if free, err := Free(root); err == nil && free < info.Size() {
			return NoSpaceError{Need: info.Size(), Free: free}
		}
	}
	return library.RenameProfile(root, catalogPath(root), library.Renaming{From: from, To: to}, followRename(root))
}

// checkRename is whether the profile from is there to be renamed to, a name
// no other profile or folder of the library has, nor with other capitals.
func checkRename(from, to string) error {
	names, err := profilesThere("")
	if err != nil {
		return err
	}
	if from == library.Unassigned || !slices.Contains(names, from) {
		return fmt.Errorf("no profile %s: %w", from, fs.ErrNotExist)
	}
	names = slices.DeleteFunc(names, func(n string) bool { return n == from })
	if slices.Contains(names, to) {
		return library.ErrProfileTaken
	}
	return notByCase(to, names)
}

// followRename is what names a renamed profile outside the library at root,
// named again: the takeouts begun into it, the accounts it is the profile of,
// the people hidden, those imported into, the phones paired into it or once,
// a pairing code waiting. Done twice, it is as once.
func followRename(root string) func(library.Renaming) error {
	return func(r library.Renaming) error {
		swap := func(p *string) {
			if *p == r.From {
				*p = r.To
			}
		}
		work := filepath.Join(root, library.WorkDir)
		entries, _ := os.ReadDir(work)
		for _, e := range entries {
			st, err := loadState(filepath.Join(work, e.Name()))
			if err != nil || st.Profile != r.From {
				continue
			}
			st.Profile = r.To
			if err := st.save(filepath.Join(work, e.Name())); err != nil {
				return err
			}
		}
		accounts, err := Accounts()
		if err != nil {
			return err
		}
		_, err = UpdateSettings(func(s *Settings) {
			// An account named by its address's name is given the new one.
			for _, a := range accounts {
				if s.ProfileFor(a.ID) == r.From {
					if s.Profiles == nil {
						s.Profiles = map[string]string{}
					}
					s.Profiles[a.ID] = r.To
				}
			}
			for id, p := range s.Profiles {
				if p == r.From {
					s.Profiles[id] = r.To
				}
			}
			for i := range s.Hidden {
				swap(&s.Hidden[i])
			}
			for i := range s.Imported {
				swap(&s.Imported[i])
			}
		})
		if err != nil {
			return err
		}
		_, err = UpdatePhones(func(p *Phones) error {
			for i := range p.Phones {
				swap(&p.Phones[i].Profile)
			}
			for i := range p.Unpaired {
				swap(&p.Unpaired[i].Profile)
			}
			if id, ok := p.Owners[r.From]; ok {
				p.Owners[r.To] = id
				delete(p.Owners, r.From)
			}
			return nil
		})
		if err != nil {
			return err
		}
		pairing.Lock()
		swap(&pairing.profile)
		pairing.Unlock()
		renamedWaiting(r.From)
		return nil
	}
}
