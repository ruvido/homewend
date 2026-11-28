// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !linux

package raw

import (
	"os/exec"
	"time"
)

// tied does nothing here: only Linux kills a child with its parent; the
// helper ends by its own timeout, or once its file is developed.
func tied(*exec.Cmd) {}

// cpuLimited does nothing here: a job in the background has no limit but
// the end of its file.
// shortcut: a file that never ends decoding is not stopped on macOS and
// Windows until the app quits; limit it (setrlimit on macOS, a job object on
// Windows) if one is ever met.
func cpuLimited(*exec.Cmd, time.Duration) {}
