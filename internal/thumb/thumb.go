// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package thumb makes the small pictures the window's grid shows, and keeps
// them. Everything is Go but video: no cgo, so the app still cross-compiles.
//
// JPEG, PNG and GIF are the standard library's, WebP is x/image's, HEIC is
// gen2brain/heic (a Rust decoder run as WASM, or the system's libheif when
// there is one). A video's first frame is ffmpeg's, as every photo app we
// looked at does it: Ente, Immich, PhotoPrism, Photoview.
// When it is there, the tiles of a HEIC are too, for the large picture.
package thumb

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/heic"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/ruvido/homewend/internal/edit"
	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/raw"
)

// Size is the long edge of a thumbnail: the grid's tiles are about 104 to 180
// pixels, twice that on a high-density screen.
const Size = 256

// PreviewSize is the long edge of the picture the viewer shows a photo with:
// a 1440-pixel-high window at twice the density, so the original is read only
// to zoom in.
const PreviewSize = 2560

// ScreenSize is the long edge of the copy a phone keeps of each photo: its
// screen's width, so a photo opened away from home is sharp but for zoom.
const ScreenSize = 1080

// quality of the JPEG written; the size where artefacts stop showing at Size.
const quality = 80

// ErrNoDecoder is a file no decoder here reads: a video without ffmpeg, a
// format nobody knows.
var ErrNoDecoder = errors.New("no decoder for this file")

// Path is where the thumbnail of the photo with this hash is kept: by
// content, so it never goes stale and a moved photo keeps it. One of an edit
// is under Edited.
func Path(cacheDir, hash string) string {
	if len(hash) < 2 {
		return filepath.Join(cacheDir, hash+".jpg")
	}
	return filepath.Join(cacheDir, hash[:2], hash+".jpg")
}

// editMark is between a name and its edit's: no file's version has it, all
// hexadecimal.
const editMark = "-edit-"

// Edited is the name Path keeps the picture of the content or file name by,
// with the edit named digest.
func Edited(name, digest string) string {
	if digest == "" {
		return name
	}
	return name + editMark + digest
}

// ForgetEdits deletes the pictures of hash, with an edit, in cacheDir that are
// of none of digests.
func ForgetEdits(cacheDir, hash string, digests []string) {
	files, _ := filepath.Glob(Path(cacheDir, hash+"*"+editMark+"*"))
	for _, file := range files {
		_, digest, _ := strings.Cut(strings.TrimSuffix(filepath.Base(file), ".jpg"), editMark)
		if !slices.Contains(digests, digest) {
			os.Remove(file)
		}
	}
}

// Make writes the picture of src at dst, with its edit, its long edge size at
// most (Size or PreviewSize), whole or not at all.
func Make(ctx context.Context, src, dst string, size int, e edit.Edit) (err error) {
	// A damaged file a decoder does not survive is a file with no picture,
	// not the end of the program that met it.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("decoding %s: %v", src, r)
		}
	}()
	// What is drawn from: for an edit, what leaves enough in its frame.
	from := size
	if !e.None() {
		from = drawnFrom(src, size, e)
	}
	img, colours, err := decode(ctx, src, from)
	if err != nil {
		return err
	}
	if img.Bounds().Empty() {
		return fmt.Errorf("%w: %s is empty", ErrNoDecoder, src)
	}
	// Scaled first: there are fewer pixels to convert.
	img = upright(colours.toSRGB(fit(img, from)), library.Orientation(src))
	if !e.None() {
		img = fit(rgba(edit.Draw(img, e)), size)
	}
	return save(dst, img, quality)
}

