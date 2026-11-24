// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ruvido/homewend/internal/engine"
)

// stalled is how long a phone that takes no bytes is waited for: an answer
// holds a file of the library's disk, or a month's memory, until it is taken.
// One that takes them, however slowly, has as long as it needs.
var stalled = time.Minute

// libraryRoutes are the calls a phone reads its library with. Nothing is
// changed, and nothing of the catalog is open while an answer is sent.
func libraryRoutes(mux *http.ServeMux) {
	mux.Handle("GET /v1/library", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		tag, list, err := engine.PhoneList(r.Context(), phone)
		if err != nil {
			phoneReply(w, nil, err)
			return
		}
		tag = `"` + tag + `"`
		w.Header().Set("ETag", tag)
		// A tag in its quotes is found in no other.
		if strings.Contains(r.Header.Get("If-None-Match"), tag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		sendJSON(w, r, list)
	}))
	mux.Handle("GET /v1/library/{month}", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		month := r.PathValue("month")
		tag, photos, done, err := engine.PhoneMonth(r.Context(), phone, month)
		if err != nil {
			phoneReply(w, nil, err)
			return
		}
		defer done()
		sendJSON(w, r, fmt.Appendf(nil, `{"month":%q,"tag":%q,"photos":`, month, tag), photos, []byte("}"))
	}))
	for path, picture := range map[string]func(context.Context, engine.Phone, string) (string, bool, error){"thumb": engine.PhoneThumbnail, "preview": engine.PhonePreview} {
		mux.Handle("GET /v1/"+path+"/{hash}", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
			hash, ok := askedHash(w, r)
			if !ok {
				return
			}
			file, kept, err := picture(r.Context(), phone, hash)
			if errors.Is(err, engine.ErrNoPicture) || path == "thumb" && errors.Is(err, engine.ErrFileGone) {
				phoneJSON(w, http.StatusNotFound, map[string]bool{"none": true})
				return
			}
			if err != nil {
				phoneReply(w, nil, err)
				return
			}
			cacheFor(w, kept)
			http.ServeFile(&toPhone{ResponseWriter: w, ctx: r.Context()}, r, file)
		}))
	}
	mux.Handle("GET /v1/info/{hash}", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		hash, ok := askedHash(w, r)
		if !ok {
			return
		}
		info, err := engine.PhoneInfoOf(r.Context(), phone, hash)
		phoneReply(w, info, err)
	}))
	mux.Handle("GET /v1/original/{hash}", paired(func(w http.ResponseWriter, r *http.Request, phone engine.Phone) {
		hash, ok := askedHash(w, r)
		if !ok {
			return
		}
		o, err := engine.PhoneOriginal(phone, hash)
		if err != nil {
			phoneReply(w, nil, err)
			return
		}
		defer o.Close()
		w.Header().Set("Content-Type", o.Type)
		w.Header().Set("ETag", `"`+o.Version+`"`)
		cacheFor(w, o.Kept)
		file := &cutShort{ReadSeeker: o.File}
		http.ServeContent(&toPhone{ResponseWriter: w, ctx: r.Context()}, r, "", o.Changed, file)
		if file.cut {
			// The disk went, or the file was changed: the answer is not ended
			// as if it were whole.
			panic(http.ErrAbortHandler)
		}
	}))
}

// cutShort is a file that remembers it could not be read: ServeContent asks
// for no more bytes than there were.
type cutShort struct {
	io.ReadSeeker
	cut bool
}

func (f *cutShort) Read(b []byte) (int, error) {
	n, err := f.ReadSeeker.Read(b)
	f.cut = f.cut || err != nil
	return n, err
}

// toPhone is an answer a phone takes as it can: it ends when the phone takes
// nothing for a while, or is gone, or Homewend quits.
type toPhone struct {
	http.ResponseWriter
	ctx context.Context
}

// Unwrap is for http.ResponseController.
func (w *toPhone) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *toPhone) Write(b []byte) (n int, err error) {
	for {
		if err := w.ctx.Err(); err != nil {
			return n, err
		}
		// One that takes no deadline is a test's.
		_ = http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(stalled))
		piece := b[n:min(n+64<<10, len(b))]
		m, err := w.ResponseWriter.Write(piece)
		if n += m; err != nil || n == len(b) {
			return n, err
		}
	}
}

// sendJSON sends the pieces of an answer as they are, gzipped when the phone
// takes it.
func sendJSON(w http.ResponseWriter, r *http.Request, pieces ...[]byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("Vary", "Accept-Encoding")
	var out io.Writer = &toPhone{ResponseWriter: w, ctx: r.Context()}
	if takesGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		z, _ := gzip.NewWriterLevel(out, gzip.BestSpeed)
		defer z.Close()
		out = z
	}
	for _, p := range pieces {
		if _, err := out.Write(p); err != nil {
			return
		}
	}
}

// takesGzip is whether the request accepts it by name, and not as q=0.
func takesGzip(r *http.Request) bool {
	for _, line := range r.Header.Values("Accept-Encoding") {
		for one := range strings.SplitSeq(line, ",") {
			name, with, _ := strings.Cut(one, ";")
			if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
				continue
			}
			q, isQ := strings.CutPrefix(strings.TrimSpace(with), "q=")
			weight, err := strconv.ParseFloat(q, 64)
			return !isQ || err == nil && weight > 0
		}
	}
	return false
}
