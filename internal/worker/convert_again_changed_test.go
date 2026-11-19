// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// trashedBeside lists the files in the .syp-trash folder beside out (none when there is no folder).
func trashedBeside(t *testing.T, out string) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(filepath.Dir(out), TrashDir, "*"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// folderWorker is a worker whose flow saves to the folder dir (Save to another folder, one folder for all).
func (s *scenario) folderWorker(dir string) *Worker {
	sel := rules.DefaultSelectors(3)
	sel.AfterConverting, sel.OutputDir = "folder", dir
	if err := sel.Validate(); err != nil {
		s.t.Fatal(err)
	}
	tmpl := sel.Template()
	w := s.worker(Folder)
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	return w
}

// policyCase is the scenario, worker and first output path for one output policy: beside the original, or in a
// folder of its own.
func policyCase(t *testing.T, folder bool) (*scenario, *Worker, string) {
	t.Helper()
	s := newScenario(t)
	if !folder {
		return s, s.worker(Beside), BesidePath(s.src)
	}
	dir := t.TempDir()
	return s, s.folderWorker(dir), FolderPath(dir, s.dir, s.src, ".mkv")
}

// convertedThenAgain makes the first output, changes it with change, then runs Convert again. It returns the
// scenario, the output's path and the first output's bytes.
func convertedThenAgain(t *testing.T, folder bool, change func(out string)) (*scenario, string, string) {
	t.Helper()
	s, w, out := policyCase(t, folder)
	if _, err := w.RunOne(context.Background()); err != nil || s.job().Status != jobs.Done {
		t.Fatalf("first: %v %s", err, s.job().Status)
	}
	old := read(out)
	change(out)
	if err := s.store.ConvertAgain(s.id); err != nil {
		t.Fatal(err)
	}
	s.content = "newer"
	w.RunOne(context.Background()) // the job's own status says how it went; the tests check it
	return s, out, old
}

// forBothPolicies runs f as a subtest for beside and for a folder of its own.
func forBothPolicies(t *testing.T, f func(t *testing.T, folder bool)) {
	t.Helper()
	t.Run("beside", func(t *testing.T) { f(t, false) })
	t.Run("folder", func(t *testing.T) { f(t, true) })
}

// A touched earlier output (only its time changed) is moved to the trash, the new file takes its name, and the job
// log says so.
func TestConvertAgainMovesATouchedOutputAside(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		later := time.Now().Add(72 * time.Hour)
		s, out, old := convertedThenAgain(t, folder, func(out string) {
			if err := os.Chtimes(out, later, later); err != nil {
				t.Fatal(err)
			}
		})
		if j := s.job(); j.Status != jobs.Done {
			t.Fatalf("again: %s %q", j.Status, j.Error)
		}
		if read(out) != "newer" {
			t.Errorf("at the name %q, want the new file", read(out))
		}
		kept := trashedBeside(t, out)
		if len(kept) != 1 || read(kept[0]) != old {
			t.Fatalf("trash %v, want the earlier output %q kept once", kept, old)
		}
		if !logHas(t, s, "was moved to the trash") {
			t.Errorf("job log has no line saying the changed file went to the trash")
		}
	})
}

// A changed earlier output (its size changed) is moved aside the same way.
func TestConvertAgainMovesAChangedSizeOutputAside(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		const changed = "changed-to-something-longer-than-the-output"
		s, out, _ := convertedThenAgain(t, folder, func(out string) {
			if err := os.WriteFile(out, []byte(changed), 0o644); err != nil {
				t.Fatal(err)
			}
		})
		if j := s.job(); j.Status != jobs.Done {
			t.Fatalf("again: %s %q", j.Status, j.Error)
		}
		if read(out) != "newer" {
			t.Errorf("at the name %q, want the new file", read(out))
		}
		kept := trashedBeside(t, out)
		if len(kept) != 1 || read(kept[0]) != changed {
			t.Fatalf("trash %v, want the changed output kept", kept)
		}
	})
}

// An exact match is replaced as before: nothing is moved to the trash.
func TestConvertAgainReplacesAnExactOutputWithoutTrash(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		s, out, _ := convertedThenAgain(t, folder, func(string) {})
		if j := s.job(); j.Status != jobs.Done {
			t.Fatalf("again: %s %q", j.Status, j.Error)
		}
		if read(out) != "newer" {
			t.Errorf("at the name %q", read(out))
		}
		if kept := trashedBeside(t, out); len(kept) != 0 {
			t.Errorf("trash %v, want none for an exact match", kept)
		}
	})
}

