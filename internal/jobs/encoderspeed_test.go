// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// F7: a finished conversion's encode step keeps its speed, and the remembered speed is the median per
// worker and encoder over its latest conversions: the newest ones, a copy and a refused job left out, and
// fps over the samples that said it (an older remote worker says none).
func TestEncoderSpeedsAreMediansPerWorkerAndEncoder(t *testing.T) {
	s := open(t)
	l, err := s.AddLibrary("m", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	code, _, err := s.NewPairing(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rw, _, err := s.Pair(code, "Laptop", "1", "", "", 1, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	keep := func(worker, encoder string, speed, fps float64) {
		n++
		id, err := s.Enqueue(Job{Library: "m", LibraryID: l.ID, Path: filepath.Join(l.Path, fmt.Sprintf("%d.mkv", n))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(worker); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordRunStep(id, RunStep{Index: 0, Name: "encode", Encoder: encoder, Speed: speed, FPS: fps}); err != nil {
			t.Fatal(err)
		}
		if err := s.Complete(id, filepath.Join(l.Path, "out.mkv"), 1); err != nil {
			t.Fatal(err)
		}
	}
	keep(LocalWorker, "hevc_qsv", 1, 10)
	keep(LocalWorker, "hevc_qsv", 2, 0) // an older remote worker's conversion: no fps
	keep(LocalWorker, "hevc_qsv", 60, 30)
	keep(rw.ID, "libx265", 2, 0)
	keep(rw.ID, "libx265", 4, 0)
	keep(rw.ID, "libx265", 6, 0)
	keep(rw.ID, "libx265", 8, 0)
	keep(LocalWorker, "", 0, 0) // a copy: nothing measured it, so it is not a sample
	// a refused conversion measured its speed too, but it is not a finished conversion
	id, err := s.Enqueue(Job{Library: "m", LibraryID: l.ID, Path: filepath.Join(l.Path, "kept.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(LocalWorker); err != nil {
		t.Fatal(err)
	}
	s.RecordRunStep(id, RunStep{Index: 0, Name: "encode", Encoder: "libx265", Speed: 99, FPS: 999})
	if err := s.Keep(id, ReasonKeptOriginal, "the picture differs"); err != nil {
		t.Fatal(err)
	}

	got, err := s.EncoderSpeeds(50)
	if err != nil {
		t.Fatal(err)
	}
	want := []EncoderSpeed{
		{Worker: LocalWorker, Encoder: "hevc_qsv", Speed: 2, FPS: 20, Samples: 3},
		{Worker: rw.ID, Encoder: "libx265", Speed: 5, Samples: 4},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("speeds %+v, want %+v", got, want)
	}
}

// F7: a pair with more conversions than asked for keeps its newest: the median moves with the new ones
// and the old ones fall away.
func TestEncoderSpeedsKeepTheNewestConversions(t *testing.T) {
	s := open(t)
	l, err := s.AddLibrary("m", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 52; i++ {
		id, err := s.Enqueue(Job{Library: "m", LibraryID: l.ID, Path: filepath.Join(l.Path, fmt.Sprintf("%d.mkv", i))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(LocalWorker); err != nil {
			t.Fatal(err)
		}
		s.RecordRunStep(id, RunStep{Index: 0, Name: "encode", Encoder: "libx264", Speed: float64(i)})
		if err := s.Complete(id, filepath.Join(l.Path, "out.mkv"), 1); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.EncoderSpeeds(50)
	if err != nil {
		t.Fatal(err)
	}
	// the newest 50 ran at 3x to 52x: their median is 27.5, with 1x and 2x left out
	if len(got) != 1 || got[0].Samples != 50 || got[0].Speed != 27.5 || got[0].FPS != 0 {
		t.Fatalf("speeds %+v, want 50 samples at 27.5x", got)
	}
	if got, _ := s.EncoderSpeeds(2); len(got) != 1 || got[0].Samples != 2 || got[0].Speed != 51.5 {
		t.Fatalf("two samples %+v, want the newest two (51.5x)", got)
	}
}

// F7 review #638 §3: the scan behind a workers GET is bounded, so an old conversion falls out of the
// figures once enough newer ones are there (it is still served, only not read).
func TestEncoderSpeedsReadOnlyTheNewestEncodes(t *testing.T) {
	old := encoderSpeedScan
	encoderSpeedScan = 2
	t.Cleanup(func() { encoderSpeedScan = old })
	s := open(t)
	l, err := s.AddLibrary("m", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		id, err := s.Enqueue(Job{Library: "m", LibraryID: l.ID, Path: filepath.Join(l.Path, fmt.Sprintf("%d.mkv", i))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(LocalWorker); err != nil {
			t.Fatal(err)
		}
		s.RecordRunStep(id, RunStep{Index: 0, Name: "encode", Encoder: "libx264", Speed: float64(i)})
		if err := s.Complete(id, filepath.Join(l.Path, "out.mkv"), 1); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.EncoderSpeeds(50)
	if err != nil {
		t.Fatal(err)
	}
	// the newest two ran at 2x and 3x: the first conversion is past the scan
	if len(got) != 1 || got[0].Samples != 2 || got[0].Speed != 2.5 {
		t.Fatalf("speeds %+v, want the newest two (2.5x over 2 samples)", got)
	}
}

// F7 review2 #638 §4: samples are ordered by when the conversion ran, not by the job's id, because Convert
// again reuses the id: an old job converted again today is the newest sample.
func TestEncoderSpeedsOrderByWhenItRan(t *testing.T) {
	s := open(t)
	dir := t.TempDir()
	finished := func(name, at string, speed float64) int64 {
		src := filepath.Join(dir, "in", name)
		if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(src, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		id, err := s.Enqueue(Job{Path: src, SizeBefore: 100})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(LocalWorker); err != nil {
			t.Fatal(err)
		}
		s.RecordRunStep(id, RunStep{Index: 0, Name: "encode", StartedAt: at, Encoder: "libx265", Speed: speed})
		if err := s.Complete(id, filepath.Join(dir, "out", name), 1); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first := finished("a.mkv", "2026-01-01T00:00:00Z", 1)
	finished("b.mkv", "2026-01-02T00:00:00Z", 2)
	finished("c.mkv", "2026-01-03T00:00:00Z", 3)
	// the first job is converted again today: its new run replaces its step and is the newest sample
	if err := s.ConvertAgain(first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(LocalWorker); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRunStep(first, RunStep{Index: 0, Name: "encode", StartedAt: "2026-01-04T00:00:00Z", Encoder: "libx265", Speed: 40}); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(first, filepath.Join(dir, "out", "a.mkv"), 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.EncoderSpeeds(2)
	if err != nil {
		t.Fatal(err)
	}
	// the newest two conversions are the re-converted old job (40x) and the last one (3x): 21.5x
	if len(got) != 1 || got[0].Samples != 2 || got[0].Speed != 21.5 {
		t.Fatalf("speeds %+v, want the newest two by start time (21.5x over 2 samples)", got)
	}
}

// Migration 61 (F7): an install from before it keeps its run steps, which read no speed, and a new step
// keeps the encoder, the realtime multiple and the frames per second.
func TestMigration61AddsEncoderSpeedsKeepingOldSteps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := latestVersion(); v != 61 {
		t.Fatalf("latest migration %d, want 61", v)
	}
	id, err := s.Enqueue(Job{Path: "/m/a.mkv"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordRunStep(id, RunStep{Index: 0, Name: "encode", Command: "ffmpeg -i a.mkv", Encoder: "hevc_qsv", Speed: 3.5, FPS: 120}); err != nil {
		t.Fatal(err)
	}
	steps, err := s.RunSteps(id)
	if err != nil || len(steps) != 1 || steps[0].Encoder != "hevc_qsv" || steps[0].Speed != 3.5 || steps[0].FPS != 120 {
		t.Fatalf("a new step reads back %+v, %v", steps, err)
	}
	indexExists := func() bool {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'job_run_steps_name_started'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	if !indexExists() { // review2 #638 §3: the workers read of the newest encodes is served by it
		t.Error("migration 61 made no index on job_run_steps(name, started_at, job_id)")
	}
	s.Close()
	db := rawDB(t, path)
	for _, q := range append(rewindEncoderSpeeds, `PRAGMA user_version=60`) {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()
	s, err = Open(path) // migration 61 runs again
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	steps, err = s.RunSteps(id)
	if err != nil || len(steps) != 1 || steps[0].Command != "ffmpeg -i a.mkv" {
		t.Fatalf("an old step reads back %+v, %v", steps, err)
	}
	if steps[0].Encoder != "" || steps[0].Speed != 0 || steps[0].FPS != 0 {
		t.Errorf("an old step kept a speed: %+v", steps[0])
	}
	// migration 61 ran again with the index still there (a rewind fixture's path): IF NOT EXISTS kept it
	if !indexExists() {
		t.Error("re-running migration 61 lost the index")
	}
	if got, err := s.EncoderSpeeds(50); err != nil || len(got) != 0 {
		t.Errorf("an old install has speeds %+v, %v; want none", got, err)
	}
}
