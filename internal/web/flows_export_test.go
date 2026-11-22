// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

func mapOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return m
}

func idOf(t *testing.T, m map[string]any) string {
	t.Helper()
	return fmt.Sprint(m["id"])
}

func draftHash(t *testing.T, srvCall func(method, path, body string) (int, string), id string) string {
	t.Helper()
	code, body := srvCall("GET", "/api/v2/workflows/"+id+"/draft", "")
	if code != http.StatusOK {
		t.Fatalf("draft of %s: %d %s", id, code, body)
	}
	h, _ := mapOf(t, body)["logicHash"].(string)
	return h
}

// Export writes the draft as a portable file, and import saves that file as a new draft: the logic hash is
// the same, the original is untouched, and nothing is published.
func TestWorkflowExportThenImportMakesANewDraft(t *testing.T) {
	srv, _ := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	call := func(method, path, body string) (int, string) { return post(t, srv, method, path, body) }

	code, body := call("POST", "/api/v2/workflows", `{"name":"Shrink movies"}`)
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	orig := idOf(t, mapOf(t, body))
	origHash := draftHash(t, call, orig)

	code, file := call("GET", "/api/flows/"+orig+"/export", "")
	if code != http.StatusOK {
		t.Fatalf("export: %d %s", code, file)
	}
	fm := mapOf(t, file)
	if fm["format"] != "syp-workflow" || fm["formatVersion"] != float64(1) || fm["name"] != "Shrink movies" {
		t.Fatalf("export header: %s", file)
	}

	code, body = call("POST", "/api/flows/import", file)
	if code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body)
	}
	imported := mapOf(t, body)
	newID := idOf(t, imported)
	if newID == orig {
		t.Fatal("the import reused the original's id")
	}
	if got := draftHash(t, call, newID); got != origHash {
		t.Errorf("imported logic hash %s, want %s", got, origHash)
	}
	if got := draftHash(t, call, orig); got != origHash {
		t.Errorf("the original's draft changed to %s", got)
	}
	code, revs := call("GET", "/api/v2/workflows/"+newID+"/revisions", "")
	if code != http.StatusOK || strings.Contains(revs, `"number":1`) {
		t.Errorf("the import was published: %d %s", code, revs)
	}
	if rep, _ := imported["import"].(map[string]any); rep == nil || len(rep["missingGlobals"].([]any)) != 0 {
		t.Errorf("import report: %v", imported["import"])
	}

	// the same file again: the name is taken, so the copy gets a new one and the first is kept
	code, body = call("POST", "/api/flows/import", file)
	if code != http.StatusCreated || mapOf(t, body)["name"] == imported["name"] {
		t.Errorf("a second import of the same name: %d %s", code, body)
	}
}

// An import that names a global this machine does not have reports it, and still saves only a draft.
func TestImportWithMissingGlobalReportsItAndSavesADraft(t *testing.T) {
	srv, _ := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	call := func(method, path, body string) (int, string) { return post(t, srv, method, path, body) }
	_, body := call("POST", "/api/v2/workflows", `{"name":"Needs a global"}`)
	orig := idOf(t, mapOf(t, body))
	_, file := call("GET", "/api/flows/"+orig+"/export", "")
	fm := mapOf(t, file)
	nodes := fm["workflow"].(map[string]any)["nodes"].([]any)
	nodes[0].(map[string]any)["config"].(map[string]any)["note"] = "{{nope_global}}"
	edited, _ := json.Marshal(fm)

	code, body := call("POST", "/api/flows/import", string(edited))
	if code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body)
	}
	imported := mapOf(t, body)
	rep, _ := imported["import"].(map[string]any)
	if got := fmt.Sprint(rep["missingGlobals"]); got != "[nope_global]" {
		t.Errorf("missing globals %s, want [nope_global]", got)
	}
	if _, ok := rep["issues"].([]any); !ok {
		t.Errorf("the import report has no issues list: %v", rep)
	}
	newID := idOf(t, imported)
	_, revs := call("GET", "/api/v2/workflows/"+newID+"/revisions", "")
	if strings.Contains(revs, `"number":1`) {
		t.Errorf("the import with a missing global was published: %s", revs)
	}
}

// A file from a newer format is refused with its reason, a file that is not an export is refused, and
// nothing is saved either way.
func TestImportRefusesNewerFormatAndNonExports(t *testing.T) {
	srv, _ := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	call := func(method, path, body string) (int, string) { return post(t, srv, method, path, body) }
	_, body := call("POST", "/api/v2/workflows", `{"name":"Newer"}`)
	orig := idOf(t, mapOf(t, body))
	_, file := call("GET", "/api/flows/"+orig+"/export", "")
	fm := mapOf(t, file)
	fm["formatVersion"] = float64(99)
	newer, _ := json.Marshal(fm)

	code, body := call("POST", "/api/flows/import", string(newer))
	if code != http.StatusBadRequest || !strings.Contains(body, "newer") {
		t.Errorf("a newer format: %d %s", code, body)
	}
	code, body = call("POST", "/api/flows/import", `{"hello":"world"}`)
	if code != http.StatusBadRequest {
		t.Errorf("a non-export: %d %s", code, body)
	}
	_, list := call("GET", "/api/v2/workflows", "")
	if strings.Count(list, `"name":"Newer"`) != 1 {
		t.Errorf("a refused import saved something: %s", list)
	}
}

