// homewend — Copyright (C) 2026 Ron Alder
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// A card is a disk with a camera's DCIM folder: found one level under where
// the system mounts disks, whatever its name and whatever case the camera
// wrote; other disks, and a mount root that is not there, are passed over.
func TestACardIsADiskWithADCIMFolder(t *testing.T) {
	media := t.TempDir()
	for _, dir := range []string{"NO NAME/DCIM/100_FUJI", "EOS_DIGITAL/dcim", "BACKUP/Photos", "NO NAME/MISC"} {
		if err := os.MkdirAll(filepath.Join(media, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cards := cardsOf(disksUnder([]string{media, filepath.Join(media, "not-there")}))
	if len(cards) != 2 || cards[0].Name != "EOS_DIGITAL" || cards[1].Name != "NO NAME" {
		t.Errorf("cards %+v, want EOS_DIGITAL and NO NAME", cards)
	}
}

// No card is an empty list, which the window can count; nil is null to it.
func TestNoCardIsAnEmptyList(t *testing.T) {
	if cards := cardsOf(nil); cards == nil {
		t.Error("no card is nil, sent as null")
	}
}

func TestOnlyACardIsEjected(t *testing.T) {
	if err := EjectCard(t.TempDir()); !errors.Is(err, ErrNotACard) {
		t.Fatalf("a folder that is no card: %v, want ErrNotACard", err)
	}
}
