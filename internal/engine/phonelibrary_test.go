// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
)

// A phone has two pictures made at once, another its own two, and all of them
// leave the window a slot where there are two; one that goes waits no more.
func TestAPhoneHasTwoPicturesMadeAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		defer func(n int) { allMaking.n = n }(allMaking.n)
		making.of.Clear()
		allMaking.of.Clear()
		allMaking.n = 3
		defer making.of.Clear()
		defer allMaking.of.Clear()
		ctx := context.Background()
		began := make(chan func(), 10)
		ask := func(id string) {
			go func() {
				if end, err := phoneTurn(ctx, Phone{ID: id}); err == nil {
					began <- end
				}
			}()
			synctest.Wait()
		}
		for range 3 {
			ask("ron")
		}
		if len(began) != 2 {
			t.Errorf("one phone has %d made at once", len(began))
		}
		ask("ann")
		ask("ann")
		if len(began) != 3 {
			t.Errorf("all the phones have %d made at once", len(began))
		}
		gone, leave := context.WithCancel(ctx)
		leave()
		if _, err := phoneTurn(gone, Phone{ID: "ron"}); !errors.Is(err, context.Canceled) {
			t.Errorf("a phone gone: %v", err)
		}
		for range 5 {
			(<-began)()
			synctest.Wait()
		}
	})
}
