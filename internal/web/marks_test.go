// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/engine"
	"github.com/ruvido/homewend/internal/library"
)

// testPhone is a phone that speaks to the phones' listener at url.
type testPhone struct {
	t          *testing.T
	url, token string
	id         string
}

// answered is what a call was: any of the fields it has.
type answered struct {
	Error                string
	Features             []string
	Phone, Token         string
	Shelf                string
	Cursor               *int64
	Owner                string
	Other, More, Changed bool
	List                 bool
	Said, Marks          []library.Mark
	Unknown              []string
	Refused              []int64
	Have                 []string
}

func (p *testPhone) call(method, path, body string) (int, answered) {
	p.t.Helper()
	code, a, err := p.try(context.Background(), method, path, body)
	if err != nil {
		p.t.Fatal(err)
	}
	return code, a
}

func (p *testPhone) try(ctx context.Context, method, path, body string) (int, answered, error) {
	var a answered
	r, err := http.NewRequestWithContext(ctx, method, p.url+path, strings.NewReader(body))
	if err != nil {
		return 0, a, err
	}
	if p.token != "" {
		r.Header.Set("Authorization", "Bearer "+p.token)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, a, err
	}
	defer res.Body.Close()
	json.NewDecoder(res.Body).Decode(&a)
	return res.StatusCode, a, nil
}

// wait is a held question, its status and answer when they come.
func (p *testPhone) wait(ctx context.Context, shelf string, after int64, states ...string) <-chan answered {
	out := make(chan answered, 1)
	go func() {
		code, a, err := p.try(ctx, "GET", fmt.Sprintf("/v1/marks?shelf=%s&after=%d&states=%s", shelf, after, strings.Join(states, ",")), "")
		if err != nil {
			a.Error = err.Error()
		} else if code != http.StatusOK {
			a.Error = fmt.Sprint(code)
		}
		out <- a
	}()
	// Held, when it is to be.
	time.Sleep(150 * time.Millisecond)
	return out
}

func (p *testPhone) ends(what string, held <-chan answered, changed bool) answered {
	p.t.Helper()
	select {
	case a := <-held:
		if a.Changed != changed {
			p.t.Errorf("%s: %+v, want changed %v", what, a, changed)
		}
		return a
	case <-time.After(4 * time.Second):
		p.t.Errorf("%s: still held", what)
		return answered{}
	}
}

// newLibrary is a library with nothing in it, chosen.
func newLibrary(t *testing.T) string {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AppData", cfg)
	lib := t.TempDir()
	if err := engine.SetDefaultLibrary(lib); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stillTrash(t, lib) })
	return lib
}

// stillTrash waits for the files the Trash moves beside the calls, which would
// find the test's library gone.
func stillTrash(t *testing.T, lib string) {
	path := filepath.Join(lib, library.WorkDir, "catalog.db")
	if _, err := os.Stat(path); err != nil {
		return
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		moving := 1
		err := db.QueryRow(`SELECT COUNT(*) FROM library_photos WHERE out = 0 AND (trashed = 1) <> (path LIKE '.trash/%')`).Scan(&moving)
		_, journal := os.Stat(filepath.Join(lib, library.WorkDir, "trash-moves.json"))
		if err == nil && moving == 0 && journal != nil {
			return
		}
	}
}

func pairWith(t *testing.T, url, profile string) *testPhone {
	t.Helper()
	p := &testPhone{t: t, url: url}
	code, err := engine.StartPairing(profile)
	if err != nil {
		t.Fatal(err)
	}
	status, a := p.call("POST", "/v1/pair", `{"code":"`+code+`","name":"Pixel"}`)
	if status != http.StatusOK || !slices.Contains(a.Features, "marks") || !slices.Contains(a.Features, "edits") {
		t.Fatalf("pairing: %d %+v", status, a)
	}
	p.token, p.id = a.Token, a.Phone
	return p
}

func (p *testPhone) sends(body string) (hash string) {
	p.t.Helper()
	sum := sha256.Sum256([]byte(body))
	hash = hex.EncodeToString(sum[:])
	if code, a := p.call("PUT", "/v1/photo/"+hash+"?name=IMG.JPG", body); code != http.StatusCreated && code != http.StatusOK {
		p.t.Fatalf("sent: %d %+v", code, a)
	}
	return hash
}

func (p *testPhone) says(shelf string, after int64, marks ...string) (int, answered) {
	p.t.Helper()
	return p.call("POST", "/v1/marks", fmt.Sprintf(`{"shelf":%q,"after":%d,"install":%q,"marks":[%s]}`, shelf, after, p.id, strings.Join(marks, ",")))
}

