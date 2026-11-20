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

// A folder template that does not resolve to a full path stops the job with a plain message before anything
// is made: a value is one path piece, so the title cannot make it absolute, and this template's own "/small"
// leaves it relative. Without the check the run would create "small" beside the daemon's working directory.
func TestRelativeFolderTemplateStopsTheJob(t *testing.T) {
	s := newScenario(t)
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting, sel.OutputDir = "folder", "{{title}}/small"
	})
	w.RunInfo = func(jobs.Job) (jobs.RunInfo, error) { return jobs.RunInfo{Library: "Films"}, nil }
	w.RunOne(context.Background()) // the failure is the job's, checked below
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, `came out as "movie/small", which isn't a full path`) {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if read(s.src) != "original-bytes" {
		t.Errorf("the original %q", read(s.src))
	}
	if _, err := os.Stat(filepath.Join(s.dir, "small")); err == nil {
		t.Error("a folder was made")
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

// useTestHome points this computer's share key at a temp folder: a secret variable's value is stored sealed,
// and a test must not write a key into the real home.
func useTestHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home)
}

// A workflow's secret is stored sealed and still resolves for a job through the store's RunInfo: the run
// finishes, and the log says the secret is hidden without ever writing its value.
func TestAWorkflowSecretResolvesInAJobAndStaysOutOfItsLog(t *testing.T) {
	useTestHome(t)
	const secret = "w4b-job-secret-77"
	s := newScenario(t)
	f, err := s.store.AddWorkflow("Sealed", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := s.store.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = []workflow.Variable{{Name: "api_key", Type: "secret", Value: secret, HasValue: true}}
	saved, err := s.store.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	tm, issues := workflow.ToTemplate(saved.Doc, rules.SliderTemplate(3))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if _, err := s.store.Publish(f.ID, saved.Etag, tm); err != nil {
		t.Fatal(err)
	}
	if _, err := s.store.AddLibraryOnFlow("Movies", s.dir, f.ID, false); err != nil {
		t.Fatal(err)
	}
	w := selectorsWorker(s, func(sel *rules.Selectors) { sel.AfterConverting = "beside" })
	w.RunInfo = s.store.RunInfoFor
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	log := strings.Join(logTexts(t, s), "\n")
	if !strings.Contains(log, "{{api_key}} = Hidden") {
		t.Errorf("the log does not name the hidden secret:\n%s", log)
	}
	if strings.Contains(log, secret) {
		t.Errorf("the secret's value was written to the log:\n%s", log)
	}
}

// A workflow's secret that this computer can't open (it was sealed with another machine's share key, as a
// restored backup's was) stops the job before anything is converted, naming it: the run never goes on with
// the secret silently empty.
func TestAnUnopenableSecretStopsTheJob(t *testing.T) {
	s := newScenario(t)
	w := selectorsWorker(s, func(sel *rules.Selectors) { sel.AfterConverting = "beside" })
	w.RunInfo = func(jobs.Job) (jobs.RunInfo, error) {
		return jobs.RunInfo{}, &jobs.SecretUnreadableError{Names: []string{"api_key"}}
	}
	w.RunOne(context.Background()) // the failure is the job's, checked below
	j := s.job()
	// like the other unreadable-setting failures it waits for a retry (the person can enter the secret in
	// the meantime), and the job says why meanwhile
	if j.Status != jobs.Queued || !strings.Contains(j.Error, "The workflow secret “api_key” can't be opened on this computer") ||
		!strings.Contains(j.Error, "Enter it again in the workflow, then press Retry.") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if n := len(encodeCalls(s)); n != 0 {
		t.Errorf("%d encode(s) ran before the secret was checked", n)
	}
	if read(s.src) != "original-bytes" {
		t.Errorf("the original is %q", read(s.src))
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

// A name the filesystem would refuse is refused before the conversion, naming the setting that made it
// (review2-504 P3: it used to convert, then fail at the final rename with "could not be put in place",
// which Retry could never fix).
func TestATooLongOutputNameStopsBeforeTheConversion(t *testing.T) {
	s := newScenario(t)
	long := strings.Repeat("a", 250) + ".mkv" // more than any of the two filesystems' limits
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting = "beside"
		sel.OutputName = strings.Repeat("a", 250)
	})
	w.RunOne(context.Background()) // the failure is the job's, checked below
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "The name from “Output file name” would be 254 characters long") ||
		!strings.Contains(j.Error, "Nothing was changed.") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if n := len(encodeCalls(s)); n != 0 {
		t.Errorf("%d encode(s) ran before the name was checked", n)
	}
	if read(s.src) != "original-bytes" {
		t.Errorf("the original is %q", read(s.src))
	}
	if _, err := os.Stat(filepath.Join(s.dir, long)); err == nil {
		t.Error("a file at the too-long name exists")
	}
}

// The cap covers each piece of the move-to folder too (review2-511 P3a, M6): a folder template that
// resolves to a piece the filesystem would refuse stops before the folder is made or anything is
// converted.
func TestATooLongFolderPieceStopsBeforeTheConversion(t *testing.T) {
	title := strings.Repeat("m", 220) // a long original's name fills the {{title}} piece
	s := newScenarioNamed(t, title+".mkv")
	moveRoot := t.TempDir()
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting, sel.OutputDir = "folder", filepath.Join(moveRoot, "{{title}}")
	})
	w.RunOne(context.Background()) // the failure is the job's, checked below
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "The name from “Folder” would be 220 characters long") ||
		!strings.Contains(j.Error, "Nothing was changed.") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if n := len(encodeCalls(s)); n != 0 {
		t.Errorf("%d encode(s) ran before the folder was checked", n)
	}
	if read(s.src) != "original-bytes" {
		t.Errorf("the original is %q", read(s.src))
	}
	if _, err := os.Stat(filepath.Join(moveRoot, title)); err == nil {
		t.Error("a folder at the too-long piece was made")
	}
}

// And the .srt names (review2-511 P3a, M7): a template that resolves past the cap stops before the
// conversion, naming that setting rather than failing on the write.
func TestATooLongSrtNameStopsBeforeTheConversion(t *testing.T) {
	s := newScenario(t)
	s.srcSpec.streams = []map[string]any{vid("h264", 640, 360, "yuv420p"), aud("aac", "eng", 2),
		sub("subrip", "eng", false), att("font.ttf")}
	out := goodOutput()
	out.streams = append(out.streams[:2], out.streams[3]) // no subtitle inside the new file
	s.outSpec = out
	w := selectorsWorker(s, func(sel *rules.Selectors) {
		sel.AfterConverting, sel.Subtitles = "beside", "srt"
		sel.SrtName = strings.Repeat("s", 210) // ".srt" makes it 214
	})
	w.RunOne(context.Background()) // the failure is the job's, checked below
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "The name from “Subtitle file name” would be 214 characters long") ||
		!strings.Contains(j.Error, "Nothing was changed.") {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if n := len(encodeCalls(s)); n != 0 {
		t.Errorf("%d encode(s) ran before the .srt name was checked", n)
	}
	if read(s.src) != "original-bytes" {
		t.Errorf("the original is %q", read(s.src))
	}
}

// The cap's edge: 200 bytes is allowed, 201 is not (review2-511 P3a asked for the boundary).
func TestNameLengthBoundary(t *testing.T) {
	if nameTooLong(strings.Repeat("x", maxNameLen)) {
		t.Errorf("%d bytes must be allowed", maxNameLen)
	}
	if !nameTooLong(strings.Repeat("x", maxNameLen+1)) {
		t.Errorf("%d bytes must be refused", maxNameLen+1)
	}
}
