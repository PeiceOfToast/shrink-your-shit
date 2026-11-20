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
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W4b: the Save the result step's {{name}} templates on a run (design-joblog-vars sections 2 and 3):
// the output name, the .srt names and the move-to folder resolve from the same values the editor's
// variable list shows, an empty name falls back to today's, and the job log says what they were.

// selectorsWorker is s.worker with the selectors after change as its workflow.
func selectorsWorker(s *scenario, change func(*rules.Selectors)) *Worker {
	sel := rules.DefaultSelectors(3)
	change(&sel)
	tmpl := sel.Template()
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	return w
}

// The output name's template names the new file, in the source's own folder beside it: the original
// stays where it is.
func TestTemplatedOutputNameBeside(t *testing.T) {
	s := newScenario(t)
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting = "beside"
		sel.OutputName = "{{title}}-{{resolution}}"
	})
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(s.dir, "movie-360p.mkv")
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != out {
		t.Fatalf("status %s, output %q (error %q), want %s", j.Status, j.OutputPath, j.Error, out)
	}
	if read(out) != "new" || read(s.src) != "original-bytes" {
		t.Errorf("beside %q, original %q", read(out), read(s.src))
	}
}

// The .srt name's template sees sub_lang and sub_kind, which only it has: the forced English subtitle
// is saved at the name the template made.
func TestTemplatedSrtNameUsesSubLangAndKind(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams = []map[string]any{vid("h264", 640, 360, "yuv420p"), aud("aac", "eng", 2),
		sub("subrip", "eng", true), att("font.ttf")}
	out := goodOutput()
	out.streams = append(out.streams[:2], out.streams[3]) // no subtitle inside the new file
	s.outSpec = out
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting = "beside"
		sel.Subtitles = "srt"
		sel.SrtName = "{{title}}-{{sub_lang}}{{sub_kind}}"
	})
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	srt := filepath.Join(s.dir, "movie-eng.forced.srt")
	if b := read(srt); !strings.Contains(b, "subtitle from") {
		t.Errorf("%s: %q", srt, b)
	}
	if left, _ := s.store.Sidecars(); len(left) != 0 {
		t.Errorf("sidecar records left: %+v", left)
	}
}

// The move-to folder is a template too: the folder is the path a person typed, and a value fills in one
// piece of it (a value never brings separators of its own).
func TestTemplatedFolder(t *testing.T) {
	s := newScenario(t)
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting, sel.OutputDir = "folder", filepath.Join(s.dir, "Shrunk", "{{library}}")
	})
	w.RunInfo = func(jobs.Job) (jobs.RunInfo, error) { return jobs.RunInfo{Library: "Films"}, nil }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(s.dir, "Shrunk", "Films", "movie.mkv")
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != out {
		t.Fatalf("status %s, output %q (error %q), want %s", j.Status, j.OutputPath, j.Error, out)
	}
	if read(out) != "new" || read(s.src) != "original-bytes" {
		t.Errorf("in the folder %q, original %q", read(out), read(s.src))
	}
}

// The never-overwrite checks run on the path the template resolved to: a stranger's file at that name
// stops the job before anything is converted, and nothing is touched.
func TestResolvedNameTakenStopsTheJob(t *testing.T) {
	s := newScenario(t)
	stranger := filepath.Join(s.dir, "movie.small.mkv")
	os.WriteFile(stranger, []byte("someone else's"), 0o644)
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting = "beside"
		sel.OutputName = "{{title}}.small"
	})
	w.RunOne(context.Background()) // the failure is the job's, checked below
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "There is already a file named movie.small.mkv") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if read(stranger) != "someone else's" || read(s.src) != "original-bytes" {
		t.Errorf("the stranger %q, the original %q", read(stranger), read(s.src))
	}
}

// A template that resolves to the source's own path renames nothing: beside, the original is the file
// at the name, so the job stops and the original survives.
func TestTemplateResolvingToTheSourceKeepsIt(t *testing.T) {
	s := newScenario(t)
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting = "beside"
		sel.OutputName = "{{title}}.{{ext}}"
	})
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "movie.mkv") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	s.assertOriginalSafe()
}

// An unknown name (a draft's typo, refused at publish) resolves to nothing: the name that is left is
// today's, and the log says which name came out empty.
func TestUnknownVariableNameFallsBackToTodaysNameAndTheLogNamesIt(t *testing.T) {
	s := newScenario(t)
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.OutputName = "{{titel}} {{title}}"
	})
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done || j.OutputPath != s.src {
		t.Fatalf("status %s, output %q (error %q)", j.Status, j.OutputPath, j.Error)
	}
	if read(s.src) != "new" {
		t.Errorf("at the name %q", read(s.src))
	}
	log := strings.Join(logTexts(t, s), "\n")
	if !strings.Contains(log, "Empty for this file, so a {{name}} of theirs is nothing: {{titel}}") {
		t.Errorf("the log does not name the empty variable:\n%s", log)
	}
	if !strings.Contains(log, "{{title}} = movie") {
		t.Errorf("the log does not show title's value:\n%s", log)
	}
}

// The variables section shows the run's values and its workflow's own, and a secret's value is never
// written: only that it is hidden.
func TestVariablesLogNamesTheValuesAndHidesASecret(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.RunInfo = func(jobs.Job) (jobs.RunInfo, error) {
		return jobs.RunInfo{Library: "Films", Workflow: "My workflow", Revision: 3, Variables: []workflow.Variable{
			{Name: "api_key", Type: "secret", Value: "s3cret", HasValue: true},
			{Name: "studio", Type: "text", Value: "Mine"},
		}}, nil
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	log := strings.Join(logTexts(t, s), "\n")
	for _, want := range []string{"{{api_key}} = Hidden", "{{studio}} = Mine", "{{title}} = movie"} {
		if !strings.Contains(log, want) {
			t.Errorf("the log has no %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "s3cret") {
		t.Errorf("a secret's value was written:\n%s", log)
	}
}

// A .srt name the template made never overwrites a stranger's file: the job stops before converting.
func TestTemplatedSrtNameTakenStopsTheJob(t *testing.T) {
	s := newScenario(t)
	out := goodOutput()
	out.streams = append(out.streams[:2], out.streams[3])
	s.outSpec = out
	taken := filepath.Join(s.dir, "movie.eng.srt")
	os.WriteFile(taken, []byte("mine"), 0o644)
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting = "beside"
		sel.Subtitles = "srt"
		sel.SrtName = "{{title}}.{{sub_lang}}"
	})
	w.RunOne(context.Background())
	if j := s.job(); j.Status != jobs.Failed || !strings.Contains(j.Error, "A subtitle file named movie.eng.srt is already there") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if read(taken) != "mine" || read(s.src) != "original-bytes" {
		t.Errorf("the stranger %q, the original %q", read(taken), read(s.src))
	}
}

// logTexts are the job's log lines as text.
func logTexts(t *testing.T, s *scenario) []string {
	t.Helper()
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(lines))
	for k, l := range lines {
		out[k] = l.Text
	}
	return out
}
