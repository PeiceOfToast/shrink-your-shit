// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// syncEvent is one sync the worker asked for, with what was at the original's name at that moment.
type syncEvent struct {
	kind, path, atName string
}

// recordSyncs replaces syncFile and syncDir for the test (they still sync), noting each call and
// what was at name then.
func recordSyncs(t *testing.T, name string) *[]syncEvent {
	var got []syncEvent
	f, d := syncFile, syncDir
	t.Cleanup(func() { syncFile, syncDir = f, d })
	syncFile = func(p string) error { got = append(got, syncEvent{"file", p, read(name)}); return f(p) }
	syncDir = func(p string) error { got = append(got, syncEvent{"dir", p, read(name)}); return d(p) }
	return &got
}

// R1: the new file is synced before the first rename, and every folder a rename touched is synced after
// it: the original's folder once the new file has its name, and the trash folder once the original is in it.
func TestReplaceSyncsFileThenFolders(t *testing.T) {
	for _, days := range []int{0, 7} {
		s := newScenario(t)
		got := recordSyncs(t, s.src)
		if _, err := s.keeping(days).RunOne(context.Background()); err != nil {
			t.Fatal(err)
		}
		if j := s.job(); j.Status != jobs.Done {
			t.Fatalf("days %d: %s %q", days, j.Status, j.Error)
		}
		ev := *got
		if len(ev) == 0 || ev[0].kind != "file" || ev[0].atName != "original-bytes" {
			t.Fatalf("days %d: the new file must be synced first, while the original is still at its name: %+v", days, ev)
		}
		if filepath.Dir(filepath.Dir(ev[0].path)) != s.dir { // the staged file, in its folder beside the original
			t.Errorf("days %d: synced %s, not the staged file", days, ev[0].path)
		}
		newDirSynced := false
		for _, e := range ev[1:] {
			if e.kind == "dir" && e.path == s.dir && e.atName == "new" {
				newDirSynced = true
			}
		}
		if !newDirSynced {
			t.Errorf("days %d: the folder was not synced after the new file took the name: %+v", days, ev)
		}
		trashSynced := false
		for _, e := range ev {
			if e.kind == "dir" && e.path == filepath.Join(s.dir, TrashDir) {
				trashSynced = true
			}
		}
		if trashSynced != (days > 0) {
			t.Errorf("days %d: trash folder synced %v: %+v", days, trashSynced, ev)
		}
	}
}

// R1: beside the original, the new file is synced before its rename and its folder after.
func TestBesideSyncsFileThenFolder(t *testing.T) {
	s := newScenario(t)
	got := recordSyncs(t, s.src)
	if _, err := s.worker(Beside).RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Done || j.OutputPath == s.src {
		t.Fatalf("%s %q %s", j.Status, j.Error, j.OutputPath)
	}
	ev := *got
	if len(ev) < 2 || ev[0].kind != "file" || ev[len(ev)-1].kind != "dir" {
		t.Fatalf("%+v", ev)
	}
	ok := false
	for _, e := range ev {
		if e.kind == "dir" && e.path == filepath.Dir(j.OutputPath) {
			ok = true
		}
	}
	if !ok {
		t.Errorf("the new file's folder was not synced: %+v", ev)
	}
}

// R1: a new file that can't be synced never replaces the original.
func TestSyncFailureKeepsTheOriginal(t *testing.T) {
	s := newScenario(t)
	f := syncFile
	t.Cleanup(func() { syncFile = f })
	syncFile = func(string) error { return errors.New("disk said no") }
	s.keeping(0).RunOne(context.Background())
	if j := s.job(); j.Status == jobs.Done || read(s.src) != "original-bytes" {
		t.Fatalf("%s %q, at the name %q", j.Status, j.Error, read(s.src))
	}
	if tempLeft(s.src) {
		t.Error("a temp file was left behind")
	}
}

// R1: a folder sync that fails after the rename is only logged: the file is in place.
func TestFolderSyncFailureIsLogged(t *testing.T) {
	s := newScenario(t)
	d := syncDir
	t.Cleanup(func() { syncDir = d })
	syncDir = func(string) error { return errors.New("not here") }
	s.keeping(7).RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done || read(s.src) != "new" {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
}

// A file system that says it can't sync is not an error.
func TestSyncUnsupportedIsNotAnError(t *testing.T) {
	if !syncUnsupported(&os.PathError{Op: "sync", Path: "x", Err: errors.ErrUnsupported}) || syncUnsupported(errors.New("io")) {
		t.Error("syncUnsupported")
	}
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("x"), 0o444) // read-only: still synced (or skipped on Windows)
	if err := syncFile(p); err != nil {
		t.Error(err)
	}
	if err := syncDir(filepath.Dir(p)); err != nil {
		t.Error(err)
	}
}

// Real ffmpeg (R1): a 10 s sample converts end to end with the syncs in place; the new file plays and
// the original is in the trash.
func TestRealReplaceSyncs(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "clip-s.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=10:size=640x360:rate=25", "-f", "lavfi", "-i", "sine=duration=10",
		"-map", "0", "-map", "1", "-c:v", "libx264", "-b:v", "4000k", "-c:a", "aac", src)
	got := recordSyncs(t, src)
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false)
	id, _ := st.Enqueue(jobs.Job{Library: dir, Path: src, DurationSec: 10})
	w := realWorker(st, media.OSExec{})
	tmpl := rules.DefaultSelectors(3).Template()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	w.KeepDays, w.Journal = func() int { return 7 }, st
	w.run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	runFFmpeg(t, "-v", "error", "-i", j.OutputPath, "-f", "null", "-") // plays to the end
	if _, err := os.Stat(filepath.Join(dir, TrashDir)); err != nil {
		t.Errorf("no trash entry: %v", err)
	}
	files, dirs := 0, 0
	for _, e := range *got {
		if e.kind == "file" {
			files++
		} else {
			dirs++
		}
	}
	if files == 0 || dirs < 2 {
		t.Errorf("syncs: %+v", *got)
	}
}
