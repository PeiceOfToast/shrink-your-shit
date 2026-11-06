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

func TestTempNamesAreUnpredictableAndFound(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "My.Movie (2020).MKV")
	a, _, err := newTemp(src, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := newTemp(src, 0o644)
	if a == b {
		t.Fatalf("two temp files got the same name %q", a)
	}
	for _, p := range []string{a, b} {
		n := filepath.Base(p)
		stem := strings.TrimSuffix(n, filepath.Ext(n))
		if filepath.Ext(n) != ".MKV" || !strings.HasPrefix(n, "My.Movie (2020).") || !strings.HasSuffix(stem, ".syp-tmp") {
			t.Errorf("%q must keep the extension and end in .syp-tmp before it (the scanner skips those)", n)
		}
	}
	legacy := filepath.Join(dir, "My.Movie (2020).syp-tmp.MKV")
	others := []string{"My.Movie (2020).part2.syp-tmp.MKV", "My.Movie (2020).hevc.MKV", "My.Movie (2020).1.syp-tmp.mkv", "Other.1.syp-tmp.MKV"}
	for _, n := range append(others, filepath.Base(legacy)) {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	got, _ := TempFiles(src)
	want := []string{a, b, legacy}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("TempFiles = %q, want %q (ours and the old fixed name only)", got, want)
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
	for _, n := range []string{"movie.syp-tmp.mkv", "movie.123.syp-tmp.mkv"} {
		if err := os.Symlink(victim, filepath.Join(s.dir, n)); err != nil {
			t.Fatal(err)
		}
	}
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("%s %q", j.Status, j.Error)
	}
	if b, _ := os.ReadFile(victim); string(b) != "someone else's file" {
		t.Errorf("the link's target was written: %q", b)
	}
	if tempLeft(s.src) {
		t.Error("a temp entry was left behind")
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
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "cannot remove the old temp file") {
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

// swapTemp puts a different file, holding body, where the temp file is.
func swapTemp(t *testing.T, s *scenario, body string) {
	t.Helper()
	l, _ := TempFiles(s.src)
	if len(l) != 1 {
		t.Fatalf("want one temp file, have %q", l)
	}
	other := filepath.Join(s.dir, "swap.bin")
	os.WriteFile(other, []byte(body), 0o644)
	if err := os.Rename(other, l[0]); err != nil {
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
