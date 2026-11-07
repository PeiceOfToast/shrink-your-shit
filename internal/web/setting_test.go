// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func TestSliderTable(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings/slider"})
	var out struct{ Positions []map[string]any }
	json.Unmarshal([]byte(body), &out)
	if code != 200 || len(out.Positions) != 5 || out.Positions[2]["name"] != "Balanced" || out.Positions[0]["name"] != "Smallest file" || out.Positions[2]["crf"] != 23.0 || out.Positions[2]["minSavingPct"] != 10.0 {
		t.Fatalf("%d %s", code, body)
	}
	for k := range out.Positions[0] {
		if !slices.Contains([]string{"position", "name", "crf", "minSavingPct"}, k) {
			t.Errorf("the table shows only the contract's fields, not %q", k)
		}
	}
}

func setting(t *testing.T, body string) (kind string, quality float64) {
	t.Helper()
	var s struct {
		Kind      string
		Quality   float64
		Positions []any
	}
	json.Unmarshal([]byte(body), &s)
	if s.Kind == "slider" && len(s.Positions) != 5 {
		t.Errorf("a slider setting carries the table: %s", body)
	}
	return s.Kind, s.Quality
}

func TestLibrarySliderSetting(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: path})
	if k, q := setting(t, body); k != "slider" || q != 3 {
		t.Fatalf("%s", body)
	}
	code, body := post(t, srv, "PUT", path, `{"quality":5}`)
	if k, q := setting(t, body); code != 200 || k != "slider" || q != 5 || fe.scans != 1 {
		t.Fatalf("%d %s, scans %d", code, body, fe.scans)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template != rules.SliderTemplate(5) {
		t.Errorf("the library's own flow is changed in place: %+v", f.Template)
	}
	for _, b := range []string{`{"quality":0}`, `{"quality":6}`, `{}`} {
		if code, body := post(t, srv, "PUT", path, b); code != 400 || errCode(body) != "invalid_quality" {
			t.Errorf("%s: %d %s", b, code, body)
		}
	}
	if code, body := post(t, srv, "PUT", path, `{"quality":2,"sharedFlowChoice":"both"}`); code != 400 || errCode(body) != "invalid_choice" {
		t.Errorf("bad choice: %d %s", code, body)
	}
	if code, _ := post(t, srv, "PUT", "/api/libraries/999/setting", `{"quality":2}`); code != 404 {
		t.Errorf("unknown library: %d", code)
	}
}

// A legacy (or custom) flow is never overwritten: saving a slider gives the library a new flow.
func TestSliderOnALegacyFlowMakesANewFlow(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Old", t.TempDir(), rules.LegacyTemplate(rules.Default(), "replace"), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: path}); !strings.Contains(body, `"kind":"custom"`) {
		t.Errorf("a legacy flow shows as custom: %s", body)
	}
	if code, body := post(t, srv, "PUT", path, `{"quality":2}`); code != 200 || !strings.Contains(body, `"kind":"slider"`) {
		t.Fatalf("%d %s", code, body)
	}
	now, _ := st.GetLibrary(l.ID)
	nf, _ := st.GetFlow(now.FlowID)
	old, err := st.GetFlow(l.FlowID)
	if now.FlowID == l.FlowID || nf.Name != "Old 2" || nf.Template != rules.SliderTemplate(2) || err != nil || old.Template.Kind != rules.KindLegacy {
		t.Errorf("new flow %+v, old %+v %v", nf, old, err)
	}
	// replace-custom does the same at Balanced
	l2, _ := st.AddLibrary("Other", t.TempDir(), rules.LegacyTemplate(rules.Default(), "beside"), true)
	code, body := post(t, srv, "POST", fmt.Sprintf("/api/libraries/%d/setting/replace-custom", l2.ID), "{}")
	if k, q := setting(t, body); code != 200 || k != "slider" || q != 3 {
		t.Errorf("replace-custom: %d %s", code, body)
	}
}

