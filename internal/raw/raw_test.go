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
		fmt.Println(`{"width":2,"height":1,"make":"Fujifilm","model":"X-T5","lens":"XF35mmF1.4 R","iso":400,"wb":[2.1,1,1.6,0],"exposure_shift":-0.72,"baseline_exposure":0,"frame":{"width":2,"height":1},"preview":{"offset":1024,"length":2048,"width":160,"height":120}}`)
		binary.Write(os.Stdout, binary.LittleEndian, []uint16{1, 2, 3, 65535, 0, 300})
	case "fail":
		fmt.Fprintln(os.Stderr, "open: Unsupported file format or not RAW file")
		os.Exit(1)
	case "short":
		fmt.Println(`{"width":2,"height":1}`)
		binary.Write(os.Stdout, binary.LittleEndian, []uint16{1, 2})
	case "hang":
		time.Sleep(time.Minute)
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
	img, meta, err := Develop(context.Background(), "photo.RAF")
	if err != nil {
		t.Fatal(err)
	}
	if img.Width != 2 || img.Height != 1 || fmt.Sprint(img.Pix) != "[1 2 3 65535 0 300]" {
		t.Errorf("image = %+v", img)
	}
	want := Meta{Make: "Fujifilm", Model: "X-T5", Lens: "XF35mmF1.4 R", ISO: 400,
		WB: [4]float64{2.1, 1, 1.6, 0}, ExposureShift: -0.72,
		Preview: &Preview{Offset: 1024, Length: 2048, Width: 160, Height: 120}}
	want.Frame.Width, want.Frame.Height = 2, 1
	if !reflect.DeepEqual(*meta, want) {
		t.Errorf("meta = %+v, want %+v", *meta, want)
	}
}

func TestDevelopFails(t *testing.T) {
	for mode, says := range map[string]string{
		"fail":  "Unsupported file format",
		"short": "unexpected EOF",
	} {
		t.Run(mode, func(t *testing.T) {
			fake(t, mode)
			_, _, err := Develop(context.Background(), "photo.RAF")
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
	_, _, err := Develop(ctx, "photo.RAF")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the deadline", err)
	}
}

func TestDevelopNoHelper(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, _, err := Develop(context.Background(), "photo.RAF"); !errors.Is(err, ErrNoHelper) {
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
	img, meta, err := Develop(context.Background(), files[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(img.Pix) != img.Width*img.Height*3 || meta.Make == "" || meta.WB[1] == 0 {
		t.Errorf("%dx%d %+v", img.Width, img.Height, meta)
	}
}