func said(n int, hash string, on bool, ago int) string {
	return fmt.Sprintf(`{"n":%d,"hash":%q,"state":"favorite","value":%t,"ago":%d}`, n, hash, on, ago)
}

// A phone and the window give hearts to each other, over the calls the
// contract names.
func TestAPhonesHeartsAreTheWindows(t *testing.T) {
	lib := newLibrary(t)
	srv := httptest.NewServer(phoneRoutes(&phoneListener{port: 1}))
	defer srv.Close()
	ron, ann := pairWith(t, srv.URL, "ron"), pairWith(t, srv.URL, "ann")
	if _, a := ron.call("GET", "/v1/hello", ""); !slices.Contains(a.Features, "marks") || !slices.Contains(a.Features, "edits") {
		t.Errorf("hello: %+v", a)
	}
	// Nothing is in the library yet: it has no marks to speak of.
	if code, _ := ron.says("", 0); code != http.StatusServiceUnavailable {
		t.Errorf("an empty library: %d, want 503", code)
	}
	if code, a := ron.call("POST", "/v1/have", `{"hashes":[]}`); code != http.StatusOK || a.Cursor != nil {
		t.Errorf("have, an empty library: %d %+v", code, a)
	}
	one, two := ron.sends("one"), ron.sends("two")
	ann.sends("one")

	_, have := ron.call("POST", "/v1/have", `{"hashes":["`+one+`"]}`)
	if len(have.Have) != 1 || have.Shelf == "" || have.Cursor == nil || *have.Cursor != 0 {
		t.Fatalf("have: %+v", have)
	}
	shelf := have.Shelf
	// What the phone says the window shows; it hears its own, and what is not here.
	nowhere := strings.Repeat("0", 64)
	code, a := ron.says(shelf, 0, said(1, one, true, 10), said(2, nowhere, true, 10),
		`{"n":3,"hash":"`+two+`","state":"edit","value":{"a":[1]},"ago":5}`)
	if code != http.StatusOK || a.Other || a.More || a.Owner != "ron" || a.Shelf != shelf || *a.Cursor != 1 || len(a.Said) != 1 || string(a.Said[0].Value) != "true" ||
		len(a.Marks) != 1 || !reflect.DeepEqual(a.Marks, a.Said) || !slices.Equal(a.Unknown, []string{nowhere}) {
		t.Errorf("said: %d %+v", code, a)
	}
	list, err := engine.Photos(engine.Settings{}, "", "", "", false, "", 0)
	hearts := 0
	for _, p := range list.Photos {
		if p.Favorite {
			hearts++
		}
		if p.Favorite != (p.Hash == one && p.Profile == "ron") {
			t.Errorf("the window shows %+v", p)
		}
	}
	if err != nil || hearts != 1 {
		t.Errorf("%d hearts in the window, %v", hearts, err)
	}
	// The window's, in ann's library: ann's phone is told at once, ron's is not.
	_, annHave := ann.call("POST", "/v1/have", `{"hashes":[]}`)
	rons, anns := ron.wait(context.Background(), shelf, 1), ann.wait(context.Background(), annHave.Shelf, 0)
	before, err := engine.LibraryChanges()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Mark(library.Favorite, []library.Key{{Hash: one, Profile: "ann"}}, true); err != nil {
		t.Fatal(err)
	}
	ann.ends("a heart of ann's", anns, true)
	if _, a := ann.says(annHave.Shelf, 0); len(a.Marks) != 1 || a.Marks[0].Hash != one || string(a.Marks[0].Value) != "true" {
		t.Errorf("ann hears %+v", a)
	}
	// The page follows LibraryChanges: a heart that comes in moves it.
	if now, _ := engine.LibraryChanges(); now == before {
		t.Error("the window is not told of a heart")
	}
	select {
	case a := <-rons:
		t.Errorf("ron's phone was told of ann's heart: %+v", a)
	default:
	}
	// Asked again, the question before ends.
	again := ron.wait(context.Background(), shelf, 1)
	ron.ends("asked again", rons, false)
	// The phone leaves the app.
	ctx, leave := context.WithCancel(context.Background())
	gone := ron.wait(ctx, shelf, 1)
	ron.ends("asked once more", again, false)
	leave()
	ron.ends("left", gone, false)
	// Another shelf: the phone decides, nothing is applied.
	if code, a := ron.says(annHave.Shelf, 0, said(4, two, true, 0)); code != http.StatusOK || !a.Other || a.Shelf != shelf || a.Owner != "ron" || *a.Cursor != 0 || a.Said == nil || a.Marks == nil || a.Unknown == nil {
		t.Errorf("another shelf: %d %+v", code, a)
	}

	// The disk away: 503, and not an empty answer to what was queued.
	held := ron.wait(context.Background(), shelf, 1)
	if err := os.Rename(lib, lib+"-away"); err != nil {
		t.Fatal(err)
	}
	if a := ron.ends("the disk away", held, false); a.Error != "503" {
		t.Errorf("held, the disk away: %+v", a)
	}
	if code, _ := ron.says(shelf, 1); code != http.StatusServiceUnavailable {
		t.Errorf("the disk away: %d", code)
	}
	for _, path := range []string{"have", "archive", "unarchive", "trash", "untrash"} {
		if code, a := ron.call("POST", "/v1/"+path, `{"hashes":["`+one+`"]}`); code != http.StatusServiceUnavailable {
			t.Errorf("%s, the disk away: %d %+v", path, code, a)
		}
	}
	if err := os.Rename(lib+"-away", lib); err != nil {
		t.Fatal(err)
	}

	held = ron.wait(context.Background(), shelf, 1)
	if code, _ := ron.call("POST", "/v1/unpair", ""); code != http.StatusOK {
		t.Errorf("unpair: %d", code)
	}
	ron.ends("unpaired", held, false)
	for _, method := range []string{"GET", "POST"} {
		if code, _ := ron.call(method, "/v1/marks?after=0", "{}"); code != http.StatusUnauthorized {
			t.Errorf("%s unpaired: %d", method, code)
		}
	}
	if code, _ := (&testPhone{t: t, url: srv.URL}).call("POST", "/v1/marks", "{}"); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
}

