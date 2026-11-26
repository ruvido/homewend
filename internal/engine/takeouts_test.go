// homewend — Copyright (C) 2026 ruvido
// SPDX-License-Identifier: AGPL-3.0-or-later

package engine

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ruvido/homewend/internal/library"
	"github.com/ruvido/homewend/internal/takeout"
)

var now = time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)

func ready(job string, age time.Duration) takeout.Export {
	return takeout.Export{Job: job, Created: now.Add(-age),
		Parts: []takeout.Part{{Filename: "a.zip"}}, Manifest: takeout.Part{Filename: "m.zip"}}
}

// preparing is an export Google has not finished; gone, one it no longer offers.
func preparing(job string, age time.Duration) takeout.Export {
	return takeout.Export{Job: job, Created: now.Add(-age)}
}

func gone(job string, age time.Duration) takeout.Export {
	return takeout.Export{Job: job, Created: now.Add(-age), Expired: true}
}

func TestStatusOf(t *testing.T) {
	day := 24 * time.Hour
	for _, c := range []struct {
		e    takeout.Export
		want Status
	}{
		{ready("a", 5*day), Ready},
		{preparing("b", time.Hour), Preparing},
		{gone("c", 8*day), Expired},
	} {
		if got := StatusOf(c.e); got != c.want {
			t.Errorf("%s: got %s, want %s", c.e.Job, got, c.want)
		}
	}
}

func TestByID(t *testing.T) {
	exports := []takeout.Export{ready("8f6c3233-aaaa", 0), ready("8f6d0000-bbbb", 0)}
	if e, err := byID(exports, "8f6c"); err != nil || e.Job != "8f6c3233-aaaa" {
		t.Errorf("got %v, %v", e.Job, err)
	}
	if _, err := byID(exports, "8f6"); err == nil {
		t.Error("an ambiguous id was accepted")
	}
	if _, err := byID(exports, "ffff"); !errors.Is(err, ErrNoSuchTakeout) {
		t.Errorf("got %v, want ErrNoSuchTakeout", err)
	}
}

func TestLatestForPicksTheNewestStillOffered(t *testing.T) {
	day := 24 * time.Hour
	exports := []takeout.Export{
		ready("old-2025", 3*day),
		ready("new-2025", day),
		ready("2024", 0),
		gone("gone-2025", 10*day),
	}
	notes := []note{
		{Year: 2025, Job: "old-2025"},
		{Year: 2025, Job: "new-2025"},
		{Year: 2024, Job: "2024"},
		{Year: 2025, Job: "gone-2025"},
	}
	if job, _ := latestFor(2025, notes, exports); job != "new-2025" {
		t.Errorf("got %q, want new-2025", job)
	}
	if job, _ := latestFor(2023, notes, exports); job != "" {
		t.Errorf("got %q for a year never asked for, want none", job)
	}
}

func TestLatestForSkipsAnExpiredExport(t *testing.T) {
	exports := []takeout.Export{gone("gone", 8*24*time.Hour)}
	if job, _ := latestFor(2025, []note{{Year: 2025, Job: "gone"}}, exports); job != "" {
		t.Errorf("got %q, want none: the export expired", job)
	}
}

// A run stopped between sending the form and seeing the export listed finds
// it by its time, and keeps the job for next time.
func TestLatestForFindsTheExportOfAnUnfinishedNote(t *testing.T) {
	asked := now.Add(-time.Hour)
	exports := []takeout.Export{preparing("before", 2*time.Hour), preparing("after", 50*time.Minute)}
	job, notes := latestFor(2025, []note{{Year: 2025, Asked: asked}}, exports)
	if job != "after" || notes[0].Job != "after" {
		t.Errorf("got %q, note %q; want after", job, notes[0].Job)
	}
}