// save writes img at dst as a JPEG of quality q, whole or not at all.
func save(dst string, img image.Image, q int) error {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	// Two may make the same one, the window and its background work: each
	// writes its own file.
	f, err := os.CreateTemp(filepath.Dir(dst), "*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(buf.Bytes())
	err = errors.Join(err, f.Close())
	if err == nil {
		err = os.Rename(f.Name(), dst)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// developedQuality is that of a raw file's developed picture, which every
// other picture of it is made from: 8 bits, as an edit's input is (docs/edit.md).
const developedQuality = 95

// Develop writes at dst the raw file src developed (package raw), turned as
// its EXIF says, at full size: from then on its pictures are made from it as
// from a JPEG. Only a RAF: its embedded JPEG is what the look is fitted to.
func Develop(ctx context.Context, src, dst string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("developing %s: %v", src, r)
		}
	}()
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	jpg, err := rafPreview(f)
	if err != nil {
		return err
	}
	preview, err := jpeg.Decode(jpg)
	if err != nil {
		return err
	}
	img, meta, err := raw.Develop(ctx, src)
	if err != nil {
		return err
	}
	return save(dst, upright(raw.Picture(img, meta, preview), library.Orientation(src)), developedQuality)
}

// minDrawn is the long edge an edit is drawn at, at least. Off the edit drawn
// on the whole of 12 MP, smooth, by strong tools: 3 of 255 at most at 256, 512,
// 1024 and whole; with noise or texture, 512 and 1024 are half as far off as
// 256 and the whole, which are scaled in one step (measured 2026-11-25).
const minDrawn = 2 * Size

// drawnFrom is how large the whole of src is to be, for what the edit e keeps
// of it to be size. Past Size: the camera's thumbnail is not read.
func drawnFrom(src string, size int, e edit.Edit) int {
	w, h := Dimensions(src)
	size = max(size, minDrawn)
	long := max(w, h)
	if long == 0 {
		return size
	}
	kw, kh := e.Size(w, h)
	kept := max(kw, kh)
	return max(min((long*size+kept-1)/kept, long), Size+1)
}

// Dimensions are the pixels of the photo at path as it is shown, turned as
// its EXIF says; 0 for what no decoder here reads, a video included, or does
// not survive.
func Dimensions(path string) (w, h int) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	defer func() {
		if recover() != nil {
			w, h = 0, 0
		}
	}()
	// The decoder of a HEIC turns it already.
	if library.IsHEIC(path) {
		if c, err := heic.DecodeConfig(f); err == nil {
			return c.Width, c.Height
		}
		// A JPEG saved with the wrong name.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, 0
		}
	}
	c, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0
	}
	if library.Orientation(path) >= 5 {
		return c.Height, c.Width
	}
	return c.Width, c.Height
}

// Kept are the hashes whose picture Path keeps in cacheDir: read from its
// folders, not asked one by one.
func Kept(cacheDir string) map[string]bool {
	kept := map[string]bool{}
	folders, _ := os.ReadDir(cacheDir)
	for _, f := range folders {
		files, _ := os.ReadDir(filepath.Join(cacheDir, f.Name()))
		for _, file := range files {
			if hash, ok := strings.CutSuffix(file.Name(), ".jpg"); ok {
				kept[hash] = true
			}
		}
	}
	return kept
}

// decode is the picture of src, size large or more, and what its colours are.
func decode(ctx context.Context, src string, size int) (image.Image, *profile, error) {
	ext := strings.ToLower(filepath.Ext(src))
	if library.IsVideo(src) {
		img, err := frame(ctx, src, size)
		return img, nil, err
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	colours := profileOf(f, src)
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, nil, err
	}
	// The camera's thumbnail does not say its colours, where the photo's are
	// not sRGB.
	if colours == nil {
		if img := embedded(f, src, size); img != nil {
			return img, nil, nil
		}
	}
	if ext == ".raf" {
		jpg, err := rafPreview(f)
		if err != nil {
			return nil, nil, err
		}
		img, err := jpeg.Decode(jpg)
		return img, colours, err
	}
	if library.IsHEIC(src) {
		data := readHEIC(src)
		if img, own := smallHEIC(src, data, size); img != nil {
			return img, own, nil
		}
		if img := gridHEIC(ctx, data, size); img != nil {
			return img, heicProfile(data), nil
		}
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if img, err := heic.Decode(f); err == nil {
			return img, heicProfile(data), nil
		}
		// A JPEG saved with the wrong name.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil, nil, err
		}
	}
	img, _, err := image.Decode(f)
	if errors.Is(err, image.ErrFormat) {
		return nil, nil, fmt.Errorf("%w: %s", ErrNoDecoder, ext)
	}
	switch img.(type) {
	case *image.CMYK, *image.Gray, *image.Gray16: // not the three colours of a profile
		colours = nil
	}
	return img, colours, err
}

