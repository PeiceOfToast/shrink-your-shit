// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Failures that may pass are tried again later (DD-2). The worker's clock is set hours back, so a
// retry it schedules is already due by the store's clock and the next RunOne takes it.

var retryPast = time.Now().Add(-3 * time.Hour).Truncate(time.Second)

func (s *scenario) retrying() *Worker {
	w := s.worker(Replace)
	w.Now = func() time.Time { return retryPast }
	return w
}

// wantRetry: the job is back in the queue, its tries counted, not before now+wait, with its plain
// reason, and nothing of the try left: the original as it was, no temp file, no output.
func (s *scenario) wantRetry(attempts int, wait time.Duration, reason string) {
	s.t.Helper()
	j := s.job()
	if j.Status != jobs.Queued || j.Attempts != attempts || j.RetryAt != retryPast.Add(wait).UTC().Format(time.RFC3339) || !strings.Contains(j.Error, reason) {
		s.t.Fatalf("want queued for a retry (try %d, +%s, %q): %s attempts %d at %q %q", attempts, wait, reason, j.Status, j.Attempts, j.RetryAt, j.Error)
	}
	s.assertOriginalSafe()
}

func TestFailuresThatMayPassAreTriedAgain(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(s *scenario)
		wait   time.Duration
		reason string
	}{
		{"disk full while converting", func(s *scenario) {
			s.encodeErr = &os.PathError{Op: "write", Path: "/media/x.mkv", Err: syscall.ENOSPC}
		}, 30 * time.Minute, "The disk is full"},
		{"not enough free space to start", func(s *scenario) { s.freeBytes = 1 << 20 }, 30 * time.Minute, "not enough free disk space"},
		{"free space cannot be checked", func(s *scenario) { s.freeErr = errors.New("no such device") }, 5 * time.Minute, "free disk space could not be checked"},
		{"a check cannot run", func(s *scenario) { s.ssimErr = errors.New("exit status 1") }, 5 * time.Minute, "could not be done"},
		{"the original changes while converting", func(s *scenario) {
			s.onEncode = func() { os.WriteFile(s.src, []byte("original-bytes, longer"), 0o644) }
		}, 5 * time.Minute, "changed while it was being converted"},
		{"an old temp file is busy", func(s *scenario) {
			os.WriteFile(s.dir+"/movie.syp-tmp.mkv", []byte("x"), 0o644)
			removeFile = func(p string) error { return &os.PathError{Op: "remove", Path: p, Err: syscall.EBUSY} }
			s.t.Cleanup(func() { removeFile = os.Remove })
		}, 5 * time.Minute, "old temp file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			c.mutate(s)
			s.retrying().RunOne(context.Background())
			switch c.name {
			case "the original changes while converting":
				// the edit is the file now; "original-bytes" is what assertOriginalSafe expects
				os.WriteFile(s.src, []byte("original-bytes"), 0o644)
			case "an old temp file is busy": // the test's own busy file, which nothing could remove
				removeFile = os.Remove
				os.Remove(s.dir + "/movie.syp-tmp.mkv")
			}
			s.wantRetry(1, c.wait, c.reason)
		})
	}
}

// Three tries in all, waits growing; the third failure ends as today, with its sentence and the tries.
func TestTheThirdFailureEndsWithItsReason(t *testing.T) {
	s := newScenario(t)
	s.ssimErr = errors.New("exit status 1")
	w := s.retrying()
	w.RunOne(context.Background())
	s.wantRetry(1, 5*time.Minute, "could not be done")
	w.RunOne(context.Background())
	s.wantRetry(2, 30*time.Minute, "could not be done")
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Kept || j.Attempts != 3 || j.RetryAt != "" || !strings.Contains(j.Error, "could not be done") {
		t.Fatalf("%s attempts %d at %q %q", j.Status, j.Attempts, j.RetryAt, j.Error)
	}
	if n := s.count("libx265"); n != 3 {
		t.Errorf("each try converts from the start: %d encodes", n)
	}
	s.assertOriginalSafe()
	// Retry by the person: a fresh start
	s.store.Retry(s.id)
	if j := s.job(); j.Attempts != 0 || j.RetryAt != "" {
		t.Errorf("after Retry: attempts %d at %q", j.Attempts, j.RetryAt)
	}
}

