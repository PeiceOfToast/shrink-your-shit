// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Every /api answer with a body is labelled JSON, on success and on error: the page reads only
// answers labelled JSON (a 201 without the label left first-run without the new library's id). The
// routes are read from this package's source, so a new one is covered without editing this test.
func TestEveryAPIAnswerIsLabelledJSON(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.mkv"), []byte("x"), 0o644)
	st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)

	files, _ := filepath.Glob("*.go")
	route := regexp.MustCompile(`HandleFunc\("(GET|POST|PUT|PATCH|DELETE) (/api/[^"]+)"`)
	seen := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, _ := os.ReadFile(f)
		for _, m := range route.FindAllStringSubmatch(string(src), -1) {
			method, pattern := m[1], m[2]
			if pattern == "/api/logout" || pattern == "/api/settings/token/rotate" {
				continue // would end the test's session or change its token; answered by writeJSON like the rest
			}
			seen++
			for _, id := range []string{"1", "999", "x"} {
				path := regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(pattern, id)
				for _, body := range []string{"", "{}", "not json"} {
					if method == "GET" && body != "" {
						continue
					}
					c := call{method: method, path: path, body: body}
					if method != "GET" {
						c.ctype = "application/json"
					}
					code, got, h := do(t, nil, srv, c)
					if strings.TrimSpace(got) == "" {
						continue
					}
					if ct := h.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
						t.Errorf("%s %s %q: %d with Content-Type %q: %.80s", method, path, body, code, ct, got)
					}
				}
			}
		}
	}
	// Success paths that need a real body, among them the 201s (status set before the body).
	dir2 := t.TempDir()
	for _, c := range []call{
		{method: "POST", path: "/api/libraries", body: `{"name":"Shows","path":` + strconv.Quote(dir2) + `}`},
		{method: "POST", path: "/api/flows", body: `{"name":"Mine"}`},
		{method: "POST", path: "/api/flows/1/duplicate", body: `{}`},
	} {
		c.ctype = "application/json"
		code, got, h := do(t, nil, srv, c)
		if code >= 300 || !strings.HasPrefix(h.Header.Get("Content-Type"), "application/json") {
			t.Errorf("%s %s: %d with Content-Type %q: %.80s", c.method, c.path, code, h.Header.Get("Content-Type"), got)
		}
	}
	if seen < 25 {
		t.Errorf("only %d routes found in the source; the pattern no longer matches how routes are written", seen)
	}
}
