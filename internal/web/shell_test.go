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
		if p.Section != "flows" && p.Kind != "wizard" { // the sent menu is the Basic one; the browser adds Flows
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
	// Design decision: no raw "quality N" / CRF number on screen at Basic or Standard.
	number := regexp.MustCompile(`(?i)\b(quality|crf) \d`)
	for name, s := range texts {
		if m := number.FindString(s); m != "" {
			t.Errorf("COPY: %q in %s (names and plain effects only)", m, name)
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
	// Scripts: only the Flows menu entry (nav.json) and code names may use it; no text a script shows.
	for name, js := range allText(t) {
		if path.Ext(name) != ".js" || name == "static/mock.js" {
			continue
		}
		if m := regexp.MustCompile(`"[^"\n]*"`).FindAllString(js, -1); m != nil {
			for _, lit := range m {
				if flow.MatchString(lit) && !strings.HasPrefix(lit, `"/api/`) { // address of a call, not text on screen
					t.Errorf("NAV-7: %s in %s", lit, name)
				}
			}
		}
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

// PKG-2: size budget, per page load. CSS under 20 KB; the scripts a page loads under 30 KB in total.
// mock.js is a stand-in for the server that goes away when the real endpoints land, so it is not counted.
// Every script a page names must exist, and every script must be used by some page.
func TestAssetSizes(t *testing.T) {
	size := func(name string) int {
		b, err := fs.ReadFile(static, "static/"+name)
		if err != nil {
			t.Errorf("a page loads %s, which is not embedded", name)
		}
		return len(b)
	}
	css := size("app.css")
	if css >= 20*1024 {
		t.Errorf("PKG-2: css %d bytes, limit 20 KB", css)
	}
	used := map[string]bool{}
	worst, worstPage := 0, ""
	for _, p := range pages {
		total := 0
		for _, js := range scripts(p.Kind) {
			used[js] = true
			if js != "mock.js" {
				total += size(js)
			}
		}
		if total > worst {
			worst, worstPage = total, p.Path
		}
		if total >= 30*1024 {
			t.Errorf("PKG-2: %s loads %d bytes of script, limit 30 KB", p.Path, total)
		}
	}
	fs.WalkDir(static, "static", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && path.Ext(p) == ".js" && !used[path.Base(p)] {
			t.Errorf("%s is not loaded by any page", p)
		}
		return nil
	})
	t.Logf("css %d bytes; largest page %s loads %d bytes of script (mock.js not counted)", css, worstPage, worst)
}

// First-run: a wizard with five steps and no menu in the top bar; every step has one primary button.
func TestFirstRunPage(t *testing.T) {
	srv := newServer(t)
	code, body, _ := get(t, srv.URL+"/welcome/")
	if code != 200 {
		t.Fatalf("/welcome/: %d", code)
	}
	if strings.Contains(body, `aria-label="Main"`) || strings.Contains(body, "main-nav") {
		t.Error("the wizard shows the menu")
	}
	for _, id := range []string{"s1", "s2", "s3", "s4", "s5"} {
		sec := body[strings.Index(body, `<section id="`+id+`"`):]
		sec = sec[:strings.Index(sec, "</section>")]
		if n := strings.Count(sec, "btn primary"); n != 1 {
			t.Errorf("step %s has %d primary buttons, want 1", id, n)
		}
		if !strings.Contains(sec, "<h1") {
			t.Errorf("step %s has no heading", id)
		}
	}
	if !strings.Contains(body, `aria-label="Setup progress"`) || !strings.Contains(body, "<noscript>") {
		t.Error("missing stepper or no-script note")
	}
}

// ACCEPTANCE: no screen has more than one primary button.
func TestOnePrimaryButtonPerPage(t *testing.T) {
	srv := newServer(t)
	for _, p := range pages {
		if p.Kind == "wizard" {
			continue // one per step, checked in TestFirstRunPage
		}
		_, body, _ := get(t, srv.URL+p.Path)
		if n := strings.Count(body, "btn primary"); n > 1 {
			t.Errorf("%s has %d primary buttons", p.Path, n)
		}
	}
}

// The five Activity pages share the header and tab row, and each loads the Activity script.
func TestActivityPages(t *testing.T) {
	srv := newServer(t)
	for _, p := range pages {
		if p.Section != "activity" {
			continue
		}
		_, body, _ := get(t, srv.URL+p.Path)
		for _, want := range []string{"<h1>Activity</h1>", `/assets/activity.js`, `aria-label="Activity sections"`, `id="pause"`, `href="/activity/kept/"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %s", p.Path, want)
			}
		}
		if strings.Count(body, `aria-current="page"`) != 2 { // the menu's Activity and the current tab
			t.Errorf("%s: want the menu and one tab marked current", p.Path)
		}
	}
}
