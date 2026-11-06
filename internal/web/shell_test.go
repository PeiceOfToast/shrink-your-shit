// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

func labels(items []navItem) string {
	var l []string
	for _, n := range items {
		l = append(l, n.Label)
	}
	return strings.Join(l, ",")
}

// NAV-3 / NAV-4: the menu at each control level.
func TestNavByControlLevel(t *testing.T) {
	items := loadNav()
	seen := map[string]bool{}
	for _, n := range items {
		for _, k := range []string{n.ID, n.Href} {
			if seen[k] {
				t.Errorf("NAV-3: %q appears twice in nav.json", k)
			}
			seen[k] = true
		}
	}
	cases := []struct {
		level      string
		perLibrary bool
		want       string
	}{
		{"basic", false, "Libraries,Activity,Settings"},
		{"standard", false, "Libraries,Activity,Settings"},
		{"advanced", false, "Libraries,Activity,Flows,Settings"},
		{"basic", true, "Libraries,Activity,Flows,Settings"},
		{"standard", true, "Libraries,Activity,Flows,Settings"},
	}
	for _, c := range cases {
		if got := labels(navFor(items, c.level, c.perLibrary)); got != c.want {
			t.Errorf("NAV-4 level=%s perLibrary=%v: menu = %s, want %s", c.level, c.perLibrary, got, c.want)
		}
	}
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(Handler(st, nil))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

// Every section loads, has the skip link, a main landmark and the right menu with the current page marked.
func TestPagesServeWithShell(t *testing.T) {
	srv := newServer(t)
	for _, p := range pages {
		code, body, h := get(t, srv.URL+p.Path)
		if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/html") {
			t.Errorf("%s: %d %s", p.Path, code, h.Get("Content-Type"))
			continue
		}
		want := []string{`class="skip" href="#main"`, `<main id="main"`, `href="/assets/app.css"`}
		if p.Section != "flows" { // the sent menu is the Basic one; the browser adds Flows
			want = append(want, `aria-current="page"`)
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %s", p.Path, w)
			}
		}
	}
	if code, _, _ := get(t, srv.URL+"/nope/"); code != 404 {
		t.Errorf("unknown page: %d", code)
	}
	if code, _, _ := get(t, srv.URL+"/assets/"); code != 404 {
		t.Errorf("asset directory listing: %d", code)
	}
	if _, _, h := get(t, srv.URL+"/assets/app.css"); !strings.HasPrefix(h.Get("Content-Type"), "text/css") {
		t.Errorf("css type %q", h.Get("Content-Type"))
	}
	if code, _, _ := get(t, srv.URL+"/assets/nav.json"); code != 200 {
		t.Errorf("nav.json: %d", code)
	}
}

// The menu the server sends is the Basic one; every nav entry has a page.
func TestServedMenuIsBasicAndRoutesExist(t *testing.T) {
	items := loadNav()
	have := map[string]bool{}
	for _, p := range pages {
		have[p.Path] = true
	}
	for _, n := range items {
		if !have[n.Href] {
			t.Errorf("nav item %s has no page at %s", n.Label, n.Href)
		}
	}
	b, _ := render(items, pages[0])
	if strings.Contains(string(b), "Flows") {
		t.Error("the page sent for Basic mentions Flows")
	}
}

// Text of every embedded asset and template, plus every rendered page.
func allText(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, fsys := range []fs.FS{static, templates} {
		fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := fs.ReadFile(fsys, p)
				out[p] = string(b)
			}
			return nil
		})
	}
	return out
}

// COPY-1: banned words appear nowhere in what ships.
func TestCopyBannedWords(t *testing.T) {
	banned := regexp.MustCompile(`(?i)\b(node|nodes|runner|runners|plugin stack|formula|formulas|preset|presets|transcode|transcoding)\b`)
	texts := allText(t)
	for _, p := range pages {
		b, _ := render(loadNav(), p)
		texts["page "+p.Path] = string(b)
	}
	for name, s := range texts {
		if m := banned.FindString(s); m != "" {
			t.Errorf("COPY-1: %q in %s", m, name)
		}
	}
}

// NAV-7: at Basic and Standard the word "flow" is nowhere in the UI. Every page except the Flows page
// (not in the menu at those levels) is checked as sent, and so is the text the scripts put on screen.
func TestNoFlowWordOutsideFlows(t *testing.T) {
	flow := regexp.MustCompile(`(?i)\bflows?\b`)
	for _, p := range pages {
		if p.Section == "flows" {
			continue
		}
		b, _ := render(loadNav(), p)
		if m := flow.FindString(string(b)); m != "" {
			t.Errorf("NAV-7: %q on %s", m, p.Path)
		}
	}
	// Scripts: only the Flows menu entry (nav.json) and code names may use it; no string literal in app.js.
	js := allText(t)["static/app.js"]
	if m := flow.FindString(js); m != "" {
		t.Errorf("NAV-7: %q in app.js", m)
	}
}

// PKG-1: nothing points off the machine.
func TestNoExternalRequests(t *testing.T) {
	ext := regexp.MustCompile(`(?i)(https?:)?//[a-z0-9-]+\.[a-z]{2,}|@import|url\(\s*["']?https?:`)
	for name, s := range allText(t) {
		if path.Ext(name) == ".json" {
			continue
		}
		if m := ext.FindString(s); m != "" {
			t.Errorf("PKG-1: %q in %s", m, name)
		}
	}
}

// PKG-2: size budget. CSS under 20 KB, page scripts under 30 KB in total. mock.js is a stand-in for
// the server that goes away when the real endpoints land, so it is reported but not counted.
func TestAssetSizes(t *testing.T) {
	var css, js, mock int
	fs.WalkDir(static, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, _ := fs.ReadFile(static, p)
		switch path.Ext(p) {
		case ".css":
			css += len(b)
		case ".js":
			if path.Base(p) == "mock.js" {
				mock = len(b)
			} else {
				js += len(b)
			}
		}
		return nil
	})
	t.Logf("css %d bytes, js %d bytes (mock.js %d bytes not counted)", css, js, mock)
	if css >= 20*1024 {
		t.Errorf("PKG-2: css %d bytes, limit 20 KB", css)
	}
	if js >= 30*1024 {
		t.Errorf("PKG-2: js %d bytes, limit 30 KB", js)
	}
}
