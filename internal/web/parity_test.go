// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
)

// A phone that keeps copies says them, and hears them with their names; one
// that names none speaks of the photo; a copy made is in the list.
func TestAPhonesCopies(t *testing.T) {
	newLibrary(t)
	srv := httptest.NewServer(phoneRoutes(&phoneListener{port: 1}))
	defer srv.Close()
	ron := pairWith(t, srv.URL, "ron")
	if _, a := ron.call("GET", "/v1/hello", ""); !a.Copies || !slices.Contains(a.Features, "dates") || !slices.Contains(a.Features, "empty") {
		t.Errorf("hello: %+v", a)
	}
	one := ron.sends("one")
	_, have := ron.call("POST", "/v1/have", `{"hashes":[]}`)
	if m := ron.month("none"); len(m.Photos) != 1 {
		t.Fatalf("%+v", m)
	}
	code, a := ron.says(have.Shelf, 0, `{"n":1,"hash":"`+one+`","copy":"c1","state":"favorite","value":true,"ago":5}`)
	if code != http.StatusOK || len(a.Said) != 1 || a.Said[0].Copy != "c1" || len(a.Marks) != 1 || a.Marks[0].Copy != "c1" {
		t.Errorf("a copy's heart: %d %+v", code, a)
	}
	// An older phone's heart is the photo's.
	if _, a = ron.says(have.Shelf, 0, said(2, one, true, 0)); len(a.Said) != 1 || a.Said[0].Copy != "" || len(a.Marks) != 2 {
		t.Errorf("the photo's heart: %+v", a)
	}
	m := ron.month("none")
	if len(m.Photos) != 2 || m.Photos[0].Copy != "" || m.Photos[1].Copy != "c1" || m.Photos[1].Hash != one {
		t.Errorf("listed: %+v", m)
	}
}

// A date said by a phone files its photo as the window's Change date; null
// takes it back to the file's own.
func TestAPhonesDate(t *testing.T) {
	lib := newLibrary(t)
	srv := httptest.NewServer(phoneRoutes(&phoneListener{port: 1}))
	defer srv.Close()
	ron := pairWith(t, srv.URL, "ron")
	one := ron.sends("one")
	_, have := ron.call("POST", "/v1/have", `{"hashes":[]}`)
	date := func(n int, value string) string {
		return fmt.Sprintf(`{"n":%d,"hash":%q,"state":"date","value":%s,"ago":0}`, n, one, value)
	}
	ask := func(marks ...string) answered {
		t.Helper()
		code, a := ron.call("POST", "/v1/marks", fmt.Sprintf(`{"shelf":%q,"install":"A","states":["favorite","date"],"marks":[%s]}`, have.Shelf, strings.Join(marks, ",")))
		if code != http.StatusOK {
			t.Fatalf("%d %+v", code, a)
		}
		return a
	}
	const may = `{"taken":1715000000000,"zone":"+02:00"}`
	if a := ask(date(1, may)); len(a.Said) != 1 || string(a.Said[0].Value) != may {
		t.Errorf("%+v", a)
	}
	// Moved beside the answer, in the library's turn.
	filed := func(month string) {
		t.Helper()
		for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if res, _ := ron.gets("/v1/library/" + month); res.StatusCode == http.StatusOK {
				return
			}
		}
		t.Fatalf("%s: never filed", month)
	}
	filed("2024-05")
	m := ron.month("2024-05")
	if len(m.Photos) != 1 || m.Photos[0].Taken != "2024-05-06T14:53:20" {
		t.Errorf("%+v", m)
	}
	files, _ := filepath.Glob(filepath.Join(lib, "ron", "2024", "05", "*"))
	if len(files) != 2 { // the photo and its .xmp
		t.Errorf("filed: %v", files)
	}
	ask(date(2, "null"))
	filed("none")
	if m := ron.month("none"); len(m.Photos) != 1 {
		t.Errorf("back: %+v", m)
	}
	if files, _ := filepath.Glob(filepath.Join(lib, "ron", "undated", "*")); len(files) != 1 {
		t.Errorf("back: %v", files)
	}
}

// Emptied on the phone: what is in this library's Trash and was not restored
// since goes, by the window's Empty; another library's file stays.
func TestAPhoneEmptiesTheTrash(t *testing.T) {
	lib := newLibrary(t)
	srv := httptest.NewServer(phoneRoutes(&phoneListener{port: 1}))
	defer srv.Close()
	ron, ann := pairWith(t, srv.URL, "ron"), pairWith(t, srv.URL, "ann")
	one, back, again, live := ron.sends("one"), ron.sends("back"), ron.sends("again"), ron.sends("live")
	ann.sends("one")
	hashes := func(h ...string) string { return `{"hashes":["` + strings.Join(h, `","`) + `"]}` }
	ron.call("POST", "/v1/trash", hashes(one, back, again))
	ron.call("POST", "/v1/untrash", hashes(back, again))
	ron.call("POST", "/v1/trash", hashes(again))
	stillTrash(t, lib)
	nowhere := strings.Repeat("0", 64)
	body := fmt.Sprintf(`{"photos":[{"hash":%q,"ago":0},{"hash":%q,"ago":60000},{"hash":%q,"ago":60000},{"hash":%q,"ago":0},{"hash":%q,"ago":0}]}`, one, back, again, live, nowhere)
	for range 2 {
		code, a := ron.call("POST", "/v1/trash/empty", body)
		slices.Sort(a.Kept)
		want := []string{back, again, live}
		slices.Sort(want)
		if code != http.StatusOK || !slices.Equal(a.Emptied, []string{one}) || !slices.Equal(a.Kept, want) {
			t.Errorf("%d %+v", code, a)
		}
	}
	if _, a := ann.call("POST", "/v1/have", hashes(one)); !slices.Equal(a.Have, []string{one}) {
		t.Errorf("ann's: %+v", a)
	}
	if _, a := ron.call("POST", "/v1/have", hashes(one, again)); len(a.Have) != 0 {
		t.Errorf("ron's: %+v", a)
	}
	// "again", restored after the phone saw it and put back, is still there.
	if files, _ := filepath.Glob(filepath.Join(lib, library.TrashDir, "ron", "*", "*")); len(files) != 1 {
		t.Errorf("in the Trash: %v", files)
	}
	if code, _ := ron.call("POST", "/v1/trash/empty", `{"photos":[{"hash":"x","ago":0}]}`); code != http.StatusBadRequest {
		t.Errorf("not a hash: %d", code)
	}
}
