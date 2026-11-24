// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package library

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// The camera is the make and the model, the make once; a short text is in
// its entry, and nothing that cannot be shown comes out.
func TestTheCameraAPhotoNames(t *testing.T) {
	for _, c := range []struct{ maker, model, want string }{
		{"FUJIFILM\x00", "X-T3\x00", "FUJIFILM X-T3"},
		{"Canon\x00", "Canon EOS R6\x00", "Canon EOS R6"},
		{"DJI\x00", "FC3170\x00", "DJI FC3170"},
		{"\x00", "\x00", ""},
		{"Canon\x00junk\x01\x1b\x00", "R\x1b[1mX\xff\x00", "Canon R[1mX"},
	} {
		le := binary.LittleEndian
		tiff := append([]byte("II*\x00"), 8, 0, 0, 0)
		tiff = le.AppendUint16(tiff, 2)
		after := []byte{}
		for i, s := range []string{c.maker, c.model} {
			tiff = le.AppendUint16(tiff, uint16(0x010f+i))
			tiff = le.AppendUint16(tiff, 2)
			tiff = le.AppendUint32(tiff, uint32(len(s)))
			if len(s) <= 4 {
				tiff = append(tiff, append([]byte(s), 0, 0, 0, 0)[:4]...)
				continue
			}
			tiff = le.AppendUint32(tiff, uint32(38+len(after)))
			after = append(after, s...)
		}
		tiff = append(le.AppendUint32(tiff, 0), after...)
		path := filepath.Join(t.TempDir(), "a.jpg")
		if err := os.WriteFile(path, append([]byte("\xff\xd8\xff\xe1\x00\x00Exif\x00\x00"), tiff...), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := CameraOf(path); got != c.want {
			t.Errorf("%q %q: %q, want %q", c.maker, c.model, got, c.want)
		}
	}
}
