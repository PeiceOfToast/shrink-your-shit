// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// realDir makes dir and returns it as a library stores it (links resolved).
func realDir(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r, err := RealPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// moveJobs points a fixture's jobs (library "/lib") at dir.
func moveJobs(t *testing.T, dbPath, dir string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE jobs SET library=?, path=?||substr(path,5)`, dir, dir); err != nil {
		t.Fatal(err)
	}
}

var flagRule = rules.Rule{FromCodec: "h264", MinBitrateKbps: 3000, CRF: 24, Preset: "fast"}

func TestUpgradeAdoptsFlagLibrariesAsLegacyFlows(t *testing.T) {
	for name, path := range map[string]string{"from v2 (slice 08-14)": v2DB(t), "from before migrations": legacyDB(t)} {
		t.Run(name, func(t *testing.T) {
			dir := realDir(t, filepath.Join(t.TempDir(), "lib"))
			moveJobs(t, path, dir)
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			before, _ := s.List(100)
			n, _, err := s.EnsureLibraries([]string{dir}, rules.LegacyTemplate(flagRule, "beside"))
			if err != nil || n != 1 {
				t.Fatalf("added %d, %v", n, err)
			}
			libs, _ := s.Libraries()
			if len(libs) != 1 || libs[0].Name != "lib" || libs[0].Path != dir || !libs[0].Started {
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
			if n, _, _ := s.EnsureLibraries([]string{dir, realDir(t, filepath.Join(t.TempDir(), "new"))}, rules.LegacyTemplate(rules.Default(), "replace")); n != 1 {
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
	root := t.TempDir()
	s.EnsureLibraries([]string{realDir(t, filepath.Join(root, "a", "lib")), realDir(t, filepath.Join(root, "b", "lib"))}, rules.LegacyTemplate(rules.Default(), "replace"))
	libs, _ := s.Libraries()
	f0, _ := s.GetFlow(libs[0].FlowID)
	f1, _ := s.GetFlow(libs[1].FlowID)
	if f0.Name != "lib" || f1.Name != "lib 2" {
		t.Errorf("flow names %q %q: want after the library, a clash adding 2", f0.Name, f1.Name)
	}
	// fresh install: flag libraries wait for first-run, each with a Balanced slider flow (no Legacy)
	for i, l := range libs {
		f, _ := s.GetFlow(l.FlowID)
		if l.Started || f.Template.Kind != rules.KindSlider || f.Template.Params.Quality != 3 {
			t.Errorf("library %d: started=%v flow %+v; want not started, slider Balanced", i, l.Started, f.Template)
		}
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
		s.Complete(id, fmt.Sprint(i), size/4) // replaced in place
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

func TestFlagLibrariesMayNotOverlap(t *testing.T) {
	s := open(t)
	root := t.TempDir()
	media, tv := filepath.Join(root, "media"), filepath.Join(root, "media", "tv")
	os.MkdirAll(tv, 0o755)
	n, skipped, err := s.EnsureLibraries([]string{media, tv}, rules.LegacyTemplate(rules.Default(), "replace"))
	if err != nil || n != 1 || len(skipped) != 1 || !strings.Contains(skipped[0], tv) || !strings.Contains(skipped[0], "inside") {
		t.Fatalf("added %d, skipped %q, %v", n, skipped, err)
	}
	// the other way round too: a folder around an existing library
	other := t.TempDir()
	os.Mkdir(filepath.Join(other, "inner"), 0o755)
	s.EnsureLibraries([]string{filepath.Join(other, "inner")}, rules.LegacyTemplate(rules.Default(), "replace"))
	if n, skipped, _ := s.EnsureLibraries([]string{other}, rules.LegacyTemplate(rules.Default(), "replace")); n != 0 || len(skipped) != 1 {
		t.Errorf("a folder holding a library: added %d, skipped %q", n, skipped)
	}
}

func TestFlagLibraryLinkIsStoredAsTheRealFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making links needs extra rights on Windows")
	}
	s := open(t)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "videos")
	os.Symlink(real, link)
	s.EnsureLibraries([]string{link}, rules.LegacyTemplate(rules.Default(), "replace"))
	libs, _ := s.Libraries()
	want, _ := filepath.EvalSymlinks(real)
	if len(libs) != 1 || libs[0].Path != want {
		t.Fatalf("%+v, want the real folder %s", libs, want)
	}
	if n, _, _ := s.EnsureLibraries([]string{link, real}, rules.LegacyTemplate(rules.Default(), "replace")); n != 0 {
		t.Errorf("the same folder by another name was added again (%d)", n)
	}
}

// An upgraded install whose flags had a folder inside another: the inner one is not added, and its
// old jobs belong to the library that holds them.
func TestUpgradeLinksJobsOfASkippedNestedFolder(t *testing.T) {
	root := t.TempDir()
	media, tv := filepath.Join(root, "media"), filepath.Join(root, "media", "tv")
	os.MkdirAll(tv, 0o755)
	path := v2DB(t)
	db, _ := sql.Open("sqlite", path)
	db.Exec(`UPDATE jobs SET library=?, path=?||'/'||id||'.mkv'`, tv, tv)
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.EnsureLibraries([]string{media, tv}, rules.LegacyTemplate(rules.Default(), "replace"))
	libs, _ := s.Libraries()
	list, _ := s.List(10)
	for _, j := range list {
		if j.LibraryID != libs[0].ID {
			t.Errorf("job %d (%s) linked to %d, want the library holding it (%d)", j.ID, j.Path, j.LibraryID, libs[0].ID)
		}
	}
}

func TestFreedBytesLeavesOutBesideResults(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	a, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Complete(a, "/m/a.mkv", 400) // replaced: 600 freed
	b, _ := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/b.mkv", SizeBefore: 1000})
	s.Claim("w")
	s.Complete(b, "/m/b.hevc.mkv", 400) // beside: the original is still there
	c, _ := s.Counts()
	if c[l.ID].FreedBytes != 600 {
		t.Errorf("freed %d, want 600 (beside results free nothing)", c[l.ID].FreedBytes)
	}
}

func TestJobsOnlyForStartedLibraries(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), false)
	if _, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv"}); err != ErrLibraryGone {
		t.Errorf("not started: %v", err)
	}
	s.StartLibrary(l.ID)
	if _, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/a.mkv"}); err != nil {
		t.Errorf("started: %v", err)
	}
	s.DeleteLibrary(l.ID)
	if _, err := s.Enqueue(Job{LibraryID: l.ID, Path: "/m/b.mkv"}); err != ErrLibraryGone {
		t.Errorf("removed: %v", err)
	}
}

func TestChangingALibrarysFlowForgetsItsFiles(t *testing.T) {
	s := open(t)
	a, _ := s.AddLibrary("A", "/media/a", rules.SliderTemplate(3), true)
	other, _ := s.AddFlow("Other", rules.SliderTemplate(1))
	for _, p := range []string{"/media/a/x.mkv", "/media/a/sub/y.mkv", "/media/ab/z.mkv", "/media/b/w.mkv"} {
		s.PutFile(File{Path: p, Size: 1, Decision: "skip: rule"})
	}
	if _, err := s.SetLibraryFlow(a.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	for p, kept := range map[string]bool{"/media/a/x.mkv": false, "/media/a/sub/y.mkv": false, "/media/ab/z.mkv": true, "/media/b/w.mkv": true} {
		if f, _ := s.GetFile(p); (f != nil) != kept {
			t.Errorf("%s: kept=%v, want %v (only the library's own files are forgotten)", p, f != nil, kept)
		}
	}
	if _, err := s.SetLibraryFlow(a.ID, 999); err != ErrNotFound {
		t.Errorf("unknown flow: %v", err)
	}
}

func TestFlowNamesAreUniqueInTheDatabase(t *testing.T) {
	s := open(t)
	s.AddFlow("Movies", rules.SliderTemplate(3))
	// past the name check, as two requests at the same moment could be: the index refuses it
	if _, err := s.addFlow(s.db, "Movies", rules.SliderTemplate(3)); nameErr(err) != ErrNameTaken {
		t.Errorf("duplicate name: %v", err)
	}
}

func TestAFlowInUseCannotBeDeletedEvenBehindTheAPI(t *testing.T) {
	s := open(t)
	l, _ := s.AddLibrary("A", "/a", rules.SliderTemplate(3), true)
	if _, err := s.db.Exec(`DELETE FROM flows WHERE id=?`, l.FlowID); err == nil {
		t.Error("foreign keys must refuse deleting a flow a library uses")
	}
	if err := s.DeleteFlow(l.FlowID); err == nil {
		t.Error("DeleteFlow must refuse too")
	}
}
