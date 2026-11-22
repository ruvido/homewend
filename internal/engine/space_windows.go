// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import "golang.org/x/sys/windows"

// diskSpace is the space the current user may still write under dir, and
// the size of its disk.
func diskSpace(dir string) (free, total int64, err error) {
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var available, size, all uint64
	if err := windows.GetDiskFreeSpaceEx(path, &available, &size, &all); err != nil {
		return 0, 0, err
	}
	return int64(available), int64(size), nil
}
