// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package edit reads a photo's edit and draws it, so that a photo edited on
// the phone is the same picture here.
package edit

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"unicode/utf8"
)

// ErrNewer is an edit made with tools this version does not know: it is kept
// as it is, and the photo shown without it.
var ErrNewer = errors.New("the edit was made by a newer Homewend")

// MaxSize is the longest JSON an edit may have, in bytes.
const MaxSize = 64 << 10

const (
	exposure = iota
	contrast
	highlights
	shadows
	whites
	blacks
	warmth
	vibrance
	clarity
	vignette
	blackWhite
	toolCount
)

var toolNames = [toolCount]string{"Exposure", "Contrast", "Highlights", "Shadows", "Whites",
	"Blacks", "Warmth", "Vibrance", "Clarity", "Vignette", "BlackWhite"}

// Edit is one edit. Two that draw the same picture are equal with ==, and the
// zero one leaves the photo as it came.
type Edit struct {
	adjust [toolCount]int
	crop   crop
}

// crop is the frame kept; the zero one keeps the whole photo.
type crop struct {
	turns, angle int // quarter turns, and half degrees, clockwise
	flip         bool
	x, y, w, h   float32
}

// None says whether the edit leaves the photo as it came.
func (e Edit) None() bool { return e == Edit{} }

// Parse reads an edit's JSON, or null, which is none. The error is ErrNewer
// for an edit that is to be kept and not drawn; any other refuses it.
func Parse(data []byte) (Edit, error) {
	if len(data) > MaxSize {
		return Edit{}, errors.New("edit: too long")
	}
	if !utf8.Valid(data) {
		return Edit{}, errors.New("edit: not UTF-8")
	}
	// Maps and not structs: a struct's names match whatever their case.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Edit{}, fmt.Errorf("edit: %w", err)
	}
	if fields == nil {
		return Edit{}, nil
	}
	tools, err := integer(fields["tools"])
	if err != nil || tools < 2 {
		return Edit{}, errors.New("edit: tools is not an integer from 2 up")
	}
	if tools > 2 {
		return Edit{}, ErrNewer
	}

	var e Edit
	var adjust map[string]json.RawMessage
	if err := json.Unmarshal(fields["adjust"], &adjust); err != nil || adjust == nil {
		return Edit{}, errors.New("edit: adjust is not an object")
	}
	for tool, name := range toolNames {
		raw, ok := adjust[name]
		if !ok {
			continue
		}
		v, err := integer(raw)
		if err != nil {
			return Edit{}, fmt.Errorf("edit: %s is not an integer", name)
		}
		low := -100
		if tool == blackWhite {
			low = 0
		}
		e.adjust[tool] = min(max(v, low), 100)
	}
	if raw, ok := fields["crop"]; ok {
		if e.crop, err = parseCrop(raw); err != nil {
			return Edit{}, err
		}
	}
	return e, nil
}

// Removes says whether data, an edit not refused, is the photo as it came: null,
// or no tool set and nothing else. A crop that changes nothing is an edit: it is
// the photo as its file has it, not as it was turned here.
func Removes(data []byte) bool {
	e, err := Parse(data)
	if err != nil || !e.None() {
		return false
	}
	var fields, adjust map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || fields == nil {
		return true
	}
	// The two it must have: a crop, or a field not known here, is something.
	if len(fields) != 2 || json.Unmarshal(fields["adjust"], &adjust) != nil {
		return false
	}
	for name := range adjust {
		if !slices.Contains(toolNames[:], name) {
			return false // a tool not known here may do something elsewhere
		}
	}
	return true
}

func parseCrop(raw json.RawMessage) (crop, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return crop{}, errors.New("edit: crop is not an object")
	}
	var c crop
	var flip *bool
	var shape *string
	if json.Unmarshal(fields["flip"], &flip) != nil || flip == nil {
		return crop{}, errors.New("edit: crop flip is not true or false")
	}
	if json.Unmarshal(fields["shape"], &shape) != nil || shape == nil {
		return crop{}, errors.New("edit: crop shape is not a string")
	}
	c.flip = *flip
	for name, to := range map[string]*int{"turns": &c.turns, "angle": &c.angle} {
		v, err := integer(fields[name])
		if err != nil {
			return crop{}, fmt.Errorf("edit: crop %s is not an integer", name)
		}
		*to = v
	}
	c.turns &= 3
	c.angle = min(max(c.angle, -90), 90)
	for name, to := range map[string]*float32{"x": &c.x, "y": &c.y, "w": &c.w, "h": &c.h} {
		// A pointer tells null from 0; past 32 bits is an error, so infinity is too.
		var v *float32
		if err := json.Unmarshal(fields[name], &v); err != nil || v == nil {
			return crop{}, fmt.Errorf("edit: crop %s is not a number", name)
		}
		*to = *v
	}
	if c.w <= 0 || c.w > 1 || c.h <= 0 || c.h > 1 {
		return crop{}, errors.New("edit: crop frame out of the photo")
	}
	if c.turns == 0 && !c.flip && c.angle == 0 && c.w > 0.999 && c.h > 0.999 {
		return crop{}, nil
	}
	return c, nil
}

// integer reads a JSON integer; one past an int's range is its end.
func integer(raw json.RawMessage) (int, error) {
	n, err := strconv.Atoi(string(raw))
	if errors.Is(err, strconv.ErrRange) {
		err = nil
	}
	return n, err
}
