// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"sync"

	"github.com/ruvido/homewend/internal/library"
)

// Overview is what the window's sidebar shows: the accounts, and the years
// and albums of the library folder chosen once (DefaultLibrary), of the
// profiles shown.
type Overview struct {
	Accounts []AccountInfo `json:"accounts"`
	Total    int           `json:"total"`
	Years    []YearCount   `json:"years"`
	Albums   []AlbumCount  `json:"albums"`
	Takeouts int           `json:"takeouts"`
	// Archived, Trashed and Favorites count the photos of the profiles shown
	// in the Archive, in the Trash and with a heart.
	Archived  int `json:"archived"`
	Trashed   int `json:"trashed"`
	Favorites int `json:"favorites"`
	// Profiles are those there are, for an import to choose among; Profile,
	// the one it goes to when none is named, "" when it is to be asked.
	Profiles []string `json:"profiles"`
	Profile  string   `json:"profile"`
	// Missing counts by year ("" for no date) the photos whose file is
	// gone from the library, deleted or moved by hand.
	Missing map[string]int `json:"missing"`
	// Gone is the library folder when it is away (libraryAway): its disk
	// unplugged, its share not mounted. Its catalog is in it, so nothing is
	// counted.
	Gone string `json:"gone,omitempty"`
}

// YearCount is a year, newest first, or "" for photos with no date.
type YearCount struct {
	Year  string `json:"year"`
	Count int    `json:"count"`
}

// AlbumCount is an album of one account.
type AlbumCount struct {
	Account string `json:"account"`
	Name    string `json:"name"`
	Count   int    `json:"count"`
}

// catalogPath is where an account's library keeps its catalog.
func catalogPath(dir string) string {
	return filepath.Join(dir, library.WorkDir, "catalog.db")
}

// LibraryChanges moves each time the chosen library's catalog changes,
// whoever changed it (library.Changes), or a file of it is found gone or back.
func LibraryChanges() (string, error) {
	root, err := DefaultLibrary()
	if err != nil || root == "" {
		return "", err
	}
	missing.Lock()
	defer missing.Unlock()
	return fmt.Sprint(changesShown(library.Changes(catalogPath(root))), missing.moves), nil
}

// unseen is the last write to a catalog that changed no list: what the
// catalog's files said after it, and before.
var unseen struct {
	sync.Mutex
	after, before string
}

// changesShown is library.Changes, the same before and after such a write.
func changesShown(changes string) string {
	unseen.Lock()
	defer unseen.Unlock()
	if changes == unseen.after {
		return unseen.before
	}
	return changes
}

func sameChanges(before, after library.Written) {
	shown := changesShown(before.Changes)
	unseen.Lock()
	defer unseen.Unlock()
	unseen.after, unseen.before = after.Changes, shown
}

// LibraryOverview reads the catalog for the profiles shown. An account with no
// library yet counts nothing; that is not an error.
func LibraryOverview(s Settings) (Overview, error) {
	root, err := DefaultLibrary()
	if err != nil {
		return Overview{}, err
	}
	if err := settle(root); err != nil {
		return Overview{}, err
	}
	if err := dateByName(root); err != nil {
		return Overview{}, err
	}
	accounts, err := Accounts()
	if err != nil {
		return Overview{}, err
	}
	findLinks(root, accounts, s)
	tidyTrashOnce(root)
	o, err := overview(root, accounts, s.Hidden, s.Filled)
	if err != nil {
		return o, err
	}
	if o.Gone == "" {
		err = seenFilled(root, s)
	}
	if err != nil {
		return o, err
	}
	_, o.Profile = ProfileNames(accounts, s)
	names, err := knownProfiles(accounts, s)
	o.Profiles = append(o.Profiles, names...)
	return o, err
}

func overview(root string, accounts []AccountInfo, hidden []string, filled string) (Overview, error) {
	o := Overview{Profiles: []string{}, Years: []YearCount{}, Albums: []AlbumCount{}, Accounts: []AccountInfo{}, Missing: map[string]int{}}
	idOf := map[string]string{}
	for _, a := range accounts {
		// A profile that is signed out is nobody yet.
		if a.Email == "" {
			continue
		}
		idOf[a.ID] = a.ID
		o.Accounts = append(o.Accounts, a)
		if notes, err := loadNotes(a.Dir); err == nil {
			o.Takeouts += len(asked(notes))
		}
	}
	// No library chosen yet, or nothing in it yet: the accounts are there,
	// their photos are not.
	if root == "" {
		return o, nil
	}
	if libraryAway(root, filled) {
		o.Gone = root
		return o, nil
	}
	c, err := openAt(root, library.OpenCatalog)
	if err != nil || c == nil {
		return o, err
	}
	defer c.Close()
	years, err := c.CountsByYear(hidden)
	if err != nil {
		return o, err
	}
	albums, err := c.AlbumsByOwner(hidden)
	if err != nil {
		return o, err
	}
	if o.Archived, o.Trashed, o.Favorites, err = c.CountsByState(hidden); err != nil {
		return o, err
	}
	// A photo two libraries have is one tile: missing once.
	counted := map[library.Gone]bool{}
	for _, g := range shownOf(missingIn(root), hidden) {
		if g.Profile = ""; !counted[g] {
			counted[g] = true
			o.Missing[g.Year]++
		}
	}
	for y, n := range years {
		o.Years = append(o.Years, YearCount{Year: y, Count: n})
		o.Total += n
	}
	for _, al := range albums {
		id := idOf[al.Account]
		if al.Account != "" && id == "" {
			continue
		}
		o.Albums = append(o.Albums, AlbumCount{Account: id, Name: al.Name, Count: al.Count})
	}
	// Newest first; the photos with no date last.
	sort.Slice(o.Years, func(i, j int) bool {
		a, b := o.Years[i].Year, o.Years[j].Year
		if a == "" || b == "" {
			return b == ""
		}
		return a > b
	})
	return o, nil
}
