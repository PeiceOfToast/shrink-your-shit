// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// ui-shop 11:59Z: the old pages (v1) read a workflow saved through v2 with "next to each library", "one
// folder" or branches without breaking; the old editor's save, which can't say "next to each library",
// is refused with words saying so instead of changing it.
func TestV1ReadsV2Folders(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	sel := func(mode, dir string) rules.Template {
		s := rules.DefaultSelectors(3)
		s.AfterConverting, s.FolderMode, s.OutputDir = "folder", mode, dir
		return s.Template()
	}
	next, _ := st.AddFlow("Next to each library", sel(rules.NextToLibrary, ""))
	one, _ := st.AddFlow("One folder", sel("", t.TempDir()))
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if _, err := st.SetLibraryFlow(lib.ID, next.ID); err != nil {
		t.Fatal(err)
	}
	libPath := "/api/libraries/" + strconv.FormatInt(lib.ID, 10)
	for _, p := range []string{"/api/libraries", "/api/flows", libPath, libPath + "/setting",
		"/api/flows/" + strconv.FormatInt(next.ID, 10) + "/setting", "/api/flows/" + strconv.FormatInt(one.ID, 10) + "/setting"} {
		if code, body, _ := do(t, nil, srv, call{method: "GET", path: p}); code != 200 {
			t.Errorf("GET %s: %d %s", p, code, body)
		}
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: libPath + "/setting"}); !strings.Contains(body, `"folderMode":"next_to_library"`) {
		t.Errorf("the setting hides its folder mode: %s", body)
	}
	// up front: the old page shows it read-only with a link, rather than failing on Save
	editable := func(p string) (bool, string) {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: p})
		var v struct {
			EditableHere *bool
			Reason       *struct{ Code, Message string }
		}
		json.Unmarshal([]byte(body), &v)
		if v.EditableHere == nil {
			t.Fatalf("%s: no editableHere: %s", p, body)
		}
		code := ""
		if v.Reason != nil {
			code = v.Reason.Code
			if !strings.Contains(v.Reason.Message, "Edit it in the new UI") && code == "edit_in_new_ui" {
				t.Errorf("%s: words %q", p, v.Reason.Message)
			}
		}
		return *v.EditableHere, code
	}
	branches := rules.Template{Kind: rules.KindBranches, Branches: &[]rules.Branch{{End: "Left alone."}}}
	two, _ := st.AddFlow("Paths", branches)
	for p, want := range map[string]string{libPath + "/setting": "edit_in_new_ui", "/api/flows/" + strconv.FormatInt(next.ID, 10) + "/setting": "edit_in_new_ui",
		"/api/flows/" + strconv.FormatInt(two.ID, 10) + "/setting": "edit_in_new_ui", "/api/flows/" + strconv.FormatInt(one.ID, 10) + "/setting": ""} {
		if ok, code := editable(p); ok != (want == "") || code != want {
			t.Errorf("%s: editableHere %v %q, want %q", p, ok, code, want)
		}
	}
	// the old Standard editor's save: every choice, the folder for new files empty
	old := `{"quality":3,"speed":"slower","format":"hevc","resolution":"keep","maxBitrate":"none","audio":"stereo_aac","onlyMyLanguages":false,"subtitles":"keep_all","fileType":"mkv","afterConverting":"folder","outputDir":"","ignore":[],"includeDolbyVision":false}`
	for _, p := range []string{libPath + "/setting", "/api/flows/" + strconv.FormatInt(next.ID, 10) + "/setting"} {
		code, body, _ := do(t, nil, srv, call{method: "PUT", path: p, body: old, ctype: "application/json"})
		if code != 409 || errCode(body) != "edit_in_new_ui" || !strings.Contains(body, "next to each library") {
			t.Errorf("PUT %s: %d %s", p, code, body)
		}
	}
	if f, _ := st.GetFlow(next.ID); f.Template.Selectors == nil || f.Template.Selectors.FolderMode != rules.NextToLibrary || f.Template.Selectors.Speed != "normal" {
		t.Errorf("changed: %+v", f.Template.Selectors)
	}
	// whatever the body says (review ac8fde4 P2): the old editor's exact body choosing replace, to the flow
	// and to its library, and a stale old page saving after the new UI made it "next to each library"
	replace := strings.Replace(old, `"afterConverting":"folder","outputDir":""`, `"afterConverting":"replace"`, 1)
	for _, p := range []string{libPath + "/setting", "/api/flows/" + strconv.FormatInt(next.ID, 10) + "/setting"} {
		if code, body, _ := do(t, nil, srv, call{method: "PUT", path: p, body: replace, ctype: "application/json"}); code != 409 || errCode(body) != "edit_in_new_ui" {
			t.Errorf("replace %s: %d %s", p, code, body)
		}
	}
	if f, _ := st.GetFlow(next.ID); f.Template.Selectors == nil || f.Template.Selectors.AfterConverting != "folder" || f.Template.Selectors.FolderMode != rules.NextToLibrary {
		t.Errorf("changed: %+v", f.Template.Selectors)
	}
	stale, _ := st.AddFlow("Standard", rules.DefaultSelectors(3).Template()) // open in an old page...
	st.SetFlowTemplate(stale.ID, sel(rules.NextToLibrary, ""))               // ...made "next to each library" in the new UI
	if code, body, _ := do(t, nil, srv, call{method: "PUT", path: "/api/flows/" + strconv.FormatInt(stale.ID, 10) + "/setting", body: replace, ctype: "application/json"}); code != 409 || errCode(body) != "edit_in_new_ui" {
		t.Errorf("stale page: %d %s", code, body)
	}
	if f, _ := st.GetFlow(stale.ID); f.Template.Selectors == nil || f.Template.Selectors.FolderMode != rules.NextToLibrary {
		t.Errorf("stale page changed it: %+v", f.Template.Selectors)
	}
	// a flow the old page can show still saves
	if code, body, _ := do(t, nil, srv, call{method: "PUT", path: "/api/flows/" + strconv.FormatInt(one.ID, 10) + "/setting", body: replace, ctype: "application/json"}); code != 200 {
		t.Errorf("one folder: %d %s", code, body)
	}
}

// User review 11-12 item 1: the old editor can't show VBR, so a VBR workflow is read-only there.
func TestV1VBRIsReadOnly(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	s := rules.DefaultSelectors(3)
	s.MaxBitrate, s.RateControl, s.VBRMaxKbps = "medium", "vbr", 6000
	f, _ := st.AddFlow("VBR", s.Template())
	path := "/api/flows/" + strconv.FormatInt(f.ID, 10) + "/setting"
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: path}); !strings.Contains(body, `"editableHere":false`) || !strings.Contains(body, "uses VBR") {
		t.Errorf("%s", body)
	}
	body := `{"quality":3,"speed":"normal","format":"hevc","resolution":"keep","maxBitrate":"medium","audio":"stereo_aac","onlyMyLanguages":true,"subtitles":"keep_all","fileType":"mkv","afterConverting":"replace","ignore":[],"includeDolbyVision":false}`
	if code, b, _ := do(t, nil, srv, call{method: "PUT", path: path, body: body, ctype: "application/json"}); code != 409 || errCode(b) != "edit_in_new_ui" {
		t.Errorf("save: %d %s", code, b)
	}
}
