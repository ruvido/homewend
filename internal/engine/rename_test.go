// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// paired is a phone paired into profile, by its code, and its token.
func paired(t *testing.T, profile, name string) (Phone, string) {
	t.Helper()
	code, err := StartPairing(profile)
	if err != nil {
		t.Fatal(err)
	}
	phone, token, err := Pair(code, name)
	if err != nil {
		t.Fatal(err)
	}
	return phone, token
}

// Renamed, a profile's folder moves and everything that names it follows: the
// account it is the profile of, the people hidden and imported into, the
// phones paired into it or once, with the owner id they learnt, a takeout
// begun, a pairing code waiting, a phone waiting for its marks.
func TestARenameIsFollowedEverywhere(t *testing.T) {
	root, _, _, hash, rons := twoPhones(t)
	account := "google/ron@example.com"
	dir, _ := AccountDir(account)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateSettings(func(s *Settings) { s.Hidden, s.Imported = []string{"ron"}, []string{"ron", "ann"} }); err != nil {
		t.Fatal(err)
	}
	gone, _ := paired(t, "ron", "Old Pixel")
	if err := Unpair(gone.ID, "computer"); err != nil {
		t.Fatal(err)
	}
	phone, token := paired(t, "ron", "Pixel")
	if phone.OwnerID == "" || phone.OwnerID != gone.OwnerID {
		t.Fatalf("owner ids %q and %q", phone.OwnerID, gone.OwnerID)
	}
	if _, err := StartPairing("ron"); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(root, library.WorkDir, "a-takeout")
	if err := (state{Profile: "ron"}).save(job); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := waits(ctx, phone, shelfOf(t, phone))
	held(t, 1)

	if err := RenameProfile("ron", "papà rossi", &Jobs{}, &Importing{}); err != nil {
		t.Fatal(err)
	}
	if err := answer(t, "the phone waiting", waiting, true, 5*time.Second); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(root, "papà rossi", strings.TrimPrefix(rons.Path, "ron/"))); err != nil {
		t.Error(err)
	}
	s, _ := LoadSettings()
	if s.ProfileFor(account) != "papà rossi" || !slices.Equal(s.Hidden, []string{"papà rossi"}) || !slices.Equal(s.Imported, []string{"papà rossi", "ann"}) {
		t.Errorf("settings %+v", s)
	}
	phones, _ := LoadPhones()
	if phones.Phones[0].Profile != "papà rossi" || phones.Unpaired[0].Profile != "papà rossi" {
		t.Errorf("phones %+v", phones)
	}
	if st, _ := loadState(job); st.Profile != "papà rossi" {
		t.Errorf("the takeout's profile %q", st.Profile)
	}
	now, err := PhoneOf(token)
	if err != nil || now.Profile != "papà rossi" || now.OwnerID != phone.OwnerID {
		t.Errorf("the phone %+v %v", now, err)
	}
	a, err := Marks(now, library.MarkQuestion{})
	if err != nil || a.Owner != "papà rossi" || a.OwnerID != phone.OwnerID {
		t.Errorf("marks %+v %v", a, err)
	}
	haveIs(t, now, hash, "have")
	// The old phone still names the old profile as it sends: its photo goes
	// in the new one.
	got, _ := send(t, phone, "IMG_2.JPG", "another photo")
	if !strings.HasPrefix(got.Path, "papà rossi/") {
		t.Errorf("filed at %s", got.Path)
	}
	if _, err := os.Stat(filepath.Join(root, "ron")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ron's folder: %v", err)
	}
	// A new code was for ron: it is papà rossi's now.
	code, _ := StartPairing("papà rossi")
	if p, _, err := Pair(code, "Tab"); err != nil || p.OwnerID != phone.OwnerID {
		t.Errorf("paired again %+v %v", p, err)
	}
}

// A name in use, by capitals too, an unknown profile, or a library busy are
// refused, and nothing changes.
func TestARenameIsRefused(t *testing.T) {
	root, _, _, _, _ := twoPhones(t)
	if _, err := os.Stat(filepath.Join(root, "ann")); err != nil {
		os.MkdirAll(filepath.Join(root, "ann"), 0o755)
	}
	jobs, im := &Jobs{}, &Importing{}
	for to, want := range map[string]error{"ann": library.ErrProfileTaken, "Ann": ErrProfileCase, "a/b": ErrBadProfile, ".x": ErrBadProfile} {
		if err := RenameProfile("ron", to, jobs, im); !errors.Is(err, want) {
			t.Errorf("to %s: %v, want %v", to, err, want)
		}
	}
	if err := RenameProfile("zoe", "eve", jobs, im); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an unknown one: %v", err)
	}
	unlock, err := lockLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := RenameProfile("ron", "eve", jobs, im); !errors.Is(err, ErrLibraryBusy) {
		t.Errorf("another run placing photos: %v", err)
	}
	unlock()
	account := "google/ron@example.com"
	dir, _ := AccountDir(account)
	os.MkdirAll(dir, 0o700)
	var ended bool
	if err := jobs.start(account, Get{}, slow(&ended)); err != nil {
		t.Fatal(err)
	}
	if err := RenameProfile("ron", "eve", jobs, im); !errors.Is(err, ErrLibraryBusy) {
		t.Errorf("a download of ron's: %v", err)
	}
	<-jobs.Stop(account)
	if _, err := os.Stat(filepath.Join(root, "ron")); err != nil {
		t.Error(err)
	}
	if err := RenameProfile("ron", "eve", jobs, im); err != nil {
		t.Errorf("once free: %v", err)
	}
}
