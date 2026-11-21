// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W3 (workflow-architecture.md 2.2 rule 1): a library's own values for the variables its workflow lets
// a library change, read and saved through the library endpoint. Anything the workflow doesn't let a
// library change is refused with override_not_allowed; a value of the wrong kind with
// variable_wrong_type; a library with no workflow has nothing to change.

type libVarsView struct {
	Variables []workflow.Variable `json:"variables"`
}

func TestLibraryVariablesAPI(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	f, err := st.AddWorkflow("Tuned", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := st.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = []workflow.Variable{
		{Name: "q", Type: "number", Value: float64(1), TemplateParam: true},
		{Name: "keep", Type: "number", Value: float64(2)},
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
	if _, err := st.Publish(f.ID, saved.Etag, rules.SliderTemplate(1)); err != nil {
		t.Fatal(err)
	}
	lib, err := st.AddLibraryOnFlow("Movies", t.TempDir(), f.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/libraries/" + strconv.FormatInt(lib.ID, 10)

	if code, body := post(t, srv, "GET", path, ""); code != 200 {
		t.Fatalf("GET: %d %s", code, body)
	} else if got := decode[libVarsView](t, body).Variables; len(got) != 0 {
		t.Fatalf("a new library has values %+v, want none", got)
	}

	// its own value for the variable the workflow lets it change, with the workflow's kind
	code, body := post(t, srv, "PATCH", path, `{"variables":[{"name":"q","value":4}]}`)
	if code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	got := decode[libVarsView](t, body).Variables
	if len(got) != 1 || got[0].Name != "q" || got[0].Value != float64(4) || got[0].Type != "number" {
		t.Errorf("the view says %+v, want q = 4 as a number", got)
	}
	if tmpl, err := st.TemplateFor(jobs.Job{LibraryID: lib.ID}); err != nil {
		t.Error(err)
	} else if sel, ok := tmpl.SelectorsView(); !ok || sel.Quality != 4 {
		t.Errorf("the library's job would run quality %+v (a selectors template: %v), want its 4", sel.Quality, ok)
	}

	// a variable the workflow keeps is refused, and the saved value is unchanged
	code, body = post(t, srv, "PATCH", path, `{"variables":[{"name":"keep","value":7}]}`)
	if code != 400 || errCode(body) != "override_not_allowed" || !strings.Contains(body, "“keep”") || !strings.Contains(body, "Nothing was changed") {
		t.Errorf("a kept variable: %d %s", code, body)
	}
	if _, body := post(t, srv, "GET", path, ""); decode[libVarsView](t, body).Variables[0].Value != float64(4) {
		t.Errorf("the refused save changed the library: %s", body)
	}

	// a value that doesn't fit the workflow's kind
	if code, body := post(t, srv, "PATCH", path, `{"variables":[{"name":"q","value":"four"}]}`); code != 400 || errCode(body) != "variable_wrong_type" {
		t.Errorf("a text value for a number: %d %s", code, body)
	}

	// a library with no workflow has nothing a library may change
	later, err := st.AddLibraryWithoutFlow("Later", t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if code, body := post(t, srv, "PATCH", "/api/v2/libraries/"+strconv.FormatInt(later.ID, 10), `{"variables":[{"name":"q","value":4}]}`); code != 409 || errCode(body) != "no_workflow" {
		t.Errorf("a library with no workflow: %d %s", code, body)
	}

	// clearing them goes back to the workflow's own values
	code, body = post(t, srv, "PATCH", path, `{"variables":[]}`)
	if code != 200 || len(decode[libVarsView](t, body).Variables) != 0 {
		t.Errorf("clearing: %d %s", code, body)
	}
}