// rafPreview is the JPEG a Fujifilm RAF carries of its photo, at full size:
// its offset and length are at 84 and 88 of the file's header, big-endian.
// The raw data itself is not developed.
func rafPreview(f *os.File) (io.Reader, error) {
	var head [92]byte
	if _, err := f.ReadAt(head[:], 0); err != nil || !bytes.HasPrefix(head[:], []byte("FUJIFILMCCD-RAW")) {
		return nil, fmt.Errorf("%w: not a RAF", ErrNoDecoder)
	}
	at, n := int64(binary.BigEndian.Uint32(head[84:])), int64(binary.BigEndian.Uint32(head[88:]))
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if at < int64(len(head)) || n == 0 || at+n > info.Size() {
		return nil, fmt.Errorf("%w: a RAF with no preview", ErrNoDecoder)
	}
	return io.NewSectionReader(f, at, n), nil
}

// embedded is the thumbnail the camera wrote in the JPEG f, when it is as
// large as asked and of the photo's shape: a photo cropped or turned since by
// a program that left it behind is another. It is read in 5 ms where decoding
// 12 MP takes 350 and 26 MP a second; the 491 large enough in a real library
// are all the photo.
// shortcut: an edit that kept the shape and left the old thumbnail shows the
// photo as it was, in the grid only; decode the photo if one is ever met.
func embedded(f io.ReadSeeker, src string, size int) image.Image {
	if size > Size {
		return nil
	}
	whole, err := jpeg.DecodeConfig(f)
	if _, back := f.Seek(0, io.SeekStart); err != nil || back != nil {
		return nil
	}
	if whole.Width == 0 || whole.Height == 0 {
		return nil
	}
	exif, flashPix := library.EmbeddedThumbnails(src)
	if img := cameras(exif, size); img != nil {
		if sameShape(img, whole.Width, whole.Height) {
			return img
		}
	}
	// Bars are looked for only here: the dark top of a night photo, cropped
	// since, is not one.
	if img := cameras(flashPix, size); img != nil {
		if img = unbarred(img, whole.Width, whole.Height); img != nil && max(img.Bounds().Dx(), img.Bounds().Dy()) >= size {
			return img
		}
	}
	return nil
}

// smallHEIC is the own thumbnail of the HEIC data, at src, as embedded is a
// JPEG's, and what its colours are.
func smallHEIC(src string, data []byte, size int) (image.Image, *profile) {
	if size > Size {
		return nil, nil
	}
	img, colours := heicThumbnail(data)
	if img == nil || max(img.Bounds().Dx(), img.Bounds().Dy()) < size {
		return nil, nil
	}
	if w, h := Dimensions(src); w == 0 || !sameShape(img, w, h) {
		return nil, nil
	}
	return img, colours
}

// sameShape says whether img has the shape of w by h, within two of its own
// pixels: cameras round.
func sameShape(img image.Image, w, h int) bool {
	b := img.Bounds()
	off := b.Dx()*h - b.Dy()*w
	return max(off, -off) <= 2*max(w, h)
}

// cameras is the thumbnail data decoded: as large as asked, and no larger
// than a camera makes one.
func cameras(data []byte, size int) image.Image {
	b, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if long := max(b.Width, b.Height); err != nil || long < size || long > 4*Size {
		return nil
	}
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return img
}

