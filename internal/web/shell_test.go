// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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
		if name == "static/mock.js" { // server data: a flow step's detail may carry the raw number
			continue
		}
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
		if path.Ext(name) != ".js" || name == "static/mock.js" || strings.Contains(name, "flow") { // flows.js, flow.js, flowpick.js: shown only on Flows pages or with per-library choices on
			continue
		}
		if m := regexp.MustCompile(`"[^"\n]*"`).FindAllString(js, -1); m != nil {
			for _, lit := range m {
				if flow.MatchString(lit) && !strings.HasPrefix(lit, `"/api/`) && !strings.HasPrefix(lit, `"/assets/`) { // an address, not text on screen
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

// PKG-2: size budget, per page load, measured as the bytes the server sends a browser (gzip; ACCEPTANCE.md):
// CSS under 20 KB; the scripts a page loads, lazy ones included, under 30 KB in total. Sizes go through the
// /assets/ handler, so this fails if compression ever stops. mock.js is a stand-in for the server that goes
// away when the real endpoints land, so it is not counted. Every script a page names must be served, and
// every script must be used by some page.
func TestAssetSizes(t *testing.T) {
	h := Handler(nil, nil) // /assets/ never touches the store
	sent := func(name string) (raw, gz int) {
		req := httptest.NewRequest("GET", "/assets/"+name, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Errorf("a page loads %s, which is not served (%d)", name, rec.Code)
		}
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("PKG-2: %s is not sent gzipped", name)
		}
		b, _ := fs.ReadFile(static, "static/"+name)
		return len(b), rec.Body.Len()
	}
	cssRaw, css := sent("app.css")
	if css >= 20*1024 {
		t.Errorf("PKG-2: css sends %d bytes, limit 20 KB", css)
	}
	t.Logf("css: %d bytes raw, %d sent", cssRaw, css)
	used := map[string]bool{}
	for _, p := range pages {
		raw, total := 0, 0
		for _, js := range append(scripts(p.Kind), lazyScripts(p.Kind)...) {
			used[js] = true
			if js != "mock.js" {
				r, g := sent(js)
				raw, total = raw+r, total+g
			}
		}
		if total >= 30*1024 {
			t.Errorf("PKG-2: %s sends %d bytes of script, limit 30 KB", p.Path, total)
		}
		t.Logf("%-24s script %6d bytes raw, %5d sent", p.Path, raw, total) // CI's report
	}
	fs.WalkDir(static, "static", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && path.Ext(p) == ".js" && !used[path.Base(p)] {
			t.Errorf("%s is not loaded by any page", p)
		}
		return nil
	})
}

// Assets go gzipped only to a browser that asks for it, and unzip to exactly the embedded file.
func TestAssetsGzip(t *testing.T) {
	h := Handler(nil, nil)
	want, _ := fs.ReadFile(static, "static/app.js")
	for _, c := range []struct{ accept, enc string }{{"", ""}, {"identity", ""}, {"gzip;q=0", ""}, {"gzip, deflate, br", "gzip"}, {"br;q=1.0, gzip;q=0.8", "gzip"}} {
		for _, method := range []string{"GET", "HEAD"} {
			req := httptest.NewRequest(method, "/assets/app.js", nil)
			if c.accept != "" {
				req.Header.Set("Accept-Encoding", c.accept)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			hd := rec.Header()
			if rec.Code != 200 || hd.Get("Content-Encoding") != c.enc || !strings.Contains(hd.Get("Vary"), "Accept-Encoding") || !strings.HasPrefix(hd.Get("Content-Type"), "text/javascript") {
				t.Errorf("%s %q: %d, encoding %q, vary %q, type %q", method, c.accept, rec.Code, hd.Get("Content-Encoding"), hd.Get("Vary"), hd.Get("Content-Type"))
			}
			if method == "HEAD" {
				if rec.Body.Len() != 0 {
					t.Errorf("HEAD %q sent a body", c.accept)
				}
				continue
			}
			body := rec.Body.Bytes()
			if c.enc == "gzip" {
				zr, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, _ = io.ReadAll(zr)
			}
			if !bytes.Equal(body, want) {
				t.Errorf("GET %q: body differs from the embedded app.js", c.accept)
			}
		}
	}
	for _, url := range []string{"/assets/", "/assets/nope.js"} {
		req := httptest.NewRequest("GET", url, nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 404 {
			t.Errorf("%s with gzip: %d, want 404", url, rec.Code)
		}
	}
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

// NAV-2: the settings registry gives every setting exactly one home. It fails on a setting listed twice,
// on one the contract does not have, and on a contract setting with no home at all.
func TestSettingsRegistry(t *testing.T) {
	b, err := fs.ReadFile(static, "static/settings.json")
	if err != nil {
		t.Fatal(err)
	}
	var reg []struct {
		Key, Tab, Type, Label string
		Options               []struct{ Value, Label string }
	}
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	// GET /api/settings fields (UI-API-CONTRACT.md); "token" has its own endpoint.
	contract := []string{"controlLevel", "perLibraryFlow", "firstRunDone", "firstRunStep", "authRequired", "keepOriginalsDays", "trashPath",
		"parallelJobs", "quietHours", "lowPriority", "scanSchedule", "integrations", "updateAvailable"}
	known := map[string]bool{"token": true}
	for _, k := range contract {
		known[k] = true
	}
	tabs := map[string]bool{"general": true, "safety": true, "performance": true, "integrations": true, "system": true, "first-run": true}
	types := map[string]bool{"radio": true, "checkbox": true, "number": true, "time": true, "text": true, "secret": true, "readout": true, "token": true, "info": true}
	seen, homes := map[string]bool{}, map[string]bool{}
	for _, e := range reg {
		if seen[e.Key] {
			t.Errorf("NAV-2: %s is listed twice", e.Key)
		}
		seen[e.Key] = true
		top := strings.SplitN(e.Key, ".", 2)[0]
		homes[top] = true
		if !known[top] {
			t.Errorf("NAV-2: %s is not a setting in the contract", e.Key)
		}
		if !tabs[e.Tab] || !types[e.Type] {
			t.Errorf("NAV-2: %s has an unknown tab %q or type %q", e.Key, e.Tab, e.Type)
		}
		if e.Type == "radio" && len(e.Options) == 0 {
			t.Errorf("NAV-2: %s has no options", e.Key)
		} else if e.Type != "info" && e.Type != "radio" && e.Label == "" {
			t.Errorf("NAV-2: %s has no label", e.Key)
		}
	}
	for _, k := range contract {
		if !homes[k] {
			t.Errorf("NAV-2: contract setting %s has no home in the registry", k)
		}
	}
	// NAV-7: nothing the registry says uses the word "flow".
	if m := regexp.MustCompile(`(?i)\bflows?\b`).FindString(string(b)); m != "" {
		t.Errorf("NAV-7: %q in settings.json", m)
	}
}
