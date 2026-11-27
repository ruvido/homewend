// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"os/exec"
	"syscall"
)

// tied has the helper killed when the engine dies, not left developing.
func tied(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
