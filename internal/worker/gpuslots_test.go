// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// graphicsSlotWorker is a scenario worker that encodes on the card and may hold one of its slots.
func graphicsSlotWorker(s *scenario, id string) *Worker {
	w := s.worker(Replace)
	w.ID = id
	w.GPUSlots = func() int { return 1 }
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	return w
}

// waitForLog waits until the job's log has a line containing sub (it is written by the running job).
func waitForLog(t *testing.T, s *scenario, id int64, sub string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		lines, err := s.store.LogLines(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lines {
			if strings.Contains(l.Text, sub) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %d's log never said %q", id, sub)
}

// Two graphics jobs with the limit at 1: the second waits while the first encodes on the card, and the
// count the Workers page shows follows them (user bug 11-20: two QSV jobs ran at once with the limit at 1,
// and the page said 0 used always).
func TestGraphicsSlotLimitHoldsTheSecondJob(t *testing.T) {
	s := newScenario(t)
	other := filepath.Join(s.dir, "other.mkv")
	if err := os.WriteFile(other, []byte("original-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.moreSrcs = map[string]bool{other: true}
	id2, err := s.store.Enqueue(jobs.Job{Library: s.dir, Path: other, DurationSec: scenarioDuration, SizeBefore: 14})
	if err != nil {
		t.Fatal(err)
	}

	first := make(chan struct{}) // the first job's graphics encode has started
	release := make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	t.Cleanup(letGo) // a failure before the first job ends must not leave it stuck
	var cmu sync.Mutex
	started := 0
	s.onEncode = func() { // the first encode waits here until the test lets it finish
		cmu.Lock()
		started++
		n := started
		cmu.Unlock()
		if n == 1 {
			close(first)
			<-release
		}
	}

	ctx := context.Background()
	done1 := make(chan error, 1)
	go func() { _, err := graphicsSlotWorker(s, "t1").RunOne(ctx); done1 <- err }()
	select {
	case <-first:
	case <-time.After(30 * time.Second):
		t.Fatal("the first job's graphics encode never started")
	}
	if n := GPURunning(); n != 1 {
		t.Fatalf("graphics used while the first job encodes: %d, want 1", n)
	}

	done2 := make(chan error, 1)
	go func() { _, err := graphicsSlotWorker(s, "t2").RunOne(ctx); done2 <- err }()
	// The second job reached the card and waits for its place: no second graphics encode has started.
	waitForLog(t, s, id2, "waiting for a graphics slot (1 of 1 in use)")
	if n := GPURunning(); n != 1 {
		t.Errorf("graphics used while the second job waits: %d, want 1", n)
	}
	if enc := encodeCalls(s); len(enc) != 1 {
		t.Fatalf("encodes before the first job ended: %d, want 1:\n%s", len(enc), strings.Join(enc, "\n"))
	}

	letGo() // the first graphics encode ends: its slot is free
	for _, d := range []chan error{done1, done2} {
		select {
		case err := <-d:
			if err != nil {
				t.Fatalf("RunOne: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("a job did not finish")
		}
	}
	if n := GPURunning(); n != 0 {
		t.Errorf("graphics used after both jobs: %d, want 0", n)
	}
	for _, id := range []int64{s.id, id2} {
		j, err := s.store.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != jobs.Done {
			t.Errorf("job %d: %s (%q)", id, j.Status, j.Error)
		}
	}
	enc := encodeCalls(s)
	if len(enc) != 2 || !strings.Contains(enc[0], "hevc_nvenc") || !strings.Contains(enc[1], "hevc_nvenc") {
		t.Fatalf("encodes, want two on the card:\n%s", strings.Join(enc, "\n"))
	}
}

// The limit at 0: the processor does the encoding, and the job says why once.
func TestGraphicsSlotsAtZeroUseTheProcessor(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.GPUSlots = func() int { return 0 }
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || strings.Contains(enc[0], "hevc_nvenc") || !strings.Contains(enc[0], "libx265") {
		t.Fatalf("encodes, want the processor alone:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "the graphics slots are set to 0, so the processor does the encoding"); n != 1 {
		t.Errorf("graphics-slots-0 line: %d, want 1", n)
	}
	if n := GPURunning(); n != 0 {
		t.Errorf("graphics used: %d, want 0", n)
	}
}

// A job that is waiting for a graphics slot stops waiting when it is stopped.
func TestAGraphicsSlotWaitEndsWhenTheJobStops(t *testing.T) {
	w := &Worker{GPUSlots: func() int { return 1 }}
	held, err := w.takeGPUSlot(context.Background(), jobs.Job{ID: 1})
	if err != nil || held == nil {
		t.Fatalf("first slot: %v %v", held, err)
	}
	defer held.Release() // however the test ends, the card is free again
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.takeGPUSlot(ctx, jobs.Job{ID: 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting ended with %v, want context.Canceled", err)
	}
	if n := GPURunning(); n != 1 {
		t.Errorf("graphics used while the slot is held: %d, want 1", n)
	}
	held.Release()
	if n := GPURunning(); n != 0 {
		t.Errorf("graphics used after the release: %d, want 0", n)
	}
}
