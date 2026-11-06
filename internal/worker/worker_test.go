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

type setup struct {
	store *jobs.Store
	dir   string
	src   string
	id    int64
}

func newSetup(t *testing.T) setup {
	t.Helper()
	s, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	dir := t.TempDir()
	src := filepath.Join(dir, "movie.mkv")
	os.WriteFile(src, []byte("original-bytes"), 0o644)
	id, _ := s.Enqueue(jobs.Job{Library: dir, Path: src, DurationSec: 100, SizeBefore: 14})
	return setup{s, dir, src, id}
}

func (s setup) worker(x media.Exec, mode OutputMode) *Worker {
	return &Worker{ID: "t", Queue: s.store, Exec: x, FFmpeg: "ffmpeg", Rule: rules.Default(), Mode: mode}
}

// fakeFFmpeg writes the output file like ffmpeg would (last arg is the output path).
func fakeFFmpeg(content string) *media.FakeExec {
	f := &media.FakeExec{Lines: []string{"out_time_us=50000000"}}
	f.OnCall = func(name string, args []string) {
		os.WriteFile(args[len(args)-1], []byte(content), 0o644)
	}
	return f
}

func TestReplaceSuccess(t *testing.T) {
	s := newSetup(t)
	ran, err := s.worker(fakeFFmpeg("new"), Replace).RunOne(context.Background())
	if !ran || err != nil {
		t.Fatal(ran, err)
	}
	b, _ := os.ReadFile(s.src)
	if string(b) != "new" {
		t.Errorf("original should be replaced, has %q", b)
	}
	if _, err := os.Stat(TempPath(s.src)); err == nil {
		t.Error("temp file should be gone")
	}
	j, _ := s.store.Get(s.id)
	if j.Status != jobs.Done || j.SizeAfter != 3 || j.Progress != 100 {
		t.Errorf("%+v", j)
	}
}

func TestBesideKeepsOriginal(t *testing.T) {
	s := newSetup(t)
	s.worker(fakeFFmpeg("new"), Beside).RunOne(context.Background())
	if b, _ := os.ReadFile(s.src); string(b) != "original-bytes" {
		t.Errorf("original changed: %q", b)
	}
	if b, _ := os.ReadFile(BesidePath(s.src)); string(b) != "new" {
		t.Errorf("output missing: %q", b)
	}
}

func TestFailureNeverLosesOriginal(t *testing.T) {
	s := newSetup(t)
	f := fakeFFmpeg("partial")
	f.Err = errors.New("ffmpeg exited 1")
	ran, err := s.worker(f, Replace).RunOne(context.Background())
	if !ran || err == nil {
		t.Fatal("want failure")
	}
	if b, _ := os.ReadFile(s.src); string(b) != "original-bytes" {
		t.Errorf("original lost: %q", b)
	}
	if _, err := os.Stat(TempPath(s.src)); err == nil {
		t.Error("partial temp file should be cleaned up")
	}
	if j, _ := s.store.Get(s.id); j.Status != jobs.Failed || j.Error == "" {
		t.Errorf("%+v", j)
	}
}

func TestEmptyOutputIsFailure(t *testing.T) {
	s := newSetup(t)
	s.worker(fakeFFmpeg(""), Replace).RunOne(context.Background())
	if b, _ := os.ReadFile(s.src); string(b) != "original-bytes" {
		t.Error("original must survive empty output")
	}
	if j, _ := s.store.Get(s.id); j.Status != jobs.Failed {
		t.Errorf("%+v", j)
	}
}

func TestMissingSourceFails(t *testing.T) {
	s := newSetup(t)
	os.Remove(s.src)
	s.worker(fakeFFmpeg("x"), Replace).RunOne(context.Background())
	if j, _ := s.store.Get(s.id); j.Status != jobs.Failed {
		t.Errorf("%+v", j)
	}
}

func TestNoJob(t *testing.T) {
	s := newSetup(t)
	w := s.worker(fakeFFmpeg("x"), Replace)
	w.RunOne(context.Background())
	if ran, err := w.RunOne(context.Background()); ran || err != nil {
		t.Errorf("%v %v", ran, err)
	}
}

type recQueue struct {
	Queue
	progress []float64
}

func (r *recQueue) Progress(id int64, p float64) error {
	r.progress = append(r.progress, p)
	return r.Queue.Progress(id, p)
}

func TestProgressReported(t *testing.T) {
	s := newSetup(t)
	w := s.worker(fakeFFmpeg("new"), Replace)
	rq := &recQueue{Queue: s.store}
	w.Queue = rq
	w.RunOne(context.Background())
	if len(rq.progress) != 1 || rq.progress[0] != 50 {
		t.Errorf("want [50] (50s of 100s), got %v", rq.progress)
	}
}
