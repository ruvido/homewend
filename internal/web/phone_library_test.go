// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/engine"
	"github.com/ruvido/homewend/internal/library"
)

// gets asks for path, with headers as "name: value".
func (p *testPhone) gets(path string, headers ...string) (*http.Response, []byte) {
	p.t.Helper()
	return p.asks("GET", path, headers...)
}

func (p *testPhone) asks(method, path string, headers ...string) (*http.Response, []byte) {
	p.t.Helper()
	r, err := http.NewRequest(method, p.url+path, nil)
	if err != nil {
		p.t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+p.token)
	r.Header.Set("Accept-Encoding", "identity")
	for _, h := range headers {
		name, value, _ := strings.Cut(h, ": ")
		r.Header.Set(name, value)
	}
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		p.t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	return res, body
}

func (p *testPhone) list() (tag string, l library.Listing) {
	p.t.Helper()
	res, body := p.gets("/v1/library")
	if err := json.Unmarshal(body, &l); err != nil || res.StatusCode != http.StatusOK {
		p.t.Fatalf("the list: %d %s", res.StatusCode, body)
	}
	return res.Header.Get("ETag"), l
}

// aMonth is a month's answer.
type aMonth struct {
	Month, Tag string
	Photos     []aPhoto
}

type aPhoto struct {
	Hash, Taken, Name, Type string
	Bytes                   int64
	Turn                    int
	Archived                bool
	Albums                  []string
}

func (p *testPhone) month(month string) (m aMonth) {
	p.t.Helper()
	res, body := p.gets("/v1/library/" + month)
	if err := json.Unmarshal(body, &m); err != nil || res.StatusCode != http.StatusOK {
		p.t.Fatalf("%s: %d %s", month, res.StatusCode, body)
	}
	return m
}

func (p *testPhone) is(what, path string, status int) []byte {
	p.t.Helper()
	res, body := p.gets(path)
	if res.StatusCode != status {
		p.t.Errorf("%s: %s is %d %s, want %d", what, path, res.StatusCode, body, status)
	}
	return body
}

// shelve puts a photo in the library at lib: its file, and what is known of it.
func shelve(t *testing.T, lib, path string, body []byte, taken time.Time, albums ...string) string {
	t.Helper()
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	file := filepath.Join(lib, filepath.FromSlash(path))
	for _, dir := range []string{filepath.Dir(file), filepath.Join(lib, library.WorkDir)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(file, body, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := library.OpenCatalog(filepath.Join(lib, library.WorkDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	profile, _, _ := strings.Cut(path, "/")
	if err := c.Put(library.Photo{Hash: hash, Profile: profile, Path: path, Name: filepath.Base(path), Bytes: int64(len(body)), Taken: taken, Albums: albums}); err != nil {
		t.Fatal(err)
	}
	return hash
}

func jpegOf(w, h int) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h)), nil)
	return b.Bytes()
}

