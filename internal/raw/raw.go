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

// timeout is how long one file asked for may take: a 26 MP file takes 15 s
// on four cores. One in the background, at the lowest priority, waits for the
// CPU as long as others need it (26 MP took 12 min under load): it is limited
// in CPU time instead, cpuLimit, about ten times what 26 MP uses.
var timeout, cpuLimit = 2 * time.Minute, 3 * time.Minute

// Image is the developed photo: linear ProPhoto RGB, 16 bits a sample, rows
// top to bottom as the sensor has them, not turned. Three samples a pixel and
// not image.RGBA64's four: a 40 MP photo is 240 MB as it is.
type Image struct {
	Width, Height int
	Pix           []uint16
}

// Meta is what the helper read from the file besides the pixels.
type Meta struct {
	Model string
	// ExposureShift is how many stops the camera exposed under what it
	// metered (Fuji's RawExposureBias, Canon's highlight tone priority).
	ExposureShift float64 `json:"exposure_shift"`
	// Frame is the size of the camera's own picture, in the middle of the
	// sensor's, as the raw file says; zero if it does not.
	Frame struct{ Width, Height int }
	// RGBCam takes the camera's RGB to linear sRGB.
	RGBCam [3][3]float64 `json:"rgb_cam"`
}

// Mode is how a file is decoded: developed, or as a film negative to invert,
// whole or at half the size.
type Mode string

const (
	Developed    Mode = ""
	Negative     Mode = "-negative"
	NegativeHalf Mode = "-negative-half"
)

// header is the helper's first line.
type header struct {
	Width, Height int
	Meta
}

// Installed says whether raw files can be developed here.
func Installed() bool {
	_, err := find()
	return err == nil
}

// Develop decodes the raw file at path as mode says; in the background, at
// the lowest priority the system has, with no time limit but its CPU's.
func Develop(ctx context.Context, path string, mode Mode, background bool) (*Image, *Meta, error) {
	helper, err := find()
	if err != nil {
		return nil, nil, err
	}
	if !background {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, helper, path)
	if mode != Developed {
		cmd = exec.CommandContext(ctx, helper, string(mode), path)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	tied(cmd)
	started := func() {}
	if background {
		started = idle(cmd)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("homewend-raw: %w", err)
	}
	started()
	if background {
		cpuLimited(cmd, cpuLimit)
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
	// As the helper refuses: a header that lies is not given the memory.
	if h.Width <= 0 || h.Height <= 0 || h.Width > 65535 || h.Height > 65535 || float64(h.Width)*float64(h.Height) > 200e6 {
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
