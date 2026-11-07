// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
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

// The Standard selectors (slice 25a): saved and read back; at a slider position's values they are that
// position (kind derived); choices this version cannot do yet are refused and nothing is written.
func TestSelectorsSaveAndReadBack(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	path := fmt.Sprintf("/api/libraries/%d/setting", l.ID)
	get := func() map[string]any {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: path})
		var v map[string]any
		json.Unmarshal([]byte(body), &v)
		return v
	}
	// every answer, whatever its kind, says what can be saved (addenda 13, 14)
	for _, k := range []string{"availableFormats", "availableAudio", "availableSubtitles", "availableFileTypes"} {
		if get()[k] == nil {
			t.Errorf("slider kind: no %s", k)
		}
	}
	for f, want := range map[string]string{
		`"audio":"mp3"`:                          "invalid",
		`"audioBitrate":"128"`:                   "invalid", // no bitrate choice at Standard (25b)
		`"audio":"my_languages"`:                 "languages_needed",
		`"subtitles":"burn_in"`:                  "invalid",
		`"format":"av1"`:                         "not_available",
		`"fileType":"mp4"`:                       "not_available", // replace mode
		`"fileType":"mp4","afterConverting":"x"`: "invalid",
		`"speed":"fast"`:                         "invalid",
		`"resolution":"720p"`:                    "invalid",
		`"maxBitrate":"15"`:                      "invalid",
		`"maxBitrate":10`:                        "invalid",
		`"minSavingPct":95`:                      "invalid",
		`"ignore":["/etc"]`:                      "invalid",
		`"ignore":["../up"]`:                     "invalid",
		`"quality":9,"speed":"faster"`:           "invalid_quality",
	} {
		code, body := post(t, srv, "PUT", path, `{"quality":4,`+f+`}`)
		// every refusal says nothing changed, except languages_needed, which is design's sentence as it is
		if code != 400 || errCode(body) != want || (want != "languages_needed" && !strings.Contains(body, "Nothing was changed")) {
			t.Errorf("%s: %d %s, want 400 %s", f, code, body, want)
		}
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template != rules.SliderTemplate(3) || fe.scans != 0 {
		t.Errorf("nothing may be written: %+v, %d scans", f.Template, fe.scans)
	}
	// Standard left at Balanced's values reads back as the slider
	if code, body := post(t, srv, "PUT", path, `{"quality":3,"speed":"normal","format":"hevc","resolution":"keep","maxBitrate":"none","audio":"keep_all","subtitles":"keep_all","fileType":"keep","afterConverting":"replace","ignore":[],"skipHdr":true,"skipSmall":true,"minSavingPct":10,"order":"newest","kind":"selectors"}`); code != 200 {
		t.Fatalf("defaults: %d %s", code, body)
	}
	if v := get(); v["kind"] != "slider" || v["quality"] != 3.0 {
		t.Errorf("Balanced through the selectors should be the slider: %v", v)
	}
	// any other choice is a selectors setting, read back as saved; minSavingPct follows Quality when left out
	if code, body := post(t, srv, "PUT", path, `{"quality":2,"speed":"slower","resolution":"720","maxBitrate":"10","fileType":"mkv","afterConverting":"beside","ignore":["Extras","*.sample.mkv"],"skipSmall":false}`); code != 200 {
		t.Fatalf("selectors: %d %s", code, body)
	}
	v := get()
	for k, want := range map[string]any{"kind": "selectors", "quality": 2.0, "speed": "slower", "resolution": "720", "maxBitrate": "10",
		"fileType": "mkv", "afterConverting": "beside", "skipSmall": false, "skipHdr": true, "minSavingPct": 15.0} {
		if v[k] != want {
			t.Errorf("%s = %v, want %v", k, v[k], want)
		}
	}
	if ig, _ := v["ignore"].([]any); len(ig) != 2 {
		t.Errorf("ignore %v", v["ignore"])
	}
	if libs := listLibraries(t, srv); libs[0]["settingKind"] != "selectors" || libs[0]["settingLabel"] != "Smaller" {
		t.Errorf("library card: %v", libs[0])
	}
	// turning Skip HDR off says what that means
	post(t, srv, "PUT", path, `{"quality":2,"skipHdr":false}`)
	if w, _ := get()["warnings"].([]any); len(w) != 1 {
		t.Errorf("warnings %v", get()["warnings"])
	}
	if code, body := post(t, srv, "PUT", path, `{"quality":4,"kind":"slider"}`); code != 200 || get()["kind"] != "slider" {
		t.Errorf("quality only still saves the slider: %d %s", code, body)
	}
}

func listLibraries(t *testing.T, srv *httptest.Server) []map[string]any {
	t.Helper()
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
	var v struct{ Items []map[string]any }
	json.Unmarshal([]byte(body), &v)
	return v.Items
}

// The person's languages (Settings > General, slice 25b): stored in one form; "my languages" can be
// saved once there are some.
func TestLanguagesSetting(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"languages":["en","deu","fre","EN"]}`); code != 200 || !strings.Contains(body, `"languages":["eng","ger","fre"]`) {
		t.Errorf("%d %s", code, body)
	}
	if got := st.Languages(); strings.Join(got, ",") != "eng,ger,fre" {
		t.Errorf("store sees %v", got)
	}
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"languages":["xx"]}`); code != 400 || errCode(body) != "invalid_setting" {
		t.Errorf("bad code: %d %s", code, body)
	}
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if code, body := post(t, srv, "PUT", fmt.Sprintf("/api/libraries/%d/setting", l.ID), `{"quality":3,"audio":"my_languages","subtitles":"keep_all"}`); code != 200 {
		t.Errorf("my languages with languages set: %d %s", code, body)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template.Languages != "eng,ger,fre" {
		t.Errorf("the flow is read with the person's languages: %q", f.Template.Languages)
	}
}
