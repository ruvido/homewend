// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/thumb"
)

// A phone's edit is drawn in the window's pictures of its library's photo:
// not in a phone's, not in another library's, and never in the file.
func TestAnEditIsShown(t *testing.T) {
	config(t)
	for _, cache := range []string{"XDG_CACHE_HOME", "LocalAppData"} {
		t.Setenv(cache, t.TempDir())
	}
	root := t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	// Red, blue on its right.
	img := image.NewRGBA(image.Rect(0, 0, 900, 600))
	for i := range 900 * 600 {
		c := color.RGBA{200, 0, 0, 255}
		if i%900 >= 450 {
			c = color.RGBA{0, 0, 200, 255}
		}
		img.SetRGBA(i%900, i/900, c)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	ron, ann := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}, Phone{ID: "p2", Name: "iPhone", Profile: "ann"}
	rons, hash := send(t, ron, "A.PNG", b.String())
	send(t, ann, "A.PNG", b.String())
	k, hers := library.Key{Hash: hash, Profile: "ron"}, library.Key{Hash: hash, Profile: "ann"}
	n := int64(0)
	say := func(phone Phone, value string) {
		t.Helper()
		n++
		time.Sleep(2 * time.Millisecond)
		ago := int64(0)
		a, err := Marks(phone, library.MarkQuestion{Shelf: shelfOf(t, phone).ID, Install: phone.ID, States: []library.State{library.Edit},
			Marks: []library.MarkAction{{N: n, Hash: hash, State: library.Edit, Value: json.RawMessage(value), Ago: &ago}}})
		if err != nil || len(a.Said) != 1 {
			t.Fatalf("%+v %v", a, err)
		}
	}
	tile := func(k library.Key) GridPhoto {
		t.Helper()
		list, err := Photos(Settings{}, "", "", "", false, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range copiesIn(list) {
			if p.Key() == k {
				return p
			}
		}
		t.Fatal("no tile")
		return GridPhoto{}
	}
	// is looks at a picture: its shape, and what is at its left.
	is := func(what, file string, err error, w, h int, left color.RGBA) {
		t.Helper()
		if err != nil {
			t.Fatal(what, err)
		}
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(what, err)
		}
		defer f.Close()
		img, _, err := image.Decode(f)
		if err != nil {
			t.Fatal(what, err)
		}
		got := color.RGBAModel.Convert(img.At(5, img.Bounds().Dy()/2)).(color.RGBA)
		off := func(a, b uint8) bool { return max(a, b)-min(a, b) > 12 }
		if img.Bounds().Dx() != w || img.Bounds().Dy() != h || off(got.R, left.R) || off(got.G, left.G) || off(got.B, left.B) {
			t.Errorf("%s: %v with %v at its left, want %dx%d with %v", what, img.Bounds().Size(), got, w, h, left)
		}
	}
	red, blue, grey := color.RGBA{200, 0, 0, 255}, color.RGBA{0, 0, 200, 255}, color.RGBA{97, 97, 97, 255}
	thumbnail := func(what string, k library.Key, edit string, kept bool, w, h int, left color.RGBA) {
		t.Helper()
		file, got, err := Thumbnail(ctx, k, edit, "")
		is(what, file, err, w, h, left)
		if got != kept {
			t.Errorf("%s: kept %v", what, got)
		}
	}
	edits := func() []string {
		t.Helper()
		folder, _ := picturesDir("thumbs")
		files, err := filepath.Glob(thumb.Path(folder, hash+"-*"))
		if err != nil {
			t.Fatal(err)
		}
		return files
	}

	if _, err := Turn(k, false); err != nil {
		t.Fatal(err)
	}
	const (
		rightHalf = `{"adjust":{},"tools":2,"crop":{"turns":0,"flip":false,"angle":0,"x":0.25,"y":0,"w":0.5,"h":1,"shape":"Free"}}`
		noColour  = `{"adjust":{"BlackWhite":100},"tools":2}`
	)
	say(ron, rightHalf)
	p := tile(k)
	if p.Edit == "" || p.Turn != 0 {
		t.Fatalf("%+v", p)
	}
	if h := tile(hers); h.Edit != "" || h.Turn != 0 {
		t.Errorf("hers %+v", h)
	}
	thumbnail("the right half", k, p.Edit, true, 192, 256, blue)
	thumbnail("hers", hers, "", true, 256, 170, red)
	thumbnail("hers, asked with his", hers, "00", false, 256, 170, red)
	file, _, err := PhoneThumbnail(ctx, ron, hash)
	is("a phone's", file, err, 256, 170, red)
	file, _, err = PhonePreview(ctx, ron, hash)
	is("a phone's preview", file, err, 900, 600, red)
	file, _, err = Preview(ctx, k, p.Edit, "")
	is("the preview", file, err, 450, 600, blue)
	if list, err := Latest(6); err != nil || !slices.ContainsFunc(copiesIn(list), func(c GridPhoto) bool { return c.Key() == k && c.Edit == p.Edit }) {
		t.Errorf("%+v %v", list, err)
	}

	// Turned left, it is another edit: the last one's pictures go.
	was := p.Edit
	now, err := Turn(k, false)
	if err != nil || now.Turn != 0 || now.Edit == "" || now.Edit == was || tile(k).Edit != now.Edit {
		t.Fatalf("%+v %v", now, err)
	}
	thumbnail("turned", k, now.Edit, true, 256, 192, blue)
	if left := edits(); len(left) != 1 || !strings.Contains(left[0], now.Edit) {
		t.Errorf("kept %v", left)
	}
	thumbnail("asked with the last one", k, was, false, 256, 192, blue)
	// Equal edits of two are one picture, and each other's are kept.
	say(ann, noColour)
	thumbnail("hers with no colour", hers, tile(hers).Edit, true, 256, 170, grey)
	if left := edits(); len(left) != 2 {
		t.Errorf("kept %v", left)
	}
	say(ron, noColour)
	thumbnail("his with no colour", k, tile(hers).Edit, true, 256, 170, grey)

	// One by a newer program is kept and not drawn: turned as it was.
	say(ron, `{"adjust":{},"tools":3}`)
	if p = tile(k); p.Edit != "" || p.Turn != 1 {
		t.Errorf("%+v", p)
	}
	thumbnail("a newer one's", k, "00", false, 256, 170, red)
	if got, err := Turn(k, false); err != nil || got != (Turned{Turn: 1}) {
		t.Errorf("%+v %v", got, err)
	}
	say(ron, "null")
	if p = tile(k); p.Edit != "" || p.Turn != 1 {
		t.Errorf("%+v", p)
	}

	// Seen for the first time, it has its own.
	say(ron, rightHalf)
	failed := map[string]bool{}
	if _, ok := makeThumbnails(ctx, "", failed); !ok || len(failed) > 0 {
		t.Errorf("%v", failed)
	}
	folder, _ := picturesDir("thumbs")
	if kept := thumb.Kept(folder); !kept[hash] || !kept[thumb.Edited(hash, tile(k).Edit)] {
		t.Errorf("%v", kept)
	}
	if got, err := os.ReadFile(filepath.Join(root, rons.Path)); err != nil || !bytes.Equal(got, b.Bytes()) {
		t.Error("the file was changed", err)
	}
	// Its file gone and nothing drawn yet: as it came.
	say(ron, `{"adjust":{"Exposure":9},"tools":2}`)
	os.Remove(filepath.Join(root, rons.Path))
	if path, err := PathOf(hers); err == nil {
		os.Remove(path)
	}
	thumbnail("gone", k, tile(k).Edit, false, 256, 170, red)
	makeThumbnails(ctx, "", failed)
	if len(failed) != 1 {
		t.Errorf("%v", failed)
	}
}

