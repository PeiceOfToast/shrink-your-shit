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

func TestFlows(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	movies, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	st.AddLibrary("Old", t.TempDir(), rules.LegacyTemplate(rules.Default(), "replace"), true) // as an upgrade makes it

	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/flows"})
	var list struct{ Items []flowItem }
	json.Unmarshal([]byte(body), &list)
	if !strings.Contains(body, `"total":2`) || !strings.Contains(body, `"id":"1"`) {
		t.Errorf("want total and string ids (UI-API-CONTRACT.md): %s", body)
	}
	if code != 200 || len(list.Items) != 2 || list.Items[0].Name != "Movies" || !slices.Equal(list.Items[0].UsedBy, []string{"Movies"}) ||
		list.Items[0].MadeWith != "slider" || list.Items[1].MadeWith != "legacy" || list.Items[0].ChangedAt == "" {
		t.Fatalf("list: %d %s", code, body)
	}
	var f flowItem
	code, body = post(t, srv, "POST", "/api/flows", `{"name":" Shared "}`)
	json.Unmarshal([]byte(body), &f)
	if code != 201 || f.Name != "Shared" || len(f.UsedBy) != 0 || f.MadeWith != "slider" {
		t.Fatalf("create: %d %s", code, body)
	}
	if code, body = post(t, srv, "POST", "/api/flows", `{"name":"Movies"}`); code != 409 || errCode(body) != "flow_name_taken" {
		t.Errorf("duplicate name: %d %s", code, body)
	}
	if code, body = post(t, srv, "POST", "/api/flows", `{"name":""}`); code != 201 || !strings.Contains(body, `"name":"Basic flow"`) {
		t.Errorf("no name: the kind's default name (control level per flow): %d %s", code, body)
	}
	id := fmt.Sprint(f.ID)
	if code, body = post(t, srv, "PATCH", "/api/flows/"+id, `{"name":"Everything"}`); code != 200 || !json.Valid([]byte(body)) {
		t.Errorf("rename: %d %s", code, body)
	}
	code, body = post(t, srv, "POST", "/api/flows/"+id+"/duplicate", "{}")
	var dup flowItem
	json.Unmarshal([]byte(body), &dup)
	if code != 201 || dup.Name != "Everything (copy)" || dup.ID == f.ID {
		t.Errorf("duplicate: %d %s", code, body)
	}
	code, body = post(t, srv, "POST", "/api/flows/"+id+"/duplicate", "{}")
	json.Unmarshal([]byte(body), &dup)
	if dup.Name != "Everything (copy) 2" {
		t.Errorf("second duplicate: %s", body)
	}
	// in use: 409 with usedBy; unused: deleted
	code, body = post(t, srv, "DELETE", fmt.Sprintf("/api/flows/%d", movies.FlowID), "")
	var e struct {
		Error struct {
			Code   string
			UsedBy []string
		}
	}
	json.Unmarshal([]byte(body), &e)
	if code != 409 || e.Error.Code != "flow_in_use" || !slices.Equal(e.Error.UsedBy, []string{"Movies"}) {
		t.Errorf("delete in use: %d %s", code, body)
	}
	if code, _ = post(t, srv, "DELETE", "/api/flows/"+id, ""); code != 200 {
		t.Errorf("delete unused: %d", code)
	}
	if code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/flows/" + id}); code != 404 || errCode(body) != "not_found" {
		t.Errorf("after delete: %d %s", code, body)
	}
	if code, body, _ = do(t, nil, srv, call{method: "GET", path: fmt.Sprintf("/api/flows/%d", movies.FlowID)}); code != 200 || !json.Valid([]byte(body)) {
		t.Errorf("get: %d %s", code, body)
	}
	for _, k := range []call{{method: "POST", path: "/api/flows/import", body: "{}", ctype: "application/json"}, {method: "GET", path: "/api/flows/1/export"}} {
		if code, body, _ := do(t, nil, srv, k); code != 501 || errCode(body) != "not_available" {
			t.Errorf("%s: %d %s", k.path, code, body)
		}
	}
}

// Each library picks a flow (control level per flow): no "a flow per library" setting is needed.
func TestChoosingALibrarysFlow(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	a, _ := st.AddLibrary("A", t.TempDir(), rules.SliderTemplate(3), false)
	b, _ := st.AddLibrary("B", t.TempDir(), rules.SliderTemplate(1), false)
	path := fmt.Sprintf("/api/libraries/%d/flow", a.ID)
	body := fmt.Sprintf(`{"flowId":"%d"}`, b.FlowID) // the page sends a string
	if code, resp := post(t, srv, "PUT", path, body); code != 200 {
		t.Fatalf("choose: %d %s", code, resp)
	}
	if l, _ := st.GetLibrary(a.ID); l.FlowID != b.FlowID {
		t.Errorf("library A uses flow %d, want %d", l.FlowID, b.FlowID)
	}
	if fe.scans != 1 {
		t.Errorf("a new flow must wake the scanner so the library's files are looked at again (scans %d)", fe.scans)
	}
	if code, resp := post(t, srv, "PUT", path, `{"flowId":999}`); code != 404 || errCode(resp) != "not_found" {
		t.Errorf("unknown flow: %d %s", code, resp)
	}
	if code, resp := post(t, srv, "PUT", "/api/libraries/999/flow", body); code != 404 {
		t.Errorf("unknown library: %d %s", code, resp)
	}
}

