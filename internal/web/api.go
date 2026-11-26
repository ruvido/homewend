// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/ruvido/homewend/internal/engine"
	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/session"
	"github.com/ruvido/homewend/internal/takeout"
)

// apiRoutes are the engine calls the page makes. Each is a thin translation
// to JSON: what they do lives in the engine.
func apiRoutes(jobs *engine.Jobs, importing *engine.Importing, phones func() *phoneListener, phonesFailed func() error) http.Handler {
	mux := http.NewServeMux()
	// One sign-in at a time: a second click must not open a second browser.
	var signingIn sync.Mutex
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		s, err := engine.LoadSettings()
		reply(w, s, err)
	})
	// The window changes what it shows; the library and the profiles are
	// changed by their own calls, so a page holding older settings cannot
	// undo them.
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in engine.Settings
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s, err := engine.UpdateSettings(func(s *engine.Settings) { s.Theme, s.Hidden = in.Theme, in.Hidden })
		reply(w, s, err)
	})
	// Whose photos an account's are: the folder of the library they go in.
	mux.HandleFunc("PUT /api/profile", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Profile string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		reply(w, body, engine.SetProfile(r.URL.Query().Get("account"), body.Profile))
	})
	mux.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
		s, err := engine.LoadSettings()
		if err != nil {
			reply(w, nil, err)
			return
		}
		o, err := engine.LibraryOverview(s)
		reply(w, o, err)
	})
	// Cheap to ask every second: the page reads the overview again when it
	// moves. The thumbnails being made go with it.
	mux.HandleFunc("GET /api/changes", func(w http.ResponseWriter, r *http.Request) {
		n, err := engine.LibraryChanges()
		reply(w, map[string]any{"changes": n, "thumbs": engine.ThumbsMade(), "emptying": engine.EmptyingNow()}, err)
	})
	// Sign-in happens in the system browser; the request waits for it. With
	// no account named it signs in a new one.
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		if !signingIn.TryLock() {
			http.Error(w, "a sign-in is already open", http.StatusConflict)
			return
		}
		defer signingIn.Unlock()
		id := r.URL.Query().Get("account")
		if id == "" {
			var err error
			if id, err = engine.NextAccount(); err != nil {
				reply(w, nil, err)
				return
			}
		}
		sess, err := engine.AccountSession(id)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if _, _, err := engine.Login(r.Context(), sess, nil); err != nil {
			reply(w, nil, err)
			return
		}
		// A new sign-in's folder takes its address's name.
		a, err := engine.Adopt(id)
		reply(w, a, err)
	})
	mux.HandleFunc("GET /api/photos", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		s, err := engine.LoadSettings()
		if err != nil {
			reply(w, nil, err)
			return
		}
		list, err := engine.Photos(s, q.Get("year"), q.Get("album"), q.Get("account"), q.Get("nodate") != "", q.Get("view"), limit)
		reply(w, list, err)
	})
	// The photos whose file is gone, taken out of the library: one, or all;
	// kept out of the imports only when asked.
	mux.HandleFunc("POST /api/missing/out", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Photo   *library.Key `json:"photo"`
			KeepOut bool         `json:"keep_out"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		n, err := engine.TakeOutMissing(body.Photo, body.KeepOut)
		reply(w, map[string]int{"taken": n}, err)
	})
	// Photos picked in the library archived or put in the Trash, or back.
	mux.HandleFunc("POST /api/mark", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			State  library.State `json:"state"`
			On     bool          `json:"on"`
			Photos []library.Key `json:"photos"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Photos) == 0 || !slices.Contains([]library.State{library.Archived, library.Trashed, library.Favorite}, body.State) {
			http.Error(w, "photos and archived, trashed or favorite are needed", http.StatusBadRequest)
			return
		}
		done, err := engine.Mark(body.State, body.Photos, body.On)
		reply(w, map[string]int{"marked": len(done)}, err)
	})
	// The photos the Trash showed, deleted for good; the reply is those left.
	mux.HandleFunc("POST /api/trash/empty", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Photos []library.Key `json:"photos"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Photos) == 0 {
			http.Error(w, "photos are needed", http.StatusBadRequest)
			return
		}
		left, err := engine.EmptyTrash(body.Photos)
		reply(w, map[string][]library.Key{"left": left}, err)
	})
	// Dates given to photos picked in the library; when is a clock written as UTC.
	mux.HandleFunc("POST /api/dates", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Photos []library.Key `json:"photos"`
			When   time.Time     `json:"when"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Photos) == 0 || body.When.IsZero() {
			http.Error(w, "photos and a date are needed", http.StatusBadRequest)
			return
		}
		n, err := engine.GiveDates(body.Photos, body.When)
		reply(w, map[string]int{"moved": n}, err)
	})
	mux.HandleFunc("POST /api/cards/eject", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Path string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
			http.Error(w, "a card is needed", http.StatusBadRequest)
			return
		}
		reply(w, nil, engine.EjectCard(body.Path))
	})
	// The phones paired, without their tokens, those unpaired, newest first,
	// and whether a code waits.
	mux.HandleFunc("GET /api/phones", func(w http.ResponseWriter, r *http.Request) {
		p, err := engine.LoadPhones()
		for i := range p.Phones {
			p.Phones[i].Token = ""
		}
		if p.Phones == nil {
			p.Phones = []engine.Phone{}
		}
		// Never null: the page reads its length.
		unpaired := append([]engine.Unpaired{}, p.Unpaired...)
		slices.Reverse(unpaired)
		out := map[string]any{"phones": p.Phones, "unpaired": unpaired, "pairing": engine.Pairing()}
		if phonesFailed != nil && errors.Is(phonesFailed(), ErrPortsTaken) {
			out["unreachable"] = true
		}
		reply(w, out, err)
	})
	// Add a phone: a code for the profile chosen, as the QR's text.
	mux.HandleFunc("POST /api/phones/pair", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Profile string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		l := phones()
		if l == nil {
			http.Error(w, "the phones' listener did not start: see homewend.log", http.StatusServiceUnavailable)
			return
		}
		code, err := engine.StartPairing(body.Profile)
		reply(w, map[string]string{"qr": l.pairText(code)}, err)
	})
	mux.HandleFunc("DELETE /api/phones/pair", func(w http.ResponseWriter, r *http.Request) {
		engine.StopPairing()
		reply(w, nil, nil)
	})
	mux.HandleFunc("DELETE /api/phones/{id}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, engine.Unpair(r.PathValue("id"), "computer"))
	})
	mux.HandleFunc("POST /api/turn/{hash}", func(w http.ResponseWriter, r *http.Request) {
		turned, err := engine.TurnLeft(photoOf(r))
		reply(w, turned, err)
	})
	mux.HandleFunc("GET /api/latest", func(w http.ResponseWriter, r *http.Request) {
		list, err := engine.Latest(6)
		reply(w, list, err)
	})
	mux.HandleFunc("GET /api/about/{hash}", func(w http.ResponseWriter, r *http.Request) {
		a, err := engine.About(photoOf(r))
		reply(w, a, err)
	})
	mux.HandleFunc("GET /api/file/{hash}", func(w http.ResponseWriter, r *http.Request) {
		f, err := engine.FileOf(photoOf(r))
		reply(w, f, err)
	})
	for path, picture := range map[string]func(context.Context, library.Key, string) (string, bool, error){"thumb": engine.Thumbnail, "preview": engine.Preview} {
		mux.HandleFunc("GET /api/"+path+"/{hash}", func(w http.ResponseWriter, r *http.Request) {
			file, kept, err := picture(r.Context(), photoOf(r), r.URL.Query().Get("edit"))
			if err != nil {
				reply(w, nil, err)
				return
			}
			cacheFor(w, kept)
			http.ServeFile(w, r, file)
		})
	}
	mux.HandleFunc("GET /api/original/{hash}", func(w http.ResponseWriter, r *http.Request) {
		path, err := engine.PathOf(photoOf(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		http.ServeFile(w, r, path)
	})
	// Where a photo is on disk, for the shell to show it in the file manager.
	mux.HandleFunc("GET /api/path/{hash}", func(w http.ResponseWriter, r *http.Request) {
		path, err := engine.PathOf(photoOf(r))
		reply(w, map[string]string{"path": path}, err)
	})
	mux.HandleFunc("GET /api/folder", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// A takeout's folder is its profile's, which may no longer be its
		// account's.
		if p := q.Get("profile"); p != "" {
			path, err := engine.ProfileFolder(p)
			reply(w, map[string]string{"path": path}, err)
			return
		}
		s, err := engine.LoadSettings()
		if err != nil {
			reply(w, nil, err)
			return
		}
		path, err := engine.Folder(s, q.Get("account"), q.Get("year"), q.Get("album"), q.Get("nodate") != "")
		reply(w, map[string]string{"path": path}, err)
	})
	mux.HandleFunc("GET /api/library", func(w http.ResponseWriter, r *http.Request) {
		dir, err := engine.DefaultLibrary()
		var free int64
		if err == nil && dir != "" {
			free, _ = engine.Free(dir)
		}
		reply(w, map[string]any{"dir": dir, "free": free}, err)
	})
	mux.HandleFunc("PUT /api/library", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Dir string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Dir == "" {
			http.Error(w, "a folder is needed", http.StatusBadRequest)
			return
		}
		reply(w, map[string]string{"dir": body.Dir}, engine.ChooseLibrary(body.Dir))
	})
	// Bringing photos home runs in the engine; the page asks where it stands.
	mux.HandleFunc("POST /api/get", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		dir, err := engine.LibraryHere()
		if err != nil {
			reply(w, nil, err)
			return
		}
		s, err := engine.LoadSettings()
		if err != nil {
			reply(w, nil, err)
			return
		}
		year, _ := strconv.Atoi(q.Get("year"))
		g := engine.Get{Year: year, Library: dir, Takeout: q.Get("export"), New: q.Get("new") != "", Again: q.Get("again") != "", Profile: s.ProfileFor(q.Get("account"))}
		err = jobs.Start(q.Get("account"), g)
		reply(w, jobs.State(q.Get("account")), err)
	})
	mux.HandleFunc("GET /api/job", func(w http.ResponseWriter, r *http.Request) {
		reply(w, jobs.State(r.URL.Query().Get("account")), nil)
	})
	mux.HandleFunc("POST /api/job/stop", func(w http.ResponseWriter, r *http.Request) {
		account := r.URL.Query().Get("account")
		jobs.Stop(account)
		reply(w, jobs.State(account), nil)
	})
	// How many photos and videos the account has on Google, by year, as last
	// counted; and whether a count runs, or why the last one failed.
	mux.HandleFunc("GET /api/years", func(w http.ResponseWriter, r *http.Request) {
		account := r.URL.Query().Get("account")
		counts, err := engine.LoadYearCounts(account)
		if err != nil {
			reply(w, nil, err)
			return
		}
		running, failed := jobs.Counting(account)
		out := struct {
			*engine.YearCounts
			Counting bool   `json:"counting"`
			Error    string `json:"error,omitempty"`
		}{YearCounts: counts, Counting: running}
		if failed != nil {
			out.Error = failed.Error()
		}
		reply(w, out, nil)
	})
	mux.HandleFunc("POST /api/years", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{}, jobs.Count(r.URL.Query().Get("account")))
	})
	// Signing out deletes the account's browser profile: its Google cookies
	// go, its photos stay.
	mux.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("account")
		dir, err := engine.AccountDir(id)
		if err == nil {
			// The job's browser must be gone before its profile is.
			<-jobs.Stop(id)
			_, err = engine.Logout(dir)
		}
		reply(w, map[string]string{}, err)
	})
	mux.HandleFunc("GET /api/about", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]string{"version": Version}, nil)
	})
	// What a takeout made on Google holds: its manifest, fetched alone.
	mux.HandleFunc("POST /api/takeouts/contents", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		sess, err := engine.AccountSession(q.Get("account"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		root, err := engine.LibraryHere()
		if err != nil {
			reply(w, nil, err)
			return
		}
		years, err := engine.ReadContents(r.Context(), sess, root, q.Get("export"))
		reply(w, map[string][]string{"years": years}, err)
	})
	mux.HandleFunc("GET /api/takeouts", func(w http.ResponseWriter, r *http.Request) {
		sess, err := engine.AccountSession(r.URL.Query().Get("account"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		// local: what this computer knows, for when Google does not answer.
		if r.URL.Query().Get("local") != "" {
			list, err := engine.Asked(sess)
			reply(w, list, err)
			return
		}
		list, err := engine.WithPast(sess)
		reply(w, list, err)
	})
	// Importing a card or a folder: the cards in now, then a look at what
	// one would bring, then the copy, each running in the engine while the
	// page asks where it stands.
	mux.HandleFunc("GET /api/cards", func(w http.ResponseWriter, r *http.Request) {
		reply(w, engine.Cards(), nil)
	})
	mux.HandleFunc("POST /api/import/plan", func(w http.ResponseWriter, r *http.Request) {
		ask, ok := askOf(w, r)
		if !ok {
			return
		}
		err := importing.Plan(ask)
		reply(w, importing.State(), err)
	})
	mux.HandleFunc("POST /api/import", func(w http.ResponseWriter, r *http.Request) {
		ask, ok := askOf(w, r)
		if !ok {
			return
		}
		err := importing.Import(ask)
		reply(w, importing.State(), err)
	})
	mux.HandleFunc("POST /api/import/again", func(w http.ResponseWriter, r *http.Request) {
		err := importing.Again()
		reply(w, importing.State(), err)
	})
	// A file of the card looked at, small, for picking the ones with a
	// wrong date. Asked again each time: another card in the same place
	// has other photos under the same names.
	mux.HandleFunc("GET /api/import/thumb", func(w http.ResponseWriter, r *http.Request) {
		path, err := importing.Thumbnail(r.Context(), r.URL.Query().Get("file"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		w.Header().Set("Cache-Control", "private, no-cache")
		http.ServeFile(w, r, path)
	})
	mux.HandleFunc("GET /api/import", func(w http.ResponseWriter, r *http.Request) {
		reply(w, importing.State(), nil)
	})
	mux.HandleFunc("POST /api/import/stop", func(w http.ResponseWriter, r *http.Request) {
		<-importing.Stop()
		reply(w, importing.State(), nil)
	})
	return mux
}

// cacheFor says how long an answer is good for. The content's never changes:
// it is named by it. That of a file another program changed does.
func cacheFor(w http.ResponseWriter, kept bool) {
	w.Header().Set("Cache-Control", "private, no-cache")
	if kept {
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	}
}

// photoOf is the photo a request is about: its hash, and whose library's.
func photoOf(r *http.Request) library.Key {
	return library.Key{Hash: r.PathValue("hash"), Profile: r.URL.Query().Get("profile")}
}

// askOf is what the page asks of an import, read from the request; a body
// that does not read is said, and false.
func askOf(w http.ResponseWriter, r *http.Request) (engine.ImportAsk, bool) {
	var ask engine.ImportAsk
	if err := json.NewDecoder(r.Body).Decode(&ask); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return ask, false
	}
	return ask, true
}

// reply writes v as JSON, or the error: a mistake in the request is the
// caller's, anything else is ours.
func reply(w http.ResponseWriter, v any, err error) {
	switch {
	case errors.Is(err, engine.ErrBadTheme), errors.Is(err, engine.ErrBadProfile):
		http.Error(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, engine.ErrProfileCase):
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	// The library's disk is away: nothing the request did wrong, nothing
	// to try again until it is back.
	case errors.As(err, new(engine.LibraryGoneError)):
		http.Error(w, err.Error(), http.StatusGone)
	case errors.Is(err, engine.ErrJobRunning), errors.Is(err, engine.ErrImportRunning), errors.Is(err, engine.ErrNothingToRunAgain), errors.Is(err, engine.ErrNoUserYet), errors.Is(err, engine.ErrLibraryBusy):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, takeout.ErrSignedOut):
		http.Error(w, err.Error(), http.StatusUnauthorized)
	// Nothing the request or we did wrong: the machine lacks what signing
	// in needs, and the page says what to install.
	case errors.Is(err, session.ErrNoBrowser):
		http.Error(w, "no Chromium-family browser found", http.StatusFailedDependency)
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, engine.ErrNoLibrary), errors.Is(err, engine.ErrNoSuchAccount):
		http.Error(w, err.Error(), http.StatusNotFound)
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	default:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(v)
	}
}
