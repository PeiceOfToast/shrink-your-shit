// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W2: the server's global variables are read and saved at /api/v2/workflow-variables. A workflow may
// name one in a typed setting; publishing it before the global exists is refused with the name, and
// saving it makes the same publish go through. A preview says what a save would touch without saving.

type v2Globals struct {
	Items   []workflow.Variable `json:"items"`
	Total   int                 `json:"total"`
	Saved   bool                `json:"saved"`
	Affects struct {
		Workflows []int64 `json:"workflows"`
		Libraries int     `json:"libraries"`
	} `json:"affects"`
}

type v2GlobalsPut struct {
	Variables []workflow.Variable `json:"variables"`
	Total     int                 `json:"total"`
	Saved     bool                `json:"saved"`
	Affects   struct {
		Workflows []int64 `json:"workflows"`
		Libraries int     `json:"libraries"`
	} `json:"affects"`
}

func TestGlobalVariablesAPI(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	f, err := st.AddWorkflow("Global", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := st.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range d.Doc.Nodes {
		if d.Doc.Nodes[i].Type == workflow.TypeConvert {
			d.Doc.Nodes[i].Config["quality"] = "{{q}}"
		}
	}
	saved, err := st.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag)
	if err != nil {
		t.Fatal(err)
	}
	id := strconv.FormatInt(f.ID, 10)
	publish := func() (int, string) {
		return post(t, srv, "POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+saved.Etag+`"}`)
	}

	if code, body := post(t, srv, "GET", "/api/v2/workflow-variables", ""); code != 200 {
		t.Fatalf("GET: %d %s", code, body)
	} else if g := decode[v2Globals](t, body); g.Total != 0 || len(g.Items) != 0 {
		t.Fatalf("GET before any save: %d %s", code, body)
	}
	if code, body := publish(); code != 400 || errCode(body) != "unknown_variable" {
		t.Fatalf("publishing {{q}} before the global exists: %d %s", code, body)
	}

	// saving q touches nothing yet: no published workflow names it
	code, body := post(t, srv, "PUT", "/api/v2/workflow-variables", `{"variables":[{"name":"q","type":"number","value":2}]}`)
	if code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	if g := decode[v2GlobalsPut](t, body); !g.Saved || g.Total != 1 || len(g.Affects.Workflows) != 0 {
		t.Errorf("save %+v, want saved and nothing affected", g)
	}
	_, body = post(t, srv, "GET", "/api/v2/workflow-variables", "")
	if g := decode[v2Globals](t, body); g.Total != 1 || g.Items[0].Name != "q" {
		t.Errorf("GET after the save: %s", body)
	}
	if code, body := publish(); code != 200 {
		t.Fatalf("publishing after the global exists: %d %s", code, body)
	}

	// a preview says which published workflow and how many libraries a change would touch, and saves
	// nothing
	if _, err := st.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, true); err != nil {
		t.Fatal(err)
	}
	code, body = post(t, srv, "PUT", "/api/v2/workflow-variables", `{"variables":[{"name":"q","type":"number","value":4}],"preview":true}`)
	if code != 200 {
		t.Fatalf("preview: %d %s", code, body)
	}
	if g := decode[v2GlobalsPut](t, body); g.Saved || g.Total != 1 || len(g.Affects.Workflows) != 1 || g.Affects.Workflows[0] != f.ID || g.Affects.Libraries != 1 {
		t.Errorf("preview %+v, want saved false, affects [%d] and 1 library", g, f.ID)
	}
	_, body = post(t, srv, "GET", "/api/v2/workflow-variables", "")
	if g := decode[v2Globals](t, body); g.Total != 1 || g.Items[0].Value != float64(2) {
		t.Errorf("the preview saved the variables: %s", body)
	}

	// a global that can't be one is refused, and the saved ones stay
	code, body = post(t, srv, "PUT", "/api/v2/workflow-variables", `{"variables":[{"name":"key","type":"secret","value":"s"}]}`)
	if code != 400 || errCode(body) != "invalid_variable_type" {
		t.Errorf("a secret global: %d %s", code, body)
	}
	_, body = post(t, srv, "GET", "/api/v2/workflow-variables", "")
	if g := decode[v2Globals](t, body); g.Total != 1 || g.Items[0].Name != "q" {
		t.Errorf("a refused save changed the saved ones: %s", body)
	}

	events, err := st.EventsAfter(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Kind == "workflow.variables.changed" {
			found = true
		}
	}
	if !found {
		t.Errorf("the save recorded no workflow.variables.changed event: %+v", events)
	}
}
