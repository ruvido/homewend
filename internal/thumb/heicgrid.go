// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package thumb

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"io"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/heic"
)

// grid is a photo in tiles, as iPhones write them: each a picture of its own,
// all coded alike (ISO/IEC 23008-12, the image item of type grid).
type grid struct {
	data                  []byte // the file
	columns, rows         int
	width, height         int // of the photo, before it is turned
	tileWidth, tileHeight int
	orientation           int      // what turns it upright, as EXIF's
	lengthSize            int      // of a NAL unit's length in a tile
	parameters            [][]byte // the NAL units all tiles need
	tiles                 []tile
}

type tile struct {
	id   uint32
	data []byte
}

// maxGridPixels and maxTiles are more than a phone's 48 megapixels and their
// 192 tiles: the most the photo put together here takes. What the decoder
// takes for a tile is the tile's to say, and one is tried alone first.
const (
	maxGridPixels = 1 << 26
	maxTiles      = 512
)

// oneGrid: a photo at a time takes every core, so the one looked at, asked
// first, is not slowed by those either side of it.
var oneGrid = make(chan struct{}, 1)

// gridHEIC is the HEIC at src no larger than size, upright, its tiles decoded
// apart; nil when it is not a grid of the kind known here or does not decode
// as one, for the decoder to be given it whole.
//
// The whole takes 5 to 7 seconds in the decoder that comes with the app,
// nearly all of it HEVC on one core. ffmpeg, when there is one, decodes the
// tiles as the frames of a video; without it every core takes tiles.
func gridHEIC(ctx context.Context, src string, size int) image.Image {
	// libheif does the same by itself.
	if heic.Dynamic() == nil {
		return nil
	}
	select {
	case oneGrid <- struct{}{}:
	case <-ctx.Done():
		return nil
	}
	defer func() { <-oneGrid }()
	g := gridOf(readHEIC(src))
	if g == nil {
		return nil
	}
	stream, err := g.annexB()
	if err != nil {
		return nil
	}
	// What a tile really is, its colours and its size, only its own header
	// says: the one with the most in it is decoded alone, so a size the file
	// lies about is never met on every core, and ffmpeg's must be the same.
	most := 0
	for i, t := range g.tiles {
		if len(t.data) > len(g.tiles[most].data) {
			most = i
		}
	}
	var video *image.YCbCr
	var videoErr error
	var wg sync.WaitGroup
	trying, cancel := context.WithCancel(ctx)
	defer cancel()
	wg.Go(func() { video, videoErr = g.byFFmpeg(trying, stream) })
	sample, err := g.decodeTile(bytes.Clone(g.data), most)
	if err != nil {
		cancel()
	}
	wg.Wait()
	if err != nil {
		return nil
	}
	var img image.Image
	if videoErr == nil && alike(video.SubImage(g.place(most)), sample) {
		img = video.SubImage(g.bounds())
	} else if img, err = g.byDecoder(ctx); err != nil {
		return nil
	}
	return upright(fit(img, size), g.orientation)
}

