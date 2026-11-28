// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package raw

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The test binary stands in for homewend-raw when HOMEWEND_RAW_FAKE says
// how to behave.
func TestMain(m *testing.M) {
	switch os.Getenv("HOMEWEND_RAW_FAKE") {
	case "":
		os.Exit(m.Run())
	case "ok":
		fmt.Println(`{"width":2,"height":1,"model":"X-T5","exposure_shift":-0.72,"frame":{"width":2,"height":1}}`)
		binary.Write(os.Stdout, binary.LittleEndian, []uint16{1, 2, 3, 65535, 0, 300})
	case "fail":
		fmt.Fprintln(os.Stderr, "open: Unsupported file format or not RAW file")
		os.Exit(1)
	case "huge":
		fmt.Println(`{"width":100000,"height":100000}`)
	case "short":
		fmt.Println(`{"width":2,"height":1}`)
		binary.Write(os.Stdout, binary.LittleEndian, []uint16{1, 2})
	case "hang":
		time.Sleep(time.Minute)
	case "slow":
		time.Sleep(time.Second)
		fmt.Println(`{"width":1,"height":1}`)
		binary.Write(os.Stdout, binary.LittleEndian, []uint16{1, 2, 3})
	case "spin":
		for {
		}
	}
	os.Exit(0)
}

// fake puts the test binary on the PATH as homewend-raw, behaving as mode.
func fake(t *testing.T, mode string) {
	dir := t.TempDir()
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	name := "homewend-raw"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), self, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOMEWEND_RAW_FAKE", mode)
}

func TestDevelop(t *testing.T) {
	fake(t, "ok")
	img, meta, err := Develop(context.Background(), "photo.RAF", Developed, true)
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != 2 || img.Height != 1 || fmt.Sprint(img.Pix) != "[1 2 3 65535 0 300]" {
		t.Errorf("image = %+v", img)
	}
	want := Meta{Model: "X-T5", ExposureShift: -0.72}
	want.Frame.Width, want.Frame.Height = 2, 1
	if !reflect.DeepEqual(*meta, want) {
		t.Errorf("meta = %+v, want %+v", *meta, want)
	}
}

func TestDevelopFails(t *testing.T) {
	for mode, says := range map[string]string{
		"fail":  "Unsupported file format",
		"short": "unexpected EOF",
		"huge":  "size 100000x100000",
	} {
		t.Run(mode, func(t *testing.T) {
			fake(t, mode)
			_, _, err := Develop(context.Background(), "photo.RAF", Developed, false)
			if err == nil || !strings.Contains(err.Error(), says) {
				t.Errorf("err = %v, want it to say %q", err, says)
			}
		})
	}
}

func TestDevelopTimeout(t *testing.T) {
	fake(t, "hang")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, _, err := Develop(ctx, "photo.RAF", Developed, true)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
}

// A file in the background is not stopped for the time it waits for the
// CPU, as one asked for is; only for the CPU it uses.
func TestDevelopInTheBackgroundWaits(t *testing.T) {
	fake(t, "slow")
	defer func(t time.Duration) { timeout = t }(timeout)
	timeout = 200 * time.Millisecond
	if _, _, err := Develop(context.Background(), "photo.RAF", Developed, true); err != nil {
		t.Errorf("in the background: %v", err)
	}
	if _, _, err := Develop(context.Background(), "photo.RAF", Developed, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("asked: %v, want the deadline", err)
	}
	if runtime.GOOS != "linux" {
		return
	}
	fake(t, "spin")
	defer func(l time.Duration) { cpuLimit = l }(cpuLimit)
	cpuLimit = time.Second
	began := time.Now()
	if _, _, err := Develop(context.Background(), "photo.RAF", Developed, true); err == nil || time.Since(began) > 30*time.Second {
		t.Errorf("spinning: %v after %v", err, time.Since(began))
	}
}

func TestDevelopNoHelper(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, _, err := Develop(context.Background(), "photo.RAF", Developed, false); !errors.Is(err, ErrNoHelper) {
		t.Errorf("err = %v, want ErrNoHelper", err)
	}
}

// A real file, through the real helper, when both are here: no raw file is
// in the repository, for its size.
func TestDevelopRAF(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.RAF"))
	if len(files) == 0 {
		t.Skip("no RAF in testdata")
	}
	if _, err := find(); err != nil {
		t.Skip(err)
	}
	img, meta, err := Develop(context.Background(), files[0], Developed, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(img.Pix) != img.Width*img.Height*3 || meta.Model == "" {
		t.Errorf("%dx%d %+v", img.Width, img.Height, meta)
	}
}