// A retry after the original changed while converting reads it afresh: the next try sees the file as
// it is now (here it stays put, so the try finishes) instead of failing on the old state again.
func TestARetryAfterTheOriginalChangedSeesTheNewFile(t *testing.T) {
	s := newScenario(t)
	edits := 0
	s.onEncode = func() {
		if edits == 0 { // the download goes on during the first try only
			os.WriteFile(s.src, []byte("original-bytes, now complete"), 0o644)
		}
		edits++
	}
	w := s.retrying()
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Queued || j.Attempts != 1 {
		t.Fatalf("first try: %s %d %q", j.Status, j.Attempts, j.Error)
	}
	probes := s.count("ffprobe")
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done || j.Attempts != 1 {
		t.Fatalf("second try: %s attempts %d %q", j.Status, j.Attempts, j.Error)
	}
	if s.count("ffprobe") == probes {
		t.Error("the second try did not read the original again")
	}
	if read(s.src) != "new" {
		t.Errorf("at the name: %q", read(s.src))
	}
}

// What cannot pass ends at once: a name taken, a refused result, a damaged original.
func TestLastingFailuresAreNotTriedAgain(t *testing.T) {
	cases := map[string]func(s *scenario){
		"name taken (beside)": func(s *scenario) { os.WriteFile(BesidePath(s.src), []byte("theirs"), 0o644) },
		"result not smaller":  func(s *scenario) { s.content = strings.Repeat("x", 100) },
		"damaged original":    func(s *scenario) { s.encodeStderr = "[h264 @ 0x1] error while decoding MB 1 2" },
		"converter error":     func(s *scenario) { s.encodeErr = errors.New("ffmpeg: exit status 187: Invalid data found") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := newScenario(t)
			mutate(s)
			w := s.retrying()
			if name == "name taken (beside)" {
				w.Mode = Beside
			}
			w.RunOne(context.Background())
			j := s.job()
			if (j.Status != jobs.Failed && j.Status != jobs.Kept) || j.Attempts != 1 || j.RetryAt != "" {
				t.Fatalf("%s attempts %d at %q %q", j.Status, j.Attempts, j.RetryAt, j.Error)
			}
		})
	}
}

// The stall watchdog's stop is never tried again.
func TestAStallIsNotTriedAgain(t *testing.T) {
	s := newScenario(t)
	s.hang = func(name string, args []string) bool { return s.has(args, "libx265") }
	w := s.retrying()
	w.StallAfter = 50 * time.Millisecond
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed || j.RetryAt != "" || !strings.Contains(j.Error, "no progress") {
		t.Fatalf("%s at %q %q", j.Status, j.RetryAt, j.Error)
	}
}

// ffmpeg stopped by the system (killed: out of memory) is tried again; the same exit code without a
// signal is the file's problem and is not.
func TestConverterKilledBySignalIsTriedAgain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signals")
	}
	killed := exec.Command("sh", "-c", "kill -9 $$").Run()
	if !killedBySignal(killed) {
		t.Fatalf("not seen as killed: %v", killed)
	}
	s := newScenario(t)
	s.encodeErr = fmt.Errorf("ffmpeg: %w", killed)
	s.retrying().RunOne(context.Background())
	s.wantRetry(1, 5*time.Minute, "Converting stopped with an error")
	exited := exec.Command("sh", "-c", "exit 1").Run()
	if killedBySignal(exited) {
		t.Error("an ordinary exit seen as killed")
	}
}

