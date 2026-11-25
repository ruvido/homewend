// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/session"
	"github.com/ruvido/homewend/internal/takeout"
)

// ErrNoSuchTakeout means no export on /manage has the id asked for.
var ErrNoSuchTakeout = errors.New("no takeout has that id")

// ErrExpired means the export asked for can no longer be downloaded.
var ErrExpired = errors.New("that takeout has expired")

// Status is where an export stands.
type Status string

const (
	Preparing Status = "preparing"
	Ready     Status = "ready"
	Expired   Status = "expired"
)

// StatusOf tells a ready export from one Google is still preparing and from
// one it no longer offers.
func StatusOf(e takeout.Export) Status {
	switch {
	case e.Expired:
		return Expired
	case e.Ready():
		return Ready
	}
	return Preparing
}

// note is what Homewend keeps of one export it asked for. /manage does not say
// what an export holds, so this is the only way to know which year it is.
type note struct {
	Year  int       `json:"year"`          // 0: everything
	Asked time.Time `json:"asked"`         // just before the form was sent
	Job   string    `json:"job,omitempty"` // once /manage lists the export
}

// notesFile keeps the notes beside the profile, like the user id: they are
// per account, and any library can use them.
const notesFile = "homewend-takeouts.json"

func loadNotes(profile string) ([]note, error) {
	var notes []note
	data, err := os.ReadFile(filepath.Join(profile, notesFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &notes); err != nil {
		return nil, fmt.Errorf("reading %s: %w", notesFile, err)
	}
	return notes, nil
}

func saveNotes(profile string, notes []note) error {
	return writeJSON(filepath.Join(profile, notesFile), notes)
}

// Takeout is one export as the takeouts command shows it.
type Takeout struct {
	takeout.Export
	ID     string `json:"id"` // the job id's first characters, enough to tell it apart
	Status Status `json:"status"`
	Year   int    `json:"year"`  // 0 with Known: everything
	Known  bool   `json:"known"` // asked for by Homewend, so what it holds is known
	// Years its manifest lists, once read (ReadContents or a download).
	Years []string `json:"years,omitempty"`
	// Local is what of it the library holds; nil when it never came here.
	Local *Local `json:"local,omitempty"`
}

// Local is how far a takeout came into the library: its parts unpacked, and
// the last count of its files against its manifest.
type Local struct {
	// Profile is the folder of the library it goes in, which a rename of the
	// account's profile does not move.
	Profile  string `json:"profile"`
	Parts    int    `json:"parts"`
	Of       int    `json:"of"`              // 0: expired, and begun before it was kept
	Bytes    int64  `json:"bytes,omitempty"` // its size, kept when it was downloaded
	Declared int    `json:"declared,omitempty"`
	Present  int    `json:"present,omitempty"`
}

// localOf reads what the library holds of e, from the state its fetch keeps;
// nil when its download is not begun.
func localOf(root string, e takeout.Export) *Local {
	// None kept reads as not begun.
	st, err := loadState(filepath.Join(root, library.WorkDir, e.Job))
	if err != nil || !st.begun() {
		return nil
	}
	l := &Local{Of: len(e.Parts), Bytes: st.Bytes, Profile: st.Profile}
	for _, p := range e.Parts {
		if st.Unpacked[p.Filename] {
			l.Parts++
		}
	}
	if l.Of == 0 {
		// Expired: what is here is the only record of its parts. Of one counted
		// before their number was kept, all are here.
		for _, done := range st.Unpacked {
			if done {
				l.Parts++
			}
		}
		l.Of = st.Parts
		if l.Of == 0 && st.Checked != nil {
			l.Of = l.Parts
		}
	}
	if st.Checked != nil {
		l.Declared, l.Present = st.Checked.Declared, st.Checked.Present
	}
	return l
}

// idLength is how much of a job id the takeouts command shows, as git shows
// commits: a UUID's first 8 hex digits tell a handful of exports apart.
const idLength = 8

// ShortID is the part of a job id that is shown and typed.
func ShortID(job string) string { return job[:min(idLength, len(job))] }

// Takeouts lists the account's exports, newest first.
func Takeouts(sess *session.Session) ([]Takeout, error) {
	exports, err := takeout.Exports(sess)
	if err != nil {
		return nil, err
	}
	notes, err := loadNotes(sess.Profile)
	if err != nil {
		return nil, err
	}
	byJob := asked(notes)
	list := make([]Takeout, len(exports))
	for i, e := range exports {
		list[i] = Takeout{Export: e, ID: ShortID(e.Job), Status: StatusOf(e)}
		if n, ok := byJob[e.Job]; ok {
			list[i].Year, list[i].Known = n.Year, true
		}
	}
	if root, err := DefaultLibrary(); err == nil && root != "" {
		for i := range list {
			list[i].Years, _ = manifestYears(root, list[i].Job)
			list[i].Local = localOf(root, list[i].Export)
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].Created.After(list[j].Created) })
	return list, nil
}

