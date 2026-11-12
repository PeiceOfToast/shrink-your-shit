// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

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
	// V5a: the preset's Save step (next to each library) runs, so a new workflow has no errors
	if d.Status != "draft" || !d.Stackable || len(d.Validation) != 0 || d.StepSummaries[d.Workflow.Nodes[2].ID] == "" {
		t.Fatalf("draft %s", body)
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

// Contract v2 (ui-shop's Dashboard, 01:30Z): what .syp-trash holds and when it is freed; v1's settings,
// first-run progress among them, under /api/v2.
func TestV2TrashAndSettings(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/trash"}); body != `{"bytes":0,"files":0,"nextDeleteAt":null,"lastDeleteAt":null}`+"\n" {
		t.Errorf("empty trash: %s", body)
	}
	for i := range 2 { // their stored delete_on is far off: the dates follow the setting now, as Purge does (review d3d44e2)
		job, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: lib.Path + "/f" + strconv.Itoa(i) + ".mkv", SizeBefore: 1000})
		o, err := st.BeginSwap(jobs.Original{JobID: job, LibraryID: lib.ID, Path: lib.Path + "/f.mkv", Bytes: int64(1000 * (i + 1))})
		if err != nil {
			t.Fatal(err)
		}
		st.FinishSwap(o, time.Now().Add(400*24*time.Hour))
	}
	trash := func(days int) jobs.Trash {
		t.Helper()
		if code, body, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/settings", ctype: "application/json", body: `{"keepOriginalsDays":` + strconv.Itoa(days) + `}`}); code != 200 {
			t.Fatalf("setting %d: %d %s", days, code, body)
		}
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/trash"})
		return decode[jobs.Trash](t, body)
	}
	near := func(at *string, want time.Duration) bool {
		if at == nil {
			return false
		}
		got, err := time.Parse(time.RFC3339, *at)
		d := time.Until(got) - want
		return err == nil && d > -2*time.Minute && d < 2*time.Minute
	}
	if tr := trash(7); tr.Bytes != 3000 || tr.Files != 2 || !near(tr.NextDeleteAt, 7*24*time.Hour) || !near(tr.LastDeleteAt, 7*24*time.Hour) {
		t.Errorf("7 days: %+v %s %s", tr, deref(tr.NextDeleteAt), deref(tr.LastDeleteAt))
	}
	if tr := trash(1); !near(tr.NextDeleteAt, 24*time.Hour) {
		t.Errorf("1 day: %s", deref(tr.NextDeleteAt))
	}
	if tr := trash(0); !near(tr.NextDeleteAt, 0) { // 0 days: due at once (the swap deletes them)
		t.Errorf("0 days: %s", deref(tr.NextDeleteAt))
	}
	if code, body, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/settings", ctype: "application/json", body: `{"firstRunStep":3}`}); code != 200 || !strings.Contains(body, `"firstRunStep":3`) {
		t.Errorf("patch: %d %s", code, body)
	}
	_, v1, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	_, v2, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/settings"})
	if v1 != v2 || !strings.Contains(v2, `"firstRunDone"`) || !strings.Contains(v2, `"firstRunStep":3`) {
		t.Errorf("v2 settings differ from v1's:\n%s\n%s", v1, v2)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// Review acace8e (P2): a read-only step's template is the server's. A draft that changes it (any
// values: they'd reach the encoder) is refused at publish; the same settings, or an earlier revision's,
// still publish.
func TestV2LegacyTemplateIsTheServers(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Old", t.TempDir(), rules.LegacyTemplate(rules.Default(), "replace"), false)
	flow := strconv.FormatInt(lib.FlowID, 10)
	publish := func(edit func(tm map[string]any)) (int, string) {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + flow + "/draft"})
		d := decode[v2Rev](t, body) // its etag; the settings start from the published revision each time (a refused publish leaves its draft)
		pub, _, err := st.LatestRevision(lib.FlowID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(pub.Doc)
		d.Workflow = workflow.Doc{}
		json.Unmarshal(raw, &d.Workflow)
		if d.Workflow.Nodes[1].Type != workflow.TypeLegacy {
			t.Fatalf("not the legacy step: %+v", d.Workflow.Nodes)
		}
		tm := d.Workflow.Nodes[1].Config["template"].(map[string]any)
		edit(tm)
		put, _ := json.Marshal(map[string]any{"workflow": d.Workflow, "layout": d.Layout})
		req, _ := http.NewRequest("PUT", srv.URL+"/api/v2/workflows/"+flow+"/draft", strings.NewReader(string(put)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", d.Etag)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		saved := decode[v2Rev](t, readAll(t, res))
		code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workflows/" + flow + "/publish", ctype: "application/json", body: `{"etag":"` + saved.Etag + `"}`})
		return code, body
	}
	params := func(tm map[string]any) map[string]any { return tm["params"].(map[string]any) }
	for name, edit := range map[string]func(map[string]any){
		"selectors format bogus": func(tm map[string]any) {
			tm["kind"], tm["selectors"] = "selectors", map[string]any{"quality": 3, "format": "bogus", "speed": "normal"}
		},
		"selectors quality 99": func(tm map[string]any) {
			tm["kind"], tm["selectors"] = "selectors", map[string]any{"quality": 99, "format": "hevc", "speed": "normal"}
		},
		"legacy crf -5":         func(tm map[string]any) { params(tm)["crf"] = -5 },
		"legacy preset ../../x": func(tm map[string]any) { params(tm)["preset"] = "../../x" },
	} {
		if code, body := publish(edit); code != 400 || errCode(body) != "invalid_config" {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
	before, _ := st.GetFlow(lib.FlowID)
	if code, body := publish(func(map[string]any) {}); code != 200 { // unchanged: publishes
		t.Errorf("unchanged: %d %s", code, body)
	}
	if after, _ := st.GetFlow(lib.FlowID); !reflect.DeepEqual(after.Template, before.Template) {
		t.Errorf("the engine's template changed: %+v", after.Template)
	}
}

// Review 109: a never-published workflow's preset, tag and changesOriginals come from its draft.
func TestV2DraftWorkflowFields(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	_, body := post(t, srv, "POST", "/api/v2/workflows", `{"name":"Fresh"}`)
	w := decode[map[string]any](t, body)
	if w["presetId"] != "shrink_videos" || w["changesOriginals"] != false || w["summaryTag"] == "" { // next to each library runs (V5a): its tag
		t.Errorf("fresh draft %s", body)
	}
	id := w["id"].(string)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id + "/draft"})
	d := decode[v2Rev](t, body)
	d.Workflow.Nodes[7].Config = map[string]any{"policy": "replace"}
	put, _ := json.Marshal(map[string]any{"workflow": d.Workflow, "layout": d.Layout})
	req, _ := http.NewRequest("PUT", srv.URL+"/api/v2/workflows/"+id+"/draft", strings.NewReader(string(put)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", d.Etag)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id})
	w = decode[map[string]any](t, body)
	if w["changesOriginals"] != true || w["summaryTag"] == "" {
		t.Errorf("replace draft %s", body)
	}
}

// Review ae3a28f: one flow that can't be read is left out of the list (logged), so the Workflows page
// still lists the others; asked for itself, it's a clear error.
func TestV2ListSurvivesABadFlow(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	good, _ := st.AddLibrary("Good", t.TempDir(), rules.SliderTemplate(3), false)
	bad, _ := st.AddLibrary("Bad", t.TempDir(), rules.SliderTemplate(3), false)
	if err := st.CorruptFlowForTest(bad.FlowID); err != nil {
		t.Fatal(err)
	}
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows"})
	l := decode[v2List](t, body)
	if code != 200 || l.Total != 1 || l.Items[0]["id"] != strconv.FormatInt(good.FlowID, 10) {
		t.Errorf("list %d %s", code, body)
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + strconv.FormatInt(bad.FlowID, 10)}); code < 400 || !strings.Contains(body, `"message"`) {
		t.Errorf("the bad one: %d %s", code, body)
	}
}
