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
	if code, body = post(t, srv, "POST", "/api/flows", `{"name":""}`); code != 400 || errCode(body) != "invalid_name" {
		t.Errorf("empty name: %d %s", code, body)
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

func TestChoosingALibrarysFlowNeedsPerLibraryFlows(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	a, _ := st.AddLibrary("A", t.TempDir(), rules.SliderTemplate(3), false)
	b, _ := st.AddLibrary("B", t.TempDir(), rules.SliderTemplate(1), false)
	path := fmt.Sprintf("/api/libraries/%d/flow", a.ID)
	body := fmt.Sprintf(`{"flowId":"%d"}`, b.FlowID) // the page sends a string
	if code, resp := post(t, srv, "PUT", path, body); code != 409 || errCode(resp) != "per_library_flow_off" {
		t.Fatalf("with the setting off: %d %s", code, resp)
	}
	post(t, srv, "PATCH", "/api/settings", `{"perLibraryFlow":true}`)
	if code, resp := post(t, srv, "PUT", path, body); code != 200 {
		t.Fatalf("with the setting on: %d %s", code, resp)
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
