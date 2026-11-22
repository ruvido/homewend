// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build unix

package engine

import "golang.org/x/sys/unix"

// diskSpace is the space an ordinary user may still write under dir, and
// the size of its disk.
func diskSpace(dir string) (free, total int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return int64(uint64(st.Bavail) * uint64(st.Bsize)), int64(uint64(st.Blocks) * uint64(st.Bsize)), nil
}
