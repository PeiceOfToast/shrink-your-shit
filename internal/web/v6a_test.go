// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// V6a through the API: a workflow with a check publishes once every port leads somewhere; before that
// its draft names the port in the issue (code, node, port: the UI's quick fix). A job's detail has its
// path and each step's node and port.
func TestV6aBranchesAPI(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
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
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	_, body := send("POST", "/api/v2/workflows", `{"name":"By size"}`, "")
	wf := decode[map[string]any](t, body)["id"].(string)
	_, body = send("GET", "/api/v2/workflows/"+wf+"/draft", "", "")
	d := decode[v2Rev](t, body)
	// put "Is it 4K?" between the library's files and the rest; its No leads nowhere yet
	doc := d.Workflow
	check := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeCheck, TypeVersion: 1, Name: "Is it 4K?", Enabled: true, Config: map[string]any{"resolution": 2160}}
	doc.Nodes = append(doc.Nodes, check)
	for i, e := range doc.Edges {
		if e.From == doc.Nodes[0].ID {
			doc.Edges[i].To = check.ID
			doc.Edges = append(doc.Edges, workflow.Edge{ID: workflow.NewID(), From: check.ID, FromPort: "match", To: e.To, ToPort: "in"})
			break
		}
	}
	save := func(doc workflow.Doc, etag string) v2Rev {
		t.Helper()
		put, _ := json.Marshal(map[string]any{"workflow": doc, "layout": d.Layout})
		code, b := send("PUT", "/api/v2/workflows/"+wf+"/draft", string(put), etag)
		if code != 200 {
			t.Fatalf("save %d %s", code, b)
		}
		return decode[v2Rev](t, b)
	}
	d2 := save(doc, d.Etag)
	var found bool
	for _, i := range d2.Validation {
		if i.Code == "unconnected_port" && i.NodeID == check.ID && i.Port == "else" &&
			i.Message == "'Is it 4K?' has nothing connected to No. Connect a step, or leave the file alone there." {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues %+v", d2.Validation)
	}
	if code, b := send("POST", "/api/v2/workflows/"+wf+"/publish", `{"etag":"`+d2.Etag+`"}`, ""); code != 400 || errCode(b) != "has_errors" {
		t.Errorf("publish with a port leading nowhere: %d %s", code, b)
	}
	// the quick fix: "Leave the file alone" on No
	end := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeEnd, TypeVersion: 1, Enabled: true, Config: map[string]any{"reason": "Not 4K."}}
	doc.Nodes = append(doc.Nodes, end)
	doc.Edges = append(doc.Edges, workflow.Edge{ID: workflow.NewID(), From: check.ID, FromPort: "else", To: end.ID, ToPort: "in"})
	d3 := save(doc, d2.Etag)
	if len(d3.Validation) != 0 {
		t.Fatalf("still %+v", d3.Validation)
	}
	if code, b := send("POST", "/api/v2/workflows/"+wf+"/publish", `{"etag":"`+d3.Etag+`"}`, ""); code != 200 {
		t.Fatalf("publish %d %s", code, b)
	}

	// a job's detail: its path and the check's step, with node and port
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv")})
	st.AddDecision(id, check.ID, "match", "Is it 4K? → Yes (resolution is 2160p)")
	st.SetRoute(id, []string{doc.Nodes[0].ID, check.ID})
	_, body = send("GET", "/api/v2/jobs/"+strconv.FormatInt(id, 10), "", "")
	var job struct {
		Path  []string
		Steps []jobs.Step
	}
	json.Unmarshal([]byte(body), &job)
	if len(job.Path) != 2 || job.Path[1] != check.ID {
		t.Errorf("path %s", body)
	}
	var step *jobs.Step
	for i := range job.Steps {
		if job.Steps[i].Step == "branch" {
			step = &job.Steps[i]
		}
	}
	if step == nil || step.NodeID != check.ID || step.Port != "match" || step.Words != "Is it 4K? → Yes (resolution is 2160p)" {
		t.Errorf("steps %s", body)
	}
}
