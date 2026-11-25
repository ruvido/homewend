// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gen2brain/heic"
)

// Six pictures of 64 by 64, ffmpeg's testsrc2 in libx265, 8 bits, full range.
//
//go:embed testdata/tiles.hevc
var tilesHEVC []byte

// aGrid is a HEIC of three tiles by two, as an iPhone writes one; its photo is
// 150 by 100, turned.
type aGrid struct {
	width, height uint16
	columns       byte
	ofPhoto       []byte // more properties of the photo's
	ofTile        []byte // and of its last tile's
	kind          string // of the photo
	refs          []byte // more references
	after         []byte // more boxes
	wreck         func(tile []byte)
	noTiles       bool
	extents       byte
}

func defaults() aGrid { return aGrid{width: 150, height: 100, columns: 3, kind: "grid", extents: 1} }

func (a aGrid) file() []byte {
	be := binary.BigEndian
	// The stream's parameters, and then each picture's slice.
	var parameters, tiles [][]byte
	for _, nal := range bytes.Split(tilesHEVC, []byte{0, 0, 1})[1:] {
		nal = bytes.TrimRight(nal, "\x00")
		if kind := nal[0] >> 1 & 63; kind >= 32 {
			if len(parameters) < 3 {
				parameters = append(parameters, nal)
			}
		} else {
			tiles = append(tiles, append(be.AppendUint32(nil, uint32(len(nal))), nal...))
		}
	}
	if a.wreck != nil {
		a.wreck(tiles[4])
	}
	hvcC := make([]byte, 23)
	hvcC[0], hvcC[21], hvcC[22] = 1, 3, byte(len(parameters))
	copy(hvcC[1:], parameters[1][3:15]) // profile and level
	hvcC[16], hvcC[17], hvcC[18] = 0xfd, 0xf8, 0xf8
	for _, p := range parameters {
		hvcC = append(hvcC, p[0]>>1&63, 0, 1)
		hvcC = append(be.AppendUint16(hvcC, uint16(len(p))), p...)
	}
	ispe := func(w, h uint16) []byte {
		return aBox("ispe", 0, 0, 0, 0, 0, 0, byte(w>>8), byte(w), 0, 0, byte(h>>8), byte(h))
	}
	// 1 hvcC, 2 ispe of a tile, 3 ispe of the photo, 4 irot, 5 and 6 the more.
	ipco := bytes.Join([][]byte{aBox("hvcC", hvcC...), ispe(64, 64), ispe(a.width, a.height), aBox("irot", 3), a.ofPhoto, a.ofTile}, nil)
	more := byte(5)
	photo := []byte{0, 7, 2, 3, 0x84}
	if a.ofPhoto != nil {
		photo[2]++
		photo = append(photo, 0x80|more)
		more++
	}
	ipma := []byte{0, 0, 0, 0, 0, 0, 0, 7}
	iinf := []byte{0, 0, 0, 0, 0, 7}
	dimg := []byte{0, 7, 0, 6}
	for id := byte(1); id <= 6; id++ {
		if id == 6 && a.ofTile != nil {
			ipma = append(ipma, 0, id, 3, 0x81, 2, 0x80|more)
		} else {
			ipma = append(ipma, 0, id, 2, 0x81, 2)
		}
		iinf = append(iinf, aBox("infe", 2, 0, 0, 0, 0, id, 0, 0, 'h', 'v', 'c', '1', 0)...)
		dimg = append(dimg, 0, id)
	}
	ipma = append(ipma, photo...)
	iinf = append(iinf, aBox("infe", append([]byte{2, 0, 0, 0, 0, 7, 0, 0}, a.kind+"\x00"...)...)...)
	descriptor := []byte{0, 0, 1, a.columns - 1, byte(a.width >> 8), byte(a.width), byte(a.height >> 8), byte(a.height)}

	build := func(at int) []byte {
		iloc := []byte{1, 0, 0, 0, 0x44, 0, 0, 7}
		for i, t := range tiles {
			iloc = append(iloc, 0, byte(i+1), 0, 0, 0, 0, 0, a.extents)
			for range a.extents {
				iloc = be.AppendUint32(be.AppendUint32(iloc, uint32(at)), uint32(len(t)))
			}
			at += len(t)
		}
		iloc = append(iloc, 0, 7, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 8)
		meta := bytes.Join([][]byte{
			{0, 0, 0, 0},
			aBox("hdlr", append(make([]byte, 8), "pict\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"...)...),
			aBox("pitm", 0, 0, 0, 0, 0, 7),
			aBox("iinf", iinf...),
			aBox("iref", append(append([]byte{0, 0, 0, 0}, aBox("dimg", dimg...)...), a.refs...)...),
			aBox("iprp", append(aBox("ipco", ipco...), aBox("ipma", ipma...)...)...),
			aBox("idat", descriptor...),
			aBox("iloc", iloc...),
		}, nil)
		return append(append(aBox("ftyp", []byte("heic\x00\x00\x00\x00mif1heic")...), a.after...), aBox("meta", meta...)...)
	}
	head := build(len(build(0)) + 8)
	if a.noTiles {
		return head
	}
	return append(head, aBox("mdat", bytes.Join(tiles, nil)...)...)
}

