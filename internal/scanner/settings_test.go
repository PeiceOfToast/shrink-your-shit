// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W2 (review2-561 P2-1): the scanner judges a file on the settings its job will run it with, not on
// the template as it was published. A global saved after publish, or a source.*/file.* reference,
// would otherwise decide the skip on values the job never uses.

// dvProbe is a Dolby Vision profile 8 file with an HDR10 base: a flow that includes Dolby Vision
// converts it, one that does not leaves it alone (rules.dvHold). Its source.hdr is true.
const dvProbe = `{"streams":[{"codec_type":"video","codec_name":"hevc","color_transfer":"smpte2084","side_data_list":[{"side_data_type":"DOVI configuration record","dv_profile":8,"dv_bl_signal_compatibility_id":1}]}],"format":{"duration":"60","size":"5"}}`

// dvFlow is the Shrink videos preset with its Leave some files alone setting set to ref.
func dvFlow(ref string) workflow.Doc {
	d := workflow.Preset()
	for i := range d.Nodes {
		if d.Nodes[i].Type == workflow.TypeSkips {
			d.Nodes[i].Config["includeDolbyVision"] = ref
		}
	}
	return d
}

// published is the workflow's template as Publish writes it: the doc resolved with the globals it had
// then, file.* and source.* left as references.
func published(t *testing.T, d workflow.Doc, g []workflow.Variable) rules.Template {
	t.Helper()
	tmpl, issues := workflow.ToTemplateWith(d, rules.Template{}, workflow.Scopes{Global: workflow.VariableMap(g), LeaveFile: true})
	if len(issues) > 0 {
		t.Fatalf("publishing the test workflow: %+v", issues)
	}
	return tmpl
}

func firstLibrary(t *testing.T, st *jobs.Store) int64 {
	t.Helper()
	libs, err := st.Libraries()
	if err != nil || len(libs) == 0 {
		t.Fatalf("libraries: %v %d", err, len(libs))
	}
	return libs[0].ID
}

// includeDolbyVision was true when the workflow was published, and the global was saved false after:
// the job runs false, so the scanner must leave a Dolby Vision file alone, as the job would.
func TestAGlobalSavedAfterPublishDecidesTheSkip(t *testing.T) {
	s, st, lib, _ := setup(t, dvProbe)
	d := dvFlow("{{dv}}")
	atPublish := []workflow.Variable{{Name: "dv", Type: "boolean", Value: true}}
	savedSince := []workflow.Variable{{Name: "dv", Type: "boolean", Value: false}}
	s.Targets = func() ([]Target, error) {
		return []Target{{LibraryID: firstLibrary(t, st), Path: lib, Template: published(t, d, atPublish), Doc: d, Global: savedSince}}, nil
	}
	if n := settle(s); n != 0 {
		t.Errorf("queued %d: the job's setting leaves Dolby Vision alone", n)
	}
	if f, _ := st.GetFile(filepath.Join(lib, "a.mkv")); f == nil || f.Reason != rules.SkipDV || !strings.HasPrefix(f.Decision, "skip: ") {
		t.Errorf("record %+v, want a %s skip", f, rules.SkipDV)
	}
}

// includeDolbyVision is {{source.hdr}}: the published template can only hold the default (false), and
// only a job with the file resolves it. The scanner has the probe, so it must queue the HDR file.
func TestAFileValueDecidesTheSkip(t *testing.T) {
	s, st, lib, _ := setup(t, dvProbe)
	d := dvFlow("{{source.hdr}}")
	s.Targets = func() ([]Target, error) {
		return []Target{{LibraryID: firstLibrary(t, st), Path: lib, Template: published(t, d, nil), Doc: d}}, nil
	}
	if n := settle(s); n != 1 {
		t.Errorf("queued %d: source.hdr is true for this file, so its job converts it", n)
	}
	if l, _ := st.List(10); len(l) != 1 || l[0].Path != filepath.Join(lib, "a.mkv") {
		t.Errorf("jobs %+v", l)
	}
}

// A setting that can't be worked out for this file is not a skip: the file is queued and the job
// stops with the same plain words, so a value nobody resolved never leaves a file silently alone.
func TestSettingsThatCannotBeWorkedOutAreQueuedNotSkipped(t *testing.T) {
	s, st, lib, _ := setup(t, dvProbe)
	d := dvFlow("{{dv}}") // no dv variable anywhere: the resolution fails for this file
	s.Targets = func() ([]Target, error) {
		// the published template holds includeDolbyVision false, which would skip this file: the
		// scan must not decide from it once the doc can't be worked out
		return []Target{{LibraryID: firstLibrary(t, st), Path: lib, Template: rules.SliderTemplate(3), Doc: d}}, nil
	}
	if n := settle(s); n != 1 {
		t.Fatalf("queued %d, want the file queued for its job to stop with the words", n)
	}
	l, _ := st.List(10)
	if len(l) != 1 || !strings.Contains(l[0].Reason, "{{dv}}") {
		t.Errorf("jobs %+v, want one whose reason names {{dv}}", l)
	}
	f, _ := st.GetFile(filepath.Join(lib, "a.mkv"))
	if f == nil || !strings.HasPrefix(f.Decision, jobs.DecisionQueued) {
		t.Errorf("record %+v, want it queued", f)
	}
}
