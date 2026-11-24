// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package thumb makes the small pictures the window's grid shows, and keeps
// them. Everything is Go but video: no cgo, so the app still cross-compiles.
//
// JPEG, PNG and GIF are the standard library's, WebP is x/image's, HEIC is
// gen2brain/heic (a Rust decoder run as WASM, or the system's libheif when
// there is one). A video's first frame is ffmpeg's, as every photo app we
// looked at does it: Ente, Immich, PhotoPrism, Photoview.
package thumb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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
func Make(ctx context.Context, src, dst string, size int) error {
	img, err := decode(ctx, src, size)
	if err != nil {
		return err
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
	if ext == ".heic" || ext == ".heif" {
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
// 12 MP takes 350; the 453 large enough in a real library were all the photo.
// shortcut: an edit that kept the shape and left the old thumbnail shows the
// photo as it was, in the grid only; decode the photo if one is ever met.
func embedded(f io.ReadSeeker, src string, size int) image.Image {
	if size > Size {
		return nil
	}
	data := library.EmbeddedThumbnail(src)
	b, err := jpeg.DecodeConfig(bytes.NewReader(data))
	// As large as asked, and no larger than a camera makes one.
	if long := max(b.Width, b.Height); err != nil || long < size || long > 4*Size {
		return nil
	}
	whole, err := jpeg.DecodeConfig(f)
	if _, back := f.Seek(0, io.SeekStart); err != nil || back != nil {
		return nil
	}
	// Within two of its own pixels: cameras round.
	if off := b.Width*whole.Height - b.Height*whole.Width; max(off, -off) > 2*max(whole.Width, whole.Height) {
		return nil
	}
	small, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	return small
}

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
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}

// upright turns img as the EXIF orientation says.
func upright(img image.Image, orientation int) image.Image {
	if orientation <= 1 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
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
			out.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return out
}
