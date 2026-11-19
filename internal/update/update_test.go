// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memStore is an in-memory Meta, so the tests need no database.
type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) Meta(key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	return v, ok, nil
}

func (s *memStore) SetMeta(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}

// releaseJSON serves a GitHub-shaped latest release.
func releaseJSON(tag string, prerelease, draft bool) string {
	b, _ := json.Marshal(map[string]any{"tag_name": tag, "body": "notes for " + tag,
		"html_url": "https://example.test/releases/" + tag, "published_at": "2026-11-18T10:00:00Z",
		"prerelease": prerelease, "draft": draft})
	return string(b)
}

// fakeGitHub answers with body and counts the requests it gets.
func fakeGitHub(t *testing.T, status int, body string, delay time.Duration) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Header.Get("User-Agent") == "" {
			t.Errorf("no User-Agent")
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.1.2", "0.1.1", true},
		{"v0.2.0", "0.1.9", true},
		{"0.1.1", "0.1.1", false},
		{"0.1.0", "0.1.1", false},
		{"", "0.1.1", false},
		{"0.2.0", "dev", false}, // a development build never has an update
		{"junk", "0.1.1", false},
		{"0.2", "0.1.1", false},
	}
	for _, c := range cases {
		if got := newer(c.latest, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

func TestPrereleaseAndDraftAreNotUpdates(t *testing.T) {
	for _, body := range []string{releaseJSON("v0.2.0", true, false), releaseJSON("v0.2.0", false, true), releaseJSON("v0.2.0-rc1", false, false)} {
		srv, _ := fakeGitHub(t, http.StatusOK, body, 0)
		st, err := (&Checker{Store: &memStore{}, Current: "0.1.1", URL: srv.URL}).Check(0)
		if err != nil {
			t.Fatal(err)
		}
		if st.Available || st.Latest != "" {
			t.Errorf("%s: available %v latest %q, want neither", body, st.Available, st.Latest)
		}
	}
}

func TestCheckFindsANewerRelease(t *testing.T) {
	srv, _ := fakeGitHub(t, http.StatusOK, releaseJSON("v0.2.0", false, false), 0)
	st, err := (&Checker{Store: &memStore{}, Current: "0.1.1", URL: srv.URL}).Check(0)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Latest != "0.2.0" || st.Notes != "notes for v0.2.0" || st.URL == "" || st.PublishedAt == "" || st.Error != "" {
		t.Errorf("status %+v", st)
	}
}

// A failure keeps the last good release and records the error.
func TestFailureKeepsTheLastGoodRelease(t *testing.T) {
	store := &memStore{}
	good, _ := fakeGitHub(t, http.StatusOK, releaseJSON("v0.2.0", false, false), 0)
	c := &Checker{Store: store, Current: "0.1.1", URL: good.URL}
	if _, err := c.Check(0); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		status int
		delay  time.Duration
		want   string
	}{
		{http.StatusInternalServerError, 0, "HTTP 500"},
		{http.StatusForbidden, 0, "limiting requests"},
		{http.StatusTooManyRequests, 0, "limiting requests"},
		{http.StatusOK, 300 * time.Millisecond, "could not reach GitHub"},
	} {
		srv, _ := fakeGitHub(t, bad.status, "", bad.delay)
		c.URL = srv.URL
		c.Client = &http.Client{Timeout: 100 * time.Millisecond}
		st, err := c.Check(0)
		if err != nil {
			t.Fatal(err)
		}
		if st.Latest != "0.2.0" || !st.Available {
			t.Errorf("status %d: last good release lost: %+v", bad.status, st)
		}
		if st.Error == "" || !strings.Contains(st.Error, bad.want) {
			t.Errorf("status %d: error %q, want it to say %q", bad.status, st.Error, bad.want)
		}
	}
	// the next good answer clears the error
	c.URL, c.Client = good.URL, nil
	st, _ := c.Check(0)
	if st.Error != "" {
		t.Errorf("error %q after a good check, want none", st.Error)
	}
}

// A check is made only when the last real request is at least minAge old.
func TestCheckAsksGitHubOnlyWhenDue(t *testing.T) {
	srv, n := fakeGitHub(t, http.StatusOK, releaseJSON("v0.2.0", false, false), 0)
	now := time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)
	c := &Checker{Store: &memStore{}, Current: "0.1.1", URL: srv.URL, Now: func() time.Time { return now }}
	c.Check(24 * time.Hour)
	now = now.Add(time.Hour)
	c.Check(24 * time.Hour)
	if got := n.Load(); got != 1 {
		t.Errorf("requests %d within a day, want 1", got)
	}
	now = now.Add(24 * time.Hour)
	c.Check(24 * time.Hour)
	if got := n.Load(); got != 2 {
		t.Errorf("requests %d after a day, want 2", got)
	}
}

func TestLoadReadsTheCacheWithoutAskingGitHub(t *testing.T) {
	store := &memStore{}
	srv, n := fakeGitHub(t, http.StatusOK, releaseJSON("v0.2.0", false, false), 0)
	(&Checker{Store: store, Current: "0.1.1", URL: srv.URL}).Check(0)
	st, err := Load(store, "0.1.1")
	if err != nil || !st.Available || n.Load() != 1 {
		t.Errorf("Load: %+v, %v; requests %d, want the cached notice and no new request", st, err, n.Load())
	}
	if st, _ := Load(&memStore{}, "0.1.1"); st.Available || !st.CheckedAt.IsZero() {
		t.Errorf("an empty cache: %+v", st)
	}
}

func TestInstallType(t *testing.T) {
	env := func(vars map[string]string, exists bool, exe string) Env {
		return Env{
			Getenv: func(k string) string { return vars[k] },
			Exists: func(string) bool { return exists },
			Exe:    func() string { return exe },
		}
	}
	cases := []struct {
		name, goos, current string
		e                   Env
		want                string
	}{
		{"docker by /.dockerenv", "linux", "0.1.1", env(nil, true, ""), "docker"},
		{"docker by marker", "linux", "0.1.1", env(map[string]string{"SYP_INSTALL": "docker"}, false, ""), "docker"},
		{"script by marker", "linux", "0.1.1", env(map[string]string{"SYP_INSTALL": "script"}, false, "/somewhere/else"), "script"},
		{"script by /usr/local/bin", "linux", "0.1.1", env(map[string]string{"HOME": "/home/x"}, false, "/usr/local/bin/shrink-your-shit"), "script"},
		{"script by ~/.local/bin", "linux", "0.1.1", env(map[string]string{"HOME": "/home/x"}, false, "/home/x/.local/bin/shrink-your-shit"), "script"},
		{"windows", "windows", "0.1.1", env(nil, false, `C:\Program Files\syp.exe`), "windows"},
		{"windows dev build is source", "windows", "dev", env(nil, false, ""), "source"},
		{"built by hand", "linux", "dev", env(map[string]string{"HOME": "/home/x"}, false, "/home/x/src/shrink-your-shit"), "source"},
	}
	for _, c := range cases {
		if got := InstallType(c.current, c.goos, c.e); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
