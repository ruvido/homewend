// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Card is a disk with a camera's photos on it: a memory card, a camera
// plugged in as a disk, a phone that shows itself as one.
type Card struct {
	Path string `json:"path"`
	Name string `json:"name"` // the disk's name, as the system shows it
}

// Cards are the disks mounted now that hold a DCIM folder at their top,
// where every camera keeps its photos (the DCF standard). The folder, not
// the disk's "removable" flag, is what says a card: the reader of a laptop
// measured 09/11 calls its cards fixed disks.
func Cards() []Card {
	var disks []string
	if runtime.GOOS == "windows" {
		// Every drive is a disk of its own: D: to Z:, the system's C: left
		// out. Only its top is looked at: a card copied into a folder of a
		// data disk is a folder, not a card.
		for letter := 'D'; letter <= 'Z'; letter++ {
			disks = append(disks, string(letter)+`:\`)
		}
	} else {
		disks = disksUnder(mountRoots())
	}
	return cardsOf(disks)
}

// cardAt is the card mounted at dir, if a card is: the one place the
// window and the import ask it.
func cardAt(dir string) (Card, bool) {
	for _, c := range Cards() {
		if c.Path == dir {
			return c, true
		}
	}
	return Card{}, false
}

// mountRoots are the folders a Mac or a Linux system mounts the disks it is
// given in, one disk per folder.
func mountRoots() []string {
	if runtime.GOOS == "darwin" {
		return []string{"/Volumes"}
	}
	// udisks mounts under /run/media/<user> (measured 09/11), older systems
	// under /media/<user>, some under /media itself.
	roots := []string{"/media"}
	if u, err := user.Current(); err == nil {
		roots = append(roots, filepath.Join("/run/media", u.Username), filepath.Join("/media", u.Username))
	}
	return roots
}

// disksUnder are the folders one level under each root: the disks mounted
// there. A root this system does not have is passed over.
func disksUnder(roots []string) []string {
	var disks []string
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				disks = append(disks, filepath.Join(root, e.Name()))
			}
		}
	}
	return disks
}

// cardsOf are the disks with a DCIM folder at their top.
func cardsOf(disks []string) []Card {
	cards := []Card{} // none is [], not null: the window reads its length
	for _, disk := range disks {
		if !hasDCIM(disk) {
			continue
		}
		name := filepath.Base(disk)
		if runtime.GOOS == "windows" {
			name = strings.TrimSuffix(disk, `\`)
		}
		cards = append(cards, Card{Path: disk, Name: name})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Path < cards[j].Path })
	return cards
}

// hasDCIM is whether disk has a DCIM folder at its top, in any case: FAT
// keeps the name as the camera wrote it.
func hasDCIM(disk string) bool {
	entries, err := os.ReadDir(disk)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && strings.EqualFold(e.Name(), "DCIM") {
			return true
		}
	}
	return false
}

// ErrNotACard is an eject asked for a folder that is no card mounted now.
var ErrNotACard = errors.New("not a card mounted now")

// EjectCard unmounts the card at dir and, where the system can, powers it
// off, so it can be taken out: as the file manager's eject does it. Only a
// card of Cards is ejected, never any other disk. An import from it is
// stopped first by the caller's choice: the system refuses a busy disk.
func EjectCard(dir string) error {
	if _, ok := cardAt(dir); !ok {
		return ErrNotACard
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("diskutil", "eject", dir)
	case "linux":
		// gio asks udisks, as the file managers of GNOME and KDE do: unmount,
		// then power off the reader's card.
		cmd = exec.Command("gio", "mount", "--eject", dir)
	default:
		return errors.ErrUnsupported
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ejecting %s: %v: %s", dir, err, strings.TrimSpace(string(out)))
	}
	return nil
}
