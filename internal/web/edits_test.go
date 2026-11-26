// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ruvido/homewend/internal/engine"
)

// The window's pictures of a photo with an edit are named by it, and kept for
// ever; a phone's stay the file's, and so does its list.
func TestTheWindowIsGivenTheEdit(t *testing.T) {
	lib, url := phonesLibrary(t)
	hash := shelve(t, lib, "ron/2026/11/IMG_1.JPG", jpegOf(300, 200), november)
	ron := pairWith(t, url, "ron")
	before := ron.is("thumb", "/v1/thumb/"+hash, http.StatusOK)
	tag, l := ron.list()
	page := httptest.NewServer(apiRoutes(nil, nil, nil, nil))
	defer page.Close()
	window := &testPhone{t: t, url: page.URL}
	edit := func() string {
		t.Helper()
		var list engine.PhotoList
		_, body := window.gets("/api/photos")
		if err := json.Unmarshal(body, &list); err != nil || len(list.Photos) != 1 {
			t.Fatalf("%s", body)
		}
		return list.Photos[0].Edit
	}
	if code, a := ron.call("POST", "/v1/marks", fmt.Sprintf(`{"shelf":%q,"after":0,"install":"A","states":["edit"],"marks":[{"n":1,"hash":%q,"state":"edit","ago":0,
		"value":{"adjust":{},"tools":2,"crop":{"turns":0,"flip":false,"angle":0,"x":0,"y":0,"w":0.5,"h":1,"shape":"Free"}}}]}`, l.Shelf, hash)); code != http.StatusOK {
		t.Fatalf("%d %+v", code, a)
	}
	thumbIs := func(edit string, want image.Point, kept bool) {
		t.Helper()
		res, body := window.gets("/api/thumb/" + hash + "?profile=ron&edit=" + edit)
		img, _, err := image.Decode(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("%s %v", body, err)
		}
		if got := img.Bounds().Size(); got != want || strings.Contains(res.Header.Get("Cache-Control"), "immutable") != kept {
			t.Errorf("%s: %v %s", edit, got, res.Header.Get("Cache-Control"))
		}
	}
	first := edit()
	thumbIs(first, image.Pt(150, 200), true)
	// Turned it has another name; asked by the last one, it is not kept.
	var turned engine.Turned
	if _, body := window.asks("POST", "/api/turn/"+hash+"?profile=ron"); json.Unmarshal(body, &turned) != nil || turned.Edit == "" || turned.Edit == first || edit() != turned.Edit {
		t.Fatalf("%s", body)
	}
	thumbIs(turned.Edit, image.Pt(200, 150), true)
	thumbIs(first, image.Pt(200, 150), false)
	thumbIs("../x", image.Pt(200, 150), false)
	if got := ron.is("thumb", "/v1/thumb/"+hash, http.StatusOK); !bytes.Equal(got, before) {
		t.Error("a phone's small copy is the edit's")
	}
	if now, _ := ron.list(); now != tag {
		t.Error("the phone's list moved")
	}
}
