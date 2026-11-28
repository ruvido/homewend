// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package edit

import (
	"image"
	"image/color"
	"image/draw"
	"strings"
	"testing"
)

// The crop given keeps the frame and nothing else, the photo shown as the
// computer turned it; turned back, the crop is the photo as it was.
func TestFramedKeepsTheFrame(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(20, 10, 180, 90), &image.Uniform{color.RGBA{255, 0, 0, 255}}, image.Point{}, draw.Src)
	r := [4]float64{0.1, 0.1, 0.9, 0.9}
	for quarters, size := range map[int]image.Point{0: {160, 80}, 1: {80, 160}, 2: {160, 80}} {
		data, crop, before, ok := Framed("", r, quarters)
		e, err := Parse([]byte(data))
		if !ok || err != nil || crop == "" || (before == "") != (quarters == 0) {
			t.Fatalf("turned %d: %s %v %v", quarters, data, ok, err)
		}
		out := Draw(img, e)
		if got := out.Rect.Size(); got != size {
			t.Errorf("turned %d: %v, want %v", quarters, got, size)
		}
		for y := 1; y < out.Rect.Dy()-1; y++ {
			for x := 1; x < out.Rect.Dx()-1; x++ {
				if c := out.NRGBAAt(x, y); c.G > 10 {
					t.Fatalf("turned %d: %v at %d,%d is not the frame", quarters, c, x, y)
				}
			}
		}
		if back := Unframed(data, crop, before, quarters); back != "null" {
			t.Errorf("turned %d: back to %s", quarters, back)
		}
		// The computer's turn, the edit's now, stays: turned back after
		// another tool was used, it is not shown sideways.
		exposed := strings.Replace(data, `"adjust":{}`, `"adjust":{"Exposure":20}`, 1)
		if back, err := Parse([]byte(Unframed(exposed, crop, before, quarters))); err != nil || back.crop.turns != e.crop.turns || back.None() {
			t.Errorf("turned %d, exposed: back to %s", quarters, Unframed(exposed, crop, before, quarters))
		}
	}
}

// The person's own edit stays: its tools; its turn, kept; its frame, left.
func TestFramedLeavesThePersons(t *testing.T) {
	r := [4]float64{0.1, 0.1, 0.9, 0.9}
	tools := `{"adjust":{"Exposure":12},"tools":2}`
	data, crop, before, ok := Framed(tools, r, 0)
	if !ok || before != "" || Unframed(data, crop, before, 0) != tools {
		t.Errorf("tools: %s, back %s", data, Unframed(data, crop, before, 0))
	}
	turned := `{"adjust":{},"tools":2,"crop":{"turns":1,"flip":false,"angle":0,"x":0,"y":0,"w":1,"h":1,"shape":"Free"}}`
	data, crop, before, ok = Framed(turned, r, 0)
	if e, _ := Parse([]byte(data)); !ok || e.crop.turns != 1 || Unframed(data, crop, before, 0) != `{"adjust":{},"crop":{"turns":1,"flip":false,"angle":0,"x":0,"y":0,"w":1,"h":1,"shape":"Free"},"tools":2}` {
		t.Errorf("turned: %s, back %s", data, Unframed(data, crop, before, 0))
	}
	framed := `{"adjust":{},"tools":2,"crop":{"turns":0,"flip":false,"angle":0,"x":0,"y":0,"w":0.5,"h":1,"shape":"Free"}}`
	if data, _, _, ok := Framed(framed, r, 0); ok || data != framed {
		t.Errorf("framed: %s %v", data, ok)
	}
	// Changed since: theirs.
	data, crop, before, _ = Framed("", r, 0)
	changed := `{"adjust":{},"tools":2,"crop":{"turns":0,"flip":false,"angle":4,"x":0,"y":0,"w":0.5,"h":0.5,"shape":"Free"}}`
	if back := Unframed(changed, crop, before, 0); back != changed {
		t.Errorf("changed: %s", back)
	}
}
