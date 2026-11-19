// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// updateServer serves the update routes and the settings page against a fake GitHub that answers latest and
// counts the requests it gets. Nothing here reaches the real GitHub: the checker's URL is the fake.
func updateServer(t *testing.T, latest string) (srv *httptest.Server, ss *settingsStore, requests *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The checker asks the releases path, which is "/" here. Any other path is a stray request from
		// somewhere else in the package: log it, do not count it.
		if r.URL.Path == "/" {
			n.Add(1)
		} else {
			t.Logf("stray request to %s, not counted", r.URL.Path)
		}
		w.Write([]byte(latest))
	}))
	t.Cleanup(gh.Close)
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ss = &settingsStore{store: st}
	s, err := ss.load()
	if err != nil {
		t.Fatal(err)
	}
	on := true // checks start off; these tests turn them on as the person would
	s.CheckForUpdates = &on
	if err := ss.save(s); err != nil {
		t.Fatal(err)
	}
	a, _, _ := OpenAccess(t.TempDir(), false)
	eng := Engine{Version: "0.1.1"}
	mux := http.NewServeMux()
	u := newUpdater(ss, eng)
	u.ck.URL = gh.URL
	v2Update(mux, u)
	settingsRoutes(mux, ss, a, eng)
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, ss, &n
}

const newerRelease = `{"tag_name":"v0.2.0","body":"fixes","html_url":"https://example.test/r/v0.2.0","published_at":"2026-11-18T10:00:00Z"}`

func getJSON(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func postCheck(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func TestUpdateCheckFindsTheNewerRelease(t *testing.T) {
	srv, _, n := updateServer(t, newerRelease)
	code, before := getJSON(t, srv.URL+"/api/v2/update")
	if code != 200 || before["available"] != false || before["checkedAt"] != nil || before["enabled"] != true {
		t.Fatalf("before any check: %d %v", code, before)
	}
	if n.Load() != 0 {
		t.Fatalf("GET asked GitHub %d times, want none", n.Load())
	}
	code, got := postCheck(t, srv.URL+"/api/v2/update/check")
	if code != 200 || got["available"] != true || got["latest"] != "0.2.0" || got["notes"] != "fixes" ||
		got["url"] != "https://example.test/r/v0.2.0" || got["publishedAt"] != "2026-11-18T10:00:00Z" || got["error"] != nil || got["checkedAt"] == nil {
		t.Fatalf("check: %d %v", code, got)
	}
	if got["installType"] == nil || got["installType"] == "" {
		t.Errorf("installType missing: %v", got)
	}
}

// One real request a minute: a second check inside that minute answers from the cache.
func TestUpdateCheckIsRateLimited(t *testing.T) {
	srv, _, n := updateServer(t, newerRelease)
	postCheck(t, srv.URL+"/api/v2/update/check")
	_, again := postCheck(t, srv.URL+"/api/v2/update/check")
	if n.Load() != 1 {
		t.Errorf("GitHub asked %d times in a minute, want 1", n.Load())
	}
	if again["available"] != true {
		t.Errorf("the cached answer: %v", again)
	}
}

// Settings: the notice shows in the settings page, and the switch turns every check off.
func TestUpdateNoticeAndTheSwitch(t *testing.T) {
	srv, _, _ := updateServer(t, newerRelease)
	postCheck(t, srv.URL+"/api/v2/update/check")
	_, settings := getJSON(t, srv.URL+"/api/settings")
	if settings["updateAvailable"] != "0.2.0" || settings["checkForUpdates"] != true {
		t.Fatalf("settings with a newer release: updateAvailable %v, checkForUpdates %v", settings["updateAvailable"], settings["checkForUpdates"])
	}

	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/settings", strings.NewReader(`{"checkForUpdates":false}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PATCH checkForUpdates: %d", resp.StatusCode)
	}
	_, settings = getJSON(t, srv.URL+"/api/settings")
	if settings["checkForUpdates"] != false || settings["updateAvailable"] != nil {
		t.Errorf("settings with checks off: checkForUpdates %v, updateAvailable %v", settings["checkForUpdates"], settings["updateAvailable"])
	}
	_, status := getJSON(t, srv.URL+"/api/v2/update")
	if status["enabled"] != false || status["available"] != false {
		t.Errorf("GET with checks off: %v", status)
	}
}

// Off means no request at all: not on GET, and POST says so with 409.
func TestUpdateOffMakesNoRequest(t *testing.T) {
	srv, ss, n := updateServer(t, newerRelease)
	s, err := ss.load()
	if err != nil {
		t.Fatal(err)
	}
	off := false
	s.CheckForUpdates = &off
	if err := ss.save(s); err != nil {
		t.Fatal(err)
	}
	getJSON(t, srv.URL+"/api/v2/update")
	code, body := postCheck(t, srv.URL+"/api/v2/update/check")
	if code != http.StatusConflict {
		t.Errorf("POST with checks off: %d %v, want 409", code, body)
	}
	if n.Load() != 0 {
		t.Errorf("GitHub asked %d times with checks off, want none", n.Load())
	}
}

// Checks are off until the person turns them on: on a fresh install, and for settings saved before the switch existed.
func TestUpdateChecksStartOff(t *testing.T) {
	if defaultSettings().UpdateChecks() {
		t.Error("a fresh install has update checks on; they must start off")
	}
	if (Settings{}).UpdateChecks() {
		t.Error("settings saved without checkForUpdates read as on; they must read as off")
	}
}
