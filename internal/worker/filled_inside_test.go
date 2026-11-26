// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A filled-in folder for new files that is inside another library, or holds one, is refused before any write
// (#764 P2): the new files would be converted again. The original is untouched and the job is not retryable.
func TestFilledFolderInsideALibraryIsRefused(t *testing.T) {
	cases := []struct {
		name     string
		other    string // the other library's folder, under the temp dir's Films folder (the fill target)
		under    string // a folder below {{library}} in the template ("": none)
		workflow bool   // the folder is a workflow's, not an override
		where    string
	}{
		{"override inside", "Films", "", false, "the library's Output tab"},
		{"workflow inside", "Films", "", true, "the workflow's Save the result step"},
		{"override strictly inside", "Films", "Sub", false, "the library's Output tab"}, // review2-766: fills below the library, not to it
		{"override holds", filepath.Join("Films", "Sub"), "", false, "the library's Output tab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tmp, err := filepath.EvalSymlinks(t.TempDir()) // macOS: the job's path is resolved, as TestDefaultFolderIsLiteral does
			if err != nil {
				t.Fatal(err)
			}
			filled := filepath.Join(tmp, "Films", c.under)
			other := filepath.Join(tmp, c.other)
			if err := os.MkdirAll(other, 0o755); err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(tmp, "Source")
			if err := os.MkdirAll(src, 0o755); err != nil {
				t.Fatal(err)
			}
			s := newScenarioAt(t, src, "movie.mkv")
			s.store.AddLibrary("Other", other, rules.SliderTemplate(3), true)
			j := runFilled(t, s, filepath.Join(tmp, "{{library}}", c.under), c.workflow)
			want := fmt.Sprintf("%q", filled)
			if j.Status != jobs.Failed || !strings.Contains(j.Error, want) || !strings.Contains(j.Error, "inside the library at") ||
				!strings.Contains(j.Error, "Your original was not touched") || !strings.Contains(j.Error, c.where) {
				t.Fatalf("status %s, error %q: want a refusal naming %s and %s", j.Status, j.Error, want, c.where)
			}
			if j.OutputPath != "" {
				t.Errorf("output %q was made", j.OutputPath)
			}
			if _, err := os.Stat(filepath.Join(filled, "movie.mkv")); err == nil {
				t.Error("a file was written in the filled-in folder")
			}
			if read(s.src) != "original-bytes" {
				t.Errorf("the original was changed: %q", read(s.src))
			}
		})
	}
}

// runFilled runs s's file once with folder: an override of the library "Films" (workflow false), or a workflow's
// own folder (workflow true, with the library's override unset). It returns the job as it ends.
func runFilled(t *testing.T, s *scenario, folder string, workflow bool) jobs.Job {
	t.Helper()
	override := folder
	if workflow {
		override = ""
	}
	lib, err := s.store.AddLibrary("Films", s.dir, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetOverrideFolder(lib.ID, override); err != nil {
		t.Fatal(err)
	}
	s.store.Claim("setup") // the scenario's own job is set aside; the test uses one in the library
	s.store.Fail(s.id, "convert_failed", "set aside by the test")
	id, err := s.store.Enqueue(jobs.Job{Library: s.dir, LibraryID: lib.ID, Path: s.src, DurationSec: scenarioDuration, SizeBefore: 14})
	if err != nil {
		t.Fatal(err)
	}
	s.id = id

	sel := rules.DefaultSelectors(3)
	sel.AfterConverting = "folder"
	if workflow { // a workflow's own folder: no folder mode, the folder itself
		sel.OutputDir = folder
	} else {
		sel.FolderMode = rules.NextToLibrary
	}
	w := selectorsWorker(s, func(s2 *rules.Selectors) { *s2 = sel })
	w.RunInfo = func(jobs.Job) (jobs.RunInfo, error) { return jobs.RunInfo{Library: "Films"}, nil }
	w.RunOne(context.Background()) // a refused folder comes back as this error; the job's own state says the rest
	return s.job()
}
