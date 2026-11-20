// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"reflect"
	"testing"
)

// W4b: a move-to folder with {{names}} is checked when the run fills it in (it must then be a full
// path), so saving it needs no absolute path yet; a plain relative folder is still refused.
func TestFolderWithVariables(t *testing.T) {
	s := DefaultSelectors(3)
	s.AfterConverting, s.FolderMode = "folder", ""
	s.OutputDir = "{{library_root}}/small"
	if err := s.Validate(); err != nil {
		t.Errorf("a folder with variables: %v", err)
	}
	s.OutputDir = "small"
	if err := s.Validate(); err == nil {
		t.Error("a relative folder is refused")
	}
	s.OutputDir = " "
	if err := s.Validate(); err == nil {
		t.Error("an empty folder is refused")
	}
}

// W4b: the three templates are the Save the result step's fields, and reach the Plan as they are.
func TestTemplatesReachThePlan(t *testing.T) {
	s := DefaultSelectors(3)
	s.OutputName, s.SrtName, s.TrackTitle = "{{title}} ({{resolution}})", "{{title}}.{{sub_lang}}", "{{title}}"
	tmpl := s.Template()
	got := *tmpl.Selectors
	if got.OutputName != s.OutputName || got.SrtName != s.SrtName || got.TrackTitle != s.TrackTitle {
		t.Errorf("template: %q %q %q", got.OutputName, got.SrtName, got.TrackTitle)
	}
	p := tmpl.Compile(h264(1920, 1080, 8000))
	if p.OutputName != s.OutputName || p.SrtName != s.SrtName || p.TrackTitle != s.TrackTitle {
		t.Errorf("plan: %q %q %q", p.OutputName, p.SrtName, p.TrackTitle)
	}
	// nothing resolved yet: the run fills these in, so they are empty and today's naming holds
	if p.Title != "" || len(p.SrtNames) != 0 || !reflect.DeepEqual(p.SrtNames, []string(nil)) {
		t.Errorf("run values set before the run: %q %v", p.Title, p.SrtNames)
	}
}
