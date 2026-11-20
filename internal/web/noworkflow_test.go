// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library can be saved with no workflow (the user's "add a library, assign its workflow later"): the
// UI's own call (POST /api/libraries with flowId 0) saves it, nothing can start it, nothing is queued for
// it, and assigning a workflow does not start it either.
func TestLibraryWithoutWorkflow(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	send := func(method, path, body string) (int, string) {
		t.Helper()
		code, b, _ := do(t, nil, srv, call{method: method, path: path, body: body, ctype: "application/json"})
		return code, b
	}
	get := func(path string) string {
		t.Helper()
		code, b := send("GET", path, "")
		if code != 200 {
			t.Fatalf("GET %s: %d %s", path, code, b)
		}
		return b
	}
	dir := filepath.Join(t.TempDir(), "films")
	os.Mkdir(dir, 0o755)
	code, body := send("POST", "/api/libraries", `{"name":"Later","path":`+strconv.Quote(dir)+`,"flowId":"0"}`)
	made := decode[struct {
		ID, FlowID string
		Started    bool
	}](t, body)
	if code != 201 || made.FlowID != "0" || made.Started {
		t.Fatalf("add with flowId 0: %d %s", code, body)
	}
	id := made.ID
	path := "/api/v2/libraries/" + id
	if lib := decode[v2Library](t, get(path)); lib.WorkflowID != "" || lib.Status != "no_workflow" || lib.Enabled {
		t.Fatalf("item %+v: want workflowId \"\", status no_workflow, enabled false", lib)
	}

	// Start, on both the old and the new path, and with a name in the same body: refused, nothing changed
	const startSays = "This library has no workflow, so it can't be started. Assign a workflow, then start it. Nothing was changed."
	for _, c := range []struct{ method, path, body string }{
		{"POST", "/api/libraries/" + id + "/scan", `{}`},
		{"POST", path + "/scan", `{}`},
		{"PATCH", path, `{"enabled":true}`},
		{"PATCH", path, `{"name":"Films","enabled":true}`},
	} {
		if code, b := send(c.method, c.path, c.body); code != 409 || errCode(b) != "no_workflow" || !strings.Contains(b, startSays) {
			t.Errorf("%s %s: %d %s", c.method, c.path, code, b)
		}
	}
	if l, _ := st.GetLibrary(mustID(t, id)); l.Started || l.Name != "Later" {
		t.Errorf("a refused start changed it: %+v", l)
	}

	// a setting, a preview: there is no workflow to read or save under, said plainly rather than as a fault
	if code, b := send("GET", "/api/libraries/"+id+"/setting", ""); code != 409 || errCode(b) != "no_workflow" {
		t.Errorf("GET setting: %d %s", code, b)
	}
	if code, b := send("PUT", "/api/libraries/"+id+"/setting", `{"quality":3}`); code != 409 || errCode(b) != "no_workflow" {
		t.Errorf("PUT setting: %d %s", code, b)
	}
	if code, b := send("POST", "/api/libraries/"+id+"/preview", `{}`); code != 409 || errCode(b) != "no_workflow" {
		t.Errorf("preview: %d %s", code, b)
	}

	// the list's status filter: only the library with no workflow has it
	other := filepath.Join(t.TempDir(), "shows")
	os.Mkdir(other, 0o755)
	if code, b := send("POST", "/api/libraries", `{"name":"Shows","path":`+strconv.Quote(other)+`}`); code != 201 {
		t.Fatalf("a library with the default workflow: %d %s", code, b)
	}
	if b := get("/api/v2/libraries?status=no_workflow"); !strings.Contains(b, `"total":1`) || !strings.Contains(b, `"id":"`+id+`"`) {
		t.Errorf("?status=no_workflow: %s", b)
	}
	if b := get("/api/v2/libraries?status=disabled"); !strings.Contains(b, `"total":1`) || strings.Contains(b, `"id":"`+id+`"`) {
		t.Errorf("?status=disabled: %s", b)
	}
	// the overview's own state says it too (ui-shop's dashboard badge): no_workflow wins over paused, and a
	// stopped library that has a workflow still says paused
	overview := func() map[string]string {
		ov := decode[struct {
			Libraries []struct{ ID, State string }
		}](t, get("/api/v2/overview"))
		states := map[string]string{}
		for _, l := range ov.Libraries {
			states[l.ID] = l.State
		}
		return states
	}
	if s := overview()[id]; s != "no_workflow" {
		t.Errorf("the overview says the no-workflow library is %q, want no_workflow", s)
	}
	for lid, s := range overview() {
		if lid != id && s != "paused" {
			t.Errorf("a stopped library with a workflow is %q, want paused", s)
		}
	}

	// assigning a workflow: taken, never started, and nothing is queued
	flow, err := st.AddFlow("Films", rules.SliderTemplate(3))
	if err != nil {
		t.Fatal(err)
	}
	flowID := strconv.FormatInt(flow.ID, 10)
	if code, b := send("PUT", "/api/libraries/"+id+"/flow", `{"flowId":"`+flowID+`"}`); code != 200 {
		t.Fatalf("assign: %d %s", code, b)
	}
	if lib := decode[v2Library](t, get(path)); lib.WorkflowID != flowID || lib.Enabled || lib.Status != "disabled" {
		t.Errorf("after assigning: %+v, want the workflow, enabled false, disabled", lib)
	}
	if s := overview()[id]; s != "paused" { // it has a workflow and is stopped now: the ordinary stopped state
		t.Errorf("the overview says the assigned library is %q, want paused", s)
	}
	if l, _ := st.GetLibrary(mustID(t, id)); l.Started {
		t.Error("assigning a workflow started it")
	}
	if list, _ := st.List(10); len(list) != 0 {
		t.Errorf("assigning queued work: %+v", list)
	}

	// removing a workflow is not possible: refused on the library that has one, and nothing changed
	if code, b := send("PUT", "/api/libraries/"+id+"/flow", `{"flowId":"0"}`); code != 400 || errCode(b) != "workflow_required" {
		t.Errorf("removing: %d %s", code, b)
	}
	if code, b := send("PATCH", path, `{"workflowId":""}`); code != 400 || errCode(b) != "workflow_required" {
		t.Errorf("removing through v2: %d %s", code, b)
	}
	if l, _ := st.GetLibrary(mustID(t, id)); l.FlowID != flow.ID {
		t.Errorf("the refused removal changed its workflow: %+v", l)
	}
	// and now it can be started, since it has one
	if code, b := send("POST", path+"/scan", `{}`); code != 200 {
		t.Errorf("start with a workflow: %d %s", code, b)
	}
}

