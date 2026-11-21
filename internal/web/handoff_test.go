// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// FB4b through the API: "Hand to another workflow" publishes only when its target is another, published
// workflow; a hand-over to this workflow, or to a draft, is an error on the step (handoff_loop,
// handoff_unpublished) in the draft's validation, in the validate call and at publish.
func TestHandToAnotherWorkflowAPI(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
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
	newWorkflow := func(name string) (string, v2Rev) {
		t.Helper()
		_, body := send("POST", "/api/v2/workflows", `{"name":"`+name+`"}`, "")
		id := decode[map[string]any](t, body)["id"].(string)
		_, body = send("GET", "/api/v2/workflows/"+id+"/draft", "", "")
		return id, decode[v2Rev](t, body)
	}
	target, td := newWorkflow("Big films") // a draft until it is published below
	wf, d := newWorkflow("Hands over")
	// Library files → Hand to another workflow (the preset's own steps are dropped)
	doc := d.Workflow
	goTo := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeGoto, TypeVersion: 1, Enabled: true, Config: map[string]any{"workflow": target}}
	doc.Nodes = []workflow.Node{doc.Nodes[0], goTo}
	doc.Edges = []workflow.Edge{{ID: workflow.NewID(), From: doc.Nodes[0].ID, FromPort: "success", To: goTo.ID, ToPort: "in"}}
	save := func(doc workflow.Doc, etag string) v2Rev {
		t.Helper()
		put, _ := json.Marshal(map[string]any{"workflow": doc, "layout": d.Layout})
		code, b := send("PUT", "/api/v2/workflows/"+wf+"/draft", string(put), etag)
		if code != 200 {
			t.Fatalf("save %d %s", code, b)
		}
		return decode[v2Rev](t, b)
	}
	issue := func(v v2Rev) string {
		for _, i := range v.Validation {
			if i.NodeID == goTo.ID {
				return i.Code + ": " + i.Message
			}
		}
		return ""
	}
	d2 := save(doc, d.Etag)
	if got := issue(d2); !strings.HasPrefix(got, "handoff_unpublished: ") || !strings.Contains(got, "'Big films', which hasn't been published yet") {
		t.Fatalf("a draft target: %q (%+v)", got, d2.Validation)
	}
	if code, b := send("POST", "/api/v2/workflows/"+wf+"/publish", `{"etag":"`+d2.Etag+`"}`, ""); code != 400 || errCode(b) != "has_errors" {
		t.Errorf("publish with a draft target: %d %s", code, b)
	}
	// the target is published: the hand-over is fine, and the step's summary names the workflow
	if code, b := send("POST", "/api/v2/workflows/"+target+"/publish", `{"etag":"`+td.Etag+`"}`, ""); code != 200 {
		t.Fatalf("publish the target: %d %s", code, b)
	}
	_, body := send("GET", "/api/v2/workflows/"+wf+"/draft", "", "")
	d3 := decode[v2Rev](t, body)
	if got := issue(d3); got != "" {
		t.Fatalf("a published target: %q", got)
	}
	var summaries struct {
		StepSummaries map[string]string
	}
	json.Unmarshal([]byte(body), &summaries)
	if summaries.StepSummaries[goTo.ID] != "Hands the file to Big films" {
		t.Errorf("summary %q", summaries.StepSummaries[goTo.ID])
	}
	if code, b := send("POST", "/api/v2/workflows/"+wf+"/publish", `{"etag":"`+d3.Etag+`"}`, ""); code != 200 {
		t.Fatalf("publish: %d %s", code, b)
	}
	// a hand-over back to this workflow is a loop, in the validate call too
	goTo.Config = map[string]any{"workflow": wf}
	doc.Nodes[1] = goTo
	put, _ := json.Marshal(map[string]any{"workflow": doc})
	_, body = send("POST", "/api/v2/workflows/"+wf+"/validate", string(put), "")
	var res struct{ Issues []workflow.Issue }
	json.Unmarshal([]byte(body), &res)
	found := ""
	for _, i := range res.Issues {
		if i.NodeID == goTo.ID {
			found = i.Code + ": " + i.Message
		}
	}
	if !strings.HasPrefix(found, "handoff_loop: ") || !strings.Contains(found, "hands the file back to this workflow") {
		t.Errorf("a hand-over to itself: %q", found)
	}
	// and through the other workflow: Big films now hands back here
	_, body = send("GET", "/api/v2/workflows/"+target+"/draft", "", "")
	td2 := decode[v2Rev](t, body)
	back := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeGoto, TypeVersion: 1, Enabled: true, Config: map[string]any{"workflow": wf}}
	tdoc := td2.Workflow
	tdoc.Nodes = []workflow.Node{tdoc.Nodes[0], back}
	tdoc.Edges = []workflow.Edge{{ID: workflow.NewID(), From: tdoc.Nodes[0].ID, FromPort: "success", To: back.ID, ToPort: "in"}}
	tput, _ := json.Marshal(map[string]any{"workflow": tdoc, "layout": td2.Layout})
	code, b := send("PUT", "/api/v2/workflows/"+target+"/draft", string(tput), td2.Etag)
	if code != 200 {
		t.Fatalf("save the target: %d %s", code, b)
	}
	tv := decode[v2Rev](t, b)
	for _, i := range tv.Validation {
		if i.NodeID == back.ID && i.Code == "handoff_loop" && strings.Contains(i.Message, "leads back to this workflow through 'Hands over'") {
			return
		}
	}
	t.Errorf("a loop through the other workflow: %+v", tv.Validation)
}

