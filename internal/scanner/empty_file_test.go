// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// rowsOf is every job row for path, oldest first.
func rowsOf(t *testing.T, st *jobs.Store, path string) []jobs.Job {
	t.Helper()
	all, err := st.List(1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []jobs.Job
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Path == path {
			out = append(out, all[i])
		}
	}
	return out
}

// probedPath says whether ffprobe was called on path.
func probedPath(f *media.FakeExec, path string) bool {
	for _, c := range f.Recorded() {
		if strings.Contains(strings.Join(c, " "), path) {
			return true
		}
	}
	return false
}

// A 0-byte video, seen twice, is one failed job with its plain reason and is never probed. A later scan
// adds no row while it stays empty.
func TestEmptyVideoFailsOnceWithoutAProbe(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	z := filepath.Join(lib, "z.mkv")
	os.WriteFile(z, nil, 0o644)
	if n := settle(s); n != 1 { // a.mkv is queued; z is not
		t.Fatalf("queued %d, want 1", n)
	}
	rows := rowsOf(t, st, z)
	if len(rows) != 1 {
		t.Fatalf("%d job rows for the empty file, want 1", len(rows))
	}
	if rows[0].Status != jobs.Failed || rows[0].Error != emptyFileWords {
		t.Errorf("job: status %s error %q, want failed %q", rows[0].Status, rows[0].Error, emptyFileWords)
	}
	if probedPath(f, z) {
		t.Error("the empty file was probed")
	}
	settle(s)
	if got := len(rowsOf(t, st, z)); got != 1 {
		t.Errorf("%d job rows after another scan, want 1", got)
	}
}

// A 0-byte file on its first look is still_changing, not failed: it may still be being copied.
func TestEmptyVideoOnFirstLookIsStillChanging(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now().Add(time.Hour) // later than the file's own time, so the scan's age check lets it through
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	z := filepath.Join(lib, "z.mkv")
	os.WriteFile(z, nil, 0o644)
	s.ScanOnce(t.Context())
	if fi, _ := st.GetFile(z); fi == nil || fi.Reason != rules.SkipStillChanging {
		t.Fatalf("first look: file record %+v, want still_changing", fi)
	}
	if got := len(rowsOf(t, st, z)); got != 0 {
		t.Fatalf("%d job rows on the first look, want 0", got)
	}
	clock = clock.Add(2 * time.Minute)
	s.ScanOnce(t.Context())
	if rows := rowsOf(t, st, z); len(rows) != 1 || rows[0].Status != jobs.Failed {
		t.Fatalf("second look: rows %+v, want one failed job", rows)
	}
}

// When the file gets content it is decided again as normal: it is probed and queued.
func TestEmptyVideoThatGetsContentIsDecidedAgain(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	z := filepath.Join(lib, "z.mkv")
	os.WriteFile(z, nil, 0o644)
	settle(s)
	os.WriteFile(z, []byte("12345678"), 0o644)
	settle(s)
	rows := rowsOf(t, st, z)
	if len(rows) != 2 {
		t.Fatalf("%d job rows after the content arrived, want 2 (failed, then queued)", len(rows))
	}
	if rows[1].Status != jobs.Queued {
		t.Errorf("new job status %s, want queued", rows[1].Status)
	}
	if !probedPath(f, z) {
		t.Error("the file with content was not probed")
	}
}

// Touching an empty file that already failed adds no second failed row (review #429: the still-changing step overwrote
// the record, so the next look failed it again).
func TestEmptyVideoTouchedAfterFailingStaysOneRow(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	clock := time.Now().Add(time.Hour)
	s.Now, s.MinAge = func() time.Time { return clock }, time.Minute
	z := filepath.Join(lib, "z.mkv")
	os.WriteFile(z, nil, 0o644)
	for i := 0; i < 2; i++ {
		s.ScanOnce(t.Context())
		clock = clock.Add(2 * time.Minute)
	}
	if got := len(rowsOf(t, st, z)); got != 1 {
		t.Fatalf("%d job rows before the touch, want 1", got)
	}
	touched := clock.Add(-30 * time.Second)
	if err := os.Chtimes(z, touched, touched); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.ScanOnce(t.Context())
		clock = clock.Add(2 * time.Minute)
	}
	if got := len(rowsOf(t, st, z)); got != 1 {
		t.Errorf("%d job rows after a touch, want 1", got)
	}
}
