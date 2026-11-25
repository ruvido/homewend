// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"path/filepath"
	"sync"
	"testing"
)

// A file is read whole while it is being replaced, as Windows would refuse.
func TestAFileIsReadWhileReplaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := writeJSON(path, Settings{Filled: "0"}); err != nil {
		t.Fatal(err)
	}
	var all sync.WaitGroup
	for range 4 {
		all.Go(func() {
			for range 200 {
				if s, err := readJSON[Settings](path); err != nil || s.Filled == "" {
					t.Errorf("%+v %v", s, err)
					return
				}
			}
		})
	}
	for range 200 {
		if err := writeJSON(path, Settings{Filled: "1"}); err != nil {
			t.Error(err)
			break
		}
	}
	all.Wait()
}