// An .srt a failed try could not remove stays on record; the next try, in the same process (no
// restart, so no RecoverSidecars), removes it first (it is still exactly ours) instead of taking it for
// someone else's file. If it still cannot be removed, that try is repeated later, not failed as a
// name taken (review 222be8f).
func TestARetryClearsItsOwnLeftoverSrt(t *testing.T) {
	for _, stillStuck := range []bool{false, true} {
		t.Run(fmt.Sprint("still stuck ", stillStuck), func(t *testing.T) {
			s, w, srt := srtScenario(t)
			w.Now = func() time.Time { return retryPast }
			swapHook = func(at string) error {
				if at == "subtitles placed" { // the original changes just before the swap: this try ends
					os.WriteFile(s.src, []byte("original-bytes, still downloading"), 0o644)
				}
				return nil
			}
			sidecarLstat = func(string) (os.FileInfo, error) { return nil, &os.PathError{Op: "lstat", Err: syscall.EIO} }
			t.Cleanup(func() { swapHook, sidecarLstat = nil, os.Lstat })
			w.RunOne(context.Background())
			if j := s.job(); j.Status != jobs.Queued || j.Attempts != 1 {
				t.Fatalf("try 1: %s %d %q", j.Status, j.Attempts, j.Error)
			}
			if left, _ := s.store.Sidecars(); len(left) != 1 || read(srt) == "" {
				t.Fatalf("try 1 should leave its .srt on record: %+v", left)
			}
			swapHook = nil
			if !stillStuck {
				sidecarLstat = os.Lstat
			}
			w.RunOne(context.Background())
			j := s.job()
			if stillStuck {
				if j.Status != jobs.Queued || j.Attempts != 2 || strings.Contains(j.Error, "is already there") {
					t.Fatalf("try 2 must be repeated, not failed as a name taken: %s %d %q", j.Status, j.Attempts, j.Error)
				}
				return
			}
			if j.Status != jobs.Done {
				t.Fatalf("try 2: %s %q", j.Status, j.Error)
			}
			if !strings.Contains(read(srt), "subtitle from sub.0.srt") {
				t.Errorf("srt %q", read(srt))
			}
			if left, _ := s.store.Sidecars(); len(left) != 0 {
				t.Errorf("records left: %+v", left)
			}
		})
	}
}

// stopCheckFails is the store with StopRequested failing (the database busy).
type stopCheckFails struct{ *jobs.Store }

func (stopCheckFails) StopRequested(int64) (bool, error) {
	return false, errors.New("database is locked")
}

// Reading whether a stop was asked fails just before the swap: nothing was replaced, so the try is
// repeated later (review 222be8f).
func TestAFailedStopCheckIsTriedAgain(t *testing.T) {
	s := newScenario(t)
	w := s.retrying()
	w.Queue = stopCheckFails{s.store}
	w.RunOne(context.Background())
	s.wantRetry(1, 5*time.Minute, "Converting did not finish")
}

// The setting changes between two tries (from "Save as separate .srt files" to keeping every subtitle
// inside): the next try still removes the .srt the earlier one left, with its record, and finishes
// (review 13fb51f).
func TestARetryClearsItsLeftoverSrtAfterTheSettingChanged(t *testing.T) {
	s, w, srt := srtScenario(t)
	w.Now = func() time.Time { return retryPast }
	swapHook = func(at string) error {
		if at == "subtitles placed" {
			os.WriteFile(s.src, []byte("original-bytes, still downloading"), 0o644)
		}
		return nil
	}
	sidecarLstat = func(string) (os.FileInfo, error) { return nil, &os.PathError{Op: "lstat", Err: syscall.EIO} }
	t.Cleanup(func() { swapHook, sidecarLstat = nil, os.Lstat })
	w.RunOne(context.Background())
	if left, _ := s.store.Sidecars(); s.job().Status != jobs.Queued || len(left) != 1 || read(srt) == "" {
		t.Fatalf("try 1 should leave its .srt on record: %s %+v", s.job().Status, left)
	}
	swapHook, sidecarLstat = nil, os.Lstat
	keepAll := rules.DefaultSelectors(3)
	keepAll.SkipSmall = false
	tmpl := keepAll.Template()
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	s.outSpec = goodOutput() // the new file keeps its subtitle inside now
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("try 2: %s %q", j.Status, j.Error)
	}
	if !gone(srt) {
		t.Errorf("the .srt of try 1 was left: %q", read(srt))
	}
	if left, _ := s.store.Sidecars(); len(left) != 0 {
		t.Errorf("records left: %+v", left)
	}
}