// WithPast lists the account's exports as Takeouts does, and those Homewend
// asked for that Google no longer lists, expired.
func WithPast(sess *session.Session) ([]Takeout, error) {
	list, err := Takeouts(sess)
	if err != nil {
		return nil, err
	}
	notes, err := loadNotes(sess.Profile)
	if err != nil {
		return nil, err
	}
	root, err := DefaultLibrary()
	if err != nil {
		return nil, err
	}
	return append(list, past(notes, list, root)...), nil
}

// past are the exports noted that listed does not hold, newest first, with
// what is known of them here: what Google said of them is gone.
func past(notes []note, listed []Takeout, root string) []Takeout {
	byJob := asked(notes)
	for _, t := range listed {
		delete(byJob, t.Job)
	}
	var list []Takeout
	for job, n := range byJob {
		t := Takeout{Export: takeout.Export{Job: job, Created: n.Asked, Expired: true}, ID: ShortID(job), Status: Expired, Year: n.Year, Known: true}
		if root != "" {
			t.Local = localOf(root, t.Export)
		}
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Created.After(list[j].Created) })
	return list
}

// asked are the notes by the export they asked for. One that never got its
// export, because the request failed or is still going, asked for none; of
// two with the same export, the last is the one that did.
func asked(notes []note) map[string]note {
	byJob := map[string]note{}
	for _, n := range notes {
		if n.Job != "" {
			byJob[n.Job] = n
		}
	}
	return byJob
}

// byID finds the export whose job id starts with id.
func byID(exports []takeout.Export, id string) (takeout.Export, error) {
	var found []takeout.Export
	for _, e := range exports {
		if id != "" && strings.HasPrefix(e.Job, id) {
			found = append(found, e)
		}
	}
	switch len(found) {
	case 0:
		return takeout.Export{}, fmt.Errorf("%w: %s", ErrNoSuchTakeout, id)
	case 1:
		return found[0], nil
	}
	return takeout.Export{}, fmt.Errorf("%s matches %d takeouts: give more of it", id, len(found))
}

// latestFor is the job of the newest export of year that Homewend asked for
// and Google still offers or is preparing, or "" if there is none. A note
// whose job was never seen, because a run stopped right after sending the
// form, is matched to the export created after it, and filled in.
func latestFor(year int, notes []note, exports []takeout.Export) (string, []note) {
	newest := -1
	for i := range notes {
		n := &notes[i]
		if n.Year != year {
			continue
		}
		if n.Job == "" {
			n.Job = askedSince(exports, n.Asked)
		}
		j := indexOf(exports, n.Job)
		if j < 0 || StatusOf(exports[j]) == Expired {
			continue
		}
		if newest < 0 || exports[j].Created.After(exports[newest].Created) {
			newest = j
		}
	}
	if newest < 0 {
		return "", notes
	}
	return exports[newest].Job, notes
}