// What a hostile phone sends is refused, and changes nothing.
func TestAHostilePhonesMarks(t *testing.T) {
	newLibrary(t)
	srv := httptest.NewServer(phoneRoutes(&phoneListener{port: 1}))
	defer srv.Close()
	p := pairWith(t, srv.URL, "ron")
	one := p.sends("one")
	_, have := p.call("POST", "/v1/have", `{"hashes":[]}`)
	shelf := have.Shelf
	many := make([]string, 501)
	for i := range many {
		many[i] = said(i, one, true, 0)
	}
	if code, _ := p.says(shelf, 0, many...); code != http.StatusBadRequest {
		t.Errorf("501 marks: %d", code)
	}
	if code, _ := p.says(shelf, 0, strings.Repeat(" ", maxAsk)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("over 128 KB: %d", code)
	}
	for _, body := range []string{"", "[]", "{", `{"marks":{}}`, `{"after":-1}`, `{"after":1.5}`, `{"after":9223372036854775808}`, `{"shelf":7}`,
		`{"shelf":"x"} trailing`, `{"marks":[{"hash":7}]}`, `{"marks":[null]}`,
		`{"shelf":"` + shelf + `","install":"A","marks":[{"n":9223372036854775808,"hash":"` + one + `","state":"favorite","value":true}]}`,
		`{"shelf":"` + shelf + `","install":"A","marks":[{"n":1,"hash":"` + one + `","state":"favorite","value":"true"}]}`,
		`{"shelf":"` + shelf + `","marks":[` + said(1, one, true, 0) + `]}`,
		strings.Repeat("[", 20_000),
	} {
		if code, _ := p.call("POST", "/v1/marks", body); code != http.StatusBadRequest {
			t.Errorf("%.80s: %d", body, code)
		}
	}
	for _, query := range []string{"", "?after=", "?after=-1", "?after=x", "?after=9223372036854775808"} {
		if code, _ := p.call("GET", "/v1/marks"+query, ""); code != http.StatusBadRequest {
			t.Errorf("GET %s: %d", query, code)
		}
	}
	if code, a := p.call("POST", "/v1/have", `{"hashes":[]}`); code != http.StatusOK || *a.Cursor != 0 {
		t.Errorf("%d %+v, want nothing applied", code, a)
	}
	// The greatest numbers there are.
	for _, m := range []string{`{"n":9223372036854775807,"hash":"` + one + `","state":"favorite","value":true,"ago":9223372036854775807}`,
		`{"n":9223372036854775807,"hash":"` + one + `","state":"favorite","value":false,"ago":-9223372036854775808}`} {
		if code, a := p.says(shelf, 0, m); code != http.StatusOK || len(a.Said) != 1 || string(a.Said[0].Value) != "true" {
			t.Errorf("%d %+v", code, a)
		}
	}
	if _, a := p.call("GET", "/v1/marks?shelf="+shelf+"&after=9223372036854775807", ""); !a.Changed {
		t.Error("a seq not reached is another shelf's")
	}
}

