// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// subExec is a frozen encode (out_time and total_size repeat) that grows one named file, as growingExec does for the
// temp file. Its files are the sizes the watchdog's stat reads, by path; the paths it is asked for are recorded.
// With frames, the frame count rises on every report, growth steps and hang alike.
type subExec struct {
	*media.FakeExec
	clock  *fakeClockAt
	grow   []int64
	file   string // the path that grows
	hang   bool
	frames bool
	silent bool // no progress block at all: ffmpeg prints nothing while the file grows

	mu      sync.Mutex
	sizes   map[string]int64
	statted []string // every path the watchdog stat'd
	frame   int
}

func (x *subExec) onDisk(path string) (int64, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.statted = append(x.statted, path)
	n, ok := x.sizes[path]
	if !ok {
		return 0, os.ErrNotExist
	}
	return n, nil
}

// report is one progress block; the frame count rises with frames, otherwise it repeats.
func (x *subExec) report(onLine func(string)) {
	x.mu.Lock()
	x.frame++
	frame := 100
	if x.frames {
		frame += x.frame
	}
	x.mu.Unlock()
	onLine("frame=" + itoa(frame))
	onLine("total_size=10")
	onLine("out_time_us=5000000")
	onLine("progress=continue")
}

func (x *subExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name != "ffmpeg" || !slices.Contains(args, "libx265") {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	for _, n := range x.grow {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		x.mu.Lock()
		x.sizes[x.file] += n
		x.mu.Unlock()
		x.clock.add(growStep)
		if !x.silent {
			x.report(onLine)
		}
	}
	if !x.hang {
		return x.FakeExec.Stream(ctx, onLine, name, args...)
	}
	for range 60 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
		x.clock.add(growStep)
		if x.frames && !x.silent {
			x.report(onLine)
		}
	}
	return "", context.DeadlineExceeded
}

// itoa formats a small non-negative int without importing strconv into a test file for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// oneByteSteps is n growth steps of one byte: 24 stays within the 28-byte bound for a 14-byte source.
func oneByteSteps(n int) []int64 {
	g := make([]int64, n)
	for i := range g {
		g[i] = 1
	}
	return g
}

// runSubtitleEncode runs encodeLocal on a plan that extracts subtitles to a .srt file: the .srt is the last argument,
// the temp file is out. The stop is wired the way RunOne wires it, so a watchdog stop cancels this encode. sizeBefore
// is the job's source size (0: unknown). It returns the scenario, the encode's error, and the subExec for its stats.
func runSubtitleEncode(t *testing.T, x *subExec, sizeBefore int64) (*scenario, error) {
	t.Helper()
	clock := &fakeClockAt{t: time.Date(2026, 11, 21, 5, 0, 0, 0, time.UTC)}
	s := newScenario(t)
	w := s.worker(Replace)
	w.stallClock = clock.now
	w.StallAfter = 2 * time.Second
	w.FrozenPositionFactor = 1000
	x.FakeExec, x.clock, x.sizes = s.exec, clock, map[string]int64{}
	w.sizeOnDisk = x.onDisk
	w.Exec = x

	out := "/data/movie.hevc.tmp.mkv"
	srt := rules.StagedSrt(out, 0)
	args := []string{"-i", "/data/movie.mkv", "-c:v", "libx265", "-y", out, "-map", "0:2", "-c:s", "srt", srt}

	job := s.job()
	job.SizeBefore = sizeBefore
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w.mu.Lock()
	w.current, w.cancelJob = job.ID, cancel
	w.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		_, _, err := w.encodeLocal(ctx, job, args, out)
		done <- err
	}()
	select {
	case err := <-done:
		// the watchdog must stat the temp file and only the temp file: the .srt is not the encode's output
		for _, p := range x.statted {
			if p != out {
				t.Errorf("the watchdog stat'd %s, want only the temp file %s", p, out)
				break
			}
		}
		return s, err
	case <-time.After(60 * time.Second):
		t.Fatal("the encode did not end")
		return nil, nil
	}
}

