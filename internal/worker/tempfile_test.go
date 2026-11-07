// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

func tempLeft(src string) bool {
	l, _ := TempFiles(src)
	return len(l) > 0
}

func TestStagingFoldersAreUnpredictableAndFound(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "My.Movie (2020).MKV")
	a, err := newStaged(src, filepath.Ext(src))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newStaged(src, filepath.Ext(src))
	if a.dir == b.dir {
		t.Fatalf("two staging folders got the same name %q", a.dir)
	}
	for _, st := range []*staged{a, b} {
		if n := filepath.Base(st.dir); !strings.HasPrefix(n, ".") {
			t.Errorf("%q must start with a dot (the scanner skips those)", n)
		}
		if filepath.Ext(st.path) != ".MKV" || filepath.Dir(st.path) != st.dir {
			t.Errorf("%q must be inside the folder and keep the extension", st.path)
		}
	}
	legacy := filepath.Join(dir, "My.Movie (2020).syp-tmp.MKV")
	others := []string{".My.Movie (2020).part2.syp-tmp", "My.Movie (2020).hevc.MKV", ".Other.1.syp-tmp", "My.Movie (2020).1.syp-tmp"}
	for _, n := range append(others, filepath.Base(legacy)) {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	got, _ := TempFiles(src)
	want := []string{a.dir, b.dir, legacy}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("TempFiles = %q, want %q (our folders and the old fixed name only)", got, want)
	}
}

// While ffmpeg writes and the checks run, only this user can reach the new file.
func TestStagingIsPrivateUntilTheMove(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	s := newScenario(t)
	os.Chmod(s.src, 0o664)
	var dirMode, fileMode os.FileMode
	s.afterChecks = func() {
		l, _ := TempFiles(s.src)
		if len(l) != 1 {
			t.Fatalf("want one staging folder, have %q", l)
		}
		dirMode = mustStat(t, l[0]).Mode().Perm()
		fileMode = mustStat(t, filepath.Join(l[0], "new.mkv")).Mode().Perm()
	}
	s.run(Replace)
	if dirMode != 0o700 || fileMode != 0o600 {
		t.Errorf("staging folder %v, file %v; want 0700 and 0600", dirMode, fileMode)
	}
	if st := mustStat(t, s.src); st.Mode().Perm() != 0o664 {
		t.Errorf("the replaced file has %v, want the original's 0664", st.Mode().Perm())
	}
	if tempLeft(s.src) {
		t.Error("the staging folder was left behind after a successful move")
	}
}

// A link left where a temp file goes is removed, and the file it points to is never written.
func TestLeftoverTempLinksAreRemovedNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making links needs extra rights on Windows")
	}
	s := newScenario(t)
	victim := filepath.Join(t.TempDir(), "other.mkv")
	os.WriteFile(victim, []byte("someone else's file"), 0o644)
	// a link under the old fixed temp name is removed; one under a staging-folder name is not a
	// staging folder, so it is left alone; neither is followed
	if err := os.Symlink(victim, filepath.Join(s.dir, "movie.syp-tmp.mkv")); err != nil {
		t.Fatal(err)
	}
	stagingLink := filepath.Join(s.dir, ".movie.123.syp-tmp")
	if err := os.Symlink(s.dir, stagingLink); err != nil {
		t.Fatal(err)
	}
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if b, _ := os.ReadFile(victim); string(b) != "someone else's file" {
		t.Errorf("the link's target was written: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(s.dir, "movie.syp-tmp.mkv")); err == nil {
		t.Error("the link under the old temp name was not removed")
	}
	if _, err := os.Lstat(stagingLink); err != nil {
		t.Error("a link that is not a staging folder was removed")
	}
	if b, _ := os.ReadFile(s.src); string(b) != "new" {
		t.Errorf("the library folder behind the link was touched: original is %q", b)
	}
}

