// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library's own folder for new files ("Next to each library", with an override) is a template too: the
// preview fills {{library}} in, so the worker fills it the same way. Without the fill the new file lands in a
// literal "{{library}}" folder, and the preview's folder never gets a file.
func TestOverrideFolderTemplateIsFilled(t *testing.T) {
	s := newScenario(t)
	lib, err := s.store.AddLibrary("Films", s.dir, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(t.TempDir(), "Shrunk", "{{library}}")
	if err := s.store.SetOverrideFolder(lib.ID, override); err != nil {
		t.Fatal(err)
	}
	s.store.Claim("setup") // the scenario's own job is set aside; the test uses one in the library
	s.store.Fail(s.id, "convert_failed", "set aside by the test")
	s.id, _ = s.store.Enqueue(jobs.Job{Library: s.dir, LibraryID: lib.ID, Path: s.src, DurationSec: scenarioDuration, SizeBefore: 14})

	sel := rules.DefaultSelectors(3)
	sel.AfterConverting, sel.FolderMode = "folder", rules.NextToLibrary
	w := selectorsWorker(s, func(s2 *rules.Selectors) { *s2 = sel })
	w.RunInfo = func(jobs.Job) (jobs.RunInfo, error) { return jobs.RunInfo{Library: "Films"}, nil }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(filepath.Dir(override), "Films", "movie.mkv")
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != out {
		t.Fatalf("status %s, output %q (error %q), want %s", j.Status, j.OutputPath, j.Error, out)
	}
	if read(out) != "new" || read(s.src) != "original-bytes" {
		t.Errorf("in the folder %q, original %q", read(out), read(s.src))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(override), "{{library}}")); err == nil {
		t.Error("a literal {{library}} folder was made")
	}
}

// A library's default folder beside it is literal, even when the library's own path has {{ in it (#764):
// only an override folder, or a workflow's folder, is a template.
func TestDefaultFolderIsLiteral(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "{{library}}", "Films")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir) // macOS: the temp folder is under /var, a link to /private/var, and the job's path is resolved
	if err != nil {
		t.Fatal(err)
	}
	s := newScenarioAt(t, dir, "movie.mkv")
	j := runOverride(t, s, "", "Films")
	out := filepath.Join(dir+" (shrunk)", "movie.mkv")
	if j.Status != jobs.Done || j.OutputPath != out {
		t.Fatalf("status %s, output %q (error %q), want %s", j.Status, j.OutputPath, j.Error, out)
	}
	if read(out) != "new" {
		t.Errorf("output %q", read(out))
	}
}

// An override folder that fills in to a folder we refuse (a relative path, a '..', a piece too long for a
// file name) stops before any write, and the message names the library's Output tab, where it is changed.
func TestOverrideFolderRefusalsNameOutputTab(t *testing.T) {
	cases := []struct {
		name, override, library, want string
	}{
		{"relative", "Shrunk/{{library}}", "Films", "isn't a full path"},
		{"dot dot", "{{tmp}}/../{{library}}", "Films", "'..'"},
		{"too long", "{{tmp}}/{{library}}", strings.Repeat("x", 300), "characters long"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			tmp := t.TempDir()
			override := strings.ReplaceAll(c.override, "{{tmp}}", tmp)
			j := runOverride(t, s, override, c.library)
			if j.Status != jobs.Failed || !strings.Contains(j.Error, c.want) || !strings.Contains(j.Error, "the library's Output tab") {
				t.Fatalf("status %s, error %q, want a refusal with %q naming the library's Output tab", j.Status, j.Error, c.want)
			}
			if j.OutputPath != "" {
				t.Errorf("output %q was made", j.OutputPath)
			}
			if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
				t.Errorf("something was written in %s: %d entries", tmp, len(entries))
			}
		})
	}
}

// runOverride sets library override (a "" is the default), queues s's file in that library, and runs it once;
// the library is named name for the folder's {{library}}. It returns the job as it ends, whether converted or refused.
func runOverride(t *testing.T, s *scenario, override, name string) jobs.Job {
	t.Helper()
	lib, err := s.store.AddLibrary("Films", s.dir, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetOverrideFolder(lib.ID, override); err != nil {
		t.Fatal(err)
	}
	s.store.Claim("setup") // the scenario's own job is set aside; the test uses one in the library
	s.store.Fail(s.id, "convert_failed", "set aside by the test")
	s.id, _ = s.store.Enqueue(jobs.Job{Library: s.dir, LibraryID: lib.ID, Path: s.src, DurationSec: scenarioDuration, SizeBefore: 14})

	sel := rules.DefaultSelectors(3)
	sel.AfterConverting, sel.FolderMode = "folder", rules.NextToLibrary
	w := selectorsWorker(s, func(s2 *rules.Selectors) { *s2 = sel })
	w.RunInfo = func(jobs.Job) (jobs.RunInfo, error) { return jobs.RunInfo{Library: name}, nil }
	w.RunOne(context.Background()) // a refused folder comes back as this error; the job's own state says the rest
	return s.job()
}
