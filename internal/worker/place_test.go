// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"os"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestPlaceErrorBasic: placeError wraps an os.PathError, extracts the path.
func TestPlaceErrorBasic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path patterns differ on Windows")
	}
	err := &os.PathError{Op: "rename", Path: "/mnt/library/new.mkv", Err: os.ErrPermission}
	w := &Worker{} // Queue is nil; replaceStep checks and returns
	result := w.placeError(1, time.Time{}, "/mnt/library/new.mkv", err)
	pe, ok := result.(*placeError)
	if !ok {
		t.Fatalf("not a placeError: %T", result)
	}
	if !strings.Contains(pe.Path, "/mnt/library") {
		t.Errorf("path should contain /mnt/library: %q", pe.Path)
	}
}

// TestPlaceErrorStaging: a path inside .syp-tmp names its parent.
func TestPlaceErrorStaging(t *testing.T) {
	err := &os.PathError{Op: "rename", Path: "/mnt/library/.syp-tmp/new.mkv", Err: os.ErrPermission}
	w := &Worker{}
	result := w.placeError(1, time.Time{}, "/mnt/library/.syp-tmp/new.mkv", err)
	pe, ok := result.(*placeError)
	if !ok {
		t.Fatalf("not a placeError: %T", result)
	}
	// placeError keeps the exact path; the person's sentence names the staging folder's parent (jobs.DeniedFolder)
	if pe.Path != "/mnt/library/.syp-tmp/new.mkv" {
		t.Errorf("path %q", pe.Path)
	}
	if got := jobs.DeniedFolder([]string{pe.keyLine()}); got != "/mnt/library" {
		t.Errorf("key line %q names %q, want /mnt/library", pe.keyLine(), got)
	}
}

// TestPlaceErrorLinkError: an os.LinkError path is extracted.
func TestPlaceErrorLinkError(t *testing.T) {
	err := &os.LinkError{Op: "rename", Old: "/tmp/orig.mkv", New: "/mnt/library/movie.mkv", Err: os.ErrPermission}
	w := &Worker{}
	result := w.placeError(1, time.Time{}, "/mnt/library/movie.mkv", err)
	pe, ok := result.(*placeError)
	if !ok {
		t.Fatalf("not a placeError: %T", result)
	}
	if !strings.Contains(pe.Path, "/mnt/library") {
		t.Errorf("path should contain /mnt/library: %q", pe.Path)
	}
}

// The manager's check of item E (job 7): the new file is made and checked, but putting it in place is refused
// (the folder turned read-only after the encode). The job's log gets a failed 'replace' step, the failure summary
// points at it with the OS error as its key line (which names the folder), and the timeline says 'replaced', not a
// second 'converted'. A good job records the replace step as ok.
func TestPlaceRefusedGetsAReplaceStep(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write (POSIX, not root)")
	}
	old := maxTries
	maxTries = 1 // the last try: the failure is final, not put back in the queue
	t.Cleanup(func() { maxTries = old })

	s := newScenario(t)
	swapHook = func(at string) error {
		if at == "before swap" {
			os.Chmod(s.dir, 0o555)
		}
		return nil
	}
	t.Cleanup(func() { swapHook = nil; os.Chmod(s.dir, 0o755) })
	s.run(Replace)

	if j := s.job(); j.Status != jobs.Failed {
		t.Fatalf("job %s (%s)", j.Status, j.Error)
	}
	steps, _ := s.store.RunSteps(s.id)
	var replace *jobs.RunStep
	for i := range steps {
		if steps[i].Name == "replace" {
			replace = &steps[i]
		}
	}
	if replace == nil || replace.Index != 2 || replace.Outcome != "failed" {
		t.Fatalf("replace step %+v (all: %+v)", replace, steps)
	}
	f, err := s.store.FailureOf(s.id)
	if err != nil || f == nil || f.Step != 2 || len(f.KeyLines) != 1 {
		t.Fatalf("failure %+v %v", f, err)
	}
	if got := jobs.DeniedFolder(f.KeyLines); got != s.dir {
		t.Errorf("the key line %q names %q, want the library folder %q", f.KeyLines[0], got, s.dir)
	}
	if got := jobs.FailureCode(jobs.ReasonConvertFailed, f.KeyLines); got != "output_not_writable" {
		t.Errorf("code %s", got)
	}
	hist, _ := s.store.Steps(s.id)
	replaced, converted := 0, 0
	for _, h := range hist {
		switch h.Step {
		case "replaced":
			replaced++
		case "converted":
			converted++
		}
	}
	if replaced != 1 || converted != 1 {
		t.Errorf("timeline: %d replaced, %d converted (want 1 and 1): %+v", replaced, converted, hist)
	}
	if read(s.src) != "original-bytes" {
		t.Error("the original must stay")
	}
}

func TestPlaceOKRecordsAReplaceStep(t *testing.T) {
	s := newScenario(t)
	if err := s.run(Replace); err != nil {
		t.Fatal(err)
	}
	steps, _ := s.store.RunSteps(s.id)
	for _, st := range steps {
		if st.Name == "replace" && st.Index == 2 && st.Outcome == "ok" {
			return
		}
	}
	t.Errorf("no ok replace step: %+v", steps)
}
