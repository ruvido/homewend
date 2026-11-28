// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// tied has the helper killed when the engine dies, not left developing.
func tied(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}

// cpuLimited has the helper stopped once it has used limit of CPU time: a
// job in the background may wait for the CPU as long as others need it.
func cpuLimited(cmd *exec.Cmd, limit time.Duration) {
	secs := uint64(limit.Seconds())
	// The soft limit stops it (SIGXCPU), the hard one, a second later, kills it.
	unix.Prlimit(cmd.Process.Pid, unix.RLIMIT_CPU, &unix.Rlimit{Cur: secs, Max: secs + 1}, nil)
}
