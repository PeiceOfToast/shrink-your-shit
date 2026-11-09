// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return v
}

type v2List struct {
	Items []map[string]any
	Total int
}

type v2Rev struct {
	ID            any
	Number        *int
	Status        string
	Workflow      workflow.Doc
	Layout        workflow.Layout
	Etag          string
	Stackable     bool
	StepSummaries map[string]string
	Validation    []workflow.Issue
}

// V1 (UI-API-CONTRACT-v2.md): a flow from before is a published workflow; a new workflow is a draft
// until published; drafts take etags; publishing writes what the engine runs; history and restore.
func TestV2Workflows(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	get := func(path string) (int, string) {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: path})
		return code, body
	}
	send := func(method, path, body, ifMatch string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var b strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := res.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return res.StatusCode, b.String()
	}
	flow := strconv.FormatInt(lib.FlowID, 10)

	// the registry and the template
	if _, body := get("/api/v2/step-definitions?fileType=video"); decode[v2List](t, body).Total != 9 || !strings.Contains(body, `"configSchema"`) {
		t.Errorf("step definitions: %s", body[:min(300, len(body))])
	}
	if _, body := get("/api/v2/templates"); !strings.Contains(body, `"shrink_videos"`) {
		t.Errorf("templates: %s", body)
	}

	// a flow from before: published, version 1, the preset's steps, used by the library
	_, body := get("/api/v2/workflows/" + flow)
	w := decode[map[string]any](t, body)
	if w["version"] != 1.0 || w["status"] != "enabled" || w["presetId"] != "shrink_videos" || w["draftDiffers"] != false ||
		!strings.Contains(body, `"name":"Movies"`) || w["changesOriginals"] != true || w["summaryTag"] == "" {
		t.Errorf("migrated workflow %s", body)
	}

	// a new workflow: a draft, whose default "next to each library" can't run yet
	code, body := send("POST", "/api/v2/workflows", `{"name":"Shrink TV","fileType":"video","templateId":"shrink_videos"}`, "")
	nw := decode[map[string]any](t, body)
	if code != 201 || nw["status"] != "draft" || nw["version"] != nil {
		t.Fatalf("create %d %s", code, body)
	}
	id := nw["id"].(string)
	_, body = get("/api/v2/workflows/" + id + "/draft")
	d := decode[v2Rev](t, body)
	if d.Status != "draft" || !d.Stackable || len(d.Validation) != 1 || d.Validation[0].Code != "not_runnable_yet" || d.StepSummaries[d.Workflow.Nodes[2].ID] == "" {
		t.Fatalf("draft %s", body)
	}
	if code, body := send("POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+d.Etag+`"}`, ""); code != 400 || errCode(body) != "has_errors" {
		t.Errorf("publish with errors: %d %s", code, body)
	}
	// the old pages don't list a never-published workflow
	if _, body := get("/api/flows"); strings.Contains(body, "Shrink TV") {
		t.Errorf("v1 lists a draft: %s", body)
	}
	// a system folder is refused on publish (#103's checks)
	d.Workflow.Nodes[7].Config = map[string]any{"policy": "separate_folder", "folderMode": "one_folder", "folder": jobs.SystemDirForTest()}
	put, _ := json.Marshal(map[string]any{"workflow": d.Workflow, "layout": d.Layout})
	code, body = send("PUT", "/api/v2/workflows/"+id+"/draft", string(put), d.Etag)
	d2 := decode[v2Rev](t, body)
	if code != 200 || len(d2.Validation) != 0 {
		t.Fatalf("save %d %s", code, body)
	}
	if code, body := send("PUT", "/api/v2/workflows/"+id+"/draft", string(put), d.Etag); code != 409 || errCode(body) != "stale_draft" {
		t.Errorf("stale save %d %s", code, body)
	}
	if code, body := send("POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+d2.Etag+`"}`, ""); code != 400 || errCode(body) != "system_folder" {
		t.Errorf("publish to a system folder: %d %s", code, body)
	}
	// a usable folder: published, enabled, and the engine runs it
	out := t.TempDir()
	d2.Workflow.Nodes[7].Config["folder"] = out
	d2.Layout.Nodes = map[string]workflow.NodeLayout{d2.Workflow.Nodes[0].ID: {X: 10, Y: 20}}
	put, _ = json.Marshal(map[string]any{"workflow": d2.Workflow, "layout": d2.Layout})
	_, body = send("PUT", "/api/v2/workflows/"+id+"/draft", string(put), d2.Etag)
	d3 := decode[v2Rev](t, body)
	if _, body := get("/api/v2/workflows/" + id + "/publish-preview"); !strings.Contains(body, `"nextNumber":1`) || !strings.Contains(body, `"kind":"added"`) {
		t.Errorf("preview %s", body)
	}
	code, body = send("POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+d3.Etag+`"}`, "")
	pub := decode[v2Rev](t, body)
	if code != 200 || pub.Status != "published" || pub.Number == nil || *pub.Number != 1 || pub.Layout.Nodes[d2.Workflow.Nodes[0].ID].X != 10 {
		t.Fatalf("publish %d %s", code, body)
	}
	n, _ := strconv.ParseInt(id, 10, 64)
	if f, _ := st.GetFlow(n); f.Template.Selectors == nil || f.Template.Selectors.AfterConverting != "folder" || f.Template.Selectors.OutputDir != out {
		t.Errorf("the engine runs %+v", f.Template.Selectors)
	}
	if _, body := get("/api/v2/workflows/" + id); !strings.Contains(body, `"status":"enabled"`) || !strings.Contains(body, `"version":1`) {
		t.Errorf("after publish %s", body)
	}

	// the old pages change the migrated flow: version 2 on the next read
	if code, body := send("PUT", "/api/flows/"+flow+"/setting", `{"quality":5}`, ""); code != 200 {
		t.Fatalf("v1 save %d %s", code, body)
	}
	if _, body := get("/api/v2/workflows/" + flow); !strings.Contains(body, `"version":2`) {
		t.Errorf("after a v1 save %s", body)
	}
	_, body = get("/api/v2/workflows/" + flow + "/revisions")
	revs := decode[v2List](t, body)
	if revs.Total != 2 || revs.Items[0]["number"] != 2.0 {
		t.Fatalf("history %s", body)
	}
	// restore version 1 into the draft
	r1 := revs.Items[1]["id"].(string)
	code, body = send("POST", "/api/v2/workflows/"+flow+"/revisions/"+r1+"/restore", ``, "")
	if r := decode[v2Rev](t, body); code != 200 || r.Status != "draft" || r.Workflow.Nodes[2].Config["quality"] != 3.0 {
		t.Errorf("restore %d %s", code, body)
	}
	if _, body := get("/api/v2/workflows/" + flow); !strings.Contains(body, `"draftDiffers":true`) {
		t.Errorf("draft differs %s", body)
	}

	// validate without saving: a cycle
	cyc := d3.Workflow
	cyc.Edges = append(cyc.Edges, workflow.Edge{ID: workflow.NewID(), From: cyc.Nodes[5].ID, FromPort: "success", To: cyc.Nodes[2].ID, ToPort: "in"})
	vb, _ := json.Marshal(map[string]any{"workflow": cyc})
	if _, body := send("POST", "/api/v2/workflows/"+id+"/validate", string(vb), ""); !strings.Contains(body, "cycle_without_loop") || !strings.Contains(body, `"stackable":false`) {
		t.Errorf("validate %s", body)
	}

	// meta, filters, sort and paging (the list convention)
	if code, body := send("PATCH", "/api/v2/workflows/"+id, `{"favourite":true,"tags":["tv"],"description":"For shows","status":"disabled"}`, ""); code != 200 ||
		!strings.Contains(body, `"favourite":true`) || !strings.Contains(body, `"status":"disabled"`) {
		t.Errorf("patch %d %s", code, body)
	}
	for q, want := range map[string]int{"": 2, "?favourite=true": 1, "?tag=tv": 1, "?status=enabled": 1, "?q=shows": 1, "?libraryId=" + strconv.FormatInt(lib.ID, 10): 1, "?limit=1&offset=1": 2} {
		_, body := get("/api/v2/workflows" + q)
		l := decode[v2List](t, body)
		if l.Total != want {
			t.Errorf("%q: total %d, want %d", q, l.Total, want)
		}
	}
	if _, body := get("/api/v2/workflows?sort=-name"); decode[v2List](t, body).Items[0]["name"] != "Shrink TV" {
		t.Errorf("sort %s", body)
	}
	if code, _ := get("/api/v2/workflows?limit=9999"); code != 400 {
		t.Errorf("limit 9999: %d", code)
	}
	if code, body := send("DELETE", "/api/v2/workflows/"+flow, ``, ""); code != 409 || errCode(body) != "in_use" {
		t.Errorf("delete in use %d %s", code, body)
	}
	if code, _ := send("DELETE", "/api/v2/workflows/"+id, ``, ""); code != 204 {
		t.Errorf("delete %d", code)
	}
}

// /api/v2 is behind the same sign-in as /api.
func TestV2NeedsSignIn(t *testing.T) {
	srv, _, _, _ := libServer(t, true)
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows"}); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
}

// Review acace8e: a never-published workflow has only a placeholder to run, so no library may use it
// and it can't be copied; publishing looks at the libraries' files again; an oversized body says so.
func TestV2ReviewFixes(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	draft, err := st.AddWorkflow("Unpublished", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	did := strconv.FormatInt(draft.ID, 10)
	for name, c := range map[string]call{
		"library's flow (v1)": {method: "PUT", path: "/api/libraries/" + strconv.FormatInt(lib.ID, 10) + "/flow", body: `{"flowId":"` + did + `"}`},
		"new library (v1)":    {method: "POST", path: "/api/libraries", body: `{"name":"Shows","path":` + strconv.Quote(t.TempDir()) + `,"flowId":"` + did + `"}`},
		"duplicate (v1)":      {method: "POST", path: "/api/flows/" + did + "/duplicate", body: `{}`},
		"duplicate (v2)":      {method: "POST", path: "/api/v2/workflows/" + did + "/duplicate", body: `{}`},
	} {
		c.ctype = "application/json"
		if code, body, _ := do(t, nil, srv, c); code != http.StatusConflict || errCode(body) != "workflow_not_published" {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	if l, _ := st.GetLibrary(lib.ID); l.FlowID != lib.FlowID {
		t.Errorf("the library moved to the placeholder: %d", l.FlowID)
	}
	// publish looks at the files again, as the old Save does
	flow := strconv.FormatInt(lib.FlowID, 10)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + flow + "/draft"})
	d := decode[v2Rev](t, body)
	d.Workflow.Nodes[2].Config["quality"] = 4
	put, _ := json.Marshal(map[string]any{"workflow": d.Workflow, "layout": d.Layout})
	req, _ := http.NewRequest("PUT", srv.URL+"/api/v2/workflows/"+flow+"/draft", strings.NewReader(string(put)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", d.Etag)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	saved := decode[v2Rev](t, readAll(t, res))
	before := fe.scans
	if code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workflows/" + flow + "/publish", ctype: "application/json", body: `{"etag":"` + saved.Etag + `"}`}); code != 200 || fe.scans != before+1 {
		t.Errorf("publish: %d scans %d→%d %s", code, before, fe.scans, body)
	}
	// a body over 64 KiB
	big := `{"name":"` + strings.Repeat("x", 70<<10) + `"}`
	if code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workflows", ctype: "application/json", body: big}); code != http.StatusRequestEntityTooLarge || errCode(body) != "too_large" {
		t.Errorf("too large: %d %s", code, body)
	}
}

func readAll(t *testing.T, res *http.Response) string {
	t.Helper()
	defer res.Body.Close()
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String()
		}
	}
}
