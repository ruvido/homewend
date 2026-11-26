// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Settings are the choices a person makes in the window. A missing file is
// every default.
type Settings struct {
	// Theme is "system", "light" or "dark"; empty is "system".
	Theme string `json:"theme,omitempty"`
	// Hidden are the profiles whose photos the window leaves out.
	Hidden []string `json:"hidden,omitempty"`
	// Library is the library folder, chosen once.
	Library string `json:"library,omitempty"`
	// Profiles names the profile of each account, by id: the folder of the
	// library its photos go in. An account not named here goes in ProfileFor's.
	Profiles map[string]string `json:"profiles,omitempty"`
	// Imported are the profiles a card or a folder was imported into: named
	// once, found again at the next import, an account's too, whose folder
	// stays when the account goes.
	Imported []string `json:"imported_profiles,omitempty"`
	// Filled is the library folder whose catalog was seen: its catalog gone
	// from there is its disk away (a share not mounted leaves an empty
	// folder), not a new library to fill on the local disk.
	Filled string `json:"library_filled,omitempty"`
}

// ProfileFor is the profile of the account id: the one chosen, or the
// address's name before the @.
func (s Settings) ProfileFor(id string) string {
	if p := s.Profiles[id]; p != "" {
		return p
	}
	_, email, _ := strings.Cut(id, "/")
	name, _, _ := strings.Cut(email, "@")
	return name
}

// ProfileNames are the profiles there are, and the one an import goes to
// when none is named, "" when it is to be asked. The accounts' profiles
// and those named in the settings come first; then those only imported
// into, a friend's card once, which is no reason to ask about one's own.
func ProfileNames(accounts []AccountInfo, s Settings) (names []string, first string) {
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, a := range accounts {
		add(a.Profile)
	}
	for _, p := range s.Profiles {
		add(p)
	}
	slices.Sort(names)
	own := len(names)
	for _, p := range s.Imported {
		add(p)
	}
	slices.Sort(names[own:])
	if own == 1 || len(names) == 1 {
		first = names[0]
	}
	return names, first
}

// SetProfile puts the account id in the profile named profile.
func SetProfile(id, profile string) error {
	if err := checkProfile(profile, id); err != nil {
		return err
	}
	// Only an account that is here: the settings keep no profile for one
	// that is not.
	dir, err := AccountDir(id)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); err != nil {
		return ErrNoSuchAccount
	}
	_, err = UpdateSettings(func(s *Settings) {
		if s.Profiles == nil {
			s.Profiles = map[string]string{}
		}
		s.Profiles[id] = profile
	})
	return err
}

// rememberProfile keeps profile among those imported into: a card
// imported with no account names it once, and its folder is still offered
// when the account whose profile it was has gone.
func rememberProfile(profile string) error {
	_, err := UpdateSettings(func(s *Settings) {
		if !slices.Contains(s.Imported, profile) {
			s.Imported = append(s.Imported, profile)
		}
	})
	return err
}

// ErrBadProfile is a profile name that cannot be a folder.
var ErrBadProfile = errors.New("a profile is a name, without / or \\, not starting with a dot, no spaces around it")

// ValidProfile is whether p can name a profile: a folder of the library,
// so a plain name, never one that leads out of it or into its work folder.
func ValidProfile(p string) bool {
	return p != "" && p != "." && p != ".." && !strings.ContainsAny(p, "/\\") && !strings.HasPrefix(p, ".") && p == strings.TrimSpace(p)
}

// knownProfiles are the profiles there are, in one order for every list and
// every colour: ProfileNames', then the phones', paired or not any more.
func knownProfiles(accounts []AccountInfo, s Settings) ([]string, error) {
	profiles, _ := ProfileNames(accounts, s)
	phones, err := LoadPhones()
	if err != nil {
		return nil, err
	}
	var more []string
	for _, p := range phones.Phones {
		more = append(more, p.Profile)
	}
	for _, p := range phones.Unpaired {
		more = append(more, p.Profile)
	}
	slices.Sort(more)
	for _, p := range more {
		if !slices.Contains(profiles, p) {
			profiles = append(profiles, p)
		}
	}
	return profiles, nil
}

