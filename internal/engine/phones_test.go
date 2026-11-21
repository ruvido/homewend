// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A code pairs one phone, once, into the profile chosen before it.
func TestACodePairsOnePhoneOnce(t *testing.T) {
	config(t)
	code, err := StartPairing("ron")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Pair("wrong", "Pixel"); !errors.Is(err, ErrBadCode) {
		t.Fatalf("a wrong code: %v, want ErrBadCode", err)
	}
	phone, token, err := Pair(code, "Pixel")
	if err != nil {
		t.Fatal(err)
	}
	if phone.Profile != "ron" {
		t.Errorf("profile %q, want ron", phone.Profile)
	}
	if _, _, err := Pair(code, "Another"); !errors.Is(err, ErrBadCode) {
		t.Errorf("the code again: %v, want ErrBadCode", err)
	}
	if got, err := PhoneOf(token); err != nil || got.ID != phone.ID {
		t.Errorf("its token: %v %v", got.ID, err)
	}
	if strings.Contains(mustRead(t, "phones.json"), token) {
		t.Error("phones.json holds the token itself")
	}
	if err := Unpair(phone.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := PhoneOf(token); !errors.Is(err, ErrNoPhone) {
		t.Errorf("after unpairing: %v, want ErrNoPhone", err)
	}
}

// A code shown longer than its life pairs nothing.
func TestAnOldCodePairsNothing(t *testing.T) {
	config(t)
	code, err := StartPairing("ron")
	if err != nil {
		t.Fatal(err)
	}
	pairing.until = time.Now().Add(-time.Second)
	if _, _, err := Pair(code, "Pixel"); !errors.Is(err, ErrBadCode) {
		t.Errorf("%v, want ErrBadCode", err)
	}
}

// A photo from a phone is checked against its hash, filed in the phone's
// profile, not sent twice, and once deleted here not brought back.
func TestAPhotoFromAPhone(t *testing.T) {
	config(t)
	root := t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	phone := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}
	photo := []byte("a photo from the phone")
	sum := sha256.Sum256(photo)
	hash := hex.EncodeToString(sum[:])
	ctx := context.Background()

	if _, err := ReceivePhoto(ctx, phone, hash, "IMG_1.JPG", int64(len(photo)), strings.NewReader("other bytes")); !errors.Is(err, ErrNotThisHash) {
		t.Fatalf("wrong bytes: %v, want ErrNotThisHash", err)
	}
	got, err := ReceivePhoto(ctx, phone, hash, "../../IMG_1.JPG", int64(len(photo)), bytes.NewReader(photo))
	if err != nil {
		t.Fatal(err)
	}
	if !got.New || !strings.HasPrefix(got.Path, "ron/") {
		t.Fatalf("got %+v, want new in ron's folder", got)
	}
	if have, out, err := Have([]string{hash, strings.Repeat("0", 64)}); err != nil || len(have) != 1 || len(out) != 0 {
		t.Errorf("have %v out %v err %v, want it had", have, out, err)
	}
	again, err := ReceivePhoto(ctx, phone, hash, "IMG_1.JPG", int64(len(photo)), bytes.NewReader(photo))
	if err != nil || again.New || again.Path != got.Path {
		t.Errorf("sent twice: %+v %v, want the same path, not new", again, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".homewend", "phone", "p1")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the upload is left: %v", err)
	}
	// Deleted on the computer.
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(got.Path))); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := Have([]string{hash}); len(out) != 1 {
		t.Errorf("out %v, want it said deleted", out)
	}
	if back, err := ReceivePhoto(ctx, phone, hash, "IMG_1.JPG", int64(len(photo)), bytes.NewReader(photo)); err != nil || !back.Out {
		t.Errorf("deleted, sent again: %+v %v, want out", back, err)
	}
}

// Two photos from one phone at once each come in: one request's upload is
// not removed by the other's end.
func TestTwoPhotosFromOnePhoneAtOnce(t *testing.T) {
	config(t)
	if err := SetDefaultLibrary(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	phone := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}
	// The catalog made first: two requests on a library with none yet is
	// not this test's case.
	first := []byte("the first photo")
	sum := sha256.Sum256(first)
	if _, err := ReceivePhoto(context.Background(), phone, hex.EncodeToString(sum[:]), "IMG.JPG", int64(len(first)), bytes.NewReader(first)); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for i := range 2 {
		go func() {
			photo := []byte(strings.Repeat("photo ", 100000) + string(rune('a'+i)))
			sum := sha256.Sum256(photo)
			for {
				_, err := ReceivePhoto(context.Background(), phone, hex.EncodeToString(sum[:]), "IMG.JPG", int64(len(photo)), bytes.NewReader(photo))
				// The other holds the library's turn: a phone sends again.
				if !errors.Is(err, ErrLibraryBusy) {
					errs <- err
					return
				}
			}
		}()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

func mustRead(t *testing.T, name string) string {
	t.Helper()
	dir, err := configDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
