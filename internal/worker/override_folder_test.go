// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"path/filepath"
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
