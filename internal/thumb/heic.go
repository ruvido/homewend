// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"encoding/binary"
	"image"
	"os"

	"github.com/gen2brain/heic"
)

// heicThumbnail is the small picture an iPhone writes in a HEIC beside the
// photo, upright as the photo is, or nil. Decoding 12 MP takes 6 to 9
// seconds, this 0.1 (59 photos of an iPhone 7, 24/11).
//
// The decoder decodes a file's primary item, so it is given the file with the
// thumbnail as that (ISO/IEC 14496-12: in the meta box, pitm names the
// primary item, and iref's thmb says which item is a thumbnail of which).
func heicThumbnail(path string) (small image.Image) {
	// A thumbnail the decoder does not survive is none: the photo may.
	defer func() {
		if recover() != nil {
			small = nil
		}
	}()
	fi, err := os.Stat(path)
	// shortcut: read whole, a photo's few MB; by its place when one of a
	// hundred is met.
	if err != nil || fi.Size() > 64<<20 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil || !thumbnailFirst(data) {
		return nil
	}
	img, err := heic.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

// thumbnailFirst makes the thumbnail of data's primary item the primary one,
// and says whether there is one.
func thumbnailFirst(data []byte) bool {
	meta := box(data, "meta")
	if len(meta) < 4 {
		return false
	}
	// Full boxes: a version and flags before what they hold.
	pitm, iref := box(meta[4:], "pitm"), box(meta[4:], "iref")
	if len(pitm) < 4 || len(iref) < 4 {
		return false
	}
	primary, ok := item(pitm[4:], pitm[0] > 0)
	if !ok {
		return false
	}
	wide := iref[0] > 0
	size := 2
	if wide {
		size = 4
	}
	for rest := iref[4:]; len(rest) >= 8; {
		n := binary.BigEndian.Uint32(rest)
		if n < 8 || uint64(n) > uint64(len(rest)) {
			return false
		}
		ref := rest[8:n]
		if string(rest[4:8]) == "thmb" && len(ref) >= size+2 {
			from, _ := item(ref, wide)
			for to := ref[size+2:]; len(to) >= size; to = to[size:] {
				if id, _ := item(to, wide); id != primary {
					continue
				}
				if pitm[0] > 0 {
					binary.BigEndian.PutUint32(pitm[4:], from)
				} else {
					binary.BigEndian.PutUint16(pitm[4:], uint16(from))
				}
				return true
			}
		}
		rest = rest[n:]
	}
	return false
}

// item is the item's number at the start of b, in four bytes or two.
func item(b []byte, wide bool) (uint32, bool) {
	if wide {
		if len(b) < 4 {
			return 0, false
		}
		return binary.BigEndian.Uint32(b), true
	}
	if len(b) < 2 {
		return 0, false
	}
	return uint32(binary.BigEndian.Uint16(b)), true
}

// box is what the first box of kind in data holds, or nil.
func box(data []byte, kind string) []byte {
	for len(data) >= 8 {
		n, head := uint64(binary.BigEndian.Uint32(data)), uint64(8)
		if n == 1 && len(data) >= 16 { // its length in eight bytes
			n, head = binary.BigEndian.Uint64(data[8:]), 16
		} else if n == 0 { // to the end
			n = uint64(len(data))
		}
		if n < head || n > uint64(len(data)) {
			return nil
		}
		if string(data[4:8]) == kind {
			return data[head:n]
		}
		data = data[n:]
	}
	return nil
}