func phonesLibrary(t *testing.T) (lib, url string) {
	t.Helper()
	lib = newLibrary(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("LocalAppData", t.TempDir())
	srv := httptest.NewServer(phoneRoutes(&phoneListener{port: 1}))
	t.Cleanup(srv.Close)
	return lib, srv.URL
}

var november = time.Date(2026, 11, 20, 21, 53, 42, 0, time.UTC)

// A phone lists its owner's library, by month as the window does, and
// nobody else's.
func TestAPhoneListsItsLibrary(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron, ann := pairWith(t, url, "ron"), pairWith(t, url, "ann")
	if _, a := ron.call("GET", "/v1/hello", ""); !slices.Contains(a.Features, "library") {
		t.Errorf("hello: %+v", a)
	}
	ron.is("an empty library", "/v1/library", http.StatusServiceUnavailable)
	ron.is("an empty library", "/v1/library/none", http.StatusServiceUnavailable)

	sea := shelve(t, lib, "ron/2026/11/20261120_215342.jpg", []byte("sea"), november, "Sea", "A & <b>")
	same := shelve(t, lib, "ron/2026/11/clip.MP4", []byte("same"), november, "Sea")
	last := shelve(t, lib, "ron/2026/10/last.heic", []byte("last"), time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC), "Sea")
	first := shelve(t, lib, "ron/2026/11/first.jpg", []byte("first"), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	none := shelve(t, lib, "ron/undated/none.jpg", []byte("none"), time.Time{}, "Nowhere")
	old := shelve(t, lib, "ron/1969/07/moon.jpg", []byte("moon"), time.Date(1969, 7, 20, 20, 17, 0, 0, time.UTC))
	trashed := shelve(t, lib, "ron/2026/11/trashed.jpg", []byte("trashed"), november.Add(time.Hour), "Sea", "Binned")
	out := shelve(t, lib, "ron/2026/11/out.jpg", []byte("out"), november.Add(time.Hour), "Sea")
	shelve(t, lib, "ann/2026/11/anns.jpg", []byte("anns"), november, "Sea")
	shelve(t, lib, "ann/2026/11/sea.jpg", []byte("sea"), november)
	if _, err := engine.Mark(library.Trashed, []library.Key{{Hash: trashed, Profile: "ron"}}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Mark(library.Archived, []library.Key{{Hash: last, Profile: "ron"}, {Hash: sea, Profile: "ann"}}, true); err != nil {
		t.Fatal(err)
	}
	c, err := library.OpenCatalog(filepath.Join(lib, library.WorkDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.TakeOut([]library.Key{{Hash: out, Profile: "ron"}}, true); err != nil {
		t.Fatal(err)
	}
	c.Close()
	// Its file there, it would be in again.
	if err := os.Remove(filepath.Join(lib, "ron/2026/11/out.jpg")); err != nil {
		t.Fatal(err)
	}

	tag, l := ron.list()
	_, have := ron.call("POST", "/v1/have", `{"hashes":[]}`)
	if l.Shelf == "" || l.Shelf != have.Shelf {
		t.Errorf("the shelf %q, the marks' %q", l.Shelf, have.Shelf)
	}
	var months []string
	for _, m := range l.Months {
		months = append(months, fmt.Sprint(m.Month, " ", m.Count))
		if got := ron.month(m.Month); got.Tag != m.Tag || got.Month != m.Month || len(got.Photos) != m.Count || len(m.Tag) == 0 {
			t.Errorf("%+v, its answer %+v", m, got)
		}
	}
	if want := []string{"2026-11 3", "2026-10 1", "1969-07 1", "none 1"}; !slices.Equal(months, want) {
		t.Errorf("months %v, want %v", months, want)
	}
	// As the window's list groups them.
	list, err := engine.Photos(engine.Settings{}, "", "", "", false, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	more, err := engine.Photos(engine.Settings{}, "", "", "", false, engine.ViewArchive, 0)
	if err != nil {
		t.Fatal(err)
	}
	list.Photos = append(list.Photos, more.Photos...)
	window := map[string]int{}
	for _, p := range list.Photos {
		if p.Profile == "ron" {
			b, _ := json.Marshal(p.Taken)
			window[map[bool]string{true: "none", false: string(b[1:8])}[p.Taken.IsZero()]]++
		}
	}
	for _, m := range l.Months {
		if window[m.Month] != m.Count {
			t.Errorf("%s: %d, the window's %v", m.Month, m.Count, window)
		}
	}
	var albums []string
	ids := map[string]string{}
	for _, a := range l.Albums {
		albums = append(albums, fmt.Sprint(a.Name, " ", a.Count))
		ids[a.Name] = a.ID
	}
	newest := min(sea, same)
	if want := []string{"A & <b> 1", "Nowhere 1", "Sea 3"}; !slices.Equal(albums, want) || l.Albums[2].Cover != newest || l.Albums[1].Cover != none {
		t.Errorf("albums %+v, want %v", l.Albums, want)
	}
	m := ron.month("2026-11")
	if m.Photos[0].Hash != newest || m.Photos[1].Hash != max(sea, same) || m.Photos[2].Hash != first || m.Photos[2].Taken != "2026-11-01T00:00:00" || m.Photos[2].Albums == nil {
		t.Errorf("in order: %+v", m.Photos)
	}
	for _, p := range m.Photos {
		switch {
		case p.Hash == sea && (p.Taken != "2026-11-20T21:53:42" || p.Name != "20261120_215342.jpg" || p.Type != "image/jpeg" || p.Bytes != 3 || p.Turn != 0 || p.Archived || !slices.Equal(p.Albums, []string{ids["A & <b>"], ids["Sea"]})):
			t.Errorf("sea: %+v", p)
		case p.Hash == same && p.Type != "video/mp4":
			t.Errorf("a video: %+v", p)
		}
	}
	if m := ron.month("2026-10"); !m.Photos[0].Archived || m.Photos[0].Type != "image/heic" {
		t.Errorf("archived: %+v", m)
	}
	if m := ron.month("none"); m.Photos[0].Taken != "" || m.Photos[0].Hash != none {
		t.Errorf("no date: %+v", m)
	}
	if m := ron.month("1969-07"); m.Photos[0].Hash != old {
		t.Errorf("before 1970: %+v", m)
	}
	// Ann's, the same content with its own state, under other ids.
	if _, l := ann.list(); len(l.Months) != 1 || l.Months[0].Count != 2 || len(l.Albums) != 1 || l.Albums[0].Count != 1 || l.Albums[0].ID == ids["Sea"] {
		t.Errorf("ann's list: %+v", l)
	}

	// Each change moves its month alone.
	moved := func(what string, want ...string) {
		t.Helper()
		was := map[string]string{}
		for _, m := range l.Months {
			was[m.Month] = m.Tag
		}
		var moved []string
		var now string
		now, l = ron.list()
		for _, m := range l.Months {
			if was[m.Month] != m.Tag {
				moved = append(moved, m.Month)
			}
			delete(was, m.Month)
		}
		for gone := range was {
			moved = append(moved, "no "+gone)
		}
		if !slices.Equal(moved, want) || (now != tag) != (len(want) > 0) {
			t.Errorf("%s: moved %v, want %v", what, moved, want)
		}
		tag = now
	}
	moved("nothing")
	key := func(hash string) library.Key { return library.Key{Hash: hash, Profile: "ron"} }
	if _, err := engine.TurnLeft(key(sea)); err != nil {
		t.Fatal(err)
	}
	moved("turned", "2026-11")
	if _, err := engine.Mark(library.Archived, []library.Key{key(last)}, false); err != nil {
		t.Fatal(err)
	}
	moved("out of the archive", "2026-10")
	if _, err := engine.Mark(library.Favorite, []library.Key{key(last)}, true); err != nil {
		t.Fatal(err)
	}
	moved("a heart")
	if _, err := engine.Mark(library.Trashed, []library.Key{key(old)}, true); err != nil {
		t.Fatal(err)
	}
	moved("trashed", "no 1969-07")
	ron.is("emptied", "/v1/library/1969-07", http.StatusNotFound)
	if _, err := engine.Mark(library.Trashed, []library.Key{key(old)}, false); err != nil {
		t.Fatal(err)
	}
	moved("restored", "1969-07")
	shelve(t, lib, "ann/2026/10/more.jpg", []byte("more"), november.AddDate(0, -1, 0))
	moved("another's")
	for _, month := range []string{"2026-13", "2026-1", "2026-011", "NONE", "nonsense", "-001-01", "2026-11%2F..", "0000-00", "2026-11%22", "%00", strings.Repeat("9", 4000)} {
		ron.is("an odd month", "/v1/library/"+month, http.StatusNotFound)
	}
	for _, p := range []*testPhone{{t: t, url: url}, {t: t, url: url, token: "nope"}} {
		for _, path := range []string{"library", "library/none", "thumb/" + sea, "preview/" + sea, "info/" + sea, "original/" + sea} {
			p.is("not paired", "/v1/"+path, http.StatusUnauthorized)
		}
	}
}

// The list is worked out once for each state of the library, asked or not by
// many at once; that it is the same costs no reading of the catalog.
func TestTheListIsWorkedOutOnce(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron := pairWith(t, url, "ron")
	shelve(t, lib, "ron/2026/11/a.jpg", []byte("a"), november)
	tag, _ := ron.list()
	// The photos cannot be read now, and nothing says the catalog changed.
	catalog := filepath.Join(lib, library.WorkDir, "catalog.db")
	var whole []byte
	var at time.Time
	put := func(b []byte) {
		t.Helper()
		if err := os.WriteFile(catalog, b, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(catalog, at, at); err != nil {
			t.Fatal(err)
		}
	}
	spoil := func() {
		t.Helper()
		db, err := sql.Open("sqlite", catalog)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var page, size int
		if err := db.QueryRow(`SELECT rootpage, (SELECT page_size FROM pragma_page_size) FROM sqlite_schema WHERE name = 'library_photos'`).Scan(&page, &size); err != nil {
			t.Fatal(err)
		}
		if whole, err = os.ReadFile(catalog); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(catalog)
		if err != nil {
			t.Fatal(err)
		}
		at = info.ModTime()
		bad := slices.Clone(whole)
		clear(bad[(page-1)*size : page*size])
		put(bad)
	}
	spoil()
	// They are indeed not.
	ron.is("spoilt", "/v1/library/2026-11", http.StatusInternalServerError)
	var all sync.WaitGroup
	for range 20 {
		all.Go(func() {
			if res, body := ron.gets("/v1/library", `If-None-Match: W/"x", `+tag); res.StatusCode != http.StatusNotModified || len(body) != 0 || res.Header.Get("ETag") != tag {
				t.Errorf("nothing changed: %d %s", res.StatusCode, body)
			}
			if res, _ := ron.gets("/v1/library", `If-None-Match: "`+tag[2:]); res.StatusCode != http.StatusOK {
				t.Errorf("another tag: %d", res.StatusCode)
			}
		})
	}
	all.Wait()
	put(whole)
	// A heart, set here or said by a phone, is not in the list: it costs no
	// reading either.
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("a")))
	if _, err := engine.Mark(library.Favorite, []library.Key{{Hash: hash, Profile: "ron"}}, true); err != nil {
		t.Fatal(err)
	}
	_, have := ron.call("POST", "/v1/have", `{"hashes":[]}`)
	if code, _ := ron.says(have.Shelf, 0, said(1, hash, false, 0)); code != http.StatusOK {
		t.Fatal(code)
	}
	spoil()
	if res, _ := ron.gets("/v1/library", "If-None-Match: "+tag); res.StatusCode != http.StatusNotModified {
		t.Errorf("a heart: %d", res.StatusCode)
	}
	put(whole)
	// A write the disk's clock does not tell from the one before, then a heart.
	if _, err := engine.TurnLeft(library.Key{Hash: hash, Profile: "ron"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(catalog, at, at); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Mark(library.Favorite, []library.Key{{Hash: hash, Profile: "ron"}}, true); err != nil {
		t.Fatal(err)
	}
	if res, _ := ron.gets("/v1/library", "If-None-Match: "+tag); res.StatusCode != http.StatusOK {
		t.Errorf("turned: %d", res.StatusCode)
	}
	shelve(t, lib, "ron/2026/11/b.jpg", []byte("b"), november)
	if res, _ := ron.gets("/v1/library", "If-None-Match: "+tag); res.StatusCode != http.StatusOK || res.Header.Get("ETag") == tag {
		t.Errorf("changed: %d", res.StatusCode)
	}
	// Another library chosen: its list, and the first's again when back.
	other := t.TempDir()
	one := shelve(t, other, "ron/2020/01/c.jpg", []byte("c"), november.AddDate(-6, 0, 0))
	tag, was := ron.list()
	if err := engine.SetDefaultLibrary(other); err != nil {
		t.Fatal(err)
	}
	if _, l := ron.list(); l.Shelf == was.Shelf || len(l.Months) != 1 || l.Months[0].Month != "2020-11" {
		t.Errorf("another library: %+v", l)
	}
	ron.is("another library's", "/v1/original/"+one, http.StatusOK)
	if err := engine.SetDefaultLibrary(lib); err != nil {
		t.Fatal(err)
	}
	if now, _ := ron.list(); now != tag {
		t.Error("the first library is another list")
	}
	ron.is("another library's", "/v1/original/"+one, http.StatusNotFound)
	// The same photos on another shelf: the albums' ids are others, so the tag is.
	one = shelve(t, lib, "ron/2026/12/in.jpg", []byte("in"), november, "Sea")
	before := ron.month("2026-11")
	db, err := sql.Open("sqlite", filepath.Join(lib, library.WorkDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE catalog_id SET id = 'another'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	_, l := ron.list()
	if after := ron.month("2026-11"); after.Tag == before.Tag || after.Tag != l.Months[0].Tag || l.Albums[0].ID == before.Photos[slices.IndexFunc(before.Photos, func(p aPhoto) bool { return p.Hash == one })].Albums[0] {
		t.Errorf("another shelf: %+v, was %+v", after, before)
	}
	tag, _ = ron.list()
	// Its disk away.
	if err := os.Rename(lib, lib+"-away"); err != nil {
		t.Fatal(err)
	}
	a := fmt.Sprintf("%x", sha256.Sum256([]byte("a")))
	for _, path := range []string{"library", "library/2026-11", "thumb/" + a, "preview/" + a, "info/" + a, "original/" + a} {
		ron.is("the disk away", "/v1/"+path, http.StatusServiceUnavailable)
	}
	if err := os.Rename(lib+"-away", lib); err != nil {
		t.Fatal(err)
	}
	if now, _ := ron.list(); now != tag {
		t.Error("the disk back, the list is another")
	}
}

// A phone waiting for marks with its list is told of another, and one without
// never hears the word.
func TestAPhoneWaitsForAnotherList(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron, old := pairWith(t, url, "ron"), pairWith(t, url, "ron")
	hash := shelve(t, lib, "ron/2026/11/a.jpg", []byte("a"), november)
	tag, l := ron.list()
	waits := func(p *testPhone, list string) <-chan string {
		out := make(chan string, 1)
		go func() {
			res, body := p.gets("/v1/marks?shelf=" + l.Shelf + "&after=0" + list)
			out <- fmt.Sprint(res.StatusCode, " ", strings.TrimSpace(string(body)))
		}()
		time.Sleep(150 * time.Millisecond)
		return out
	}
	is := func(what string, got <-chan string, want string) {
		t.Helper()
		select {
		case a := <-got:
			if a != want {
				t.Errorf("%s: %s, want %s", what, a, want)
			}
		case <-time.After(4 * time.Second):
			t.Errorf("%s: still held", what)
		}
	}
	const other = `200 {"changed":false,"list":true}`
	held, olds := waits(ron, "&list="+strings.Trim(tag, `"`)), waits(old, "")
	if _, err := engine.TurnLeft(library.Key{Hash: hash, Profile: "ron"}); err != nil {
		t.Fatal(err)
	}
	is("turned", held, other)
	for _, bad := range []string{"", "x", "%00", strings.Trim(tag, `"`)} {
		is("not this list", waits(ron, "&list="+bad), other)
	}
	old.call("POST", "/v1/unpair", "")
	is("no list", olds, `200 {"changed":false}`)
}

// Gzip is for who takes it by name, and is the same answer.
func TestAListIsGzippedForWhoTakesIt(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron := pairWith(t, url, "ron")
	shelve(t, lib, "ron/2026/11/a.jpg", []byte("a"), november)
	for _, path := range []string{"/v1/library", "/v1/library/2026-11"} {
		_, plain := ron.gets(path)
		for accepts, want := range map[string]bool{"gzip": true, "br, GZIP;q=0.5": true, "gzip;q=0": false, "gzip; q=0.000": false, "gzip;q=x": false, "identity": false, "*": false, "xgzip": false} {
			res, body := ron.gets(path, "Accept-Encoding: "+accepts)
			if got := res.Header.Get("Content-Encoding") == "gzip"; got != want || res.Header.Get("Vary") != "Accept-Encoding" {
				t.Errorf("%s, %q: gzipped %v", path, accepts, got)
				continue
			}
			if want {
				z, err := gzip.NewReader(bytes.NewReader(body))
				if err == nil {
					body, err = io.ReadAll(z)
				}
				if err != nil {
					t.Error(err)
				}
			}
			if !bytes.Equal(body, plain) {
				t.Errorf("%s, %q: another answer", path, accepts)
			}
		}
	}
}

// A phone's pictures are of its library's photos alone, good for ever when
// they are the content's.
func TestAPhonesPictures(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron, ann := pairWith(t, url, "ron"), pairWith(t, url, "ann")
	photo := jpegOf(400, 300)
	sea := shelve(t, lib, "ron/2026/11/sea.jpg", photo, november)
	bad := shelve(t, lib, "ron/2026/11/bad.jpg", []byte("no picture"), november)
	trashed := shelve(t, lib, "ron/2026/11/trashed.jpg", jpegOf(30, 20), november)
	if _, err := engine.Mark(library.Trashed, []library.Key{{Hash: trashed, Profile: "ron"}}, true); err != nil {
		t.Fatal(err)
	}
	anns := shelve(t, lib, "ann/2026/11/anns.jpg", jpegOf(50, 40), november)
	shared := shelve(t, lib, "ann/2026/11/shared.jpg", jpegOf(70, 40), november)
	shelve(t, lib, "ron/2026/11/shared.jpg", jpegOf(70, 40), november)
	if err := os.Remove(filepath.Join(lib, "ron/2026/11/shared.jpg")); err != nil {
		t.Fatal(err)
	}
	// Listed or not: the second time a small copy is found in the list.
	for _, p := range []*testPhone{ron, ann} {
		p.is("not listed", "/v1/thumb/"+strings.Repeat("0", 64), http.StatusNotFound)
		p.list()
	}
	const forEver = "private, max-age=31536000, immutable"
	for path, long := range map[string]int{"/v1/thumb/": 256, "/v1/preview/": 400} {
		res, body := ron.gets(path + sea)
		img, err := jpeg.DecodeConfig(bytes.NewReader(body))
		if res.StatusCode != http.StatusOK || err != nil || img.Width != long || res.Header.Get("Content-Type") != "image/jpeg" || res.Header.Get("Cache-Control") != forEver {
			t.Errorf("%s: %d %v %v %v", path, res.StatusCode, img, err, res.Header)
		}
		if res, body := ron.gets(path + bad); res.StatusCode != http.StatusNotFound || strings.TrimSpace(string(body)) != `{"none":true}` || strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
			t.Errorf("%s, no picture: %d %s", path, res.StatusCode, body)
		}
		ron.is("in the Trash", path+trashed, http.StatusOK)
		ron.is("its file gone, another's there", path+shared, http.StatusOK)
		// Made for ann, and still not ron's.
		ann.is("ann's", path+anns, http.StatusOK)
		nowhere := ron.is("nowhere", path+strings.Repeat("0", 64), http.StatusNotFound)
		if body := ron.is("ann's", path+anns, http.StatusNotFound); !bytes.Equal(body, nowhere) {
			t.Errorf("%s: ann's is told from nothing: %s", path, body)
		}
		for _, hash := range []string{"x", strings.ToUpper(sea), sea[1:], sea + "0", "..%2F..%2Fcatalog.db", strings.Repeat("g", 64)} {
			ron.is("no hash", path+hash, http.StatusBadRequest)
		}
	}
	// Another program's edit is not the content's picture.
	edited := shelve(t, lib, "ron/2026/11/edited.jpg", jpegOf(60, 50), november)
	if err := os.WriteFile(filepath.Join(lib, "ron/2026/11/edited.jpg"), jpegOf(80, 90), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/thumb/", "/v1/preview/", "/v1/original/"} {
		if res, _ := ron.gets(path + edited); res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "private, no-cache" || res.Header.Get("ETag") == `"`+edited+`"` {
			t.Errorf("%s, edited: %d %v", path, res.StatusCode, res.Header)
		}
	}
	gone := shelve(t, lib, "ron/2026/11/gone.jpg", jpegOf(61, 50), november)
	if err := os.Remove(filepath.Join(lib, "ron/2026/11/gone.jpg")); err != nil {
		t.Fatal(err)
	}
	if body := ron.is("its file gone", "/v1/thumb/"+gone, http.StatusNotFound); !strings.Contains(string(body), `"none":true`) {
		t.Errorf("a thumbnail, its file gone: %s", body)
	}
	for _, path := range []string{"preview", "info", "original"} {
		ron.is("its file gone", "/v1/"+path+"/"+gone, http.StatusGone)
	}
}

// What leads out of the library is not in it.
func TestALinkOutOfTheLibraryIsNotFollowed(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron := pairWith(t, url, "ron")
	secret := filepath.Join(t.TempDir(), "secret.jpg")
	if err := os.WriteFile(secret, jpegOf(20, 10), 0o644); err != nil {
		t.Fatal(err)
	}
	out := shelve(t, lib, "ron/2026/11/out.jpg", jpegOf(20, 10), november)
	inside := shelve(t, lib, "ron/2026/11/inside.jpg", jpegOf(30, 10), november)
	folder := shelve(t, lib, "ron/2026/11/folder.jpg", jpegOf(40, 10), november)
	dir := shelve(t, lib, "ron/2026/11/dir.jpg", jpegOf(50, 10), november)
	at := func(name string) string { return filepath.Join(lib, "ron", "2026", "11", name) }
	for _, name := range []string{"out.jpg", "folder.jpg", "dir.jpg"} {
		if err := os.Remove(at(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(secret, at("out.jpg")); err != nil {
		t.Skip("no links here")
	}
	for _, err := range []error{os.Rename(at("inside.jpg"), at("real.jpg")), os.Symlink("real.jpg", at("inside.jpg")),
		os.Symlink(filepath.Join("..", "..", "..", "..", filepath.Base(filepath.Dir(secret)), "secret.jpg"), at("folder.jpg")), os.Mkdir(at("dir.jpg"), 0o755)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"preview", "info", "original"} {
		for _, hash := range []string{out, folder, dir} {
			ron.is("out of the library", "/v1/"+path+"/"+hash, http.StatusGone)
		}
		ron.is("a link inside it", "/v1/"+path+"/"+inside, http.StatusOK)
	}
	// A folder that is a file now, and a link to itself.
	nowhere := shelve(t, lib, "ron/2025/11/nowhere.jpg", jpegOf(60, 10), november)
	loop := shelve(t, lib, "ron/2026/11/loop.jpg", jpegOf(70, 10), november)
	for _, err := range []error{os.RemoveAll(filepath.Join(lib, "ron/2025")), os.WriteFile(filepath.Join(lib, "ron/2025"), nil, 0o644),
		os.Remove(at("loop.jpg")), os.Symlink("loop.jpg", at("loop.jpg"))} {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, hash := range []string{nowhere, loop} {
		for _, path := range []string{"preview", "info", "original"} {
			ron.is("no file there", "/v1/"+path+"/"+hash, http.StatusGone)
		}
		ron.is("no file there", "/v1/thumb/"+hash, http.StatusNotFound)
	}
	ron.is("out of the library", "/v1/thumb/"+out, http.StatusNotFound)
	ron.is("a link inside it", "/v1/thumb/"+inside, http.StatusOK)
}

// An original is the file as it is, any part of it.
func TestAPhonesOriginals(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron, ann := pairWith(t, url, "ron"), pairWith(t, url, "ann")
	body := bytes.Repeat([]byte("0123456789"), 50_000)
	clip := shelve(t, lib, "ron/2026/11/clip.mov", body, november)
	raw := shelve(t, lib, "ron/2026/11/raw.dng", []byte("raw"), november)
	empty := shelve(t, lib, "ron/2026/11/empty.jpg", nil, november)
	path := "/v1/original/" + clip
	tag := `"` + clip + `"`
	res, got := ron.gets(path)
	if res.StatusCode != http.StatusOK || !bytes.Equal(got, body) || res.Header.Get("ETag") != tag || res.Header.Get("Content-Type") != "video/quicktime" ||
		res.Header.Get("Accept-Ranges") != "bytes" || res.ContentLength != int64(len(body)) {
		t.Errorf("whole: %d %v", res.StatusCode, res.Header)
	}
	if res, got := ron.asks("HEAD", path); res.StatusCode != http.StatusOK || len(got) != 0 || res.ContentLength != int64(len(body)) || res.Header.Get("Accept-Ranges") != "bytes" || res.Header.Get("ETag") != tag {
		t.Errorf("HEAD: %d %v", res.StatusCode, res.Header)
	}
	for _, c := range []struct {
		headers []string
		status  int
		want    []byte
	}{
		{[]string{"Range: bytes=10-19"}, 206, body[10:20]},
		{[]string{"Range: bytes=-7"}, 206, body[len(body)-7:]},
		{[]string{"Range: bytes=499990-"}, 206, body[499990:]},
		{[]string{"Range: bytes=499990-999999999"}, 206, body[499990:]},
		{[]string{"Range: bytes=0-9", "If-Range: " + tag}, 206, body[:10]},
		{[]string{"Range: bytes=0-9", `If-Range: "other"`}, 200, body},
		{[]string{"Range: bytes=500000-"}, 416, nil},
		{[]string{"Range: bytes=9-1"}, 416, nil},
		{[]string{"Range: bytes=99999999999999999999-"}, 416, nil},
		{[]string{"Range: rows=1-2"}, 416, nil},
		{[]string{"Range: bytes=0-" + strings.Repeat(",0-", 10_000)}, 200, body},
		{[]string{"If-None-Match: " + tag}, 304, nil},
		{[]string{"Accept-Encoding: gzip"}, 200, body},
	} {
		res, got := ron.gets(path, c.headers...)
		if res.StatusCode != c.status || c.want != nil && !bytes.Equal(got, c.want) || res.Header.Get("Content-Encoding") != "" {
			t.Errorf("%.60s: %d, %d bytes, want %d", c.headers, res.StatusCode, len(got), c.status)
		}
	}
	if res, _ := ron.gets("/v1/original/" + raw); res.Header.Get("Content-Type") != "image/x-adobe-dng" {
		t.Errorf("raw: %v", res.Header)
	}
	if res, got := ron.gets("/v1/original/" + empty); res.StatusCode != http.StatusOK || len(got) != 0 {
		t.Errorf("empty: %d", res.StatusCode)
	}
	ann.is("ron's", path, http.StatusNotFound)
	ron.is("no hash", path+"0", http.StatusBadRequest)
}

// Info is as the window's, and a video's length.
func TestAPhonesInfo(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron, ann := pairWith(t, url, "ron"), pairWith(t, url, "ann")
	sea := shelve(t, lib, "ron/2026/11/sea.jpg", jpegOf(40, 30), november)
	if body := ron.is("a photo", "/v1/info/"+sea, http.StatusOK); strings.TrimSpace(string(body)) != `{"w":40,"h":30}` {
		t.Errorf("a photo: %s", body)
	}
	ann.is("ron's", "/v1/info/"+sea, http.StatusNotFound)
	// The smallest MP4 with a length: 2.5 seconds, then none known, then no timescale.
	for i, c := range []struct {
		scale, length uint32
		want          string
	}{{1000, 2500, `{"w":0,"h":0,"ms":2500}`}, {1000, 0xffffffff, `{"w":0,"h":0}`}, {0, 5, `{"w":0,"h":0}`}} {
		moov := make([]byte, 116)
		binary.BigEndian.PutUint32(moov, 116)
		copy(moov[4:], "moov")
		binary.BigEndian.PutUint32(moov[8:], 108)
		copy(moov[12:], "mvhd")
		binary.BigEndian.PutUint32(moov[28:], c.scale)
		binary.BigEndian.PutUint32(moov[32:], c.length)
		clip := shelve(t, lib, fmt.Sprintf("ron/2026/11/%d.mp4", i), moov, november)
		if body := ron.is("a video", "/v1/info/"+clip, http.StatusOK); strings.TrimSpace(string(body)) != c.want {
			t.Errorf("a video of %+v: %s", c, body)
		}
	}
}

// openIn are the files of dir this process holds open.
func openIn(dir string) (open []string) {
	fds, _ := filepath.Glob("/proc/self/fd/*")
	for _, fd := range fds {
		if to, err := os.Readlink(fd); err == nil && strings.HasPrefix(to, dir) {
			open = append(open, to)
		}
	}
	return open
}

// A phone slow to take a month holds nothing of
// the library; one that takes nothing of an original is let go of, and one
// that takes it slowly is not.
func TestASlowPhoneHoldsNoDisk(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("what is open is read from /proc")
	}
	defer func(d time.Duration) { stalled = d }(stalled)
	stalled = 2 * time.Second
	lib := newLibrary(t)
	l, err := listenPhones()
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- l.srv.Serve(l.ln) }()
	defer func() { l.srv.Close(); <-served; l.shutdown(context.Background()) }()
	url := fmt.Sprintf("https://127.0.0.1:%d", l.port)
	// A month of 15 MB, more than a connection holds.
	shelve(t, lib, "ron/2025/11/one.jpg", []byte("one"), november.AddDate(-1, 0, 0))
	c, err := library.OpenCatalog(filepath.Join(lib, library.WorkDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 60 {
		hash := fmt.Sprintf("%064x", i)
		if err := c.Put(library.Photo{Hash: hash, Profile: "ron", Path: hash, Name: strings.Repeat(hash, 4000), Taken: november.AddDate(-1, 0, 0)}); err != nil {
			t.Fatal(err)
		}
	}
	c.Close()
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	http.DefaultTransport = tr
	ron := pairWith(t, url, "ron")
	clip := shelve(t, lib, "ron/2026/11/clip.mp4", make([]byte, 40<<20), november)
	begin := func(path string) (*http.Response, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		r, _ := http.NewRequestWithContext(ctx, "GET", url+path, nil)
		r.Header.Set("Authorization", "Bearer "+ron.token)
		r.Header.Set("Accept-Encoding", "identity")
		res, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return res, cancel
	}
	res, cancel := begin("/v1/library/2025-11")
	if open := openIn(lib); len(open) > 0 {
		t.Errorf("held while a month is sent: %v", open)
	}
	cancel()
	res.Body.Close()
	// Taken slowly, for longer than a stall.
	res, cancel = begin("/v1/original/" + clip)
	n := 0
	for range 15 {
		m, err := io.CopyN(io.Discard, res.Body, 2<<20)
		if n += int(m); err != nil {
			t.Errorf("taken slowly: %v after %d bytes", err, n)
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	rest, err := io.Copy(io.Discard, res.Body)
	if n+int(rest) != 40<<20 || err != nil {
		t.Errorf("taken slowly: %d bytes, %v", n+int(rest), err)
	}
	cancel()
	// Not taken at all.
	res, cancel = begin("/v1/original/" + clip)
	released := func(what string) {
		t.Helper()
		for range 100 {
			if len(openIn(lib)) == 0 {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Errorf("%s: %v still open", what, openIn(lib))
	}
	if len(openIn(lib)) == 0 {
		t.Error("an original being sent is not open: the test sees nothing")
	}
	released("a phone that takes nothing")
	if n, err := io.Copy(io.Discard, res.Body); err == nil {
		t.Errorf("let go of, %d bytes and no error", n)
	}
	cancel()
	// Gone.
	res, cancel = begin("/v1/original/" + clip)
	cancel()
	released("a phone gone")
	res.Body.Close()
	tr.CloseIdleConnections()
}

// Quitting does not wait for a phone that takes nothing of an original, over
// the listener as it is.
func TestQuittingEndsAnOriginalNotTaken(t *testing.T) {
	lib := newLibrary(t)
	s := newTestServer(t)
	if err := s.ListenPhones(); err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- s.Serve() }()
	original := http.DefaultTransport
	defer func() { http.DefaultTransport = original }()
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	http.DefaultTransport = tr
	ron := pairWith(t, fmt.Sprintf("https://127.0.0.1:%d", s.phones.port), "ron")
	clip := shelve(t, lib, "ron/2026/11/clip.mp4", make([]byte, 40<<20), november)
	r, _ := http.NewRequest("GET", ron.url+"/v1/original/"+clip, nil)
	r.Header.Set("Authorization", "Bearer "+ron.token)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	// Every buffer on the way is full.
	time.Sleep(500 * time.Millisecond)
	began := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Error(err)
	}
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("it took %v", took)
	}
	if err := <-served; err != nil {
		t.Error(err)
	}
}

// A phone has a few originals open at once, and no more; the rest are sent as
// those end.
func TestAPhoneHasAFewOriginalsOpen(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron, ann := pairWith(t, url, "ron"), pairWith(t, url, "ann")
	body := make([]byte, 40<<20)
	clip := shelve(t, lib, "ron/2026/11/clip.mp4", body, november)
	shelve(t, lib, "ann/2026/11/clip.mp4", body, november)
	var all sync.WaitGroup
	begun := make(chan *http.Response, 20)
	ask := func(p *testPhone) {
		all.Go(func() {
			r, _ := http.NewRequest("GET", url+"/v1/original/"+clip, nil)
			r.Header.Set("Authorization", "Bearer "+p.token)
			if res, err := http.DefaultClient.Do(r); err != nil {
				t.Error(err)
			} else {
				begun <- res
			}
		})
	}
	for range 9 {
		ask(ron)
	}
	time.Sleep(time.Second)
	if n := len(begun); n != 6 {
		t.Errorf("%d originals open for one phone", n)
	}
	ask(ann)
	time.Sleep(time.Second)
	if n := len(begun); n != 7 {
		t.Errorf("another phone waits for its own: %d", n)
	}
	for range 10 {
		res := <-begun
		if n, err := io.Copy(io.Discard, res.Body); n != int64(len(body)) || err != nil {
			t.Errorf("%d bytes, %v", n, err)
		}
		res.Body.Close()
	}
	all.Wait()
}

// A file cut while it is sent does not end as if it were whole.
func TestAFileCutWhileSentIsNotWhole(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron := pairWith(t, url, "ron")
	clip := shelve(t, lib, "ron/2026/11/clip.mp4", make([]byte, 60<<20), november)
	r, _ := http.NewRequest("GET", url+"/v1/original/"+clip, nil)
	r.Header.Set("Authorization", "Bearer "+ron.token)
	res, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if err := os.Truncate(filepath.Join(lib, "ron/2026/11/clip.mp4"), 1<<20); err != nil {
		t.Fatal(err)
	}
	if n, err := io.Copy(io.Discard, res.Body); err == nil {
		t.Errorf("%d bytes and no error", n)
	}
}

// A flood of questions at once is answered, and leaves nothing open.
func TestAFloodIsAnswered(t *testing.T) {
	lib, url := phonesLibrary(t)
	ron := pairWith(t, url, "ron")
	var hashes []string
	for i := range 12 {
		hashes = append(hashes, shelve(t, lib, fmt.Sprintf("ron/2026/11/%d.jpg", i), jpegOf(300+i, 200), november))
	}
	var all sync.WaitGroup
	for i := range 120 {
		all.Go(func() {
			for _, path := range []string{"library", "library/2026-11", "thumb/" + hashes[i%12], "preview/" + hashes[i%12], "info/" + hashes[i%12], "original/" + hashes[i%12]} {
				if res, body := ron.gets("/v1/" + path); res.StatusCode != http.StatusOK {
					t.Errorf("%s: %d %s", path, res.StatusCode, body)
				}
			}
		})
	}
	all.Wait()
	http.DefaultClient.CloseIdleConnections()
	if open := openIn(lib); len(open) > 0 {
		t.Errorf("left open: %v", open)
	}
}