// The temp file is what the watchdog watches when the plan extracts subtitles: a frozen encode whose temp file grows
// is alive, and the job finishes. Watching the last argument (the .srt) would stop it at StallAfter.
func TestSubtitlePlanWatchesTheTempFile(t *testing.T) {
	s, err := runSubtitleEncode(t, &subExec{file: "/data/movie.hevc.tmp.mkv", grow: oneByteSteps(24)}, 14)
	if err != nil {
		t.Fatalf("a frozen encode whose temp file grew was stopped: %v", err)
	}
	if n := logCount(t, s, "no progress"); n != 0 {
		t.Errorf("%d stall stop lines for a growing temp file, want 0", n)
	}
}

// The subtitle file growing alone does not keep the encode alive: the temp file is still, so it stops at StallAfter.
func TestGrowingSubtitleFileAloneDoesNotKeepItAlive(t *testing.T) {
	x := &subExec{file: rules.StagedSrt("/data/movie.hevc.tmp.mkv", 0), grow: oneByteSteps(24), hang: true}
	s, err := runSubtitleEncode(t, x, 14)
	if err == nil {
		t.Fatal("a frozen encode whose temp file did not grow finished: the .srt growth kept it alive")
	}
	if n := logCount(t, s, "no progress for 2s"); n == 0 {
		t.Errorf("no stall stop for a still temp file (err %v)", err)
	}
}

// With the source size unknown, growth is no bound and is not "more than twice the original": the stall stop says
// no progress, not that the output is too large.
func TestUnknownSourceSizeIsNotOverSize(t *testing.T) {
	s, err := runSubtitleEncode(t, &subExec{file: "/data/movie.hevc.tmp.mkv", grow: oneByteSteps(24), hang: true}, 0)
	if err == nil {
		t.Fatal("a frozen encode with an unknown source size finished")
	}
	if n := logCount(t, s, "more than twice the original's size"); n != 0 {
		t.Errorf("%d over-size stop lines with the source size unknown, want 0", n)
	}
	if n := logCount(t, s, "no progress for 2s"); n == 0 {
		t.Errorf("no stall stop with the source size unknown (err %v)", err)
	}
}

// A runaway temp file is stopped while it is still growing: past twice the source its growth is no progress, so the
// encode stops at StallAfter even though the file keeps growing and the frame count rises. The callback's view of the
// output's size (the disk term) must agree with the watchdog's: total_size alone says 10 bytes.
func TestRunawayTempFileIsStoppedWhileGrowing(t *testing.T) {
	x := &subExec{file: "/data/movie.hevc.tmp.mkv", grow: []int64{1000, 1000, 1000}, hang: true, frames: true}
	s, err := runSubtitleEncode(t, x, 14)
	if err == nil {
		t.Fatal("a runaway temp file (3000 bytes from a 14-byte source) finished")
	}
	if n := logCount(t, s, "more than twice the original's size"); n == 0 {
		t.Errorf("no over-size stop while the output grew (err %v)", err)
	}
}

// A silent encode (no progress block at all) whose temp file grows past twice the source is stopped by the watchdog's
// own limit, with the "more than twice" reason. Here nothing reaches the progress callback, so only the watchdog's
// outputWithin check can stop it; under `if true` in place of that check it runs on and this test fails.
func TestSilentEncodeGrowingPastTwiceTheSourceIsStopped(t *testing.T) {
	x := &subExec{file: "/data/movie.hevc.tmp.mkv", grow: []int64{1000, 1000, 1000}, hang: true, silent: true}
	s, err := runSubtitleEncode(t, x, 14)
	if err == nil {
		t.Fatal("a silent encode whose temp file grew to 3000 bytes from a 14-byte source finished")
	}
	if n := logCount(t, s, "more than twice the original's size"); n == 0 {
		t.Errorf("no over-size stop for a silent runaway (err %v)", err)
	}
	if n := logCount(t, s, "size (0 B, original"); n != 0 {
		t.Errorf("the over-size line names 0 B for a file that grew to 3000 bytes")
	}
}