// Quitting does not wait for a phone's question.
func TestQuittingEndsAQuestion(t *testing.T) {
	newLibrary(t)
	s := newTestServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l := &phoneListener{port: 1, ln: ln}
	l.srv = &http.Server{Handler: phoneRoutes(l), BaseContext: func(net.Listener) context.Context { return s.ctx }}
	s.phones = l
	served := make(chan error, 1)
	go func() { served <- s.Serve() }()
	p := pairWith(t, "http://"+ln.Addr().String(), "ron")
	p.sends("one")
	_, have := p.call("POST", "/v1/have", `{"hashes":[]}`)
	held := p.wait(context.Background(), have.Shelf, 0)
	began := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Error(err)
	}
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("it took %v", took)
	}
	p.ends("quitting", held, false)
	if err := <-served; err != nil {
		t.Error(err)
	}
}

// Two phones of one library hear each other's edits, as they were said; one
// from before edits is neither told of one nor carries it.
func TestAPhonesEdits(t *testing.T) {
	newLibrary(t)
	srv := httptest.NewServer(phoneRoutes(&phoneListener{port: 1}))
	defer srv.Close()
	first, second, old := pairWith(t, srv.URL, "ron"), pairWith(t, srv.URL, "ron"), pairWith(t, srv.URL, "ron")
	one := first.sends("one")
	_, have := first.call("POST", "/v1/have", `{"hashes":[]}`)
	shelf := have.Shelf
	asks := func(p *testPhone, after int64, marks ...string) (int, answered) {
		return p.call("POST", "/v1/marks", fmt.Sprintf(`{"shelf":%q,"after":%d,"install":%q,"states":["favorite","edit"],"marks":[%s]}`, shelf, after, p.id, strings.Join(marks, ",")))
	}
	edit := func(n int, value string) string {
		return fmt.Sprintf(`{"n":%d,"hash":%q,"state":"edit","value":%s,"ago":0}`, n, one, value)
	}
	const newer = `{"tools":9,"by":"<a&b>","x":[1.50,null]}`
	told, untold := second.wait(context.Background(), shelf, 0, "favorite", "edit"), old.wait(context.Background(), shelf, 0)
	code, a := asks(first, 0, edit(1, newer), edit(2, `{"tools":1}`), said(3, one, true, 0))
	if code != http.StatusOK || len(a.Said) != 2 || !slices.Equal(a.Refused, []int64{2}) || string(a.Said[0].Value) != newer {
		t.Errorf("%d %+v", code, a)
	}
	second.ends("an edit", told, true)
	old.ends("a heart", untold, true)
	if _, a := asks(second, 0); len(a.Marks) != 2 || string(a.Marks[0].Value) != newer || a.Marks[0].State != library.Edit || *a.Cursor != 2 {
		t.Errorf("heard: %+v", a)
	}
	if _, a := old.says(shelf, 0); len(a.Marks) != 1 || a.Marks[0].State != library.Favorite || *a.Cursor != 2 {
		t.Errorf("an older phone hears %+v", a)
	}
	// Removed: null to the one, nothing to the other.
	told, untold = second.wait(context.Background(), shelf, 2, "favorite", "edit"), old.wait(context.Background(), shelf, 2)
	if _, a := asks(first, 2, edit(4, `null`)); len(a.Said) != 1 || string(a.Said[0].Value) != "null" || a.Said[0].State != library.Edit {
		t.Errorf("removed: %+v", a)
	}
	second.ends("removed", told, true)
	if code, _ := asks(first, 3, edit(5, `null}`)); code != http.StatusBadRequest {
		t.Errorf("not JSON: %d", code)
	}
	if code, _ := asks(first, 3, edit(5, `{"tools":2,"x":"`+strings.Repeat("y", 70_000)+`"}`)); code != http.StatusOK {
		t.Errorf("a long one: %d", code)
	}
	select {
	case a := <-untold:
		t.Errorf("an older phone was told of an edit: %+v", a)
	case <-time.After(1200 * time.Millisecond): // a look at what another process wrote too
	}
	srv.CloseClientConnections()
}
