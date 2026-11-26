// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package raw develops camera raw files with homewend-raw, a helper program
// built from LibRaw (tools/libraw), as thumb runs ffmpeg for videos: a
// program of its own and not cgo, so the engine still cross-compiles.
package raw

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrNoHelper is homewend-raw found neither beside the engine nor on the PATH.
var ErrNoHelper = errors.New("raw photos need homewend-raw, which is not installed")

// timeout is how long one file may take: a 40 MP file takes a few seconds.
const timeout = 2 * time.Minute

// Image is the developed photo: linear ProPhoto RGB, 16 bits a sample, rows
// top to bottom, already upright. Three samples a pixel and not
// image.RGBA64's four: a 40 MP photo is 240 MB as it is.
type Image struct {
	Width, Height int
	Pix           []uint16
}

// Meta is what the helper read from the file besides the pixels.
type Meta struct {
	Make, Model, Lens string
	ISO               float64
	// WB is the camera's white balance as shot, R G B G2 multipliers.
	WB [4]float64
	// ExposureShift is how many stops the camera exposed under what it
	// metered (Fuji's RawExposureBias, Canon's highlight tone priority).
	ExposureShift float64 `json:"exposure_shift"`
	// BaselineExposure is a DNG's, in stops.
	BaselineExposure float64 `json:"baseline_exposure"`
	// Preview is the largest JPEG the camera embedded, nil if none.
	Preview *Preview
}

// Preview is where an embedded JPEG lies in the raw file.
type Preview struct {
	Offset, Length int64
	Width, Height  int
}

// header is the helper's first line.
type header struct {
	Width, Height int
	Meta
}

// Develop develops the raw file at path.
func Develop(ctx context.Context, path string) (*Image, *Meta, error) {
	helper, err := find()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, helper, path)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("homewend-raw: %w", err)
	}
	img, meta, readErr := read(bufio.NewReader(stdout))
	// Drained, so an error in the middle still lets the helper exit.
	io.Copy(io.Discard, stdout)
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, nil, fmt.Errorf("homewend-raw %s: %w: %s", filepath.Base(path), err, strings.TrimSpace(stderr.String()))
	}
	if readErr != nil {
		return nil, nil, fmt.Errorf("homewend-raw %s: %w", filepath.Base(path), readErr)
	}
	return img, meta, nil
}

// read reads the helper's output: a line of JSON, then the pixels.
func read(r *bufio.Reader) (*Image, *Meta, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, nil, err
	}
	var h header
	if err := json.Unmarshal(line, &h); err != nil {
		return nil, nil, err
	}
	if h.Width <= 0 || h.Height <= 0 {
		return nil, nil, fmt.Errorf("size %dx%d", h.Width, h.Height)
	}
	pix := make([]uint16, h.Width*h.Height*3)
	// The helper writes its machine's byte order, and runs on this machine:
	// every system the app is built for is little-endian. In pieces, not
	// binary.Read, which would hold all the bytes a second time.
	buf := make([]byte, 1<<16)
	for i := 0; i < len(pix); {
		n := min(len(buf), 2*(len(pix)-i))
		if _, err := io.ReadFull(r, buf[:n]); err != nil {
			return nil, nil, err
		}
		for j := 0; j < n; j += 2 {
			pix[i] = binary.LittleEndian.Uint16(buf[j:])
			i++
		}
	}
	return &Image{h.Width, h.Height, pix}, &h.Meta, nil
}

// find finds the helper as the desktop app ships it, beside the engine, else
// on the PATH, the way thumb finds ffmpeg.
func find() (string, error) {
	name := "homewend-raw"
	if exe, err := os.Executable(); err == nil {
		beside := filepath.Join(filepath.Dir(exe), name)
		if p, err := exec.LookPath(beside); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	return "", ErrNoHelper
}
