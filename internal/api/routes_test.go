// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// get sends a signed-in GET and decodes its 200 body into v.
func get(t *testing.T, e *env, cookie *http.Cookie, path string, v any) {
	t.Helper()
	rec := e.do(http.MethodGet, path, "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body)
	}
	if v != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
			t.Fatalf("GET %s: %v; body %s", path, err, rec.Body)
		}
	}
}

// The first-run page's calls answer before setup with no cookie, and need a session once setup is done.
func TestSetupAllowListBeforeAndAfter(t *testing.T) {
	e := newEnv(t, true)
	clips := t.TempDir()
	routes := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v2/languages", ""},
		{http.MethodGet, "/api/v2/workflows/starters", ""},
		{http.MethodGet, "/api/v2/workflows/starters/shrink-videos", ""},
		{http.MethodGet, "/api/v2/workflows/guide", ""},
		{http.MethodGet, "/api/v2/folders?path=" + clips, ""},
		{http.MethodPost, "/api/v2/libraries/check", `{"path":"` + clips + `"}`},
	}
	for _, r := range routes {
		rec := e.do(r.method, r.path, r.body, write(nil), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s before setup: %d, want 200 (%s)", r.method, r.path, rec.Code, rec.Body)
		}
	}

	cookie := e.setup()
	for _, r := range routes {
		rec := e.do(r.method, r.path, r.body, write(nil), false)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s after setup without a session: %d, want 401", r.method, r.path, rec.Code)
		}
		if code, _, _ := errorBody(t, rec); code != "signed_out" {
			t.Fatalf("%s %s: code %q, want signed_out", r.method, r.path, code)
		}
		rec = e.do(r.method, r.path, r.body, write(withSession(cookie)), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s signed in: %d %s", r.method, r.path, rec.Code, rec.Body)
		}
	}
}

// The starters, the languages and the guide are the flow package's own answers.
func TestStartersLanguagesAndGuide(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	var langs []struct{ Code, Name string }
	get(t, e, cookie, "/api/v2/languages", &langs)
	want := flow.Languages()
	if len(langs) != len(want) {
		t.Fatalf("%d languages, want %d", len(langs), len(want))
	}
	if langs[0].Code != "eng" || langs[0].Name != "English" {
		t.Fatalf("first language %+v, want eng English", langs[0])
	}

	var starters []struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Line   string `json:"line"`
		Icon   string `json:"icon"`
		Guided bool   `json:"guided"`
	}
	get(t, e, cookie, "/api/v2/workflows/starters", &starters)
	if len(starters) != 1 || starters[0].ID != "shrink-videos" || !starters[0].Guided || starters[0].Kind != "video" {
		t.Fatalf("starters %+v", starters)
	}

	var starter struct {
		ID      string         `json:"id"`
		Guided  bool           `json:"guided"`
		Answers map[string]any `json:"answers"`
	}
	get(t, e, cookie, "/api/v2/workflows/starters/shrink-videos", &starter)
	if starter.ID != "shrink-videos" || !starter.Guided || starter.Answers["format"] != "hevc" {
		t.Fatalf("starter %+v", starter)
	}
	if rec := e.do(http.MethodGet, "/api/v2/workflows/starters/nope", "", withSession(cookie), false); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown starter: %d, want 404", rec.Code)
	} else if code, _, _ := errorBody(t, rec); code != "not_found" {
		t.Fatalf("unknown starter: code %q, want not_found", code)
	}

	var guide struct {
		Chips     []struct{ ID string } `json:"chips"`
		Parts     []struct{ ID string } `json:"parts"`
		Defaults  map[string]any        `json:"defaults"`
		Languages []string              `json:"languages"`
	}
	get(t, e, cookie, "/api/v2/workflows/guide", &guide)
	if len(guide.Chips) == 0 || len(guide.Parts) != 5 || len(guide.Defaults) == 0 || len(guide.Languages) == 0 {
		t.Fatalf("guide: %d chips, %d parts, %d defaults, %d languages", len(guide.Chips), len(guide.Parts), len(guide.Defaults), len(guide.Languages))
	}
}

