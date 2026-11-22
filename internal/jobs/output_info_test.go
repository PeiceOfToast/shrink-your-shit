// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Foundation slice 4 (migration 60): a database from before it opens, its jobs have no output_info, and
// the store keeps one on a job once the check has read its new file.
func TestMigration60AddsOutputInfoAndOldJobsHaveNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Enqueue(Job{Path: "/m/old.mkv", DurationSec: 100, SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	db := rawDB(t, path) // back to version 58: as an install from before this left it
	for _, q := range append([]string{`ALTER TABLE jobs DROP COLUMN output_info`, `ALTER TABLE jobs DROP COLUMN queued_rev`, `PRAGMA user_version=58`}, rewindEncoderSpeeds...) {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if raw, ok := s.OutputInfo(id); ok {
		t.Errorf("an old job has output_info %q, want none", raw)
	}
	if err := s.SetOutputInfo(id, `{"videoCodec":"hevc"}`); err != nil {
		t.Fatal(err)
	}
	if raw, ok := s.OutputInfo(id); !ok || raw != `{"videoCodec":"hevc"}` {
		t.Errorf("output_info %q, %v; want the stored probe", raw, ok)
	}
}

// Review #608: a statement after a comma in Exec is a bind argument, so output_info survived the rewind and a
// test that rewound to 59 and called only rewindDecidedAt would fail on Open ("duplicate column name"). This
// pins the split: the column is gone, and opening at 59 again works.
func TestRewindDecidedAtTakesOutputInfoAway(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rewindDecidedAt(t, s)
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('jobs') WHERE name='output_info'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("output_info survives rewindDecidedAt: migration 60 would add a duplicate column on the next open")
	}
	if _, err := s.db.Exec(`PRAGMA user_version=59`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("opening at 59 after the rewind: %v", err) // migration 60 runs again
	}
	defer s.Close()
}

// #608 follow-up: Retry and Convert again are fresh runs on the same job row, so the probe the last run took
// goes with the other per-run fields. A retried job that fails must not show the previous file's info, and the
// detail then falls back to today's read of the probes table (internal/web's test).
func TestRetryAndConvertAgainClearOutputInfo(t *testing.T) {
	const probe = `{"videoCodec":"hevc"}`
	t.Run("Retry", func(t *testing.T) {
		s := open(t)
		id, err := s.Enqueue(Job{Path: filepath.Join(t.TempDir(), "kept.mkv"), SizeBefore: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim("w"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetOutputInfo(id, probe); err != nil {
			t.Fatal(err)
		}
		if err := s.Keep(id, "pictures_differ", "the picture check refused the new file"); err != nil {
			t.Fatal(err)
		}
		if err := s.Retry(id); err != nil {
			t.Fatal(err)
		}
		if raw, ok := s.OutputInfo(id); ok {
			t.Errorf("a retried job still shows the last run's probe %q", raw)
		}
	})
	t.Run("RetryLater", func(t *testing.T) {
		s := open(t)
		id, err := s.Enqueue(Job{Path: filepath.Join(t.TempDir(), "later.mkv"), SizeBefore: 100})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim("w"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetOutputInfo(id, probe); err != nil {
			t.Fatal(err)
		}
		if err := s.RetryLater(id, time.Now().Add(time.Minute), "replace_failed", "later"); err != nil {
			t.Fatal(err)
		}
		if raw, ok := s.OutputInfo(id); ok {
			t.Errorf("a job waiting to retry still shows the last run's probe %q", raw)
		}
	})
	t.Run("Convert again", func(t *testing.T) {
		s := open(t)
		dir := t.TempDir()
		in := filepath.Join(dir, "in", "a.mkv")
		if err := os.MkdirAll(filepath.Dir(in), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(in, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := s.Enqueue(Job{Path: in, SizeBefore: 100})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim("w"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetOutputInfo(id, probe); err != nil {
			t.Fatal(err)
		}
		if err := s.Complete(id, filepath.Join(dir, "out", "a.mkv"), 40); err != nil {
			t.Fatal(err)
		}
		if err := s.ConvertAgain(id); err != nil {
			t.Fatal(err)
		}
		if raw, ok := s.OutputInfo(id); ok {
			t.Errorf("a converting-again job still shows the last run's probe %q", raw)
		}
	})
}

// SetStepWords replaces the latest step of that name (a retry adds another converted step) and leaves the
// others alone.
func TestSetStepWordsUpdatesTheLatestStepOfItsName(t *testing.T) {
	s := open(t)
	id, err := s.Enqueue(Job{Path: "/m/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range [][2]string{{"converted", "Converted: HEVC · 10-bit · MKV."}, {"verified", "Checked."},
		{"converted", "Converted: HEVC · 10-bit · MKV."}} { // a retry adds another converted step
		if err := s.AddStep(id, st[0], true, st[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetStepWords(id, "converted", "Converted: HEVC · 8-bit · MKV."); err != nil {
		t.Fatal(err)
	}
	steps, err := s.Steps(id)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Converted: HEVC · 10-bit · MKV.", "Checked.", "Converted: HEVC · 8-bit · MKV."}
	if len(steps) != len(want) {
		t.Fatalf("steps %+v", steps)
	}
	for i := range want {
		if steps[i].Words != want[i] {
			t.Errorf("step %d %q, want %q", i, steps[i].Words, want[i])
		}
	}
}