// v2's own create says the same: workflowId "" (or 0, or null) saves no workflow; absent keeps today's
// (one made from startFrom). Assigning one to a library that had none is answered, not refused.
func TestV2CreateLibraryWithoutWorkflow(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	send := func(method, path, body string) (int, string) {
		t.Helper()
		code, b, _ := do(t, nil, srv, call{method: method, path: path, body: body, ctype: "application/json"})
		return code, b
	}
	for i, wf := range []string{`"workflowId":""`, `"workflowId":"0"`, `"workflowId":0`, `"workflowId":null`} {
		dir := filepath.Join(t.TempDir(), "films")
		os.Mkdir(dir, 0o755)
		code, body := send("POST", "/api/v2/libraries", `{"name":"Later","paths":[`+strconv.Quote(dir)+`],`+wf+`}`)
		if lib := decode[v2Library](t, body); code != 201 || lib.WorkflowID != "" || lib.Status != "no_workflow" || lib.Enabled {
			t.Errorf("%d %s: %d %+v", i, wf, code, lib)
		}
	}
	dir := filepath.Join(t.TempDir(), "films")
	os.Mkdir(dir, 0o755)
	code, body := send("POST", "/api/v2/libraries", `{"name":"Default","paths":[`+strconv.Quote(dir)+`]}`)
	if lib := decode[v2Library](t, body); code != 201 || lib.WorkflowID == "" || !strings.Contains(body, `"workflowName":"Default"`) || lib.Status != "disabled" {
		t.Errorf("no workflowId: %d %+v, want today's workflow from startFrom", code, lib)
	}
	// assigning one through v2 to a library that had none
	dir = filepath.Join(t.TempDir(), "later")
	os.Mkdir(dir, 0o755)
	_, body = send("POST", "/api/v2/libraries", `{"name":"Later","paths":[`+strconv.Quote(dir)+`],"workflowId":""}`)
	lib := decode[v2Library](t, body)
	flow, err := st.AddFlow("Films", rules.SliderTemplate(3))
	if err != nil {
		t.Fatal(err)
	}
	flowID := strconv.FormatInt(flow.ID, 10)
	if code, b := send("PATCH", "/api/v2/libraries/"+lib.ID, `{"workflowId":"`+flowID+`"}`); code != 200 || !strings.Contains(b, `"workflowId":"`+flowID+`"`) {
		t.Errorf("assign through v2: %d %s", code, b)
	}
}

func mustID(t *testing.T, s string) int64 {
	t.Helper()
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("id %q: %v", s, err)
	}
	return id
}
