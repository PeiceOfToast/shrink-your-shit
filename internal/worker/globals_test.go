// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
	"testing"

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
