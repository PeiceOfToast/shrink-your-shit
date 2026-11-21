// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// fixedPrefix: the folders before the first {{name}}; a setting without a template is itself; a
// setting that starts with a template has no fixed root.
func TestFixedPrefixOfATemplatedFolder(t *testing.T) {
	for in, want := range map[string]string{
		"/srv/out":                 "/srv/out",
		"/srv/out/{{sub}}":         "/srv/out",
		"/srv/out/{{a}}/{{b}}":     "/srv/out",
		"/srv/out/films-{{year}}":  "/srv/out",
		"/srv/{{sub}}/out":         "/srv",
		"/{{sub}}":                 "",
		"{{sub}}/out":              "",
		"/srv/out/{{sub}}/":        "/srv/out",
		`C:\out\{{sub}}`:           `C:\out`,
		"/srv/out/{{file.name}}.d": "/srv/out",
		"/srv/out/{{unclosed":      "/srv/out",
	} {
		if got := fixedPrefix(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

// W3 (ui-shop's QA of #587): saving or publishing a workflow whose folder for new files holds a
// {{name}} never makes a folder with that text in its name; the fixed part before it is checked and
// made as an untemplated folder is today, and the rest is left to the job. On dev the literal
// "out/{{sub}}" folder appears at publish.
func TestPublishNeverMakesATemplatedFolder(t *testing.T) {
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
	root := t.TempDir()
	_, body := send("POST", "/api/v2/workflows", `{"name":"Templated folder"}`, "")
	id := decode[map[string]any](t, body)["id"].(string)
	_, body = send("GET", "/api/v2/workflows/"+id+"/draft", "", "")
	d := decode[v2Rev](t, body)
	doc := d.Workflow
	doc.Variables = []workflow.Variable{{Name: "sub", Type: "string", Value: "alpha"}}
	for i := range doc.Nodes {
		if doc.Nodes[i].Type == workflow.TypeCommit {
			doc.Nodes[i].Config = map[string]any{"policy": "separate_folder", "folderMode": "one_folder", "folder": filepath.Join(root, "out", "{{sub}}")}
		}
	}
	put, _ := json.Marshal(map[string]any{"workflow": doc, "layout": d.Layout})
	code, body := send("PUT", "/api/v2/workflows/"+id+"/draft", string(put), d.Etag)
	d2 := decode[v2Rev](t, body)
	if code != 200 || len(d2.Validation) != 0 {
		t.Fatalf("save %d %s", code, body)
	}
	if code, body := send("POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+d2.Etag+`"}`, ""); code != 200 {
		t.Fatalf("publish %d %s", code, body)
	}
	var made []string
	filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err == nil && e.IsDir() && p != root {
			made = append(made, strings.TrimPrefix(p, root))
		}
		return nil
	})
	for _, m := range made {
		if strings.Contains(m, "{{") {
			t.Errorf("a folder named after an unfilled template was made: %q", m)
		}
	}
	if st, err := os.Stat(filepath.Join(root, "out")); err != nil || !st.IsDir() { // the fixed part is checked and made, as an untemplated folder is
		t.Errorf("the fixed part should be made: %v", err)
	}
	// a setting that starts with a template has no fixed root: refused like any folder that isn't a full path
	_, body = send("GET", "/api/v2/workflows/"+id+"/draft", "", "") // the publish took the draft; a fresh one
	d3 := decode[v2Rev](t, body)
	for i := range doc.Nodes {
		if doc.Nodes[i].Type == workflow.TypeCommit {
			doc.Nodes[i].Config["folder"] = "{{sub}}/out"
		}
	}
	put, _ = json.Marshal(map[string]any{"workflow": doc, "layout": d.Layout})
	code, body = send("PUT", "/api/v2/workflows/"+id+"/draft", string(put), d3.Etag)
	d4 := decode[v2Rev](t, body)
	if code != 200 {
		t.Fatalf("save %d %s", code, body)
	}
	if code, body := send("POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+d4.Etag+`"}`, ""); code != 400 || errCode(body) != "not_writable" || !strings.Contains(body, "full path") {
		t.Errorf("a folder with no fixed root is refused at publish: %d %s", code, body)
	}
}
