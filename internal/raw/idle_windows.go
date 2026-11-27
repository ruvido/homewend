// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// idle gives cmd the lowest priority there is, from its start: after does
// nothing more.
func idle(cmd *exec.Cmd) (after func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.IDLE_PRIORITY_CLASS}
	return func() {}
}