// unbarred is the part of img with the shape of w by h, when all around it
// is black bars, or nothing; else nil.
func unbarred(img image.Image, w, h int) image.Image {
	b := img.Bounds()
	in := b
	if height := b.Dx() * h / w; height < b.Dy() {
		in.Min.Y += (b.Dy() - height + 1) / 2
		in.Max.Y = in.Min.Y + height
	} else {
		width := b.Dy() * w / h
		in.Min.X += (b.Dx() - width + 1) / 2
		in.Max.X = in.Min.X + width
	}
	if in == b {
		return img
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			// A pixel either side of the edge is of both.
			if (image.Point{X: x, Y: y}).In(in.Inset(-1)) {
				continue
			}
			if g := color.GrayModel.Convert(img.At(x, y)).(color.Gray); g.Y > black {
				return nil
			}
		}
	}
	cut, ok := img.(interface {
		SubImage(image.Rectangle) image.Image
	})
	if !ok {
		return nil
	}
	return cut.SubImage(in.Inset(1))
}

// black is the brightest a black bar is, JPEG's noise included: 33 the
// brightest of 38 measured.
const black = 40

// frame is a video's first frame, already at size, from ffmpeg.
func frame(ctx context.Context, src string, size int) (image.Image, error) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("%w: video needs ffmpeg", ErrNoDecoder)
	}
	// A JPEG is BT.601, full range; HD video is BT.709, which ffmpeg leaves.
	scale := "scale='min(" + strconv.Itoa(size) + ",iw)':-2:out_color_matrix=bt601:out_range=full"
	cmd := exec.CommandContext(ctx, ffmpeg, "-v", "error", "-i", src,
		"-frames:v", "1", "-vf", scale, "-f", "image2pipe", "-vcodec", "mjpeg", "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w", err)
	}
	return img, nil
}

// fit scales img so its long edge is size. ApproxBiLinear, not CatmullRom:
// on a 12 MP photo it takes 3 ms instead of 800 (measured 2026-10-03), and at
// 256 pixels the difference does not show.
func fit(img image.Image, size int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= size && h <= size {
		return img
	}
	if w >= h {
		w, h = size, max(1, h*size/w)
	} else {
		w, h = max(1, w*size/h), size
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	inStrips(h, func(from, to int) {
		strip := dst.SubImage(image.Rect(0, from, w, to)).(*image.RGBA)
		draw.ApproxBiLinear.Scale(strip, dst.Bounds(), img, b, draw.Src, nil)
	})
	return dst
}

// inStrips does the h lines of a picture in strips, one a core: the pixels are
// the same.
func inStrips(h int, do func(from, to int)) {
	var wg sync.WaitGroup
	var failed atomic.Value
	strips := min(h, runtime.GOMAXPROCS(0))
	for i := range strips {
		wg.Go(func() {
			// Make cannot recover it in here: it is handed over.
			defer func() {
				if r := recover(); r != nil {
					failed.Store(fmt.Sprint(r))
				}
			}()
			do(h*i/strips, h*(i+1)/strips)
		})
	}
	wg.Wait()
	if r := failed.Load(); r != nil {
		panic(r)
	}
}

// upright turns img as the EXIF orientation says.
func upright(img image.Image, orientation int) image.Image {
	if orientation <= 1 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src := rgba(img)
	swap := orientation >= 5
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if swap {
		out = image.NewRGBA(image.Rect(0, 0, h, w))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch orientation {
			case 2:
				nx, ny = w-1-x, y
			case 3:
				nx, ny = w-1-x, h-1-y
			case 4:
				nx, ny = x, h-1-y
			case 5:
				nx, ny = y, x
			case 6:
				nx, ny = h-1-y, x
			case 7:
				nx, ny = h-1-y, w-1-x
			case 8:
				nx, ny = y, w-1-x
			}
			from, to := src.PixOffset(b.Min.X+x, b.Min.Y+y), out.PixOffset(nx, ny)
			copy(out.Pix[to:to+4], src.Pix[from:from+4])
		}
	}
	return out
}

// rgba is img, or a copy of it, as an RGBA.
func rgba(img image.Image) *image.RGBA {
	if out, ok := img.(*image.RGBA); ok {
		return out
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, img, b.Min, draw.Src)
	return out
}
