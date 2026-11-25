// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"encoding/binary"
	"image"
	"iter"
	"os"
	"slices"

	"github.com/gen2brain/heic"
)

// heicThumbnail is the small picture an iPhone writes in a HEIC beside the
// photo, upright as the photo is, and its colours, or nil. Decoding 12 MP takes 6 to 9
// seconds, this 0.1 (59 photos of an iPhone 7, 24/11).
//
// The decoder decodes a file's primary item, so it is given the file with the
// thumbnail as that (ISO/IEC 14496-12: in the meta box, pitm names the
// primary item, and iref's thmb says which item is a thumbnail of which).
func heicThumbnail(data []byte) (small image.Image, colours *profile) {
	// A thumbnail the decoder does not survive is none: the photo may.
	defer func() {
		if recover() != nil {
			small, colours = nil, nil
		}
	}()
	data = bytes.Clone(data) // the photo is still to be read from it
	ofPhoto := heicProfile(data)
	if !thumbnailFirst(data) {
		return nil, nil
	}
	img, err := heic.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil
	}
	// One that says nothing of its colours is the photo made smaller.
	if colr := heicColours(data); colr != nil {
		return img, colrProfile(colr)
	}
	return img, ofPhoto
}

// heicProfile is what the colours of the photo in the HEIC data are.
func heicProfile(data []byte) *profile { return colrProfile(heicColours(data)) }

// heicColours is the colr of data's primary item, or of the first tile it is
// made of: the profile, when it has that and the code points.
func heicColours(data []byte) (colr []byte) {
	meta := fullBox(data, "meta")
	primary, ok := primaryItem(meta)
	if !ok {
		return nil
	}
	items := []uint32{primary}
	for _, r := range references(meta, "dimg") {
		if r.from == primary && len(r.to) > 0 {
			items = append(items, r.to[0])
		}
	}
	has := itemProperties(meta, box(box(meta, "iprp"), "ipco"))
	for _, item := range items {
		for _, p := range has[item] {
			if p.kind == "colr" && len(p.data) >= 4 && (colr == nil || string(p.data[:4]) != "nclx") {
				colr = p.data
			}
		}
		if colr != nil {
			return colr
		}
	}
	return nil
}

// colrProfile is the profile in a colr, or the one its code points name.
func colrProfile(colr []byte) *profile {
	r := reader{b: colr}
	switch string(r.next(4)) {
	case "prof", "rICC":
		return iccProfile(r.b)
	case "nclx":
		return codePoints(r.u(2), r.u(2))
	}
	return nil
}

// readHEIC is the file at path, or nil.
func readHEIC(path string) []byte {
	fi, err := os.Stat(path)
	// shortcut: read whole, a photo's few MB; by its place when one of a
	// hundred is met.
	if err != nil || fi.Size() > 64<<20 {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// thumbnailFirst makes the thumbnail of data's primary item the primary one,
// and says whether there is one.
func thumbnailFirst(data []byte) bool {
	meta := fullBox(data, "meta")
	primary, ok := primaryItem(meta)
	if !ok {
		return false
	}
	for _, r := range references(meta, "thmb") {
		if slices.Contains(r.to, primary) {
			return setPrimary(meta, r.from)
		}
	}
	return false
}

// primaryItem is the number of the item meta calls primary.
func primaryItem(meta []byte) (uint32, bool) {
	r := reader{b: box(meta, "pitm")}
	n := idSize(r.u(4))
	id := uint32(r.u(n))
	return id, !r.bad
}

// setPrimary makes id the primary item of meta, if it fits where that is.
func setPrimary(meta []byte, id uint32) bool {
	pitm := box(meta, "pitm")
	switch {
	case len(pitm) >= 8 && pitm[0] > 0:
		binary.BigEndian.PutUint32(pitm[4:], id)
	case len(pitm) >= 6 && pitm[0] == 0 && id <= 0xffff:
		binary.BigEndian.PutUint16(pitm[4:], uint16(id))
	default:
		return false
	}
	return true
}

// reference is an item and those it is made from, or of.
type reference struct {
	from uint32
	to   []uint32
}

// references are meta's of kind.
func references(meta []byte, kind string) (refs []reference) {
	r := reader{b: box(meta, "iref")}
	n := idSize(r.u(4))
	for k, b := range boxes(r.b) {
		if k != kind {
			continue
		}
		r := reader{b: b}
		ref := reference{from: uint32(r.u(n))}
		for range r.u(2) {
			if len(r.b) < n {
				break
			}
			ref.to = append(ref.to, uint32(r.u(n)))
		}
		refs = append(refs, ref)
	}
	return refs
}

// idSize is the bytes of an item's number in a box with this version and
// flags.
func idSize(versionFlags uint64) int {
	if versionFlags>>24 > 0 {
		return 4
	}
	return 2
}

// reader reads numbers off the start of b; past its end they are 0, and bad.
type reader struct {
	b   []byte
	bad bool
}

// u is the next number, of n bytes.
func (r *reader) u(n int) (v uint64) {
	if len(r.b) < n {
		r.b, r.bad = nil, true
		return 0
	}
	for _, c := range r.b[:n] {
		v = v<<8 | uint64(c)
	}
	r.b = r.b[n:]
	return v
}

// next are the next n bytes.
func (r *reader) next(n uint64) []byte {
	if uint64(len(r.b)) < n {
		r.b, r.bad = nil, true
		return nil
	}
	b := r.b[:n]
	r.b = r.b[n:]
	return b
}

// box is what the first box of kind in data holds, or nil.
func box(data []byte, kind string) []byte {
	for k, b := range boxes(data) {
		if k == kind {
			return b
		}
	}
	return nil
}

// fullBox is box, past the version and flags some begin with.
func fullBox(data []byte, kind string) []byte {
	b := box(data, kind)
	if len(b) < 4 {
		return nil
	}
	return b[4:]
}

// boxes are the kinds of the boxes in data and what each holds, up to the
// first whose length is wrong.
func boxes(data []byte) iter.Seq2[string, []byte] {
	return func(yield func(string, []byte) bool) {
		for data := data; len(data) >= 8; {
			n, head := uint64(binary.BigEndian.Uint32(data)), uint64(8)
			if n == 1 && len(data) >= 16 { // its length in eight bytes
				n, head = binary.BigEndian.Uint64(data[8:]), 16
			} else if n == 0 { // to the end
				n = uint64(len(data))
			}
			if n < head || n > uint64(len(data)) {
				return
			}
			if !yield(string(data[4:8]), data[head:n]) {
				return
			}
			data = data[n:]
		}
	}
}