// gridOf is the grid that is data's primary item, or nil when it is not one,
// or has anything not known here: a crop, a mirror, transparency, tiles that
// differ.
func gridOf(data []byte) *grid {
	meta := fullBox(data, "meta")
	primary, ok := primaryItem(meta)
	// moov: a sequence, whose first picture is another.
	if !ok || box(data, "moov") != nil {
		return nil
	}
	kinds := itemKinds(meta)
	var ids []uint32
	for _, r := range references(meta, "dimg") {
		if r.from == primary {
			ids = r.to
		}
	}
	properties := box(box(meta, "iprp"), "ipco")
	if kinds[primary] != "grid" || len(ids) == 0 || bytes.Contains(properties, []byte("aux:alpha")) ||
		bytes.Contains(properties, []byte("auxid:1")) {
		return nil
	}
	places := itemData(data, meta)
	has := itemProperties(meta, properties)

	g := &grid{data: data, orientation: 1}
	r := reader{b: places[primary]}
	version, flags := r.u(1), r.u(1)
	g.rows, g.columns = int(r.u(1))+1, int(r.u(1))+1
	n := 2
	if flags&1 > 0 {
		n = 4
	}
	g.width, g.height = int(r.u(n)), int(r.u(n))
	if r.bad || version != 0 || len(ids) != g.rows*g.columns {
		return nil
	}
	for _, p := range has[primary] {
		switch p.kind {
		case "irot":
			if len(p.data) != 1 || g.orientation != 1 {
				return nil
			}
			// Quarters anticlockwise.
			g.orientation = [4]int{1, 8, 3, 6}[p.data[0]&3]
		case "ispe":
			if w, h := extent(p.data); w != g.width || h != g.height {
				return nil
			}
		default:
			if !p.plain() {
				return nil
			}
		}
	}
	first := has[ids[0]]
	for _, p := range first {
		switch p.kind {
		case "ispe":
			g.tileWidth, g.tileHeight = extent(p.data)
		case "hvcC":
			g.lengthSize, g.parameters = hevcParameters(p.data)
		default:
			if !p.plain() {
				return nil
			}
		}
	}
	tw, th := g.tileWidth, g.tileHeight
	// Chroma has half the pixels either way; the tiles cover the photo, the
	// last ones over its edge.
	if tw <= 0 || th <= 0 || tw > 1<<13 || th > 1<<13 || tw%2 > 0 || th%2 > 0 || len(g.parameters) == 0 ||
		g.width <= (g.columns-1)*tw || g.width > g.columns*tw ||
		g.height <= (g.rows-1)*th || g.height > g.rows*th ||
		int64(g.columns*tw)*int64(g.rows*th) > maxGridPixels || len(ids) > maxTiles {
		return nil
	}
	size := 0
	for _, id := range ids {
		same := slices.EqualFunc(has[id], first, func(a, b property) bool { return a.index == b.index })
		if kinds[id] != "hvc1" || !same || len(places[id]) == 0 {
			return nil
		}
		g.tiles = append(g.tiles, tile{id, places[id]})
		size += len(places[id])
	}
	if size > len(data) { // the same piece many times over
		return nil
	}
	return g
}

// itemKinds are the types of meta's items, those protected left out.
func itemKinds(meta []byte) map[uint32]string {
	kinds := map[uint32]string{}
	r := reader{b: box(meta, "iinf")}
	if r.u(4)>>24 == 0 {
		r.u(2)
	} else {
		r.u(4)
	}
	for k, b := range boxes(r.b) {
		r := reader{b: b}
		version := r.u(4) >> 24
		if k != "infe" || version != 2 && version != 3 {
			continue
		}
		id := uint32(r.u(2 * int(version-1)))
		protection, kind := r.u(2), r.next(4)
		if protection == 0 {
			kinds[id] = string(kind)
		}
	}
	return kinds
}

// itemData is what meta's items hold, those in one piece inside data.
func itemData(data, meta []byte) map[uint32][]byte {
	held := map[uint32][]byte{}
	r := reader{b: box(meta, "iloc")}
	version := r.u(4) >> 24
	sizes := r.u(2)
	offsetSize, lengthSize, baseSize, indexSize := int(sizes>>12), int(sizes>>8&15), int(sizes>>4&15), int(sizes&15)
	if version > 2 || offsetSize > 8 || lengthSize > 8 || baseSize > 8 || indexSize > 8 {
		return nil
	}
	if version == 0 {
		indexSize = 0
	}
	idBytes := 2
	if version == 2 {
		idBytes = 4
	}
	for range r.u(idBytes) {
		id := uint32(r.u(idBytes))
		inside := data
		if version > 0 {
			switch r.u(2) & 15 {
			case 0:
			case 1:
				inside = box(meta, "idat")
			default:
				inside = nil
			}
		}
		if r.u(2) != 0 { // in another file
			inside = nil
		}
		base, extents := r.u(baseSize), r.u(2)
		for i := range extents {
			r.u(indexSize)
			offset, length := r.u(offsetSize), r.u(lengthSize)
			if r.bad {
				return held
			}
			in := reader{b: inside}
			in.next(base)
			in.next(offset)
			if b := in.next(length); extents == 1 && i == 0 && !in.bad {
				held[id] = b
			}
		}
	}
	return held
}