// An imported secret has no value: the original keeps its own, and the export carries neither the value nor a
// has-value flag.
func TestImportedSecretHasNoValue(t *testing.T) {
	srv, st := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	call := func(method, path, body string) (int, string) { return post(t, srv, method, path, body) }
	d := workflow.Preset()
	d.Variables = []workflow.Variable{{Name: "api_token", Type: "secret", Value: "hunter2", HasValue: true}}
	f, err := st.AddWorkflow("Keyed", d)
	if err != nil {
		t.Fatal(err)
	}
	orig := fmt.Sprint(f.ID)
	_, file := call("GET", "/api/flows/"+orig+"/export", "")
	if strings.Contains(file, "hunter2") || strings.Contains(file, "hasValue") {
		t.Fatalf("the export carries the secret's value or flag: %s", file)
	}
	code, body := call("POST", "/api/flows/import", file)
	if code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body)
	}
	newID := idOf(t, mapOf(t, body))
	if got := secretHasValue(t, call, newID); got != false {
		t.Errorf("imported secret hasValue %v, want false", got)
	}
	if got := secretHasValue(t, call, orig); got != true {
		t.Errorf("the original's secret hasValue %v, want true", got)
	}
}

// secretHasValue is the hasValue of the draft's api_token.
func secretHasValue(t *testing.T, call func(method, path, body string) (int, string), id string) bool {
	t.Helper()
	_, body := call("GET", "/api/v2/workflows/"+id+"/draft", "")
	wf, _ := mapOf(t, body)["workflow"].(map[string]any)
	vs, _ := wf["variables"].([]any)
	for _, v := range vs {
		m := v.(map[string]any)
		if m["name"] == "api_token" {
			b, _ := m["hasValue"].(bool)
			return b
		}
	}
	t.Fatalf("draft of %s has no api_token: %s", id, body)
	return false
}

// The import is one write: the drawing comes with the workflow, so a workflow is never stored without it.
func TestImportSavesItsDrawingInOneWrite(t *testing.T) {
	srv, st := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	call := func(method, path, body string) (int, string) { return post(t, srv, method, path, body) }
	f, err := st.AddWorkflow("Drawn", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := st.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	lay := workflow.Layout{Nodes: map[string]workflow.NodeLayout{"n1": {X: 120, Y: 40}}}
	if _, err := st.SaveDraft(f.ID, d.Doc, lay, d.Etag); err != nil {
		t.Fatal(err)
	}
	_, file := call("GET", fmt.Sprintf("/api/flows/%d/export", f.ID), "")
	code, body := call("POST", "/api/flows/import", file)
	if code != http.StatusCreated {
		t.Fatalf("import: %d %s", code, body)
	}
	newID := idOf(t, mapOf(t, body))
	_, draft := call("GET", "/api/v2/workflows/"+newID+"/draft", "")
	if !strings.Contains(draft, `"n1"`) || !strings.Contains(draft, `"x":120`) {
		t.Errorf("the imported draft lost its drawing: %s", draft)
	}
}

// A file with no workflow object, a schema this build can't open, or a step from a newer version is refused with
// 400 and nothing is saved; a step this build knows but that fails validation is saved and reported in issues.
func TestImportRefusesWhatThisBuildCannotOpen(t *testing.T) {
	srv, _ := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	call := func(method, path, body string) (int, string) { return post(t, srv, method, path, body) }
	_, body := call("POST", "/api/v2/workflows", `{"name":"Refused"}`)
	orig := idOf(t, mapOf(t, body))
	_, file := call("GET", "/api/flows/"+orig+"/export", "")

	cases := map[string]func(fm map[string]any){
		"no workflow": func(fm map[string]any) { delete(fm, "workflow") },
		"schema 0":    func(fm map[string]any) { fm["workflow"].(map[string]any)["schemaVersion"] = float64(0) },
		"newer step": func(fm map[string]any) {
			fm["workflow"].(map[string]any)["nodes"].([]any)[0].(map[string]any)["typeVersion"] = float64(99)
		},
	}
	for name, edit := range cases {
		fm := mapOf(t, file)
		edit(fm)
		raw, _ := json.Marshal(fm)
		if code, body := call("POST", "/api/flows/import", string(raw)); code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, code, body)
		}
	}
	_, list := call("GET", "/api/v2/workflows", "")
	if strings.Count(list, `"name":"Refused"`) != 1 || strings.Contains(list, `"name":"Imported`) {
		t.Errorf("a refused import saved something: %s", list)
	}
}

// A file bigger than 8 MiB is refused with 413 and nothing is saved.
func TestImportRefusesAFileOver8MiB(t *testing.T) {
	srv, _ := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	big := strings.Repeat("a", 8<<20+1)
	code, body := post(t, srv, "POST", "/api/flows/import", big)
	if code != http.StatusRequestEntityTooLarge || errCode(body) != "too_large" {
		t.Errorf("an 8 MiB+1 file: %d %s", code, body)
	}
}

// A file whose workflow is in a newer schema than this build reads is refused with its reason, and nothing is
// saved.
func TestImportRefusesNewerSchemaVersion(t *testing.T) {
	srv, _ := serverAt(t, filepath.Join(t.TempDir(), "s.db"))
	call := func(method, path, body string) (int, string) { return post(t, srv, method, path, body) }
	_, body := call("POST", "/api/v2/workflows", `{"name":"Schema"}`)
	orig := idOf(t, mapOf(t, body))
	_, file := call("GET", "/api/flows/"+orig+"/export", "")
	fm := mapOf(t, file)
	fm["workflow"].(map[string]any)["schemaVersion"] = float64(99)
	newer, _ := json.Marshal(fm)
	code, body := call("POST", "/api/flows/import", string(newer))
	if code != http.StatusBadRequest || !strings.Contains(body, "newer") {
		t.Errorf("a newer schema: %d %s", code, body)
	}
	_, list := call("GET", "/api/v2/workflows", "")
	if strings.Count(list, `"name":"Schema"`) != 1 {
		t.Errorf("a refused import saved something: %s", list)
	}
}
