// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W2: a job reads its workflow's typed settings for the file it has — the server's global variables as
// they are now, and the file's own names (file.*, source.*) — and its variables table says where each
// value came from.

// publishResolved publishes a workflow whose doc is the preset with mut applied, resolving it the way
// a publish does, and gives dir a library using it.
func publishResolved(t *testing.T, store *jobs.Store, dir, name string, globals []workflow.Variable, mut func(*workflow.Doc)) {
	t.Helper()
	f, err := store.AddWorkflow(name, workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := store.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	mut(&d.Doc)
	saved, err := store.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	sc := workflow.Scopes{Global: workflow.VariableMap(globals), LeaveFile: true}
	tm, issues := workflow.ToTemplateWith(saved.Doc, rules.SliderTemplate(3), sc)
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if _, err := store.SetGlobalVariables(globals); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Publish(f.ID, saved.Etag, tm); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddLibraryOnFlow(name, dir, f.ID, true); err != nil {
		t.Fatal(err)
	}
}

// tableValue finds name in the job's variables table and checks where its value came from.
func tableValue(t *testing.T, table []rules.RunVariable, name, origin, start string) {
	t.Helper()
	for _, v := range table {
		if v.Name == name {
			if v.Origin != origin || v.Start != start {
				t.Errorf("in the job record, %s is %q from %q; want %q from %q", name, v.Start, v.Origin, start, origin)
			}
			return
		}
	}
	t.Errorf("the job record has no %s: %+v", name, table)
}

func TestAJobReadsTheGlobalVariablesAsTheyAreNow(t *testing.T) {
	useTestHome(t)
	s := newScenario(t)
	publishResolved(t, s.store, s.dir, "Global", []workflow.Variable{{Name: "q", Type: "number", Value: float64(1)}},
		func(d *workflow.Doc) {
			for i := range d.Nodes {
				if d.Nodes[i].Type == workflow.TypeConvert {
					d.Nodes[i].Config["quality"] = "{{q}}"
				}
			}
		})
	w := s.worker(Replace)
	w.Template = s.store.TemplateFor
	w.RunInfo = s.store.RunInfoFor
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	var snap rules.Snapshot
	if err := json.Unmarshal([]byte(j.Plan), &snap); err != nil {
		t.Fatal(err)
	}
	src, err := media.Probe(context.Background(), s.exec, "ffprobe", s.src)
	if err != nil {
		t.Fatal(err)
	}
	if want := rules.SliderTemplate(1).Compile(src).Video.CRF; snap.Plan.Video.CRF != want {
		t.Errorf("the plan ran with CRF %d, want %d (quality 1's, from the global q)", snap.Plan.Video.CRF, want)
	}
	if other := rules.SliderTemplate(3).Compile(src).Video.CRF; snap.Plan.Video.CRF == other {
		t.Errorf("the plan ran with quality 3's CRF %d: the global made no difference", other)
	}
	tableValue(t, snap.Variables, "q", "global", "1")
}

func TestAJobFillsTheFilesOwnValues(t *testing.T) {
	useTestHome(t)
	s := newScenario(t)
	publishResolved(t, s.store, s.dir, "File", nil, func(d *workflow.Doc) {
		for i := range d.Nodes {
			if d.Nodes[i].Type == workflow.TypeConvert {
				d.Nodes[i].Config["rateControl"] = "vbr"
				d.Nodes[i].Config["vbrMaxKbps"] = "{{source.height}}"
			}
		}
	})
	w := s.worker(Replace)
	w.Template = s.store.TemplateFor
	w.RunInfo = s.store.RunInfoFor
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := s.job()
	if j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	var snap rules.Snapshot
	if err := json.Unmarshal([]byte(j.Plan), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Plan.Video.PeakLimitKbps != 360 { // the fake original is 640x360
		t.Errorf("the plan's VBR limit is %d kbps, want the source's 360", snap.Plan.Video.PeakLimitKbps)
	}
	tableValue(t, snap.Variables, "source.height", "source", "360")
}

// #561 review ask 1: a run-info read that fails where the settings are resolved for the file stops the
// job for a retry and never runs the store's template. That template keeps the file reference's
// placeholder (the schema's minimum, 100 kbps), which is not a value anyone chose.
func TestAResolveReadThatFailsNeverRunsTheStoredTemplate(t *testing.T) {
	useTestHome(t)
	s := newScenario(t)
	publishResolved(t, s.store, s.dir, "Placeholder", nil, func(d *workflow.Doc) {
		for i := range d.Nodes {
			if d.Nodes[i].Type == workflow.TypeConvert {
				d.Nodes[i].Config["rateControl"] = "vbr"
				d.Nodes[i].Config["vbrMaxKbps"] = "{{source.height}}"
			}
		}
	})
	w := s.retrying()
	w.Template = s.store.TemplateFor
	calls := 0
	w.RunInfo = func(j jobs.Job) (jobs.RunInfo, error) {
		calls++ // runFacts reads it first; the resolve's read is the second
		if calls == 2 {
			return jobs.RunInfo{}, errors.New("database is locked")
		}
		return s.store.RunInfoFor(j)
	}
	w.RunOne(context.Background())
	s.wantRetry(1, 5*time.Minute, "could not be read")
	if n := s.count("libx265"); n != 0 {
		t.Fatalf("%d encode(s) ran with the stored template's placeholder", n)
	}
	// the retry reads it, resolves the file's own limit, and converts with that
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Done {
		t.Fatalf("the retry: %s, error %q", j.Status, j.Error)
	}
	var snap rules.Snapshot
	if err := json.Unmarshal([]byte(j.Plan), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Plan.Video.PeakLimitKbps != 360 {
		t.Errorf("the retry's VBR limit is %d kbps, want the source's 360", snap.Plan.Video.PeakLimitKbps)
	}
}

// #561 review ask 2: a job refused where its settings resolve records the issue in its log before it
// stops, so the variables section says what it could not use (#556's lesson).
func TestARefusedSettingRecordsTheIssue(t *testing.T) {
	useTestHome(t)
	s := newScenario(t)
	// Quality takes the numbers 1-5; the file's height (360) is a number, but not one of them.
	publishResolved(t, s.store, s.dir, "Wrong", nil, func(d *workflow.Doc) {
		for i := range d.Nodes {
			if d.Nodes[i].Type == workflow.TypeConvert {
				d.Nodes[i].Config["quality"] = "{{source.height}}"
			}
		}
	})
	w := s.worker(Replace)
	w.Template = s.store.TemplateFor
	w.RunInfo = s.store.RunInfoFor
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Failed || !strings.Contains(j.Error, "{{source.height}}") || !strings.Contains(j.Error, "doesn't fit") {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if s.count("libx265") != 0 {
		t.Error("an encode ran with a setting that does not resolve")
	}
	var recorded bool
	for _, text := range logTexts(t, s) {
		if strings.Contains(text, "were not filled") && strings.Contains(text, "{{source.height}}") {
			recorded = true
		}
	}
	if !recorded {
		t.Errorf("the job log does not record the resolve issue: %q", logTexts(t, s))
	}
	s.assertOriginalSafe()
}

// #561 review ask 3: a global changed after the workflow was published can stop its settings
// resolving. The job stops, naming the workflow and the variable, instead of converting with the
// template compiled from the values from before (the server log used to be the only trace).
func TestAGlobalChangeThatBreaksAWorkflowStopsTheJob(t *testing.T) {
	useTestHome(t)
	s := newScenario(t)
	publishResolved(t, s.store, s.dir, "Global", []workflow.Variable{{Name: "q", Type: "number", Value: float64(1)}},
		func(d *workflow.Doc) {
			for i := range d.Nodes {
				if d.Nodes[i].Type == workflow.TypeConvert {
					d.Nodes[i].Config["quality"] = "{{q}}"
				}
			}
		})
	if _, err := s.store.SetGlobalVariables(nil); err != nil { // the person removed q
		t.Fatal(err)
	}
	w := s.worker(Replace)
	w.Template = s.store.TemplateFor
	w.RunInfo = s.store.RunInfoFor
	w.RunOne(context.Background())
	j := s.job()
	if j.Status != jobs.Failed {
		t.Fatalf("status %s, error %q; want failed", j.Status, j.Error)
	}
	for _, want := range []string{"“Global”", "{{q}}"} {
		if !strings.Contains(j.Error, want) {
			t.Errorf("the job's words do not name %s: %q", want, j.Error)
		}
	}
	if s.count("libx265") != 0 {
		t.Error("an encode ran with the template from before the global change")
	}
	if !strings.Contains(strings.Join(logTexts(t, s), "\n"), "were not filled") {
		t.Errorf("the job log does not record the issue: %q", logTexts(t, s))
	}
	s.assertOriginalSafe()
}
