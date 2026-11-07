// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// v2DB is a database as the program before slice 15 wrote it (schema version 2), holding finished
// and waiting work for library /lib.
func v2DB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "syp.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	for _, m := range migrations[:2] {
		if _, err := db.Exec(m.sql); err != nil {
			t.Fatal(err)
		}
	}
	db.Exec(`PRAGMA user_version=2`)
	now := time.Now().UTC().Format(time.RFC3339)
	for i, st := range []Status{Done, Done, Queued} {
		if _, err := db.Exec(`INSERT INTO jobs(library,path,status,size_before,size_after,created,updated) VALUES(?,?,?,?,?,?,?)`,
			"/lib", fmt.Sprintf("/lib/%d.mkv", i), st, 1000, map[Status]int{Done: 400}[st], now, now); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	return path
}

var flagRule = rules.Rule{FromCodec: "h264", MinBitrateKbps: 3000, CRF: 24, Preset: "fast"}

func TestUpgradeAdoptsFlagLibrariesAsLegacyFlows(t *testing.T) {
	for name, path := range map[string]string{"from v2 (slice 08-14)": v2DB(t), "from before migrations": legacyDB(t)} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			before, _ := s.List(100)
			n, err := s.EnsureLibraries([]string{"/lib"}, rules.LegacyTemplate(flagRule, "beside"))
			if err != nil || n != 1 {
				t.Fatalf("added %d, %v", n, err)
			}
			libs, _ := s.Libraries()
			if len(libs) != 1 || libs[0].Name != "lib" || libs[0].Path != "/lib" || !libs[0].Started {
				t.Fatalf("%+v", libs)
			}
			f, _ := s.GetFlow(libs[0].FlowID)
			if f.Name != "lib - Legacy" || f.Template.Kind != rules.KindLegacy {
				t.Errorf("flow %+v", f)
			}
			after, _ := s.List(100)
			if len(after) != len(before) {
				t.Fatalf("jobs %d -> %d: nothing may be added or removed", len(before), len(after))
			}
			for i := range after {
				if after[i].Status != before[i].Status || after[i].LibraryID != libs[0].ID {
					t.Errorf("job %d: %s -> %s, library %d", after[i].ID, before[i].Status, after[i].Status, after[i].LibraryID)
				}
				set, err := s.SettingsFor(after[i])
				if err != nil || set.Rule != flagRule || set.Output != "beside" {
					t.Errorf("job %d settings %+v %v, want the flags' values", after[i].ID, set, err)
				}
			}
			// the next start changes nothing, and a library added later is named normally
			if n, _ := s.EnsureLibraries([]string{"/lib", "/new"}, rules.LegacyTemplate(rules.Default(), "replace")); n != 1 {
				t.Errorf("second start added %d, want only /new", n)
			}
			libs, _ = s.Libraries()
			f0, _ := s.GetFlow(libs[0].FlowID)
			f1, _ := s.GetFlow(libs[1].FlowID)
			if f0.Name != "lib - Legacy" || f0.Template.Params.CRF != 24 || f1.Name != "new" {
				t.Errorf("flows after second start: %q (crf %d), %q", f0.Name, f0.Template.Params.CRF, f1.Name)
			}
		})
	}
}

func TestFreshInstallFlowNames(t *testing.T) {
	s := open(t)
	s.EnsureLibraries([]string{"/a/lib", "/b/lib"}, rules.LegacyTemplate(rules.Default(), "replace"))
	libs, _ := s.Libraries()
	f0, _ := s.GetFlow(libs[0].FlowID)
	f1, _ := s.GetFlow(libs[1].FlowID)
	if f0.Name != "lib" || f1.Name != "lib 2" {
		t.Errorf("flow names %q %q: want after the library, a clash adding 2", f0.Name, f1.Name)
	}
	l, err := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), false)
	if err != nil || l.Started {
		t.Fatalf("%+v %v: a new library starts not started", l, err)
	}
	if _, err := s.AddLibrary("Other", "/m", rules.SliderTemplate(3), false); err != ErrPathTaken {
		t.Errorf("same folder twice: %v", err)
	}
	set, _ := s.SettingsFor(Job{LibraryID: l.ID})
	if set.CRF != 23 || set.Preset != "medium" || set.MinSavingPct != 10 || set.Output != "replace" {
		t.Errorf("balanced slider settings %+v", set)
	}
}

func TestDeleteLibraryKeepsFlowAndFinishedJobs(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	other, _ := s.AddLibrary("Shows", "/s", rules.SliderTemplate(3), true)
	done, _ := s.Enqueue(Job{Library: "/m", LibraryID: l.ID, Path: "/m/1.mkv"})
	s.Claim("w")
	s.Complete(done, "/m/1.mkv", 10)
	running, _ := s.Enqueue(Job{Library: "/m", LibraryID: l.ID, Path: "/m/2.mkv"})
	s.Claim("w")
	queued, _ := s.Enqueue(Job{Library: "/m", LibraryID: l.ID, Path: "/m/3.mkv"})
	theirs, _ := s.Enqueue(Job{Library: "/s", LibraryID: other.ID, Path: "/s/1.mkv"})
	got, err := s.DeleteLibrary(l.ID)
	if err != nil || got != running {
		t.Fatalf("running job %d %v, want %d", got, err, running)
	}
	if _, err := s.Get(queued); err == nil {
		t.Error("its queued job must be cancelled")
	}
	for _, id := range []int64{done, running, theirs} {
		if _, err := s.Get(id); err != nil {
			t.Errorf("job %d must stay: %v", id, err)
		}
	}
	if _, err := s.GetFlow(l.FlowID); err != nil {
		t.Errorf("the flow stays: %v", err)
	}
	if _, err := s.GetLibrary(l.ID); err != ErrNotFound {
		t.Errorf("library still there: %v", err)
	}
	if _, err := s.DeleteLibrary(l.ID); err != ErrNotFound {
		t.Errorf("second delete: %v", err)
	}
	if _, err := s.SettingsFor(Job{LibraryID: l.ID}); err == nil {
		t.Error("a job of a removed library has no settings")
	}
}

func TestCounts(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	for i, size := range []int64{1000, 2000} {
		id, _ := s.Enqueue(Job{LibraryID: l.ID, Path: fmt.Sprint(i), SizeBefore: size})
		s.Claim("w")
		s.Complete(id, "x", size/4)
	}
	f, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "f"})
	s.Claim("w")
	s.Fail(f, "x")
	s.Enqueue(Job{LibraryID: l.ID, Path: "q1"})
	s.Enqueue(Job{LibraryID: l.ID, Path: "q2"})
	s.Claim("w")
	c, _ := s.Counts()
	if got := c[l.ID]; got != (LibraryCounts{Working: 1, Waiting: 1, Failed: 1, FreedBytes: 750 + 1500}) {
		t.Errorf("%+v", got)
	}
}
