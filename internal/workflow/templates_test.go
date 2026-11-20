// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package workflow

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// W4b: the Save the result step's three {{name}} templates go through a workflow and back unchanged;
// a workflow without them reads back exactly as it was (no keys in its step).
func TestTemplatesThroughAWorkflow(t *testing.T) {
	s := rules.DefaultSelectors(3)
	s.Ignore = []string{}
	s.OutputName, s.SrtName, s.TrackTitle = "{{title}} ({{resolution}})", "{{title}}.{{sub_lang}}{{sub_kind}}.srt", "{{title}} ({{video_codec}})"
	d := FromTemplate(s.Template(), nil)
	tm, issues := ToTemplate(d, rules.Template{})
	if len(issues) > 0 {
		t.Fatalf("%+v", issues)
	}
	got, ok := tm.SelectorsView()
	if !ok || got.OutputName != s.OutputName || got.SrtName != s.SrtName || got.TrackTitle != s.TrackTitle {
		t.Errorf("%+v", got)
	}
	// the step's schema knows them, so saving them is accepted
	q := rules.DefaultSelectors(3)
	q.Ignore = []string{}
	qd := FromTemplate(q.Template(), nil)
	for _, n := range qd.Nodes {
		if n.Type != TypeCommit {
			continue
		}
		for _, k := range []string{"outputName", "srtName", "trackTitle"} {
			if _, has := n.Config[k]; has {
				t.Errorf("a workflow without templates has %q", k)
			}
		}
	}
}
