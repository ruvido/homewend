// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"encoding/binary"
	"testing"
)

func aBox(kind string, inside ...byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, uint32(len(inside)+8))
	return append(append(b, kind...), inside...)
}

// The thumbnail of the primary item, and of no other, takes its place.
func TestAHEICsThumbnailFirst(t *testing.T) {
	file := func(refs ...byte) []byte {
		meta := append([]byte{0, 0, 0, 0}, aBox("pitm", 0, 0, 0, 0, 0, 49)...)
		meta = append(meta, aBox("iref", append([]byte{0, 0, 0, 0}, refs...)...)...)
		return append(aBox("ftyp", []byte("heic")...), aBox("meta", meta...)...)
	}
	thumbnailOf := func(of byte) []byte { return aBox("thmb", 0, 50, 0, 1, 0, of) }
	for _, c := range []struct {
		name string
		data []byte
		want bool
	}{
		{"its own", file(append(aBox("dimg", 0, 49, 0, 1, 0, 1), thumbnailOf(49)...)...), true},
		{"another item's", file(thumbnailOf(7)...), false},
		{"none", file(), false},
		{"a length past the end", file(append([]byte{0, 0, 9, 0}, "thmb"...)...), false},
		{"nothing", nil, false},
	} {
		if got := thumbnailFirst(c.data); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
		if c.want {
			if id, _ := item(box(box(c.data, "meta")[4:], "pitm")[4:], false); id != 50 {
				t.Errorf("%s: primary %d, want 50", c.name, id)
			}
		}
	}
}

func FuzzThumbnailFirst(f *testing.F) {
	f.Add(append(aBox("ftyp"), aBox("meta", 0, 0, 0, 0)...))
	f.Fuzz(func(t *testing.T, data []byte) { thumbnailFirst(data) })
}
