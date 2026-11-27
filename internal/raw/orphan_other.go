// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package raw

import "os/exec"

// tied does nothing here: only Linux kills a child with its parent; the
// helper ends by its own timeout.
func tied(*exec.Cmd) {}
