// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package raw

import (
	"os/exec"
	"syscall"
)

// idle gives cmd the lowest priority there is, once it has started: after
// returns the call that does it.
func idle(cmd *exec.Cmd) (after func()) {
	return func() { syscall.Setpriority(syscall.PRIO_PROCESS, cmd.Process.Pid, 19) }
}