// Control level per flow (UI-API-CONTRACT.md, Workshop addition 20:40Z): flows have a kind and a tag, a
// new flow is made by kind, a flow's own setting is read and saved by the flow page, and a library can
// be added on an existing flow.
func TestFlowsPerLevel(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	movies, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(1), false)
	shows, _ := st.AddLibrary("Shows", t.TempDir(), rules.SliderTemplate(3), false)
	st.SetLibraryFlow(shows.ID, movies.FlowID) // two libraries on one flow
	legacy, _ := st.AddFlow("Old", rules.LegacyTemplate(rules.Default(), "replace"))
	var f flowItem
	// in order (the default names count up)
	for _, c := range [][2]string{
		{`{"kind":"basic"}`, "Basic flow|basic|Basic · Balanced"},
		{`{"kind":"basic"}`, "Basic flow 2|basic|Basic · Balanced"},
		{`{"kind":"standard"}`, "Standard flow|standard|Standard · Balanced · same resolution · no bitrate limit · Stereo AAC"},
		{`{"kind":"standard","name":"Mine"}`, "Mine|standard|Standard · Balanced · same resolution · no bitrate limit · Stereo AAC"},
	} {
		code, body := post(t, srv, "POST", "/api/flows", c[0])
		f = flowItem{}
		json.Unmarshal([]byte(body), &f)
		if got := f.Name + "|" + f.Kind + "|" + f.Tag; code != 201 || got != c[1] {
			t.Errorf("%s: %d %s, want %s", c[0], code, got, c[1])
		}
	}
	if code, body := post(t, srv, "POST", "/api/flows", `{"kind":"advanced"}`); code != 400 || errCode(body) != "invalid" {
		t.Errorf("advanced can't be made here: %d %s", code, body)
	}
	std := fmt.Sprint(f.ID) // "Mine"
	// the list: every flow's kind and tag; an earlier install's flow
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/flows"})
	if !strings.Contains(body, `"name":"Old"`) || !strings.Contains(body, `"kind":"earlier","tag":"Earlier install"`) {
		t.Errorf("list: %s", body)
	}
	// a flow's own setting: GET; a Standard flow says whether it fits Basic
	get := func(id string) map[string]any {
		_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/flows/" + id + "/setting"})
		var v map[string]any
		json.Unmarshal([]byte(b), &v)
		return v
	}
	if v := get(std); v["kind"] != "selectors" || v["fitsBasic"] != true { // S6's defaults are Basic's choices
		t.Errorf("standard flow setting: %v", v)
	}
	post(t, srv, "PUT", "/api/flows/"+std+"/setting", `{"quality":3,"fileType":"keep"}`)
	if v := get(std); v["fitsBasic"] != false {
		t.Errorf("keep file type: fitsBasic %v", v["fitsBasic"])
	}
	post(t, srv, "PUT", "/api/flows/"+std+"/setting", `{"quality":3,"fileType":"mkv"}`)
	if v := get(std); v["fitsBasic"] != true {
		t.Errorf("MKV and everything else as Basic: fitsBasic %v", v["fitsBasic"])
	}
	// a PUT changes the flow for every library on it, and its kind follows the body (Switch to Standard)
	mid := fmt.Sprint(movies.FlowID)
	if code, b := post(t, srv, "PUT", "/api/flows/"+mid+"/setting", `{"quality":2,"speed":"slower"}`); code != 200 || !strings.Contains(b, `"kind":"selectors"`) {
		t.Fatalf("switch to Standard: %d %s", code, b)
	}
	for _, id := range []int64{movies.ID, shows.ID} {
		l, _ := st.GetLibrary(id)
		if fl, _ := st.GetFlow(l.FlowID); fl.ID != movies.FlowID || fl.Template.Kind != rules.KindSelectors {
			t.Errorf("library %d: flow %d %s", id, fl.ID, fl.Template.Kind)
		}
	}
	if code, b := post(t, srv, "PUT", "/api/flows/"+mid+"/setting", `{"quality":4}`); code != 200 || !strings.Contains(b, `"kind":"slider"`) {
		t.Errorf("switch back to Basic: %d %s", code, b)
	}
	// read-only flows and unknown ones
	if code, b := post(t, srv, "PUT", fmt.Sprintf("/api/flows/%d/setting", legacy.ID), `{"quality":3}`); code != 409 || errCode(b) != "flow_read_only" {
		t.Errorf("earlier flow: %d %s", code, b)
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/flows/999/setting"}); code != 404 {
		t.Errorf("no such flow: %d", code)
	}
	// a library added on an existing flow uses it (no new flow); a bad flowId is refused
	n := len(must(st.Flows()))
	code, b := post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":"Docs","path":%q,"flowId":"%s"}`, t.TempDir(), std))
	if code != 201 || !strings.Contains(b, `"tag":"Standard · `) || len(must(st.Flows())) != n {
		t.Errorf("on a flow: %d %s", code, b)
	}
	if code, b := post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":"X","path":%q,"flowId":"999"}`, t.TempDir())); code != 400 || errCode(b) != "invalid_flow" {
		t.Errorf("bad flowId: %d %s", code, b)
	}
}

func must[T any](v T, _ error) T { return v }
