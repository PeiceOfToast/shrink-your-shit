// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A page loads a library's setting, changes nothing (or one field) and saves the whole answer back.
func TestLibrarySettingSavesItsGetBodyBack(t *testing.T) {
	for _, c := range []struct {
		name string
		t    rules.Template
	}{
		{"slider", rules.SliderTemplate(3)},
		{"basic", rules.BasicTemplate(2, "stereo_aac")},
		{"legacy", rules.LegacyTemplate(rules.Rule{FromCodec: "h264", CRF: 23, Preset: "medium"}, "replace")},
	} {
		srv, st, _, _ := libServer(t, false)
		l, _ := st.AddLibrary("Movies", t.TempDir(), c.t, true)
		path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
		_, before, _ := do(t, nil, srv, call{method: "GET", path: path})

		code, out := post(t, srv, "PUT", path, before)
		if code != 200 {
			t.Fatalf("%s: PUT of the GET body: %d %s", c.name, code, out)
		}
		if _, after, _ := do(t, nil, srv, call{method: "GET", path: path}); after != before {
			t.Errorf("%s: GET after the save differs from the GET before it", c.name)
		}
		if f, _ := st.GetFlow(l.FlowID); f.Template != c.t {
			t.Errorf("%s: a save of nothing changed the flow: %+v", c.name, f.Template)
		}
	}
}

// Read-only fields are not written, and a body with one choice changed changes only that choice.
func TestLibrarySettingReadOnlyFieldsAreNotWritten(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: path})
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	m["editableHere"], m["reason"], m["quality"] = false, map[string]string{"code": "x"}, 5
	b, _ := json.Marshal(m)
	if code, out := post(t, srv, "PUT", path, string(b)); code != 200 {
		t.Fatalf("PUT with one choice changed: %d %s", code, out)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template != rules.SliderTemplate(5) {
		t.Errorf("only quality should change: %+v", f.Template)
	}
}

// A flow this page can't edit answers its own GET echo with 409 (the same as any save), and writes nothing.
func TestBranchesLibraryEchoIsRefusedNotSaved(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	branches := rules.Template{Kind: rules.KindBranches, Branches: &[]rules.Branch{{End: "Left alone."}}}
	l, _ := st.AddLibrary("Paths", t.TempDir(), branches, true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	_, get, _ := do(t, nil, srv, call{method: "GET", path: path})
	code, body, _ := do(t, nil, srv, call{method: "PUT", path: path, body: get, ctype: "application/json"})
	if code != 409 || errCode(body) != "edit_in_new_ui" {
		t.Fatalf("echo of a branches setting: %d %s", code, body)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template.Kind != rules.KindBranches || (*f.Template.Branches)[0].End != "Left alone." {
		t.Errorf("the branches flow changed: %+v", f.Template)
	}
}

// The old page's one-setting save keeps a reordered flow's step order (#651 review P3-1).
func TestV1SettingSaveKeepsAReorderedStepOrder(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	first, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	standard := `{"quality":3,"speed":"normal","format":"hevc","resolution":"keep","maxBitrate":"none","audio":"keep_all","subtitles":"keep_all","fileType":"keep","afterConverting":"replace","ignore":[],"skipHdr":true,"skipSmall":true,"minSavingPct":10,"order":"newest"}`
	if code, body := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", first.ID), standard); code != 200 {
		t.Fatalf("standard: %d %s", code, body)
	}
	f, _ := st.GetFlow(first.FlowID)
	tm := f.Template
	if tm.Kind != rules.KindSelectors {
		t.Fatalf("want a Standard flow, got %s", tm.Kind)
	}
	tm.StepOrder = "Audio,Subtitles,File type,Convert video"
	l, _ := st.AddLibrary("Reordered", t.TempDir(), tm, true)
	if code, body := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", l.ID), strings.Replace(standard, `"speed":"normal"`, `"speed":"slower"`, 1)); code != 200 {
		t.Fatalf("one setting: %d %s", code, body)
	}
	got, _ := st.GetFlow(l.FlowID)
	if got.Template.Selectors == nil || got.Template.Selectors.Speed != "slower" {
		t.Errorf("the one setting was not saved: %+v", got.Template.Selectors)
	}
	if got.Template.StepOrder != "Audio,Subtitles,File type,Convert video" {
		t.Errorf("the step order was reset to %q", got.Template.StepOrder)
	}
}
