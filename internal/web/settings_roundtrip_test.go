// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"slices"
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

// The old page echoes back the three fields GET shows (rateControl, hardware, keepIfBigger) but never
// sends a new value for them. A save with one setting changed must succeed and keep their stored values.
func TestV1SettingSaveKeepsTheFieldsItEchoes(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	first, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	standard := `{"quality":3,"speed":"normal","format":"hevc","resolution":"keep","maxBitrate":"none","audio":"keep_all","subtitles":"keep_all","fileType":"keep","afterConverting":"replace","ignore":[],"skipHdr":true,"skipSmall":true,"minSavingPct":10,"order":"newest"}`
	if code, body := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", first.ID), standard); code != 200 {
		t.Fatalf("standard: %d %s", code, body)
	}
	f, _ := st.GetFlow(first.FlowID)
	tm := f.Template
	// a real level flow: a bitrate level with its max (the page sends the max back and the level is kept)
	tm.Selectors.RateControl, tm.Selectors.MaxBitrate, tm.Selectors.Hardware, tm.Selectors.KeepIfBigger = "level", "medium", "only", true
	l, _ := st.AddLibrary("Echoed", t.TempDir(), tm, true)
	echo := `,"rateControl":"level","hardware":"only","keepIfBigger":true`
	body := strings.Replace(strings.Replace(standard, `"speed":"normal"`, `"speed":"slower"`, 1), `"maxBitrate":"none"`, `"maxBitrate":"medium"`, 1)
	body = strings.TrimSuffix(body, "}") + echo + "}"
	if code, out := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", l.ID), body); code != 200 {
		t.Fatalf("echo save: %d %s", code, out)
	}
	got, _ := st.GetFlow(l.FlowID)
	s := got.Template.Selectors
	if s == nil || s.Speed != "slower" {
		t.Fatalf("the one setting was not saved: %+v", s)
	}
	if s.RateControl != "level" || s.MaxBitrate != "medium" || s.Hardware != "only" || !s.KeepIfBigger {
		t.Errorf("echoed fields were not kept: rateControl=%q maxBitrate=%q hardware=%q keepIfBigger=%v", s.RateControl, s.MaxBitrate, s.Hardware, s.KeepIfBigger)
	}
}

// A level flow's save that sends no bitrate max (none) is refused by Validate, with the kept level, and nothing is written.
func TestV1SettingRefusesALevelWithNoMax(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	first, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	standard := `{"quality":3,"speed":"normal","format":"hevc","resolution":"keep","maxBitrate":"none","audio":"keep_all","subtitles":"keep_all","fileType":"keep","afterConverting":"replace","ignore":[],"skipHdr":true,"skipSmall":true,"minSavingPct":10,"order":"newest"}`
	if code, body := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", first.ID), standard); code != 200 {
		t.Fatalf("standard: %d %s", code, body)
	}
	f, _ := st.GetFlow(first.FlowID)
	tm := f.Template
	tm.Selectors.RateControl, tm.Selectors.MaxBitrate = "level", "medium"
	l, _ := st.AddLibrary("Level", t.TempDir(), tm, true)
	body := strings.Replace(standard, `"speed":"normal"`, `"speed":"slower"`, 1)
	code, out := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", l.ID), body)
	if code != 400 || !strings.Contains(out, "Bitrate level needs Low, Medium or High") {
		t.Fatalf("level with no max: want 400 from Validate, got %d %s", code, out)
	}
	got, _ := st.GetFlow(l.FlowID)
	if s := got.Template.Selectors; s == nil || s.Speed != "normal" || s.RateControl != "level" || s.MaxBitrate != "medium" {
		t.Errorf("a refused save changed the template: %+v", s)
	}
}

// A slider flow's setting body that names a rate control it cannot have is refused with 409 and nothing is written.
func TestBasicRefusesAChangedEchoedField(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Slider", t.TempDir(), rules.SliderTemplate(3), true)
	before, _ := st.GetFlow(l.FlowID)
	want, _ := json.Marshal(before.Template)
	code, out := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", l.ID), `{"quality":4,"rateControl":"level"}`)
	if code != 409 || !strings.Contains(out, "edit_in_new_ui") {
		t.Fatalf("basic with changed rateControl: want 409 edit_in_new_ui, got %d %s", code, out)
	}
	got, _ := st.GetFlow(l.FlowID)
	if g, _ := json.Marshal(got.Template); string(g) != string(want) {
		t.Errorf("a refused save changed the template:\n got %s\nwant %s", g, want)
	}
}

