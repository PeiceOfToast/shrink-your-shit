// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// Slice 15, review #580: a remote worker's report may carry the file-type refusal's code or the killed one
// (its converter was stopped by the system), and only those. An older worker sends none ("" stays valid);
// anything else is refused before it reaches the job.
func TestReportAcceptsOnlyKnownFailureCodes(t *testing.T) {
	_, st, a, _ := libServer(t, true)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := httptest.NewServer(Handler(st, a, Engine{RunRemote: func(jobs.Job, string, *worker.RemoteJob) { <-release }}))
	t.Cleanup(srv.Close)

	person := a.Token()
	_, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/pairings", body: `{}`, ctype: "application/json", bearer: person})
	var p struct{ Code string }
	json.Unmarshal([]byte(body), &p)
	c, b := workerCall(t, srv, "pair", "", `{"code":"`+p.Code+`","name":"Desktop"}`, WorkerAPILevel)
	var w struct{ WorkerID, Token string }
	json.Unmarshal([]byte(b), &w)
	if c != 200 || w.Token == "" {
		t.Fatalf("pair %d %s", c, b)
	}

	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	lease := func(name string) string {
		t.Helper()
		if _, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, name), SizeBefore: 1000}); err != nil {
			t.Fatal(err)
		}
		c, b := workerCall(t, srv, "lease", w.Token, `{"slots":1}`, WorkerAPILevel)
		var l struct{ Lease struct{ ID string } }
		json.Unmarshal([]byte(b), &l)
		if c != 200 || l.Lease.ID == "" {
			t.Fatalf("lease %s: %d %s", name, c, b)
		}
		return l.Lease.ID
	}
	report := func(lease, code string) (int, string) {
		t.Helper()
		body := `{"ok":false,"message":"the other computer couldn't convert it"`
		if code != "" {
			body += `,"code":"` + code + `"`
		}
		body += `}`
		req := httptest.NewRequest("POST", "/api/worker/v1/leases/"+lease+"/report", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+w.Token)
		req.Header.Set(workerHeader, "test; api="+strconv.Itoa(WorkerAPILevel))
		rec := httptest.NewRecorder()
		srv.Config.Handler.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	l1 := lease("a.mkv")
	if c, b := report(l1, "settings_rejected_x"); c != http.StatusBadRequest || !strings.Contains(b, "Unknown failure code.") {
		t.Errorf("an unknown code: %d %s, want 400", c, b)
	}
	if c, b := report(l1, ""); c != http.StatusAccepted {
		t.Errorf("an older worker's report (no code): %d %s, want 202", c, b)
	}
	l2 := lease("b.mkv")
	if c, b := report(l2, jobs.ReasonSettingsRejected); c != http.StatusAccepted {
		t.Errorf("settings_rejected: %d %s, want 202", c, b)
	}
	l3 := lease("c.mkv")
	if c, b := report(l3, jobs.ReasonKilled); c != http.StatusAccepted {
		t.Errorf("killed: %d %s, want 202", c, b)
	}
}
