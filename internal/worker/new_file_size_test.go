// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// The new file's size comes from its stat; when that stat fails, the size measured before the move stands in
// and the job log says so. Only a failed stat with no measured size is reported as unknown.
func TestNewFileSizeFallsBack(t *testing.T) {
	p := filepath.Join(t.TempDir(), "new.mkv")
	if err := os.WriteFile(p, make([]byte, 77), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if size, note := newFileSize(st, 1234); size != 77 || note != "" {
		t.Fatalf("stat ok: size %d note %q, want 77 and no note", size, note)
	}
	size, note := newFileSize(nil, 1234)
	if size != 1234 || !strings.Contains(note, "using the size measured before the move (1234 bytes)") {
		t.Fatalf("stat failed, measured 1234: size %d note %q", size, note)
	}
	size, note = newFileSize(nil, 0)
	if size != 0 || !strings.Contains(note, "size unknown") {
		t.Fatalf("stat failed, nothing measured: size %d note %q, want 0 and size unknown", size, note)
	}
}

// A stat that reports 0 bytes for a verified file uses the measured size and says so in the job log.
func TestNewFileSizeZeroStatUsesMeasured(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.mkv")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	size, note := newFileSize(st, 1234)
	if size != 1234 || !strings.Contains(note, "reported 0 bytes") {
		t.Fatalf("zero stat, measured 1234: size %d note %q", size, note)
	}
}

// Replace: when the stat of the new file fails, the job records the verified size and logs why.
func TestReplaceStatFailureRecordsVerifiedSize(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.statFinal = func(p string) (os.FileInfo, error) {
		if p == s.src {
			return nil, errors.New("stat refused")
		}
		return os.Stat(p)
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := w.verified.SizeBytes // the probe of the new file, the size measured before the move
	if want <= 0 {
		t.Fatalf("verified size %d, want the probe's size", want)
	}
	j := s.job()
	if j.Status != jobs.Done || j.SizeAfter != want {
		t.Fatalf("status %s size after %d, want done with %d bytes", j.Status, j.SizeAfter, want)
	}
	if lines, _ := s.store.LogLines(s.id); !hasLogLine(lines, fmt.Sprintf("using the size measured before the move (%d bytes)", want)) {
		t.Fatalf("no size fallback line in the job log")
	}
}

// Replace: a stat that reports 0 bytes for the new file records the verified size, not 0.
func TestReplaceZeroStatRecordsVerifiedSize(t *testing.T) {
	s := newScenario(t)
	zero := filepath.Join(t.TempDir(), "zero.mkv")
	if err := os.WriteFile(zero, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	w := s.worker(Replace)
	w.statFinal = func(p string) (os.FileInfo, error) {
		if p == s.src {
			return os.Stat(zero)
		}
		return os.Stat(p)
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := w.verified.SizeBytes
	if want <= 0 {
		t.Fatalf("verified size %d, want the probe's size", want)
	}
	if j := s.job(); j.Status != jobs.Done || j.SizeAfter != want {
		t.Fatalf("status %s size after %d, want done with %d bytes", j.Status, j.SizeAfter, want)
	}
}

func hasLogLine(lines []jobs.LogLine, want string) bool {
	for _, l := range lines {
		if strings.Contains(l.Text, want) {
			return true
		}
	}
	return false
}