func TestSharedFlowNeedsAChoice(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	a, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	b, _ := st.AddLibrary("Anime", t.TempDir(), rules.SliderTemplate(1), true)
	st.SetLibraryFlow(b.ID, a.FlowID)
	path := fmt.Sprintf("/api/libraries/%d/setting", a.ID)
	code, body := post(t, srv, "PUT", path, `{"quality":4}`)
	var e struct {
		Error struct {
			Code   string
			UsedBy []string
		}
	}
	json.Unmarshal([]byte(body), &e)
	if code != 409 || e.Error.Code != "flow_shared" || !slices.Equal(e.Error.UsedBy, []string{"Anime", "Movies"}) {
		t.Fatalf("%d %s", code, body)
	}
	if f, _ := st.GetFlow(a.FlowID); f.Template != rules.SliderTemplate(3) {
		t.Error("nothing may change before the choice")
	}
	post(t, srv, "PUT", path, `{"quality":4,"sharedFlowChoice":"change_both"}`)
	if f, _ := st.GetFlow(a.FlowID); f.Template != rules.SliderTemplate(4) {
		t.Errorf("change_both changes the shared flow: %+v", f.Template)
	}
	code, body = post(t, srv, "PUT", path, `{"quality":1,"sharedFlowChoice":"make_copy"}`)
	na, _ := st.GetLibrary(a.ID)
	nb, _ := st.GetLibrary(b.ID)
	copy, _ := st.GetFlow(na.FlowID)
	shared, _ := st.GetFlow(nb.FlowID)
	if code != 200 || copy.Name != "Movies (own copy)" || copy.Template != rules.SliderTemplate(1) || shared.Template != rules.SliderTemplate(4) || nb.FlowID != a.FlowID {
		t.Errorf("make_copy: %d %s; copy %+v, shared %+v", code, body, copy, shared)
	}
}

func TestFlowStepsInPlainWords(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: fmt.Sprintf("/api/flows/%d", l.FlowID)})
	var f struct {
		Steps []struct {
			N                   int
			Kind, Label, Detail string
		}
	}
	json.Unmarshal([]byte(body), &f)
	if len(f.Steps) < 6 || f.Steps[0].N != 1 {
		t.Fatalf("%s", body)
	}
	var video string
	for _, s := range f.Steps {
		if strings.Contains(s.Label, "CRF") || strings.Contains(s.Label, "23") {
			t.Errorf("raw numbers only in detail: %q", s.Label)
		}
		if s.Kind == "video" {
			video = s.Label + " | " + s.Detail
		}
	}
	if !strings.Contains(video, "Balanced") || !strings.Contains(video, "CRF 23 at 1080p (lower = better quality)") {
		t.Errorf("video step: %q", video)
	}
	old, _ := st.AddLibrary("Old", t.TempDir(), rules.LegacyTemplate(rules.Default(), "beside"), true)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: fmt.Sprintf("/api/flows/%d", old.FlowID)})
	if !strings.Contains(body, "Convert only H264 video at 4000 kbps or more") || !strings.Contains(body, "keep the original") {
		t.Errorf("legacy steps: %s", body)
	}
}

func TestSettingEndpointsNeedSignIn(t *testing.T) {
	srv, _, _, _ := libServer(t, true)
	for _, k := range []call{
		{method: "GET", path: "/api/settings/slider"},
		{method: "GET", path: "/api/libraries/1/setting"},
		{method: "PUT", path: "/api/libraries/1/setting", body: `{"quality":3}`, ctype: "application/json"},
		{method: "POST", path: "/api/libraries/1/setting/replace-custom", body: "{}", ctype: "application/json"},
	} {
		if code, body, _ := do(t, nil, srv, k); code != 401 || errCode(body) != "auth" {
			t.Errorf("%s %s: %d %s", k.method, k.path, code, body)
		}
	}
}

var _ = jobs.ErrNotFound

// Until slice 25 saves the selectors, a body with any of them is refused and nothing is written, so a
// choice is never dropped while the page says "Saved".
func TestSelectorFieldsAreRefusedUntilTheyCanBeSaved(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	for _, f := range []string{`"speed":"fast"`, `"format":"hevc"`, `"resolution":"720p"`, `"maxBitrate":10`, `"audio":"keep_all"`,
		`"subtitles":"keep_all"`, `"fileType":"keep"`, `"afterConverting":"replace"`, `"ignore":[]`, `"skipHdr":true`,
		`"skipSmall":true`, `"minSavingPct":10`} {
		code, body := post(t, srv, "PUT", path, `{"quality":4,`+f+`}`)
		if code != 400 || !strings.Contains(body, `"not_available"`) || !strings.Contains(body, "Nothing was changed") {
			t.Errorf("%s: %d %s", f, code, body)
		}
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template != rules.SliderTemplate(3) || fe.scans != 0 {
		t.Errorf("nothing may be written: %+v, %d scans", f.Template, fe.scans)
	}
	if code, body := post(t, srv, "PUT", path, `{"quality":4,"kind":"slider"}`); code != 200 {
		t.Errorf("quality only still saves: %d %s", code, body)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template != rules.SliderTemplate(4) {
		t.Errorf("%+v", f.Template)
	}
}
