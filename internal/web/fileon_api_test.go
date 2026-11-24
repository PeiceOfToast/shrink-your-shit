// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// #694: the draft answer carries fileOn beside stepSummaries: a question after the save reads "saved" on
// its Yes and No, even with both unlinked, so the page can say the file is already saved.
func TestFileOnIsSentBesideTheStepSummaries(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	_, body := post(t, srv, "POST", "/api/v2/workflows", `{"name":"Worth it"}`)
	id := decode[map[string]any](t, body)["id"].(string)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id + "/draft"})
	d := decode[v2Rev](t, body)
	var save workflow.Node
	for _, n := range d.Workflow.Nodes {
		if n.Type == workflow.TypeCommit {
			save = n
		}
	}
	def, _ := workflow.Definition(workflow.TypeCheckResult)
	cmp := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeCheckResult, TypeVersion: def.TypeVersion, Name: "Worth it?", Enabled: true,
		Config: map[string]any{"test": "saved_at_least", "percent": 30.0}}
	d.Workflow.Nodes = append(d.Workflow.Nodes, cmp)
	d.Workflow.Edges = append(d.Workflow.Edges, workflow.Edge{ID: workflow.NewID(), From: save.ID, FromPort: "success", To: cmp.ID, ToPort: "in"})
	put, _ := json.Marshal(map[string]any{"workflow": d.Workflow, "layout": d.Layout})
	req, _ := http.NewRequest("PUT", srv.URL+"/api/v2/workflows/"+id+"/draft", strings.NewReader(string(put)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", d.Etag)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("save the draft: status %d", res.StatusCode)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id + "/draft"})
	var got struct {
		FileOn        map[string]map[string]string `json:"fileOn"`
		StepSummaries map[string]string            `json:"stepSummaries"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if _, ok := got.StepSummaries[cmp.ID]; !ok {
		t.Fatalf("the compare has no summary: %s", body)
	}
	if f := got.FileOn[cmp.ID]; len(f) != 2 || f["match"] != "saved" || f["else"] != "saved" {
		t.Errorf("fileOn for the compare after the save: %v, want match and else saved: %s", f, body)
	}
	if f := got.FileOn[save.ID]; f["success"] != "saved" {
		t.Errorf("fileOn for the save: %v", f)
	}
}