// The folder browser lists folders only, sorted, with their parent, and answers a missing folder 404.
func TestFolderBrowser(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	root := t.TempDir()
	for _, name := range []string{"zeta", "Alpha"} {
		if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "notes.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var listing struct {
		Path    string  `json:"path"`
		Parent  *string `json:"parent"`
		Folders []struct{ Name, Path string }
	}
	get(t, e, cookie, "/api/v2/folders?path="+root, &listing)
	if listing.Path != root || listing.Parent == nil || *listing.Parent != filepath.Dir(root) {
		t.Fatalf("listing %+v", listing)
	}
	if len(listing.Folders) != 2 || listing.Folders[0].Name != "Alpha" || listing.Folders[1].Name != "zeta" {
		t.Fatalf("folders %+v; want Alpha then zeta", listing.Folders)
	}
	if rec := e.do(http.MethodGet, "/api/v2/folders?path=/no/such/folder", "", withSession(cookie), false); rec.Code != http.StatusNotFound {
		t.Fatalf("missing folder: %d, want 404", rec.Code)
	}
	file := filepath.Join(root, "notes.mkv")
	rec := e.do(http.MethodGet, "/api/v2/folders?path="+file, "", withSession(cookie), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("a file: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "path" {
		t.Fatalf("a file: %v", env)
	}
}

// "Check this folder" counts the videos a scan would queue and reports what Add would refuse.
func TestCheckLibraryFolder(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.mkv"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "two.mp4"), []byte("123"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden", "three.mkv"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var check struct {
		Videos    int   `json:"videos"`
		SizeBytes int64 `json:"sizeBytes"`
		Problems  []struct{ Code, Message string }
	}
	rec := e.do(http.MethodPost, "/api/v2/libraries/check", `{"path":"`+root+`"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if check.Videos != 2 || check.SizeBytes != 8 || len(check.Problems) != 0 {
		t.Fatalf("check %+v; want 2 videos, 8 bytes, no problems", check)
	}

	if _, err := e.catalog.AddLibrary(context.Background(), library.NewLibrary{Name: "Clips", Path: root}); err != nil {
		t.Fatal(err)
	}
	rec = e.do(http.MethodPost, "/api/v2/libraries/check", `{"path":"`+root+`"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("watched check: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &check); err != nil {
		t.Fatal(err)
	}
	if len(check.Problems) != 1 || check.Problems[0].Code != "already_watched" || check.Problems[0].Message == "" {
		t.Fatalf("watched check %+v", check)
	}

	rec = e.do(http.MethodPost, "/api/v2/libraries/check", `{"path":"/no/such/folder"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing: %d, want 404", rec.Code)
	}
	rec = e.do(http.MethodPost, "/api/v2/libraries/check", `{"path":""}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty path: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "path" {
		t.Fatalf("empty path: %v", env)
	}
	rec = e.do(http.MethodPost, "/api/v2/libraries/check", `{"shareId":"s1"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("share: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "shareId" {
		t.Fatalf("share: %v", env)
	}
}

// Setup without a first library starts no scan, and says so.
func TestSetupWithoutALibraryStartsNoScan(t *testing.T) {
	e := newEnv(t, true)
	rec := e.do(http.MethodPost, "/api/v2/setup", setupNoLibrary, write(nil), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"scanStarted":false`)) {
		t.Fatalf("setup body %s; want scanStarted false", rec.Body)
	}
	if libs := e.scans.scanned(); len(libs) != 0 {
		t.Fatalf("scanned %v with no library", libs)
	}
}

// Every route behind the gate needs a session once setup is done.
func TestRoutesNeedASession(t *testing.T) {
	e := newEnv(t, true)
	e.setup()
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v2/libraries"},
		{http.MethodPost, "/api/v2/libraries"},
		{http.MethodGet, "/api/v2/libraries/1"},
		{http.MethodPost, "/api/v2/libraries/1/scan"},
		{http.MethodPost, "/api/v2/libraries/scan"},
		{http.MethodGet, "/api/v2/summary"},
		{http.MethodGet, "/api/v2/attention"},
	}
	for _, r := range routes {
		rec := e.do(r.method, r.path, `{}`, write(nil), false)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s without a session: %d, want 401", r.method, r.path, rec.Code)
		}
		if code, _, _ := errorBody(t, rec); code != "signed_out" {
			t.Fatalf("%s %s: code %q, want signed_out", r.method, r.path, code)
		}
	}
}

// A library row shows its folder, its watch, its queue counts and its workflow (none yet: needs_workflow).
func TestLibraryRoutes(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	clips := t.TempDir()

	var empty []map[string]any
	get(t, e, cookie, "/api/v2/libraries", &empty)
	if len(empty) != 0 {
		t.Fatalf("fresh libraries %v", empty)
	}

	rec := e.do(http.MethodPost, "/api/v2/libraries", `{"name":"Clips","path":"`+clips+`"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var added struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		Workflow *struct{ ID, Name string }
		Status   string `json:"status"`
		Watch    string `json:"watch"`
		Files    int    `json:"files"`
		Waiting  int    `json:"waiting"`
		Paused   bool   `json:"paused"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if added.ID != "1" || added.Name != "Clips" || added.Path != clips || added.Workflow != nil {
		t.Fatalf("added %+v", added)
	}
	if added.Status != "needs_workflow" || added.Watch != "daily" || added.Paused {
		t.Fatalf("added %+v; want needs_workflow, daily, not paused", added)
	}

	var list []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	get(t, e, cookie, "/api/v2/libraries", &list)
	if len(list) != 1 || list[0].ID != "1" || list[0].Path != clips {
		t.Fatalf("libraries %+v", list)
	}

	var one struct {
		ID     string  `json:"id"`
		Status string  `json:"status"`
		Watch  string  `json:"watch"`
		Next   *string `json:"nextScanAt"`
	}
	get(t, e, cookie, "/api/v2/libraries/1", &one)
	if one.ID != "1" || one.Next == nil {
		t.Fatalf("library %+v", one)
	}

	for _, path := range []string{"/api/v2/libraries/99", "/api/v2/libraries/abc"} {
		rec := e.do(http.MethodGet, path, "", withSession(cookie), false)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: %d, want 404", path, rec.Code)
		}
		if code, _, _ := errorBody(t, rec); code != "not_found" {
			t.Fatalf("GET %s: code %q, want not_found", path, code)
		}
	}

	rec = e.do(http.MethodPost, "/api/v2/libraries", `{"name":"Gone","path":"/no/such/folder"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad path: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "path" {
		t.Fatalf("bad path: %v", env)
	}
	rec = e.do(http.MethodPost, "/api/v2/libraries", `{"name":"Share","shareId":"s1"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("share: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "shareId" {
		t.Fatalf("share: %v", env)
	}
	rec = e.do(http.MethodPost, "/api/v2/libraries", `{"name":"Watch","path":"`+t.TempDir()+`","watch":"sometimes"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad watch: %d, want 400", rec.Code)
	}
	if code, _, env := errorBody(t, rec); code != "invalid_request" || env["field"] != "watch" {
		t.Fatalf("bad watch: %v", env)
	}

	// Waiting counts the library's queued jobs.
	fileID, err := e.catalog.SaveFile(context.Background(), library.File{
		LibraryID: 1, RelPath: "one.mkv", Size: 10, FileID: "dev-1",
	}, library.Queued, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.queue.Enqueue(context.Background(), fileID, 1, 7, filepath.Join(clips, "one.mkv"), "scan"); err != nil {
		t.Fatal(err)
	}
	var counted struct {
		Files   int `json:"files"`
		Waiting int `json:"waiting"`
	}
	get(t, e, cookie, "/api/v2/libraries/1", &counted)
	if counted.Files != 1 || counted.Waiting != 1 {
		t.Fatalf("counted %+v; want 1 file, 1 waiting", counted)
	}
}

// A scan starts through the scanner and answers at once; a paused library is skipped by Scan all.
func TestLibraryScanRoutes(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	clips := t.TempDir()
	if _, err := e.catalog.AddLibrary(context.Background(), library.NewLibrary{Name: "Clips", Path: clips}); err != nil {
		t.Fatal(err)
	}

	rec := e.do(http.MethodPost, "/api/v2/libraries/1/scan", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}
	if lib := e.nextScan(); lib.ID != 1 || lib.Path != clips {
		t.Fatalf("scanned %+v, want library 1", lib)
	}
	if len(e.scans.wakes) == 0 {
		t.Fatal("the scan did not wake the worker")
	}

	for _, path := range []string{"/api/v2/libraries/99/scan", "/api/v2/libraries/abc/scan"} {
		rec := e.do(http.MethodPost, path, "", write(withSession(cookie)), false)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s: %d, want 404", path, rec.Code)
		}
	}

	rec = e.do(http.MethodPost, "/api/v2/libraries/scan", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("scan all: %d %s", rec.Code, rec.Body)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"ok":true`)) {
		t.Fatalf("scan all body %s", rec.Body)
	}
	e.nextScan()
}

// The summary counts the queue and reports one built-in worker; attention is empty in stage 1.
func TestSummaryAndAttention(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	var summary struct {
		Converting struct{ Running, Workers int }
		Queue      int     `json:"queue"`
		LastDoneAt *string `json:"lastDoneAt"`
		NextScanAt *string `json:"nextScanAt"`
		DiskFree   uint64  `json:"diskFreeBytes"`
		Failed24h  int     `json:"failed24h"`
		SeenSeq    int     `json:"seenSeq"`
		SeenAt     *string `json:"seenAt"`
		Health     bool    `json:"healthFailing"`
		Workers    struct {
			Paused    bool `json:"paused"`
			Finishing int  `json:"finishing"`
		}
	}
	get(t, e, cookie, "/api/v2/summary", &summary)
	if summary.Converting.Workers != 1 || summary.Converting.Running != 0 || summary.Queue != 0 {
		t.Fatalf("fresh summary %+v", summary)
	}
	if summary.LastDoneAt != nil || summary.NextScanAt != nil || summary.DiskFree != 123456789 {
		t.Fatalf("fresh summary %+v", summary)
	}

	clips := t.TempDir()
	if _, err := e.catalog.AddLibrary(context.Background(), library.NewLibrary{Name: "Clips", Path: clips}); err != nil {
		t.Fatal(err)
	}
	fileID, err := e.catalog.SaveFile(context.Background(), library.File{
		LibraryID: 1, RelPath: "one.mkv", Size: 10, FileID: "dev-1",
	}, library.Queued, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.queue.Enqueue(context.Background(), fileID, 1, 7, filepath.Join(clips, "one.mkv"), "scan"); err != nil {
		t.Fatal(err)
	}

	get(t, e, cookie, "/api/v2/summary", &summary)
	if summary.Queue != 1 || summary.NextScanAt == nil {
		t.Fatalf("summary after a queued job %+v; want queue 1 and a next scan", summary)
	}

	var attention []any
	get(t, e, cookie, "/api/v2/attention", &attention)
	if len(attention) != 0 {
		t.Fatalf("attention %v", attention)
	}
}
