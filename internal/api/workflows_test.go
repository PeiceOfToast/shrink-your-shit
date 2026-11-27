// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// apiFlow is the starter flow under a name, with the video step's quality changed.
func apiFlow(t *testing.T, name string, quality int) flow.Flow {
	t.Helper()
	s, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	f := s.Flow
	f.Name = name
	settings, err := json.Marshal(map[string]any{
		"format": "hevc", "quality": quality, "useGpu": false, "ifGpuFails": "cpu",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.Steps[0].Settings = settings
	return f
}

// apiQualityOf reads the video step's quality out of a flow the api served.
func apiQualityOf(t *testing.T, f flow.Flow) int {
	t.Helper()
	var set steps.VideoSettings
	if err := json.Unmarshal(f.Steps[0].Settings, &set); err != nil {
		t.Fatalf("read the video settings: %v", err)
	}
	return set.Quality
}

// workflowRow is WorkflowRow as the api serves it.
type workflowRow struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        string `json:"kind"`
	UsedBy      []struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"usedBy"`
	UpdatedAt    string `json:"updatedAt"`
	State        string `json:"state"`
	ProblemCount int    `json:"problemCount"`
	MadeWith     string `json:"madeWith"`
	FitsGuided   bool   `json:"fitsGuided"`
}

func listWorkflowRows(t *testing.T, e *env, cookie *http.Cookie) []workflowRow {
	t.Helper()
	var rows []workflowRow
	get(t, e, cookie, "/api/v2/workflows", &rows)
	return rows
}

// rowNamed finds one row of the list by id, failing when it is not there.
func rowNamed(t *testing.T, rows []workflowRow, id string) workflowRow {
	t.Helper()
	for _, row := range rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("no row %s in %+v", id, rows)
	return workflowRow{}
}

// The editor's whole loop over the real store: create, read, a stale save, publish (blocked once,
// then not), a second publish, the history, restore, duplicate, the list, and delete refused while
// a library uses the workflow and allowed once it does not.
func TestWorkflowLifecycle(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()

	// Create: the flow becomes a draft, nothing is published.
	rec := e.do(http.MethodPost, "/api/v2/workflows",
		mustJSON(t, map[string]any{"name": "Holidays", "flow": apiFlow(t, "Holidays", 3)}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var made struct {
		ID  string `json:"id"`
		Rev int    `json:"rev"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil || made.ID == "" || made.Rev != 1 {
		t.Fatalf("create = %+v, %v; want an id and rev 1", made, err)
	}
	id := made.ID

	var got struct {
		Flow       flow.Flow `json:"flow"`
		Rev        int       `json:"rev"`
		FitsGuided bool      `json:"fitsGuided"`
	}
	// The flow is the starter's with another quality, so the guide can say it (S2-4).
	get(t, e, cookie, "/api/v2/workflows/"+id, &got)
	if got.Rev != 1 || !got.FitsGuided || apiQualityOf(t, got.Flow) != 3 {
		t.Fatalf("get = rev %d quality %d fitsGuided %v; want the created draft", got.Rev, apiQualityOf(t, got.Flow), got.FitsGuided)
	}

	// A save from a page that read rev 0 is refused, and says which rev to reload.
	rec = e.do(http.MethodPut, "/api/v2/workflows/"+id+"/draft",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Holidays", 4), "baseRev": 0}),
		write(withSession(cookie)), false)
	code, _, _ := errorBody(t, rec)
	var stale struct {
		Rev int `json:"rev"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &stale); err != nil {
		t.Fatalf("stale save body: %v; %s", err, rec.Body)
	}
	if rec.Code != http.StatusConflict || code != "changed_since" || stale.Rev != 1 {
		t.Fatalf("stale save = %d %q rev %d, want 409 changed_since rev 1", rec.Code, code, stale.Rev)
	}

	// The save the page's own rev allows.
	rec = e.do(http.MethodPut, "/api/v2/workflows/"+id+"/draft",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Holidays", 4), "baseRev": 1}),
		write(withSession(cookie)), false)
	var saved struct {
		Rev int `json:"rev"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &saved) != nil || saved.Rev != 2 {
		t.Fatalf("save = %d %s, want rev 2", rec.Code, rec.Body)
	}

	// A flow with a problem is not published, and the page is told the first sentence.
	bad := apiFlow(t, "Holidays", 4)
	badSettings, err := json.Marshal(map[string]any{
		"format": "hevc", "quality": 4, "useGpu": false, "ifGpuFails": "cpu", "twoPass": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	bad.Steps[0].Settings = badSettings
	rec = e.do(http.MethodPost, "/api/v2/workflows/"+id+"/publish",
		mustJSON(t, map[string]any{"flow": bad}), write(withSession(cookie)), false)
	code, message, _ := errorBody(t, rec)
	if rec.Code != http.StatusConflict || code != "has_problems" || !strings.HasPrefix(message, "Fix this first: ") {
		t.Fatalf("blocked publish = %d %q %q", rec.Code, code, message)
	}
	if !strings.Contains(message, "Two passes") {
		t.Fatalf("blocked publish says %q; want the two-passes problem", message)
	}

	// The stored draft publishes as revision 1.
	rec = e.do(http.MethodPost, "/api/v2/workflows/"+id+"/publish", "", write(withSession(cookie)), false)
	var published struct {
		Rev       int  `json:"rev"`
		Published bool `json:"published"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &published) != nil ||
		published.Rev != 1 || !published.Published {
		t.Fatalf("publish = %d %s, want rev 1 published", rec.Code, rec.Body)
	}

	// Save the published flow on (the publish cleared the draft, so the page holds rev 3), then
	// publish the flow the editor sends; that is revision 2.
	rec = e.do(http.MethodPut, "/api/v2/workflows/"+id+"/draft",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Holidays", 5), "baseRev": 3}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(http.MethodPost, "/api/v2/workflows/"+id+"/publish",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Holidays", 4)}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &published) != nil || published.Rev != 2 {
		t.Fatalf("second publish = %d %s, want rev 2", rec.Code, rec.Body)
	}

	// The history, newest first.
	var revs []struct {
		Rev       int    `json:"rev"`
		CreatedAt string `json:"createdAt"`
	}
	get(t, e, cookie, "/api/v2/workflows/"+id+"/revisions", &revs)
	if len(revs) != 2 || revs[0].Rev != 2 || revs[1].Rev != 1 || revs[0].CreatedAt == "" {
		t.Fatalf("revisions = %+v; want 2 then 1", revs)
	}

	// Restoring revision 1 makes revision 3 with its flow, quality 5.
	rec = e.do(http.MethodPost, "/api/v2/workflows/"+id+"/revisions/1/restore", "", write(withSession(cookie)), false)
	var restored struct {
		Rev int `json:"rev"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &restored) != nil || restored.Rev != 3 {
		t.Fatalf("restore = %d %s, want rev 3", rec.Code, rec.Body)
	}
	get(t, e, cookie, "/api/v2/workflows/"+id, &got)
	if q := apiQualityOf(t, got.Flow); q != 4 {
		t.Fatalf("restored draft quality %d; want revision 1's 4", q)
	}

	// Duplicate copies the published flow under a new name, as a draft.
	rec = e.do(http.MethodPost, "/api/v2/workflows/"+id+"/duplicate", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("duplicate: %d %s", rec.Code, rec.Body)
	}
	var copyRow workflowRow
	if err := json.Unmarshal(rec.Body.Bytes(), &copyRow); err != nil {
		t.Fatalf("duplicate body: %v; %s", err, rec.Body)
	}
	if copyRow.ID == "" || copyRow.ID == id || copyRow.Name != "Holidays copy" || copyRow.State != "draft" ||
		copyRow.MadeWith != "editor" || len(copyRow.UsedBy) != 0 {
		t.Fatalf("duplicate = %+v; want a new draft named Holidays copy", copyRow)
	}

	// The list has both, and setup's starter row reads as guided.
	rows := listWorkflowRows(t, e, cookie)
	original := rowNamed(t, rows, id)
	if original.Name != "Holidays" || original.State != "published" || original.ProblemCount != 0 {
		t.Fatalf("original row = %+v; want published with no problems", original)
	}
	copyOf := rowNamed(t, rows, copyRow.ID)
	if copyOf.Name != "Holidays copy" || copyOf.State != "draft" {
		t.Fatalf("copy row = %+v", copyOf)
	}
	starter := rowNamed(t, rows, "1")
	if starter.MadeWith != "guided" || starter.Name != "Shrink videos" {
		t.Fatalf("setup's row = %+v; want the starter served as guided", starter)
	}

	// Delete: allowed for the original, refused while a library uses the copy, allowed after.
	rec = e.do(http.MethodDelete, "/api/v2/workflows/"+id, "", write(withSession(cookie)), false)
	var deleted struct {
		OK   bool   `json:"ok"`
		Code string `json:"code"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &deleted) != nil || !deleted.OK {
		t.Fatalf("delete = %d %s, want ok", rec.Code, rec.Body)
	}
	if rec := e.do(http.MethodGet, "/api/v2/workflows/"+id, "", withSession(cookie), false); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d, want 404", rec.Code)
	}
	copyID, err := strconv.ParseInt(copyRow.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := e.catalog.AddLibrary(ctx, library.NewLibrary{Name: "Clips", Path: t.TempDir(), WorkflowID: &copyID})
	if err != nil {
		t.Fatalf("add library: %v", err)
	}
	rec = e.do(http.MethodDelete, "/api/v2/workflows/"+copyRow.ID, "", write(withSession(cookie)), false)
	var refused struct {
		OK     bool   `json:"ok"`
		Code   string `json:"code"`
		UsedBy []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"usedBy"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &refused) != nil || refused.OK ||
		refused.Code != "in_use" || len(refused.UsedBy) != 1 || refused.UsedBy[0].Name != "Clips" ||
		refused.UsedBy[0].Type != "library" {
		t.Fatalf("delete in use = %d %s", rec.Code, rec.Body)
	}
	if err := e.catalog.DeleteLibrary(ctx, lib.ID); err != nil {
		t.Fatalf("delete the library: %v", err)
	}
	rec = e.do(http.MethodDelete, "/api/v2/workflows/"+copyRow.ID, "", write(withSession(cookie)), false)
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &deleted) != nil || !deleted.OK {
		t.Fatalf("delete after clearing the library = %d %s", rec.Code, rec.Body)
	}
	if rows := listWorkflowRows(t, e, cookie); len(rows) != 1 || rows[0].Name != "Shrink videos" {
		t.Fatalf("list after deletes = %+v; want setup's workflow only", rows)
	}
}

// The guided builder's answers (S2-4) make a workflow when they are answers the guide offers, and
// are refused on their field when they are not. A create with neither is still invalid_flow, and a
// flow that is not the three steps is refused.
func TestWorkflowCreateRefusals(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	rec := e.do(http.MethodPost, "/api/v2/workflows",
		mustJSON(t, map[string]any{"name": "Guided", "answers": map[string]any{"quality": 9}}),
		write(withSession(cookie)), false)
	if code, message, env := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_answer" ||
		env["field"] != "quality" || message != "Quality is 1 to 5." {
		t.Fatalf("bad answer = %d %q %v %q; want 400 invalid_answer on quality", rec.Code, code, env["field"], message)
	}
	// The buildable subset the engine can run today: copy the audio, every language, keep every
	// subtitle, next to the original.
	buildable := map[string]any{
		"format": "hevc", "quality": 5, "useGpu": false, "ifGpuFails": "cpu", "resolution": "keep",
		"slower": false, "audio": "as_is", "allLanguages": true, "languages": []string{},
		"languagesEverywhere": false, "subtitles": "keep_all", "subtitleLanguages": []string{},
		"fileType": "mkv", "includeDolbyVision": false, "original": "next_to_original", "folder": "",
	}
	rec = e.do(http.MethodPost, "/api/v2/workflows",
		mustJSON(t, map[string]any{"name": "Guided", "answers": buildable}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("answers create = %d %s; want 201", rec.Code, rec.Body)
	}
	var made struct {
		ID  string `json:"id"`
		Rev int    `json:"rev"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil || made.ID == "" || made.Rev != 1 {
		t.Fatalf("answers create = %+v, %v; want an id and rev 1", made, err)
	}
	var row workflowRow
	for _, r := range listWorkflowRows(t, e, cookie) {
		if r.ID == made.ID {
			row = r
		}
	}
	if row.ID == "" || row.MadeWith != "guided" || !row.FitsGuided {
		t.Fatalf("guided create row = %+v; want a guided workflow that fits the guide", row)
	}

	rec = e.do(http.MethodPost, "/api/v2/workflows",
		mustJSON(t, map[string]any{"name": "No flow"}), write(withSession(cookie)), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_flow" {
		t.Fatalf("flowless create = %d %q; want 400 invalid_flow", rec.Code, code)
	}
	oneStep := flow.Flow{Name: "One", Steps: apiFlow(t, "One", 3).Steps[:1]}
	rec = e.do(http.MethodPost, "/api/v2/workflows",
		mustJSON(t, map[string]any{"name": "One", "flow": oneStep}), write(withSession(cookie)), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_flow" {
		t.Fatalf("one-step create = %d %q; want 400 invalid_flow", rec.Code, code)
	}

	// A draft save without baseRev cannot tell a stale page from a current one.
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/draft",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Shrink videos", 3)}), write(withSession(cookie)), false)
	code, _, env := errorBody(t, rec)
	if rec.Code != http.StatusBadRequest || code != "invalid_request" || env["field"] != "baseRev" {
		t.Fatalf("save without baseRev = %d %q %v", rec.Code, code, env["field"])
	}

	// Every route says so plainly for a workflow that is not there.
	for _, path := range []string{"/api/v2/workflows/99", "/api/v2/workflows/99/revisions"} {
		rec := e.do(http.MethodGet, path, "", withSession(cookie), false)
		if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "workflow_not_found" {
			t.Fatalf("GET %s = %d %q; want 404 workflow_not_found", path, rec.Code, code)
		}
	}
	rec = e.do(http.MethodPost, "/api/v2/workflows/99/revisions/1/restore", "", write(withSession(cookie)), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "workflow_not_found" {
		t.Fatalf("restore of a missing workflow = %d %q", rec.Code, code)
	}
}