// review2-579 P3-3: the library preview of a saved setting that hands files to another workflow reads
// them as that workflow converts them, not "this setting does not convert it".
func TestPreviewOfAHandoffSaysWhatTheOtherWorkflowDoes(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	publish := func(name string, target int64) jobs.Flow {
		t.Helper()
		f, err := st.AddWorkflow(name, workflow.Preset())
		if err != nil {
			t.Fatal(err)
		}
		d, _, err := st.Draft(f.ID)
		if err != nil {
			t.Fatal(err)
		}
		tmpl := rules.SliderTemplate(3)
		if target != 0 {
			in := d.Doc.Nodes[0]
			g := workflow.Node{ID: workflow.NewID(), Type: workflow.TypeGoto, TypeVersion: 1, Enabled: true, Config: map[string]any{"workflow": strconv.FormatInt(target, 10)}}
			d.Doc.Nodes = []workflow.Node{in, g}
			d.Doc.Edges = []workflow.Edge{{ID: workflow.NewID(), From: in.ID, FromPort: "success", To: g.ID, ToPort: "in"}}
			var issues []workflow.Issue
			if tmpl, issues = workflow.ToTemplate(d.Doc, rules.Template{}); len(issues) > 0 {
				t.Fatalf("%+v", issues)
			}
		}
		saved, err := st.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Publish(f.ID, saved.Etag, tmpl); err != nil {
			t.Fatal(err)
		}
		return f
	}
	target := publish("Plain", 0)
	source := publish("Hands over", target.ID)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.mkv"), make([]byte, 1_000_000), 0o644)
	l, err := st.AddLibraryOnFlow("Movies", dir, source.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	code, b := post(t, srv, "POST", fmt.Sprintf("/api/libraries/%d/preview", l.ID), fmt.Sprintf(`{"sampleFile":%q}`, filepath.Join(dir, "a.mkv")))
	if code != 200 || strings.Contains(b, "does not convert it") || strings.Contains(b, "handed to another workflow") {
		t.Fatalf("%d %s", code, b)
	}
	var out previewOut
	json.Unmarshal([]byte(b), &out)
	if out.Sample == nil || len(out.Sample.Lines) == 0 || !strings.Contains(strings.Join(out.Sample.Lines, " "), "Video: converted") {
		t.Errorf("the sample should describe the other workflow's conversion: %s", b)
	}
}
