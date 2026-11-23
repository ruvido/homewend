// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// One run places photos in a library at a time: a second is told at once,
// and has its turn as soon as the first is done.
func TestOneRunPlacesPhotosAtATime(t *testing.T) {
	root := t.TempDir()
	unlock, err := lockLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockLibrary(root); !errors.Is(err, ErrLibraryBusy) {
		t.Errorf("second run: %v, want ErrLibraryBusy", err)
	}
	unlock()
	again, err := lockLibrary(root)
	if err != nil {
		t.Errorf("after the first: %v, want the turn", err)
	} else {
		again()
	}
}

// A download waits for its turn, or for its stop.
func TestADownloadWaitsForItsTurn(t *testing.T) {
	root := t.TempDir()
	unlock, err := lockLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := waitLibrary(ctx, root, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("waiting while held: %v, want the stop", err)
	}
	go func() { time.Sleep(200 * time.Millisecond); unlock() }()
	turn, err := waitLibrary(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("waiting for the turn: %v", err)
	}
	turn()
}

func TestAMomentsTurnIsWaitedFor(t *testing.T) {
	root := t.TempDir()
	unlock, err := lockLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	time.AfterFunc(300*time.Millisecond, unlock)
	mine, err := takeLibrary(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer mine()
	if _, err := takeLibrary(context.Background(), root); !errors.Is(err, ErrLibraryBusy) {
		t.Errorf("held longer: %v, want ErrLibraryBusy", err)
	}
}

// A run of many photos, one turn each, lets another in between two.
func TestAManyPhotosRunLetsAnotherIn(t *testing.T) {
	root := t.TempDir()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			unlock, err := yieldingTurn(root)
			if err != nil {
				t.Error(err)
				return
			}
			time.Sleep(50 * time.Millisecond)
			unlock()
		}
	}()
	for range 3 {
		unlock, err := takeLibrary(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		unlock()
	}
	close(stop)
	<-done
}