// property is one of an item's, by its place among all.
type property struct {
	index int
	kind  string
	data  []byte
}

// plain says whether p changes nothing the decoder shows: a colour profile,
// which it does not apply, or the bits of a pixel.
func (p property) plain() bool {
	return p.kind == "pixi" || p.kind == "colr" && len(p.data) >= 4 && string(p.data[:4]) == "prof"
}

// itemProperties are the properties of meta's items.
func itemProperties(meta, properties []byte) map[uint32][]property {
	all := []property{{}} // the first is number 1
	for k, b := range boxes(properties) {
		all = append(all, property{len(all), k, b})
	}
	has := map[uint32][]property{}
	r := reader{b: box(box(meta, "iprp"), "ipma")}
	head := r.u(4)
	for range r.u(4) {
		id := uint32(r.u(idSize(head)))
		for range r.u(1) {
			at := int(r.u(1)) & 0x7f // the top bit: essential
			if head&1 > 0 {
				at = at<<8 | int(r.u(1))
			}
			if at > 0 && at < len(all) {
				has[id] = append(has[id], all[at])
			}
		}
		if r.bad {
			break
		}
	}
	return has
}

// extent is the width and height an ispe says.
func extent(ispe []byte) (w, h int) {
	r := reader{b: ispe}
	r.u(4)
	return int(r.u(4)), int(r.u(4))
}

// hevcParameters are the NAL units in an hvcC, and the bytes of the length
// before each in the pictures it is for (ISO/IEC 14496-15).
func hevcParameters(hvcC []byte) (lengthSize int, nals [][]byte) {
	r := reader{b: hvcC}
	r.next(21)
	lengthSize = int(r.u(1)&3) + 1
	for range r.u(1) {
		r.u(1)
		for range r.u(2) {
			nal := r.next(r.u(2))
			if r.bad {
				return 0, nil
			}
			nals = append(nals, nal)
		}
	}
	return lengthSize, nals
}

// place is where tile i goes.
func (g *grid) place(i int) image.Rectangle {
	at := image.Pt(i%g.columns*g.tileWidth, i/g.columns*g.tileHeight)
	return image.Rectangle{Min: at, Max: at.Add(image.Pt(g.tileWidth, g.tileHeight))}
}

func (g *grid) bounds() image.Rectangle { return image.Rect(0, 0, g.width, g.height) }

