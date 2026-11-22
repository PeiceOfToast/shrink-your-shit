// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// steps is the job's run steps as they were recorded (the check step first, when it exists).
func (s *scenario) steps() []jobs.RunStep {
	s.t.Helper()
	steps, err := s.store.RunSteps(s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	return steps
}

// L1: the checks before anything is converted are one step, recorded before the encode, saying what passed.
func TestAPassingJobRecordsTheCheckBeforeTheEncode(t *testing.T) {
	s := newScenario(t)
	if err := s.run(Replace); err != nil {
		t.Fatal(err)
	}
	steps := s.steps()
	if len(steps) < 2 || steps[0].Name != "preflight" || steps[0].Outcome != "ok" || steps[1].Name != "encode" {
		t.Fatalf("steps %+v, want the check step before the encode", steps)
	}
	if !strings.Contains(steps[0].Result, "Checked") || !strings.Contains(steps[0].Result, "free space") {
		t.Errorf("the check step's result %q should say what was checked", steps[0].Result)
	}
	// its line is in the log's own section (the download shows it as "Check before converting")
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range lines {
		if l.Section == "check" && strings.Contains(l.Text, "Checked") {
			found = true
		}
	}
	if !found {
		t.Errorf("no check line in the job log: %+v", lines)
	}
}

// A Folder-mode job says the folder for new files and its free space were checked too.
func TestAPassingFolderJobNamesTheFolderInTheCheck(t *testing.T) {
	s := newScenario(t)
	if _, err := s.folderWorker(t.TempDir()).RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	steps := s.steps()
	if len(steps) == 0 || steps[0].Name != "preflight" || !strings.Contains(steps[0].Result, "folder for new files") {
		t.Fatalf("steps %+v, want the check step naming the folder", steps)
	}
}

// L1: each check that stops a job is the one named step, with the job's own words and code, and nothing was
// encoded: no encode step, no ffmpeg call.
func TestACheckThatStopsTheJobIsTheOnlyStep(t *testing.T) {
	t.Run("not enough free space", func(t *testing.T) {
		oneTry(t)
		s := newScenario(t)
		s.freeBytes = 1 << 20
		s.run(Replace)
		s.assertKept("not enough free disk space", jobs.ReasonLowDiskSpace)
		s.wantOnlyTheFailedCheck(t, "free disk space")
	})
	t.Run("no graphics encoder for the file", func(t *testing.T) {
		useTestHome(t)
		s := newScenario(t)
		publishResolved(t, s.store, s.dir, "Only", nil, func(d *workflow.Doc) {
			for i := range d.Nodes {
				if d.Nodes[i].Type == workflow.TypeConvert {
					d.Nodes[i].Config["hardware"] = "only"
				}
			}
		})
		w := s.worker(Replace)
		w.Template = s.store.TemplateFor
		w.RunInfo = s.store.RunInfoFor
		w.Encoder = func(string, bool, *rules.HDR) (string, string) {
			return "", "this computer's card can't make that bit depth"
		}
		w.RunOne(context.Background())
		if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "Hardware only: no graphics encoder took this file") {
			t.Fatalf("status %s, error %q", j.Status, j.Error)
		}
		s.wantOnlyTheFailedCheck(t, "no graphics encoder")
	})
	t.Run("folder for new files can't be written", func(t *testing.T) {
		if runtime.GOOS == "windows" || os.Geteuid() == 0 { // chmod can't make a folder read-only on Windows
			t.Skip("needs a folder this user cannot write (POSIX, not root)")
		}
		s := newScenario(t)
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		s.folderWorker(dir).RunOne(context.Background())
		if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "folder for new files can't be written to") {
			t.Fatalf("status %s, error %q", j.Status, j.Error)
		}
		s.wantOnlyTheFailedCheck(t, "folder for new files")
	})
}

// L1 fix: the encoder check takes the card's place before the checks that can still stop the job, so a refusal
// after it must give the place back. The count is all there is (user bug 11-20): a leak takes one place away
// until the program restarts, and the next graphics job waits for good.
func TestAJobStoppedByALaterCheckGivesTheCardBack(t *testing.T) {
	oneTry(t)
	s := newScenario(t)
	s.freeBytes = 1 << 20 // the disk beside the original looks full: the free-space check refuses after the slot is taken
	graphicsSlotWorker(s, "t1").RunOne(context.Background())
	s.assertKept("not enough free disk space", jobs.ReasonLowDiskSpace)
	if n := GPURunning(); n != 0 {
		t.Errorf("graphics used after a job the free-space check stopped: %d, want 0", n)
	}
}

// A job that passes the checks and finishes leaves the card free: the encode's own release at its end, with the
// defer in process behind it.
func TestAPassingGraphicsJobGivesTheCardBack(t *testing.T) {
	oneTry(t)
	s := newScenario(t)
	if _, err := graphicsSlotWorker(s, "t1").RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if enc := encodeCalls(s); len(enc) != 1 || !strings.Contains(enc[0], "hevc_nvenc") {
		t.Fatalf("encodes, want one on the card (the slot really was taken):\n%s", strings.Join(enc, "\n"))
	}
	if n := GPURunning(); n != 0 {
		t.Errorf("graphics used after a passing job: %d, want 0", n)
	}
}

// The card's place also comes back when a job fails after the checks passed: the staging folder cannot be made
// beside a read-only original's folder, which returns before the encode's release. The defer in process is the only
// thing that gives the place back there (review 646 §2, mutation Mb).
func TestAJobWhoseStagingFailsAfterTheChecksGivesTheCardBack(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 { // chmod can't make a folder read-only on Windows
		t.Skip("needs a folder this user cannot write (POSIX, not root)")
	}
	oneTry(t)
	s := newScenario(t)
	if err := os.Chmod(s.dir, 0o555); err != nil { // the original's folder itself, not the store's
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(s.dir, 0o755) })
	graphicsSlotWorker(s, "t1").RunOne(context.Background())
	if j := s.job(); j.Status == jobs.Done {
		t.Fatalf("status %s, want a failure: no staging folder can be made in a read-only folder", j.Status)
	}
	if n := GPURunning(); n != 0 {
		t.Errorf("graphics used after a staging failure below the checks: %d, want 0", n)
	}
}

// wantOnlyTheFailedCheck: the job's only step is the failed check step, its result naming want, and ffmpeg never
// ran (the check comes before the encode, so there is no encode step).
func (s *scenario) wantOnlyTheFailedCheck(t *testing.T, want string) {
	t.Helper()
	steps := s.steps()
	if len(steps) != 1 || steps[0].Name != "preflight" || steps[0].Outcome != "failed" {
		t.Fatalf("steps %+v, want one failed check step", steps)
	}
	if !strings.Contains(steps[0].Result, want) || !strings.Contains(steps[0].Result, "No new file was written") {
		t.Errorf("the check step's result %q should name %q and say nothing was written", steps[0].Result, want)
	}
	if n := s.count("ffmpeg "); n != 0 {
		t.Errorf("ffmpeg ran %d times before the checks passed:\n%s", n, strings.Join(s.calls, "\n"))
	}
}
