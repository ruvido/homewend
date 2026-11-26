// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"
)

// photosOf are n photos of their own, and their hashes.
func photosOf(name string, n int) ([][]byte, []string) {
	var photos [][]byte
	var hashes []string
	for i := range n {
		p := []byte(fmt.Sprintf("%s photo %d", name, i))
		sum := sha256.Sum256(p)
		photos, hashes = append(photos, p), append(hashes, hex.EncodeToString(sum[:]))
	}
	return photos, hashes
}

func sendPhoto(t *testing.T, phone Phone, hash string, photo []byte) error {
	t.Helper()
	_, err := ReceivePhoto(context.Background(), phone, hash, "IMG.JPG", int64(len(photo)), bytes.NewReader(photo))
	return err
}

// backing starts with no backup known, and leaves none.
func backing(t *testing.T) {
	t.Helper()
	backups.Lock()
	clear(backups.runs)
	backups.Unlock()
	t.Cleanup(func() { backups.Lock(); clear(backups.runs); backups.Unlock() })
}

// What a have says is missing is the run; each photo filed counts once; a
// photo refused does not; the last filed ends it; a have with nothing
// missing says none.
func TestABackupCounts(t *testing.T) {
	config(t)
	backing(t)
	if err := SetDefaultLibrary(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	phone := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}
	photos, hashes := photosOf("pixel", 4)
	// One already there before: not awaited.
	if err := sendPhoto(t, phone, hashes[0], photos[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := Have(phone, hashes); err != nil {
		t.Fatal(err)
	}
	want := func(done, of int) {
		t.Helper()
		got := BackingNow()
		if of == 0 && len(got) == 0 {
			return
		}
		if len(got) != 1 || got[0] != (Backing{ID: "p1", Name: "Pixel", Done: done, Of: of}) {
			t.Fatalf("%+v, want %d of %d", got, done, of)
		}
	}
	want(0, 3)
	if err := sendPhoto(t, phone, hashes[1], photos[1]); err != nil {
		t.Fatal(err)
	}
	want(1, 3)
	// Sent again: counted once.
	if err := sendPhoto(t, phone, hashes[1], photos[1]); err != nil {
		t.Fatal(err)
	}
	want(1, 3)
	// The wrong bytes: refused, not counted.
	if err := sendPhoto(t, phone, hashes[2], []byte("other bytes")); err == nil {
		t.Fatal("wrong bytes taken")
	}
	want(1, 3)
	// Asked again: the same run, nothing counted twice.
	if _, err := Have(phone, hashes); err != nil {
		t.Fatal(err)
	}
	want(1, 3)
	for _, i := range []int{2, 3} {
		if err := sendPhoto(t, phone, hashes[i], photos[i]); err != nil {
			t.Fatal(err)
		}
	}
	want(0, 0)
	if _, err := Have(phone, hashes); err != nil {
		t.Fatal(err)
	}
	want(0, 0)
}

// Two phones at once are a line each; a phone silent a minute is none, and
// its next have starts a run of its own.
func TestTwoPhonesAndASilence(t *testing.T) {
	config(t)
	backing(t)
	if err := SetDefaultLibrary(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	pixel := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}
	galaxy := Phone{ID: "p2", Name: "Galaxy", Profile: "anna"}
	pp, ph := photosOf("pixel", 2)
	_, gh := photosOf("galaxy", 3)
	for _, c := range []struct {
		p Phone
		h []string
	}{{pixel, ph}, {galaxy, gh}} {
		if _, err := Have(c.p, c.h); err != nil {
			t.Fatal(err)
		}
	}
	if err := sendPhoto(t, pixel, ph[0], pp[0]); err != nil {
		t.Fatal(err)
	}
	got := BackingNow()
	if len(got) != 2 || got[0] != (Backing{"p2", "Galaxy", 0, 3}) || got[1] != (Backing{"p1", "Pixel", 1, 2}) {
		t.Fatalf("%+v, want the Galaxy 0 of 3 and the Pixel 1 of 2", got)
	}
	// A PUT under way is not silence, however long.
	end := backupSending(galaxy)
	backups.Lock()
	backups.runs["p1"].last = time.Now().Add(-2 * backupQuiet)
	backups.runs["p2"].last = time.Now().Add(-2 * backupQuiet)
	backups.Unlock()
	if got := BackingNow(); len(got) != 1 || got[0].Name != "Galaxy" {
		t.Fatalf("%+v, want the Galaxy only", got)
	}
	// Its end is the last thing heard: the minute counts from there.
	end(gh[0], false)
	backups.Lock()
	backups.runs["p2"].last = time.Now().Add(-2 * backupQuiet)
	backups.Unlock()
	if got := BackingNow(); len(got) != 0 {
		t.Fatalf("%+v, want none after the silence", got)
	}
	if _, err := Have(pixel, ph); err != nil {
		t.Fatal(err)
	}
	if got := BackingNow(); len(got) != 1 || got[0] != (Backing{"p1", "Pixel", 0, 1}) {
		t.Fatalf("%+v, want a new run, 0 of 1", got)
	}
}
