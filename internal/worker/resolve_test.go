// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W1: a job runs the plan a typed variable makes. The workflow's Convert step says {{q}}, q is 1, and
// the job's recorded plan — what it ran with — has quality 1's CRF, and its variables table says q = 1.

// typedFlow publishes a workflow whose Convert Quality is qualityValue, and gives dir a library using it.
func typedFlow(t *testing.T, store *jobs.Store, dir string, variables []workflow.Variable, qualityValue any) {
	t.Helper()
	f, err := store.AddWorkflow("Typed", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := store.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = variables
	for i := range d.Doc.Nodes {
		if d.Doc.Nodes[i].Type == workflow.TypeConvert {
			d.Doc.Nodes[i].Config["quality"] = qualityValue
		}
	}
	saved, err := store.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	tm, issues := workflow.ToTemplate(saved.Doc, rules.SliderTemplate(3))
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if _, err := store.Publish(f.ID, saved.Etag, tm); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddLibraryOnFlow("Movies", dir, f.ID, true); err != nil {
		t.Fatal(err)
	}
}

func TestAJobRunsTheQualityAVariableNames(t *testing.T) {
	useTestHome(t)
	s := newScenario(t)
	typedFlow(t, s.store, s.dir, []workflow.Variable{{Name: "q", Type: "number", Value: float64(1)}}, "{{q}}")
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
	want := rules.SliderTemplate(1).Compile(src).Video.CRF
	if snap.Plan.Video.CRF != want {
		t.Errorf("the plan ran with CRF %d, want %d (quality 1's, from {{q}})", snap.Plan.Video.CRF, want)
	}
	if other := rules.SliderTemplate(3).Compile(src).Video.CRF; snap.Plan.Video.CRF == other {
		t.Errorf("the plan ran with quality 3's CRF %d: the variable made no difference", other)
	}
	// the job record says which values it ran with: q = 1, and a secret's is not written at all
	found := false
	for _, v := range snap.Variables {
		if v.Name == "q" {
			found = true
			if v.Start != "1" || v.End != "1" {
				t.Errorf("in the job record, q is %q at the start and %q at the end, want 1", v.Start, v.End)
			}
		}
	}
	if !found {
		t.Errorf("the job record has no q: %+v", snap.Variables)
	}
}

// With real ffmpeg: the same workflow converts for real at the quality the variable names.
func TestRealQualityFromAVariable(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "Movie.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=4:size=640x360:rate=25", "-c:v", "libx264", "-b:v", "4000k", "-pix_fmt", "yuv420p", src)
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false)
	typedFlow(t, st, dir, []workflow.Variable{{Name: "q", Type: "number", Value: float64(1)}}, "{{q}}")
	id, err := st.Enqueue(jobs.Job{Library: dir, Path: src, DurationSec: 4})
	if err != nil {
		t.Fatal(err)
	}
	w := realWorker(st, media.OSExec{})
	w.Template = st.TemplateFor
	w.RunInfo = st.RunInfoFor
	if err := w.run(t); err != nil {
		t.Fatal(err)
	}
	j, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	var snap rules.Snapshot
	if err := json.Unmarshal([]byte(j.Plan), &snap); err != nil {
		t.Fatal(err)
	}
	want := rules.SliderTemplate(1).Compile(probe(t, src))
	if snap.Plan.Video.CRF != want.Video.CRF {
		t.Errorf("the plan ran with CRF %d, want %d (quality 1's, from {{q}})", snap.Plan.Video.CRF, want.Video.CRF)
	}
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the converted file is gone: %v", err)
	}
}