// Put together from its tiles, a photo is the one the decoder makes whole.
func TestAGridIsItsWhole(t *testing.T) {
	data := defaults().file()
	want, err := heic.Decode(bytes.NewReader(data))
	if err != nil || want.Bounds().Size() != image.Pt(100, 150) {
		t.Fatal(want, err)
	}
	g := gridOf(data)
	if g == nil {
		t.Fatal("not a grid")
	}
	img, err := g.byDecoder(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(upright(img, g.orientation).(*image.RGBA).Pix, want.(*image.NRGBA).Pix) {
		t.Error("the decoder's tiles are not the whole")
	}
	t.Run("ffmpeg", func(t *testing.T) {
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no ffmpeg")
		}
		stream, err := g.annexB()
		if err != nil {
			t.Fatal(err)
		}
		img, err := g.byFFmpeg(t.Context(), stream)
		if err != nil {
			t.Fatal(err)
		}
		if !alike(upright(img.SubImage(g.bounds()), g.orientation), want) {
			t.Error("ffmpeg's tiles are not the whole")
		}
	})
}

// Each of the four turns is the decoder's.
func TestAGridTurned(t *testing.T) {
	for turn := range byte(4) {
		data := bytes.Replace(defaults().file(), aBox("irot", 3), aBox("irot", turn), 1)
		want, err := heic.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		g := gridOf(data)
		img, err := g.byDecoder(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if !alike(upright(img, g.orientation), want) {
			t.Errorf("turn %d", turn)
		}
	}
}

// What is not known here is left to the decoder.
func TestNotAKnownGrid(t *testing.T) {
	nclx := aBox("colr", []byte("nclx\x00\x01\x00\x0d\x00\x01\x80")...)
	for name, change := range map[string]func(*aGrid){
		"a crop":              func(a *aGrid) { a.ofPhoto = aBox("clap", make([]byte, 32)...) },
		"a mirror":            func(a *aGrid) { a.ofPhoto = aBox("imir", 0) },
		"its colours said":    func(a *aGrid) { a.ofPhoto = nclx },
		"a tile unlike":       func(a *aGrid) { a.ofTile = nclx },
		"transparency":        func(a *aGrid) { a.ofPhoto = aBox("auxC", []byte("\x00\x00\x00\x00urn:mpeg:hevc:2015:auxid:1\x00")...) },
		"an overlay":          func(a *aGrid) { a.kind = "iovl" },
		"a sequence":          func(a *aGrid) { a.after = aBox("moov") },
		"fewer tiles":         func(a *aGrid) { a.columns = 4 },
		"tiles too small":     func(a *aGrid) { a.width = 200 },
		"a tile outside":      func(a *aGrid) { a.width = 128 },
		"no width":            func(a *aGrid) { a.width = 0 },
		"tiles not there":     func(a *aGrid) { a.noTiles = true },
		"a tile in pieces":    func(a *aGrid) { a.extents = 2 },
		"a tile of no pieces": func(a *aGrid) { a.extents = 0 },
	} {
		a := defaults()
		change(&a)
		if gridOf(a.file()) != nil {
			t.Error(name)
		}
	}
	a := defaults()
	a.ofPhoto = aBox("colr", []byte("prof")...)
	if gridOf(a.file()) == nil {
		t.Error("a colour profile is an iPhone's")
	}
	for _, d := range [][]byte{nil, aBox("meta"), aBox("meta", 0, 0, 0, 0)} {
		if gridOf(d) != nil {
			t.Errorf("%q", d)
		}
	}
}

// A damaged tile is what the decoder makes of it, or leaves the photo to it;
// a person gone stops it.
func TestAGridDamaged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.heic")
	for name, wreck := range map[string]func([]byte){
		"length":  func(b []byte) { b[0] = 0x7f },
		"nothing": func(b []byte) { clear(b[4:]) },
	} {
		a := defaults()
		a.wreck = wreck
		if err := os.WriteFile(path, a.file(), 0o600); err != nil {
			t.Fatal(err)
		}
		whole, err := heic.Decode(bytes.NewReader(a.file()))
		img := gridHEIC(t.Context(), readHEIC(path), PreviewSize)
		if img != nil && (err != nil || !alike(img, whole)) {
			t.Error(name, err)
		}
	}
	// Tiles of another size than the file says, or with parameters of their own.
	a := defaults()
	a.width, a.height = 75, 50
	lies := bytes.Replace(a.file(), aBox("ispe", 0, 0, 0, 0, 0, 0, 0, 64, 0, 0, 0, 64), aBox("ispe", 0, 0, 0, 0, 0, 0, 0, 32, 0, 0, 0, 32), 1)
	if gridOf(lies) == nil {
		t.Error("a lie only a tile's header tells")
	}
	a = defaults()
	a.wreck = func(b []byte) { b[4] = 33 << 1 }
	for _, data := range [][]byte{lies, a.file()} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if gridHEIC(t.Context(), readHEIC(path), PreviewSize) != nil {
			t.Error("tiles that are not what they are said to be")
		}
	}
	if err := os.WriteFile(path, defaults().file(), 0o600); err != nil {
		t.Fatal(err)
	}
	if img := gridHEIC(t.Context(), readHEIC(path), 75); img == nil || img.Bounds().Size() != image.Pt(50, 75) {
		t.Error(img)
	}
	gone, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := decode(gone, path, PreviewSize); !errors.Is(err, context.Canceled) {
		t.Error(err)
	}
}

func FuzzGridOf(f *testing.F) {
	f.Add(defaults().file())
	f.Fuzz(func(t *testing.T, data []byte) {
		if g := gridOf(data); g != nil {
			g.annexB()
		}
	})
}
