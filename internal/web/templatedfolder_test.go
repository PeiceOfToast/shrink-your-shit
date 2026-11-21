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

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// fixedPrefix: the folders before the first {{name}}; a setting without a template is itself; a
// setting that starts with a template has no fixed root.
func TestFixedPrefixOfATemplatedFolder(t *testing.T) {
	for in, want := range map[string]string{
		"/data/out":                 "/data/out",
		"/data/out/{{sub}}":         "/data/out",
		"/data/out/{{a}}/{{b}}":     "/data/out",
		"/data/out/films-{{year}}":  "/data/out",
		"/data/{{sub}}/out":         "/srv",
		"/{{sub}}":                 "",
		"{{sub}}/out":              "",
		"/data/out/{{sub}}/":        "/data/out",
		`C:\out\{{sub}}`:           `C:\out`,
		"/data/out/{{file.name}}.d": "/data/out",
		"/data/out/{{unclosed":      "/data/out",
		"/other/{{x}}/../../lib":   "/lib", // review-592: cleaned first (and refused before it gets here: hasDotDot)
		"/data/out/{{sub}}/../keep": "/data/out/keep",
		"/data/out/sub/../{{sub}}":  "/data/out",
	} {
		if got := fixedPrefix(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]bool{"/data/lib/{{a}}/../..": true, "/data/out/{{sub}}/../keep": true, `C:\out\..\{{sub}}`: true, "/data/out/{{sub}}/keep": false, "/data/out/..x/{{sub}}": false, "/data/out/{{sub}}": false} {
		if got := hasDotDot(in); got != want {
			t.Errorf("hasDotDot(%q) = %v", in, got)
		}
	}
}

// W3 (ui-shop's QA of #587): saving or publishing a workflow whose folder for new files holds a
// {{name}} never makes a folder with that text in its name; the fixed part before it is checked and
// made as an untemplated folder is today, and the rest is left to the job. On dev the literal
// "out/{{sub}}" folder appears at publish.
func TestPublishNeverMakesATemplatedFolder(t *testing.T) {
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
	// a setting that starts with a template has no fixed root: refused like any folder that isn't a full path;
	// a ".." after the template that lands in a library is refused as inside_library (review-592); one that
	// stays in an allowed folder passes
	lib := filepath.Join(root, "lib")
	os.MkdirAll(lib, 0o755)
	if _, err := st.AddLibrary("Lib", lib, rules.SliderTemplate(3), false); err != nil {
		t.Fatal(err)
	}
	// review-592 round 3: any ".." in a templated folder is refused (an empty value would let it climb past the
	// checked part: /srv/lib/{{a}}/../.. with a = "" is /), so the two ".." cases are refused for that reason
	dots := "can't use '..'"
	for folder, want := range map[string]string{"{{sub}}/out": "not_writable", root + "/other/{{sub}}/../../lib": dots, lib + "/{{sub}}/../..": dots, filepath.Join(root, "out", "{{sub}}", "keep"): ""} {
		_, body = send("GET", "/api/v2/workflows/"+id+"/draft", "", "") // the publish took the draft; a fresh one
		d3 := decode[v2Rev](t, body)
		for i := range doc.Nodes {
			if doc.Nodes[i].Type == workflow.TypeCommit {
				doc.Nodes[i].Config["folder"] = folder
			}
		}
		put, _ = json.Marshal(map[string]any{"workflow": doc, "layout": d.Layout})
		code, body = send("PUT", "/api/v2/workflows/"+id+"/draft", string(put), d3.Etag)
		d4 := decode[v2Rev](t, body)
		if code != 200 {
			t.Fatalf("save %q: %d %s", folder, code, body)
		}
		code, body = send("POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+d4.Etag+`"}`, "")
		switch {
		case want == "" && code != 200:
			t.Errorf("%q should publish: %d %s", folder, code, body)
		case want == dots && (code != 400 || errCode(body) != "not_writable" || !strings.Contains(body, dots)):
			t.Errorf("%q should be refused for its '..': %d %s", folder, code, body)
		case want != "" && want != dots && (code != 400 || errCode(body) != want):
			t.Errorf("%q should be refused as %s: %d %s", folder, want, code, body)
		}
	}
	for _, p := range []string{filepath.Join(root, "out"), lib} { // the fixed part is made; nothing climbed out of it
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "out", "keep")); err == nil {
		t.Error("the templated part is the job's to make, not the publish check's")
	}
}