// byDecoder is the photo, its tiles decoded on every core: each made the
// primary item of a copy of the file, as a thumbnail is.
func (g *grid) byDecoder(ctx context.Context) (image.Image, error) {
	whole := image.NewRGBA(g.bounds())
	var next atomic.Int64
	var wg sync.WaitGroup
	errs := make([]error, min(runtime.GOMAXPROCS(0), len(g.tiles)))
	for worker := range errs {
		wg.Go(func() {
			errs[worker] = func() error {
				// Its own copy, for the number of the primary item in it.
				data := bytes.Clone(g.data)
				for i := int(next.Add(1)) - 1; i < len(g.tiles); i = int(next.Add(1)) - 1 {
					if err := ctx.Err(); err != nil {
						return err
					}
					img, err := g.decodeTile(data, i)
					if err != nil {
						next.Store(int64(len(g.tiles))) // the others need not go on
						return err
					}
					draw.Draw(whole, g.place(i), img, image.Point{}, draw.Src)
				}
				return nil
			}()
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return whole, nil
}

// decodeTile is tile i, by the decoder; data is a copy of the file, changed.
func (g *grid) decodeTile(data []byte, i int) (img image.Image, err error) {
	// In a goroutine, so Make does not see it.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tile %d: %v", i, r)
		}
	}()
	if !setPrimary(fullBox(data, "meta"), g.tiles[i].id) {
		return nil, fmt.Errorf("tile %d: no primary item", i)
	}
	img, err = heic.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if img.Bounds() != image.Rect(0, 0, g.tileWidth, g.tileHeight) {
		return nil, fmt.Errorf("tile %d: %v", i, img.Bounds())
	}
	return img, nil
}

// byFFmpeg is the photo, not yet cut to its size, its tiles decoded as the
// frames of the video stream.
func (g *grid) byFFmpeg(ctx context.Context, stream []byte) (*image.YCbCr, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// y4m says its frames' size and kind of pixel.
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-xerror", "-err_detect", "explode",
		"-f", "hevc", "-i", "pipe:0", "-f", "yuv4mpegpipe", "pipe:1")
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(stream)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	img, err := g.frames(out)
	if err != nil {
		cancel()
		cmd.Wait()
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	return img, nil
}

// annexB is the tiles as a video's stream, each NAL unit after a start code;
// none may bring parameters of its own.
func (g *grid) annexB() ([]byte, error) {
	var out []byte
	add := func(nal []byte) { out = append(append(out, 0, 0, 0, 1), nal...) }
	for _, p := range g.parameters {
		add(p)
	}
	for i, t := range g.tiles {
		r := reader{b: t.data}
		for len(r.b) > 0 {
			nal := r.next(r.u(g.lengthSize))
			if r.bad || len(nal) == 0 {
				return nil, fmt.Errorf("tile %d: cut short", i)
			}
			if kind := nal[0] >> 1 & 63; kind >= 32 && kind <= 34 { // VPS, SPS, PPS
				return nil, fmt.Errorf("tile %d: parameters", i)
			}
			add(nal)
		}
	}
	return out, nil
}

// frames puts together the tiles that are the frames of the y4m video r,
// when they are as many, and as large, as they must be, in 8 bits, full
// range, the chroma halved.
func (g *grid) frames(r io.Reader) (*image.YCbCr, error) {
	in := bufio.NewReaderSize(r, 1<<20)
	head, err := in.ReadString('\n')
	if err != nil {
		return nil, err
	}
	words := strings.Fields(head)
	for _, want := range []string{fmt.Sprint("W", g.tileWidth), fmt.Sprint("H", g.tileHeight), "C420jpeg", "XCOLORRANGE=FULL"} {
		if !slices.Contains(words, want) {
			return nil, fmt.Errorf("ffmpeg: %q", head)
		}
	}
	img := image.NewYCbCr(image.Rect(0, 0, g.columns*g.tileWidth, g.rows*g.tileHeight), image.YCbCrSubsampleRatio420)
	for i := range g.tiles {
		if line, err := in.ReadString('\n'); err != nil || line != "FRAME\n" {
			return nil, fmt.Errorf("ffmpeg: frame %d: %q, %v", i, line, err)
		}
		at := g.place(i)
		planes := []struct {
			pix                []byte
			at, stride, length int
			rows               int
		}{
			{img.Y, img.YOffset(at.Min.X, at.Min.Y), img.YStride, g.tileWidth, g.tileHeight},
			{img.Cb, img.COffset(at.Min.X, at.Min.Y), img.CStride, g.tileWidth / 2, g.tileHeight / 2},
			{img.Cr, img.COffset(at.Min.X, at.Min.Y), img.CStride, g.tileWidth / 2, g.tileHeight / 2},
		}
		for _, p := range planes {
			for y := range p.rows {
				if _, err := io.ReadFull(in, p.pix[p.at+y*p.stride:][:p.length]); err != nil {
					return nil, err
				}
			}
		}
	}
	if _, err := in.ReadByte(); err != io.EOF {
		return nil, fmt.Errorf("ffmpeg: more frames than tiles")
	}
	return img, nil
}

// alike says whether a and b are the same picture but for rounding: a pixel's
// colour was never more than 3 in 255 off, and the wrong matrix is tens.
func alike(a, b image.Image) bool {
	from, size := a.Bounds().Min, a.Bounds().Size()
	if b.Bounds().Size() != size {
		return false
	}
	far := func(x, y uint32) bool { return max(x>>8, y>>8)-min(x>>8, y>>8) > 8 }
	for y := range size.Y {
		for x := range size.X {
			r1, g1, b1, _ := a.At(from.X+x, from.Y+y).RGBA()
			r2, g2, b2, _ := b.At(b.Bounds().Min.X+x, b.Bounds().Min.Y+y).RGBA()
			if far(r1, r2) || far(g1, g2) || far(b1, b2) {
				return false
			}
		}
	}
	return true
}