// A file at the name that no job recorded is refused as before, and nothing is moved.
func TestUnrecordedFileAtTheOutputNameIsRefused(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		s, w, out := policyCase(t, folder)
		os.MkdirAll(filepath.Dir(out), 0o755)
		os.WriteFile(out, []byte("the person's own"), 0o644)
		w.RunOne(context.Background())
		words := "wasn't made by Shrink Your Shit"
		if !folder {
			words = "There is already a file named"
		}
		if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, words) {
			t.Errorf("%s %q, want it failed with %q", j.Status, j.Error, words)
		}
		if read(out) != "the person's own" || len(trashedBeside(t, out)) != 0 {
			t.Errorf("at the name %q, trash %v", read(out), trashedBeside(t, out))
		}
	})
}

// When the earlier output cannot be moved (the trash folder is a file), the job fails and the earlier output stays.
func TestFailedMoveAsideLeavesTheEarlierOutput(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		s, w, out := policyCase(t, folder)
		w.RunOne(context.Background())
		old := read(out)
		later := time.Now().Add(72 * time.Hour)
		os.Chtimes(out, later, later)
		os.WriteFile(filepath.Join(filepath.Dir(out), TrashDir), []byte("not a folder"), 0o644)
		if err := s.store.ConvertAgain(s.id); err != nil {
			t.Fatal(err)
		}
		s.content = "newer"
		w.RunOne(context.Background())
		if j := s.job(); j.Status == jobs.Done || !strings.Contains(j.Error, "could not be moved to the trash") {
			t.Errorf("%s %q", j.Status, j.Error)
		}
		if read(out) != old {
			t.Errorf("the earlier output is %q, want it kept as %q", read(out), old)
		}
	})
}

// logHas says whether job s.id's log has a line with words.
func logHas(t *testing.T, s *scenario, words string) bool {
	t.Helper()
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		if strings.Contains(l.Text, words) {
			return true
		}
	}
	return false
}

// A symlink at a recorded output name is not our output (only a regular file is moved aside): the job fails, and the
// link and its target stay where they are, with nothing in the trash.
func TestConvertAgainRefusesASymlinkAtTheOutputName(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		target := filepath.Join(t.TempDir(), "target.mkv")
		if err := os.WriteFile(target, []byte("elsewhere"), 0o644); err != nil {
			t.Fatal(err)
		}
		s, out, _ := convertedThenAgain(t, folder, func(out string) {
			if err := os.Remove(out); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, out); err != nil {
				t.Skipf("symlinks need privileges here: %v", err)
			}
		})
		if j := s.job(); j.Status != jobs.Failed {
			t.Fatalf("again: %s %q, want it failed", j.Status, j.Error)
		}
		if fi, err := os.Lstat(out); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("at the name: %v, want the symlink kept", err)
		} else if to, _ := os.Readlink(out); to != target {
			t.Errorf("symlink points at %q, want %q", to, target)
		}
		if read(target) != "elsewhere" {
			t.Errorf("the link's target changed to %q", read(target))
		}
		if kept := trashedBeside(t, out); len(kept) != 0 {
			t.Errorf("trash %v, want none", kept)
		}
	})
}

// A directory at a recorded output name is not our output either: the job fails and the directory stays, with
// nothing in the trash.
func TestConvertAgainRefusesADirectoryAtTheOutputName(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		s, out, _ := convertedThenAgain(t, folder, func(out string) {
			if err := os.Remove(out); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(out, 0o755); err != nil {
				t.Fatal(err)
			}
		})
		if j := s.job(); j.Status != jobs.Failed {
			t.Fatalf("again: %s %q, want it failed", j.Status, j.Error)
		}
		if fi, err := os.Stat(out); err != nil || !fi.IsDir() {
			t.Errorf("at the name: %v, want the directory kept", err)
		}
		if kept := trashedBeside(t, out); len(kept) != 0 {
			t.Errorf("trash %v, want none", kept)
		}
	})
}

// The job's steps carry the trash sentence too: the Job page Timeline shows steps, not the log.
func TestConvertAgainMovedAsideIsInTheKeptStep(t *testing.T) {
	forBothPolicies(t, func(t *testing.T, folder bool) {
		later := time.Now().Add(72 * time.Hour)
		s, _, _ := convertedThenAgain(t, folder, func(out string) {
			if err := os.Chtimes(out, later, later); err != nil {
				t.Fatal(err)
			}
		})
		if j := s.job(); j.Status != jobs.Done {
			t.Fatalf("again: %s %q", j.Status, j.Error)
		}
		steps, err := s.store.Steps(s.id)
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range steps {
			if st.Step == "kept" || st.Step == "replaced" {
				if strings.Contains(st.Words, "was moved to the trash") {
					return
				}
			}
		}
		t.Errorf("no kept or replaced step says the changed file was moved to the trash: %+v", steps)
	})
}