// What a picture is drawn from leaves enough in the edit's frame.
func TestASmallFrameIsDrawnFromEnough(t *testing.T) {
	config(t)
	for _, cache := range []string{"XDG_CACHE_HOME", "LocalAppData"} {
		t.Setenv(cache, t.TempDir())
	}
	root := t.TempDir()
	if err := SetDefaultLibrary(root); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 4000, 1000))); err != nil {
		t.Fatal(err)
	}
	ron := Phone{ID: "p1", Name: "Pixel", Profile: "ron"}
	_, hash := send(t, ron, "A.PNG", b.String())
	ago := int64(0)
	if _, err := Marks(ron, library.MarkQuestion{Shelf: shelfOf(t, ron).ID, Install: "A", States: []library.State{library.Edit},
		Marks: []library.MarkAction{{Hash: hash, State: library.Edit, Ago: &ago,
			Value: json.RawMessage(`{"adjust":{},"tools":2,"crop":{"turns":1,"flip":false,"angle":0,"x":0,"y":0,"w":0.5,"h":0.1,"shape":"Free"}}`)}}}); err != nil {
		t.Fatal(err)
	}
	list, err := Photos(Settings{}, "", "", "", false, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	k := library.Key{Hash: hash, Profile: "ron"}
	small, _, err := Thumbnail(context.Background(), k, list.Photos[0].Edit, "")
	if err != nil {
		t.Fatal(err)
	}
	large, _, err := Preview(context.Background(), k, list.Photos[0].Edit, "")
	if err != nil {
		t.Fatal(err)
	}
	if w, h := thumb.Dimensions(small); max(w, h) != thumb.Size {
		t.Errorf("%d x %d", w, h)
	}
	if w, h := thumb.Dimensions(large); w != 500 || h != 400 {
		t.Errorf("%d x %d", w, h)
	}
}