// Only a staging folder holding nothing but our temp file is removed. A folder with the same kind of
// name but other contents may not be ours: it and everything in it stay, and the job still runs.
func TestLeftoverCleanupRemovesOnlyRecognisedStagingFolders(t *testing.T) {
	s := newScenario(t)
	ours := filepath.Join(s.dir, ".movie.111.syp-tmp")
	empty := filepath.Join(s.dir, ".movie.222.syp-tmp")
	theirs := filepath.Join(s.dir, ".movie.333.syp-tmp")
	nested := filepath.Join(s.dir, ".movie.444.syp-tmp")
	linked := filepath.Join(s.dir, ".movie.555.syp-tmp") // holds only a link named like our file
	for _, d := range []string{ours, empty, theirs, linked, filepath.Join(nested, "new.mkv")} {
		os.MkdirAll(d, 0o700)
	}
	target := filepath.Join(t.TempDir(), "target.mkv")
	os.WriteFile(target, []byte("link target"), 0o644)
	hasLinks := os.Symlink(target, filepath.Join(linked, "new.mkv")) == nil // Windows may refuse
	os.WriteFile(filepath.Join(ours, "new.mkv"), []byte("half-written"), 0o600)
	os.WriteFile(filepath.Join(theirs, "new.mkv"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(theirs, "notes.txt"), []byte("keep me"), 0o644)
	os.WriteFile(filepath.Join(nested, "new.mkv", "inner.txt"), []byte("keep me too"), 0o644)
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	for _, gone := range []string{ours, empty} {
		if _, err := os.Lstat(gone); err == nil {
			t.Errorf("%s is ours and should have been removed", filepath.Base(gone))
		}
	}
	for p, want := range map[string]string{
		filepath.Join(theirs, "notes.txt"):            "keep me",
		filepath.Join(theirs, "new.mkv"):              "x",
		filepath.Join(nested, "new.mkv", "inner.txt"): "keep me too",
	} {
		if b, err := os.ReadFile(p); err != nil || string(b) != want {
			t.Errorf("%s: %q %v, want it left alone", p, b, err)
		}
	}
	if hasLinks {
		if fi, err := os.Lstat(filepath.Join(linked, "new.mkv")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("a link named like our temp file is not ours and must be left alone: %v", err)
		}
		if b, _ := os.ReadFile(target); string(b) != "link target" {
			t.Errorf("the link's target changed: %q", b)
		}
	}
}

// A leftover that cannot be removed stops the job before ffmpeg runs.
func TestUnremovableTempStopsBeforeEncoding(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder the test cannot delete from (POSIX, not root)")
	}
	s := newScenario(t)
	victim := filepath.Join(t.TempDir(), "other.mkv")
	os.WriteFile(victim, []byte("someone else's file"), 0o644)
	if err := os.Symlink(victim, filepath.Join(s.dir, "movie.syp-tmp.mkv")); err != nil {
		t.Fatal(err)
	}
	os.Chmod(s.dir, 0o555)
	t.Cleanup(func() { os.Chmod(s.dir, 0o755) })
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "movie.syp-tmp.mkv") || strings.Contains(j.Error, s.dir) {
		t.Errorf("want failed naming the temp file, got %s %q", j.Status, j.Error)
	}
	if n := s.count("libx265"); n != 0 {
		t.Errorf("ffmpeg ran %d times; it must not start", n)
	}
	if b, _ := os.ReadFile(victim); string(b) != "someone else's file" {
		t.Errorf("the link's target was written: %q", b)
	}
	if b, _ := os.ReadFile(s.src); string(b) != "original-bytes" {
		t.Errorf("the original was touched: %q", b)
	}
}

// swapTemp puts a different file, holding body, where the staged file is (the same user can still do
// that; other users cannot reach the folder).
func swapTemp(t *testing.T, s *scenario, body string) {
	t.Helper()
	l, _ := TempFiles(s.src)
	if len(l) != 1 {
		t.Fatalf("want one temp file, have %q", l)
	}
	other := filepath.Join(s.dir, "swap.bin")
	os.WriteFile(other, []byte(body), 0o644)
	if err := os.Rename(other, filepath.Join(l[0], "new.mkv")); err != nil {
		t.Fatal(err)
	}
}

func TestTempSwappedDuringEncodingIsNotUsed(t *testing.T) {
	s := newScenario(t)
	s.onEncode = func() { swapTemp(t, s, "unchecked") }
	s.run(Replace)
	s.assertKept("replaced by another file")
	if s.count("-lavfi") != 0 {
		t.Error("checks ran on a file that is not ours")
	}
}

func TestTempSwappedAfterTheChecksIsNotUsed(t *testing.T) {
	for _, mode := range []OutputMode{Replace, Beside} {
		s := newScenario(t)
		s.afterChecks = func() { swapTemp(t, s, "unchecked") }
		s.run(mode)
		s.assertKept("replaced by another file")
	}
}

// Same size, modification time put back: only the change time shows the edit.
func TestSourceEditKeepingSizeAndTimeIsKept(t *testing.T) {
	if _, ok := changeTime(mustStat(t, t.TempDir())); !ok {
		t.Skip("this system reports no change time")
	}
	s := newScenario(t)
	s.onEncode = func() {
		st := mustStat(t, s.src)
		time.Sleep(20 * time.Millisecond) // past the change-time resolution
		os.WriteFile(s.src, []byte("ORIGINAL-BYTES"), 0o644)
		os.Chtimes(s.src, st.ModTime(), st.ModTime())
	}
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Kept || !strings.Contains(j.Error, "changed") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if b, _ := os.ReadFile(s.src); string(b) != "ORIGINAL-BYTES" {
		t.Errorf("the edited original was replaced: %q", b)
	}
}

// Another file of the same size and modification time put in the original's place.
func TestSourceReplacedKeepingSizeAndTimeIsKept(t *testing.T) {
	s := newScenario(t)
	s.onEncode = func() {
		st := mustStat(t, s.src)
		other := filepath.Join(s.dir, "newer.bin")
		os.WriteFile(other, []byte("ORIGINAL-BYTES"), 0o644)
		os.Chtimes(other, st.ModTime(), st.ModTime())
		if err := os.Rename(other, s.src); err != nil {
			t.Fatal(err)
		}
	}
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Kept || !strings.Contains(j.Error, "changed") {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if b, _ := os.ReadFile(s.src); string(b) != "ORIGINAL-BYTES" {
		t.Errorf("the newer file was replaced: %q", b)
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
