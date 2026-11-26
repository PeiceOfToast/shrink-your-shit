// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A file younger than MinAge (still being copied) is listed as settling at once, as a still_changing record with
// no job, and it is not read. It is decided as any other still-changing file: two looks MinAge apart, unchanged.
func TestYoungFileIsSettlingAtOnceAndDecidedAsBefore(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	os.Chtimes(a, clock, clock) // written just now
	look := func(want int, wantReason string) {
		t.Helper()
		if n := s.ScanOnce(context.Background()); n != want {
			t.Errorf("queued %d, want %d", n, want)
		}
		if r, _ := st.GetFile(a); wantReason != "" && (r == nil || r.Reason != wantReason) {
			t.Errorf("record %+v, want %s", r, wantReason)
		}
	}

	look(0, rules.SkipStillChanging) // young: settling, not a job
	if l, _ := st.List(10); len(l) != 0 {
		t.Errorf("a young file became a job: %+v", l)
	}
	if len(f.Recorded()) != 0 {
		t.Errorf("a young file must not be read: %v", f.Recorded())
	}

	clock = clock.Add(2 * time.Minute) // unchanged, and older than MinAge: the first look that counts
	look(0, rules.SkipStillChanging)
	clock = clock.Add(2 * time.Minute) // unchanged for MinAge since that look: decided
	look(1, "")
	if r, _ := st.GetFile(a); r == nil || r.Reason != "" || !strings.HasPrefix(r.Decision, "queued: ") {
		t.Errorf("queued file's record %+v", r)
	}
}

// A settling file that is renamed or deleted while it settles loses its row on the next complete walk of its library.
// A cancelled walk is not complete, so it leaves the row.
func TestSettlingRowOfRemovedFileIsForgotten(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	os.Chtimes(a, clock, clock)
	s.ScanOnce(context.Background()) // young: settling
	if r, _ := st.GetFile(a); r == nil {
		t.Fatal("no settling row for a young file")
	}
	os.Remove(a)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	s.ScanOnce(cancelled)
	if r, _ := st.GetFile(a); r == nil {
		t.Error("a cancelled walk cleared the settling row")
	}

	s.ScanOnce(context.Background())
	if r, _ := st.GetFile(a); r != nil {
		t.Errorf("settling row of a removed file kept: %+v", r)
	}
}

// A file whose time is in the future (clock skew, a NAS ahead) is not young forever: its age is negative, so it is
// decided like any other file, after two looks MinAge apart.
func TestFutureMtimeFileIsQueued(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now()
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	a := filepath.Join(lib, "a.mkv")
	future := clock.Add(time.Hour)
	os.Chtimes(a, future, future)
	look := func(want int) {
		t.Helper()
		if n := s.ScanOnce(context.Background()); n != want {
			t.Errorf("queued %d, want %d", n, want)
		}
	}
	look(0) // first look: waiting
	clock = clock.Add(2 * time.Minute)
	look(1) // unchanged for MinAge: queued
	if r, _ := st.GetFile(a); r == nil || r.Reason != "" || !strings.HasPrefix(r.Decision, "queued: ") {
		t.Errorf("future-time file's record %+v, want queued", r)
	}
}
