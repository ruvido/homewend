// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package thumbtest makes the files the tests of pictures read.
package thumbtest

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// RAF is a Fujifilm RAF in dir as a camera writes its head: the name, then
// where its JPEG is, at and n long, then raw data that is not read.
func RAF(t testing.TB, dir string, preview []byte, at, n uint32) string {
	t.Helper()
	head := make([]byte, 148)
	copy(head, "FUJIFILMCCD-RAW 0201FF159501X-T3")
	binary.BigEndian.PutUint32(head[84:], at)
	binary.BigEndian.PutUint32(head[88:], n)
	path := filepath.Join(dir, "DSCF0050.RAF")
	data := append(append(head, preview...), bytes.Repeat([]byte{7}, 4096)...)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