func TestNotesSurviveBetweenRuns(t *testing.T) {
	profile := t.TempDir()
	if notes, err := loadNotes(profile); err != nil || notes != nil {
		t.Fatalf("no file: got %v, %v", notes, err)
	}
	want := []note{{Year: 2025, Asked: now, Job: "8f6c3233"}}
	if err := saveNotes(profile, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadNotes(profile)
	if err != nil || len(got) != 1 || got[0].Job != want[0].Job || !got[0].Asked.Equal(now) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestDownloadedFindsTheExportInTheLibrary(t *testing.T) {
	lib := t.TempDir()
	for _, job := range []string{"8f6c3233-aaaa", "8f6d0000-bbbb"} {
		if err := writeJSON(filepath.Join(lib, library.WorkDir, job, "state.json"), state{Unpacked: map[string]bool{"part-001.zip": true}}); err != nil {
			t.Fatal(err)
		}
	}
	if job, err := downloaded(lib, "8f6c"); err != nil || job != "8f6c3233-aaaa" {
		t.Errorf("got %q, %v", job, err)
	}
	if _, err := downloaded(lib, ""); err == nil {
		t.Error("two exports and no id: no error")
	}
	if _, err := downloaded(lib, "ffff"); !errors.Is(err, ErrNoSuchTakeout) {
		t.Errorf("got %v, want ErrNoSuchTakeout", err)
	}
}

func TestLocalOfATakeoutNeverDownloaded(t *testing.T) {
	if l := localOf(t.TempDir(), takeout.Export{Job: "job"}); l != nil {
		t.Errorf("got %+v, want nil", l)
	}
}

func TestLocalOfATakeoutWhoseContentsWereOnlyRead(t *testing.T) {
	lib := t.TempDir()
	e := takeout.Export{Job: "job", Parts: []takeout.Part{{Filename: "part-001.zip"}}}
	if err := writeJSON(filepath.Join(lib, library.WorkDir, "job", "state.json"), state{Manifest: "manifest.zip"}); err != nil {
		t.Fatal(err)
	}
	if l := localOf(lib, e); l != nil {
		t.Errorf("got %+v, want nil: only its manifest is here", l)
	}
	// A download stopped before its first part was unpacked is begun.
	if err := writeJSON(filepath.Join(lib, library.WorkDir, "job", "state.json"), state{Manifest: "manifest.zip", Profile: "ann"}); err != nil {
		t.Fatal(err)
	}
	if l := localOf(lib, e); l == nil || l.Parts != 0 || l.Of != 1 || l.Profile != "ann" {
		t.Errorf("got %+v, want 0 of 1 parts, in ann", l)
	}
}

// verify, like the list, counts only a begun download: a takeout whose list of
// files was only read is not one of the exports in the library.
func TestDownloadedSkipsATakeoutOnlyRead(t *testing.T) {
	lib := t.TempDir()
	for job, st := range map[string]state{
		"aaaa-begun": {Manifest: "m.zip", Profile: "ann"},
		"bbbb-read":  {Manifest: "m.zip"},
	} {
		if err := writeJSON(filepath.Join(lib, library.WorkDir, job, "state.json"), st); err != nil {
			t.Fatal(err)
		}
	}
	if job, err := downloaded(lib, ""); err != nil || job != "aaaa-begun" {
		t.Errorf("got %q, %v; want the begun one alone", job, err)
	}
}

// The exports asked for that Google no longer lists, newest first, each with
// what the library knows of it. One still listed, and a request that never
// got its export, are not among them.
func TestPastIsWhatWasAskedAndGoogleNoLongerLists(t *testing.T) {
	lib := t.TempDir()
	day := 24 * time.Hour
	for job, st := range map[string]state{
		"all":    {Profile: "ann", Unpacked: map[string]bool{"a.zip": true, "b.zip": true}, Checked: &checked{Declared: 10, Present: 7}, Bytes: 900, Parts: 2},
		"before": {Profile: "ann", Unpacked: map[string]bool{"a.zip": true, "b.zip": true}, Checked: &checked{Declared: 5, Present: 5}},
		"half":   {Profile: "ann", Unpacked: map[string]bool{"a.zip": true}, Bytes: 900, Parts: 2},
		"read":   {Manifest: "m.zip"},
	} {
		if err := st.save(filepath.Join(lib, library.WorkDir, job)); err != nil {
			t.Fatal(err)
		}
	}
	notes := []note{
		{Year: 2021, Asked: now.Add(-5 * day), Job: "never"},
		{Asked: now.Add(-4 * day), Job: "all"},
		{Year: 2022, Asked: now.Add(-3 * day), Job: "before"},
		{Year: 2023, Asked: now.Add(-2 * day), Job: "half"},
		{Year: 2024, Asked: now.Add(-day), Job: "read"},
		{Year: 2025, Asked: now, Job: "listed"},
		{Year: 2026, Asked: now},
		// A failed request takes the export of the next: the last one is right.
		{Year: 2020, Asked: now.Add(-6 * day), Job: "never"},
	}
	list := past(notes, []Takeout{{Export: gone("listed", 0)}}, lib, Expired)
	want := []struct {
		job   string
		year  int
		local *Local
	}{
		{"read", 2024, nil},
		{"half", 2023, &Local{Profile: "ann", Parts: 1, Of: 2, Bytes: 900}},
		{"before", 2022, &Local{Profile: "ann", Parts: 2, Of: 2, Declared: 5, Present: 5}},
		{"all", 0, &Local{Profile: "ann", Parts: 2, Of: 2, Bytes: 900, Declared: 10, Present: 7}},
		{"never", 2020, nil},
	}
	if len(list) != len(want) {
		t.Fatalf("got %d, want %d: %+v", len(list), len(want), list)
	}
	for i, w := range want {
		g := list[i]
		if g.Job != w.job || g.Year != w.year || !g.Known || g.Status != Expired || (g.Local == nil) != (w.local == nil) || g.Local != nil && *g.Local != *w.local {
			t.Errorf("%d: got %s %d %+v, want %s %d %+v", i, g.Job, g.Year, g.Local, w.job, w.year, w.local)
		}
	}
}

// Google lists no parts of an expired export: those unpacked are still here.
func TestLocalOfAnExpiredTakeoutStillListed(t *testing.T) {
	lib := t.TempDir()
	st := state{Profile: "ann", Unpacked: map[string]bool{"a.zip": true}, Checked: &checked{Declared: 3, Present: 3}}
	if err := st.save(filepath.Join(lib, library.WorkDir, "job")); err != nil {
		t.Fatal(err)
	}
	if l := localOf(lib, gone("job", 0)); l == nil || l.Parts != 1 || l.Of != 1 || l.Present != 3 {
		t.Errorf("got %+v, want its 1 part, 3 files here", l)
	}
}

// Google not answering, every export asked for is told from this computer,
// and none is said to be gone.
func TestPastWithoutGoogleSaysNoStatus(t *testing.T) {
	list := past([]note{{Year: 2025, Asked: now, Job: "listed"}}, nil, "", "")
	if len(list) != 1 || list[0].Status != "" || list[0].Expired || list[0].Local != nil {
		t.Errorf("got %+v, want the one asked, no status", list)
	}
}
