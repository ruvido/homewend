// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/session"
)

// libraryFile is where an older homewend kept the library folder, before it
// moved into the settings.
const libraryFile = "library"

// DefaultLibrary is the library folder chosen once, or "" when none was.
func DefaultLibrary() (string, error) {
	s, err := LoadSettings()
	if err != nil || s.Library != "" {
		return s.Library, err
	}
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, libraryFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	old := strings.TrimSpace(string(data))
	if err := SetDefaultLibrary(old); err != nil {
		return "", err
	}
	return old, os.Remove(filepath.Join(dir, libraryFile))
}

// ChooseLibrary makes dir the library folder from now on, as a person
// chooses it: a folder typed that does not exist yet is made. A library
// kept from before is not made again when its disk is away
// (LibraryGoneError): that is SetDefaultLibrary's.
func ChooseLibrary(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return SetDefaultLibrary(dir)
}

// SetDefaultLibrary keeps dir as the library folder from now on.
func SetDefaultLibrary(dir string) error {
	_, err := UpdateSettings(func(s *Settings) { s.Library = dir })
	return err
}

// State is where things stand, as the status command shows it.
type State struct {
	SignedIn bool   `json:"signed_in"`
	Account  string `json:"account,omitempty"`
	// Export is the one Homewend asked for last, nil when there is none or
	// the profile is not signed in.
	Export  *Takeout `json:"export,omitempty"`
	Library string   `json:"library,omitempty"`
}

// Now reads the state without changing it: the session is checked, not
// renewed, and no window opens.
func Now(sess *session.Session) (State, error) {
	var st State
	var err error
	if st.Library, err = DefaultLibrary(); err != nil {
		return st, err
	}
	if st.SignedIn, err = signedIn(sess); err != nil || !st.SignedIn {
		return st, err
	}
	st.Account = Account(sess)
	list, err := Takeouts(sess)
	if err != nil {
		return st, err
	}
	for _, t := range list {
		if t.Known {
			st.Export = &t
			break
		}
	}
	return st, nil
}

// datedByName are the libraries whose photos with no date were given the
// one their name says, in this run. Once is enough: every way in reads the
// name since.
var (
	datedByNameMu sync.Mutex
	datedByName   = map[string]bool{}
)

// dateByName files the library's photos with no date under the date their
// name says (library.DateByName), once a run and in the background: the
// window follows the catalog as they move.
func dateByName(root string) error {
	datedByNameMu.Lock()
	defer datedByNameMu.Unlock()
	if root == "" || datedByName[root] {
		return nil
	}
	if _, err := os.Stat(catalogPath(root)); err != nil {
		return nil
	}
	catalog, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		return err
	}
	photos, err := library.NamedPhotos(catalog)
	if err != nil {
		catalog.Close()
		return err
	}
	datedByName[root] = true
	if len(photos) == 0 {
		return catalog.Close()
	}
	go func() {
		defer catalog.Close()
		if err := library.DateByName(root, catalog, photos, func() (func(), error) { return yieldingTurn(root) }); err != nil {
			// Tried again at the next start.
			log.Printf("dates from names: %v", err)
		}
	}()
	return nil
}

// linksFound are the libraries findLinks was begun for, in this run.
var linksFound sync.Map

// findLinks puts in the catalog the photos older programs left in a second
// profile's folder without a record (library.FindLinks), begun once a run and in
// the background: it reads the disk.
func findLinks(root string, accounts []AccountInfo, s Settings) {
	if _, err := os.Stat(catalogPath(root)); root == "" || err != nil {
		return
	}
	if _, begun := linksFound.LoadOrStore(root, true); begun {
		return
	}
	go func() {
		if err := findLinksNow(root, accounts, s); err != nil {
			// Tried again at the next start.
			log.Printf("photos of a second profile: %v", err)
		}
	}()
}

func findLinksNow(root string, accounts []AccountInfo, s Settings) error {
	profiles, err := knownProfiles(accounts, s)
	if err != nil {
		return err
	}
	return library.FindLinks(root, catalogPath(root), profiles, s.ProfileFor, func() (func(), error) {
		return waitLibrary(context.Background(), root, nil)
	})
}

// yieldingTurn is the library's turn, for one photo of many: whoever waits
// for it asks in the pause before.
func yieldingTurn(root string) (unlock func(), err error) {
	time.Sleep(2 * lockPoll)
	return waitLibrary(context.Background(), root, nil)
}

// settled are the libraries settle was done for in this run, and the profile
// the photos of nobody went to.
var settled struct {
	sync.Mutex
	roots map[string]string
}

// settle moves a library filed before profiles into them, once a run, as
// nothing is filed so since: the photos
// of each source to its account's profile, those of no source to the first
// account's. While another run places photos it waits for the next time.
func settle(root string) error {
	if root == "" {
		return nil
	}
	if _, err := os.Stat(catalogPath(root)); err != nil {
		return nil
	}
	settled.Lock()
	defer settled.Unlock()
	// Done with nobody to file them under, it is done again for the first
	// account.
	if fallback, done := settled.roots[root]; done && fallback != "" {
		return nil
	}
	accounts, err := Accounts()
	if err != nil {
		return err
	}
	s, err := LoadSettings()
	if err != nil {
		return err
	}
	fallback := ""
	for _, a := range accounts {
		if a.Profile != "" {
			fallback = a.Profile
			break
		}
	}
	if _, done := settled.roots[root]; done && fallback == "" {
		return nil
	}
	known, err := knownProfiles(accounts, s)
	if err != nil {
		return err
	}
	unlock, err := lockLibrary(root)
	if errors.Is(err, ErrLibraryBusy) {
		return nil
	}
	if err != nil {
		return err
	}
	defer unlock()
	catalog, err := library.OpenCatalog(catalogPath(root))
	if err != nil {
		return err
	}
	defer catalog.Close()
	// A card's name is nobody's profile.
	profileOf := func(source string) string {
		if strings.HasPrefix(source, library.LocalSource) {
			return ""
		}
		return s.ProfileFor(source)
	}
	if err := library.Relayout(root, catalog, known, profileOf, fallback, nil); err != nil {
		return err
	}
	if settled.roots == nil {
		settled.roots = map[string]string{}
	}
	settled.roots[root] = fallback
	return nil
}
