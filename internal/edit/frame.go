// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package edit

import (
	"encoding/json"
)

// Framed is the edit data, its JSON ("" or null for none), with a crop that
// keeps the frame r (x0, y0, x1, y1 in shares of the picture upright) of a
// photo the computer turned quarters anticlockwise; and the crop written and
// the one it replaced, "" for none. ok is false, and data is left as it is,
// when its own crop frames, tilts or mirrors the photo already, or it is
// not read here.
func Framed(data string, r [4]float64, quarters int) (out, crop, before string, ok bool) {
	fields := map[string]json.RawMessage{"adjust": json.RawMessage("{}"), "tools": json.RawMessage("2")}
	turns := ((-quarters)%4 + 4) % 4 // the frame keeps the photo as it is shown
	if data != "" && data != "null" {
		if _, err := Parse([]byte(data)); err != nil || json.Unmarshal([]byte(data), &fields) != nil {
			return data, "", "", false
		}
		if raw, there := fields["crop"]; there {
			c, err := parseCrop(raw)
			if err != nil || c.angle != 0 || c.flip || c.w < 0.999 || c.h < 0.999 {
				return data, "", "", false
			}
			turns, before = c.turns, string(raw)
		}
	}
	// The frame as of the photo upright, then turned with it, clockwise.
	x, y, w, h := (r[0]+r[2])/2-0.5, (r[1]+r[3])/2-0.5, r[2]-r[0], r[3]-r[1]
	for range turns {
		x, y, w, h = -y, x, h, w
	}
	f32 := func(v float64) float64 { return float64(float32(v)) }
	frame, err := json.Marshal(map[string]any{"turns": turns, "flip": false, "angle": 0,
		"x": f32(x), "y": f32(y), "w": f32(w), "h": f32(h), "shape": "Free"})
	if err != nil {
		return data, "", "", false
	}
	fields["crop"] = frame
	whole, err := json.Marshal(fields)
	if err != nil {
		return data, "", "", false
	}
	return string(whole), string(frame), before, true
}

// Unframed is the edit data with the crop added by Framed put back to
// before, while it is still the one added: one the person changed since is
// theirs. An edit that is then nothing is null.
func Unframed(data, added, before string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(data), &fields) != nil || fields == nil {
		return data
	}
	now, err := parseCrop(fields["crop"])
	if was, err2 := parseCrop(json.RawMessage(added)); err != nil || err2 != nil || now != was {
		return data
	}
	delete(fields, "crop")
	if before != "" {
		fields["crop"] = json.RawMessage(before)
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return data
	}
	if Removes(out) {
		return "null"
	}
	return string(out)
}