// people are the accounts and the profiles there are.
func people(s Settings) ([]AccountInfo, []string, error) {
	accounts, err := Accounts()
	if err != nil {
		return nil, nil, err
	}
	profiles, err := knownProfiles(accounts, s)
	return accounts, profiles, err
}

// ErrProfileCase is a new profile named as one there is but for its capitals:
// on a disk that ignores them the two would be one folder.
var ErrProfileCase = errors.New("there is a profile of this name already, with other capitals")

// CheckProfile is whether p may be a profile: a valid name, and one there is
// or a new one that no other's folder could be taken for.
func CheckProfile(p string) error { return checkProfile(p, "") }

// checkProfile is CheckProfile for the account of, whose profile so far is no
// other's: only its folder, once there is one.
func checkProfile(p, of string) error {
	if !ValidProfile(p) {
		return ErrBadProfile
	}
	accounts, err := Accounts()
	if err != nil {
		return err
	}
	s, err := LoadSettings()
	if err != nil {
		return err
	}
	accounts = slices.DeleteFunc(accounts, func(a AccountInfo) bool { return a.ID == of })
	delete(s.Profiles, of)
	names, err := knownProfiles(accounts, s)
	if err != nil {
		return err
	}
	if s.Library != "" {
		entries, _ := os.ReadDir(s.Library) // away: the names known are asked alone
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	if slices.Contains(names, p) {
		return nil
	}
	if slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, p) }) {
		return ErrProfileCase
	}
	return nil
}

// Themes are the values Theme takes.
var Themes = []string{"system", "light", "dark"}

// ErrBadTheme is a theme that is not one of Themes.
var ErrBadTheme = errors.New("theme must be system, light or dark")

func settingsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "homewend", "settings.json"), nil
}

// LoadSettings reads the settings, or the defaults when none were saved.
func LoadSettings() (Settings, error) {
	path, err := settingsPath()
	if err != nil {
		return Settings{}, err
	}
	return loadSettings(path)
}

func loadSettings(path string) (Settings, error) {
	s, err := readJSON[Settings](path)
	// Hidden were accounts, "google/<address>", before they were profiles,
	// which have no "/": each is its profile's now, written at the next save.
	for i, h := range s.Hidden {
		if strings.Contains(h, "/") {
			s.Hidden[i] = s.ProfileFor(h)
		}
	}
	slices.Sort(s.Hidden)
	s.Hidden = slices.Compact(s.Hidden)
	return s, err
}

// settingsMu holds one change of the settings at a time: an import
// remembers its profile while the window may save its theme, and the
// last to write would undo the other.
var settingsMu sync.Mutex

// UpdateSettings changes the settings as they are now, checks them and
// writes them, and says them as written.
func UpdateSettings(change func(*Settings)) (Settings, error) {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	path, err := settingsPath()
	if err != nil {
		return Settings{}, err
	}
	s, err := loadSettings(path)
	if err != nil {
		return s, err
	}
	change(&s)
	return s, saveSettings(path, s)
}

func saveSettings(path string, s Settings) error {
	if s.Theme != "" && !validTheme(s.Theme) {
		return ErrBadTheme
	}
	return writeJSON(path, s)
}

func validTheme(t string) bool { return slices.Contains(Themes, t) }

// Free is how many bytes the disk of dir has free.
func Free(dir string) (int64, error) {
	free, _, err := Space(dir)
	return free, err
}

// Space is how many bytes the disk of dir has free, and its size, or those
// of the nearest folder above it that exists: a library not created yet
// has its disk too.
func Space(dir string) (free, total int64, err error) {
	for {
		if _, err := os.Stat(dir); err == nil {
			return diskSpace(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return diskSpace(dir)
		}
		dir = parent
	}
}
