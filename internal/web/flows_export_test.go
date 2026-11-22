// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
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
