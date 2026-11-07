// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func TestStatusAndRetry(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.AddLibrary("lib", "/videos", rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/videos", Path: "/videos/a.mkv"})
	st.Claim("w")
	st.Fail(id, "bad")
	srv := httptest.NewServer(Handler(st, openAccess(t), Engine{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Libraries []string
		Jobs      []jobs.Job
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Libraries) != 1 || len(out.Jobs) != 1 || out.Jobs[0].Status != jobs.Failed {
		t.Fatalf("%+v", out)
	}
	resp, _ = http.Post(srv.URL+"/api/jobs/1/retry", "application/json", strings.NewReader("{}"))
	if resp.StatusCode != 200 {
		t.Errorf("retry: %d", resp.StatusCode)
	}
	if j, _ := st.Get(id); j.Status != jobs.Queued {
		t.Errorf("%+v", j)
	}
	resp, _ = http.Post(srv.URL+"/api/jobs/1/retry", "application/json", strings.NewReader("{}"))
	if resp.StatusCode != 409 {
		t.Errorf("retrying a queued job should be 409, got %d", resp.StatusCode)
	}
	resp, _ = http.Get(srv.URL + "/")
	b := new(strings.Builder)
	buf := make([]byte, 4096)
	n, _ := resp.Body.Read(buf)
	b.Write(buf[:n])
	if !strings.Contains(b.String(), "shrink-your-shit") {
		t.Error("index page not served")
	}
}
