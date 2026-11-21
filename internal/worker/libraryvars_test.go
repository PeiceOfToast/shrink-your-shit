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

// W3 (workflow-architecture.md 2.2 rule 1): a job runs with its library's value for a variable the
// workflow lets the library change, and the variables table says the value came from the library.

func TestAJobReadsTheLibrarysValueForItsWorkflow(t *testing.T) {
	useTestHome(t)
	s := newScenario(t)
	lib := publishResolved(t, s.store, s.dir, "Library", nil, func(d *workflow.Doc) {
		d.Variables = []workflow.Variable{{Name: "q", Type: "number", Value: float64(1), TemplateParam: true}}
		for i := range d.Nodes {
			if d.Nodes[i].Type == workflow.TypeConvert {
				d.Nodes[i].Config["quality"] = "{{q}}"
			}
		}
	})
	if err := s.store.SetLibraryVariables(lib.ID, []workflow.Variable{{Name: "q", Value: float64(5)}}); err != nil {
		t.Fatal(err)
	}
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
	if want := rules.SliderTemplate(5).Compile(src).Video.CRF; snap.Plan.Video.CRF != want {
		t.Errorf("the plan ran with CRF %d, want %d (quality 5's, from the library's q)", snap.Plan.Video.CRF, want)
	}
	if own := rules.SliderTemplate(1).Compile(src).Video.CRF; snap.Plan.Video.CRF == own {
		t.Errorf("the plan ran with the workflow's quality 1 (CRF %d): the library's value made no difference", own)
	}
	tableValue(t, snap.Variables, "q", "library", "5")
}