// This page cannot set rateControl, hardware or keepIfBigger: a changed value is refused with 409 and nothing is written.
func TestV1SettingRefusesAChangedEchoedField(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	first, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	standard := `{"quality":3,"speed":"normal","format":"hevc","resolution":"keep","maxBitrate":"none","audio":"keep_all","subtitles":"keep_all","fileType":"keep","afterConverting":"replace","ignore":[],"skipHdr":true,"skipSmall":true,"minSavingPct":10,"order":"newest"}`
	if code, body := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", first.ID), standard); code != 200 {
		t.Fatalf("standard: %d %s", code, body)
	}
	f, _ := st.GetFlow(first.FlowID)
	tm := f.Template
	tm.Selectors.RateControl, tm.Selectors.MaxBitrate = "level", "medium"
	l, _ := st.AddLibrary("Changed", t.TempDir(), tm, true)
	body := strings.Replace(strings.Replace(standard, `"speed":"normal"`, `"speed":"slower"`, 1), `"maxBitrate":"none"`, `"maxBitrate":"medium"`, 1)
	body = strings.TrimSuffix(body, "}") + `,"rateControl":"quality"}`
	code, out := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", l.ID), body)
	if code != 409 || !strings.Contains(out, "edit_in_new_ui") {
		t.Fatalf("changed rateControl: want 409 edit_in_new_ui, got %d %s", code, out)
	}
	got, _ := st.GetFlow(l.FlowID)
	if s := got.Template.Selectors; s == nil || s.RateControl != "level" || s.Speed != "normal" {
		t.Errorf("a refused save changed the template: %+v", s)
	}
}

// A flow whose audio and subtitles steps keep their own lists (G2): GET shows them, the whole body saved back
// with one choice changed saves (200), and the steps' lists stay as they were (#724 review2 P2).
func TestLibrarySettingWithStepLanguagesSavesItsGetBody(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	sel := rules.DefaultSelectors(3)
	sel.Audio, sel.OnlyMyLanguages, _ = rules.StandardAudio("my_languages", true)
	sel.AudioLangs = []string{"eng", "ger"}
	sel.Subtitles = "my_languages"
	sel.SubLangs = []string{"fre"}
	l, _ := st.AddLibrary("Movies", t.TempDir(), sel.Template(), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	_, before, _ := do(t, nil, srv, call{method: "GET", path: path})
	if !strings.Contains(before, `"audioLanguages"`) || !strings.Contains(before, `"subtitleLanguages"`) {
		t.Fatalf("GET does not show the step languages: %s", before)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(before), &m); err != nil {
		t.Fatal(err)
	}
	m["quality"] = 2
	b, _ := json.Marshal(m)
	if code, out := post(t, srv, "PUT", path, string(b)); code != 200 {
		t.Fatalf("PUT of the GET body with one choice changed: %d %s", code, out)
	}
	f, _ := st.GetFlow(l.FlowID)
	got := f.Template.Selectors
	if got == nil || !slices.Equal(got.AudioLangs, []string{"eng", "ger"}) || !slices.Equal(got.SubLangs, []string{"fre"}) {
		t.Errorf("the step lists changed: audio %v, subtitles %v", got.AudioLangs, got.SubLangs)
	}
}

// A changed step list is refused with 409 and nothing is saved (#724 round 3); the same save with the list echoed
// unchanged goes through.
func TestChangedStepListIsRefusedNotDropped(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	sel := rules.DefaultSelectors(3)
	sel.Audio, sel.OnlyMyLanguages, _ = rules.StandardAudio("my_languages", true)
	sel.AudioLangs = []string{"eng", "ger"}
	l, _ := st.AddLibrary("Movies", t.TempDir(), sel.Template(), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	_, before, _ := do(t, nil, srv, call{method: "GET", path: path})
	var m map[string]any
	if err := json.Unmarshal([]byte(before), &m); err != nil {
		t.Fatal(err)
	}
	m["audioLanguages"] = []string{"fre"}
	b, _ := json.Marshal(m)
	code, out := post(t, srv, "PUT", path, string(b))
	if code != 409 || errCode(out) != "edit_in_new_ui" {
		t.Fatalf("changed step list: %d %s, want 409 edit_in_new_ui", code, out)
	}
	if !strings.Contains(out, "language list") || !strings.Contains(out, "workflow editor") {
		t.Errorf("the refusal must name the language list and the workflow editor: %s", out)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template.Selectors == nil || !slices.Equal(f.Template.Selectors.AudioLangs, []string{"eng", "ger"}) {
		t.Errorf("the refused save changed the stored list: %+v", f.Template.Selectors)
	}
}
