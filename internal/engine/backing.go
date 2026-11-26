// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"slices"
	"strings"
	"sync"
	"time"
)

// Backing is how far a phone's backup has come: Done of the Of photos its
// /v1/have calls said the library lacks, filed by its PUTs since.
type Backing struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Done int    `json:"done"`
	Of   int    `json:"of"`
}

// backupQuiet is how long a phone sends nothing before its backup is taken
// as stopped: it left the Wi-Fi, it was closed. Nothing is said of it.
const backupQuiet = time.Minute

type backupRun struct {
	name    string
	done    int
	missing map[string]bool
	sending int // PUTs under way: a video's minutes of upload are not silence
	last    time.Time
}

func (r *backupRun) quiet() bool { return r.sending == 0 && time.Since(r.last) > backupQuiet }

// Kept in memory: a backup is the phone's run, not a fact of the library.
var backups = struct {
	sync.Mutex
	runs map[string]*backupRun
}{runs: map[string]*backupRun{}}

// backupAsked notes what a /v1/have told phone: those in neither list are to come,
// those now held are no longer awaited. Nothing left to come, no line.
func backupAsked(phone Phone, hashes []string, held Held) {
	backups.Lock()
	defer backups.Unlock()
	r := backups.runs[phone.ID]
	if r == nil || r.quiet() {
		r = &backupRun{missing: map[string]bool{}}
	}
	known := map[string]bool{}
	for _, h := range append(held.Have, held.Out...) {
		known[h] = true
	}
	for _, h := range hashes {
		if known[h] {
			delete(r.missing, h)
		} else {
			r.missing[h] = true
		}
	}
	r.name, r.last = phone.Name, time.Now()
	backups.runs[phone.ID] = r
	if len(r.missing) == 0 {
		delete(backups.runs, phone.ID)
	}
}

// backupSending notes a PUT of phone's begun; the function it gives notes its end,
// filed or not. Only a photo the run awaits is counted, once.
func backupSending(phone Phone) func(hash string, filed bool) {
	backups.Lock()
	defer backups.Unlock()
	if r := backups.runs[phone.ID]; r != nil {
		r.sending++
	}
	return func(hash string, filed bool) {
		backups.Lock()
		defer backups.Unlock()
		r := backups.runs[phone.ID]
		if r == nil {
			return
		}
		r.sending = max(r.sending-1, 0)
		r.last = time.Now()
		if filed && r.missing[hash] {
			delete(r.missing, hash)
			r.done++
		}
		if len(r.missing) == 0 {
			delete(backups.runs, phone.ID)
		}
	}
}

// BackingNow is each phone's backup under way, by name.
func BackingNow() []Backing {
	backups.Lock()
	defer backups.Unlock()
	out := []Backing{}
	for id, r := range backups.runs {
		if r.quiet() {
			delete(backups.runs, id)
			continue
		}
		out = append(out, Backing{ID: id, Name: r.name, Done: r.done, Of: r.done + len(r.missing)})
	}
	slices.SortFunc(out, func(a, b Backing) int { return strings.Compare(a.Name+a.ID, b.Name+b.ID) })
	return out
}
