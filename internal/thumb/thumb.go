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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/heic"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/ruvido/homewend/internal/library"
)

// Size is the long edge of a thumbnail: the grid's tiles are about 104 to 180
// pixels, twice that on a high-density screen.
const Size = 256

// PreviewSize is the long edge of the picture the viewer shows a photo with:
// a 1440-pixel-high window at twice the density, so the original is read only
// to zoom in.
const PreviewSize = 2560

// quality of the JPEG written; the size where artefacts stop showing at Size.
const quality = 80

// ErrNoDecoder is a file no decoder here reads: a video without ffmpeg, a
// format nobody knows.
var ErrNoDecoder = errors.New("no decoder for this file")

// Path is where the thumbnail of the photo with this hash is kept: by
// content, so it never goes stale and a moved photo keeps it.
func Path(cacheDir, hash string) string {
	if len(hash) < 2 {
		return filepath.Join(cacheDir, hash+".jpg")
	}
	return filepath.Join(cacheDir, hash[:2], hash+".jpg")
}

// Make writes the picture of src at dst, its long edge size at most (Size
// or PreviewSize), whole or not at all.
func Make(ctx context.Context, src, dst string, size int) (err error) {
	// A damaged file a decoder does not survive is a file with no picture,
	// not the end of the program that met it.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("decoding %s: %v", src, r)
		}
	}()
	img, err := decode(ctx, src, size)
	if err != nil {
		return err
	}
	if img.Bounds().Empty() {
		return fmt.Errorf("%w: %s is empty", ErrNoDecoder, src)
	}
	img = upright(fit(img, size), library.Orientation(src))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
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

func decode(ctx context.Context, src string, size int) (image.Image, error) {
	ext := strings.ToLower(filepath.Ext(src))
	if library.IsVideo(src) {
		return frame(ctx, src, size)
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if img := embedded(f, src, size); img != nil {
		return img, nil
	}
	if library.IsHEIC(src) {
		if img := smallHEIC(src, size); img != nil {
			return img, nil
		}
		if img := gridHEIC(ctx, src, size); img != nil {
			return img, nil
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return heic.Decode(f)
	}
	img, _, err := image.Decode(f)
	if errors.Is(err, image.ErrFormat) {
		return nil, fmt.Errorf("%w: %s", ErrNoDecoder, ext)
	}
	return img, err
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

// smallHEIC is a HEIC's own thumbnail, as embedded is a JPEG's.
func smallHEIC(src string, size int) image.Image {
	if size > Size {
		return nil
	}
	img := heicThumbnail(src)
	if img == nil || max(img.Bounds().Dx(), img.Bounds().Dy()) < size {
		return nil
	}
	if w, h := Dimensions(src); w == 0 || !sameShape(img, w, h) {
		return nil
	}
	return img
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
	scale := "scale='min(" + strconv.Itoa(size) + ",iw)':-2"
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
	// In strips, one a core: the pixels are the same.
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
			strip := dst.SubImage(image.Rect(0, h*i/strips, w, h*(i+1)/strips)).(*image.RGBA)
			draw.ApproxBiLinear.Scale(strip, dst.Bounds(), img, b, draw.Src, nil)
		})
	}
	wg.Wait()
	if r := failed.Load(); r != nil {
		panic(r)
	}
	return dst
}

// upright turns img as the EXIF orientation says.
func upright(img image.Image, orientation int) image.Image {
	if orientation <= 1 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	src, ok := img.(*image.RGBA)
	if !ok {
		src = image.NewRGBA(b)
		draw.Draw(src, b, img, b.Min, draw.Src)
	}
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
