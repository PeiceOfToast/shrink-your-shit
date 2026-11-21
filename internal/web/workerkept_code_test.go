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
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// Slice 14, review2 P3: a remote worker's refusal carries its code (kept_code) through the report to the run. The
// run is stood in for by a reader of its answer, so what it receives is what the report carried.
func TestKeptCodeFromAReportReachesTheRun(t *testing.T) {
	_, st, a, _ := libServer(t, true)
	answers := make(chan worker.RemoteResult, 1)
	srv := httptest.NewServer(Handler(st, a, Engine{RunRemote: func(_ jobs.Job, _ string, rj *worker.RemoteJob) {
		answers <- <-rj.Answer()
	}}))
	t.Cleanup(srv.Close)

	_, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/pairings", body: `{}`, ctype: "application/json", bearer: a.Token()})
	var p struct{ Code string }
	json.Unmarshal([]byte(body), &p)
	c, b := workerCall(t, srv, "pair", "", `{"code":"`+p.Code+`","name":"Desktop"}`, WorkerAPILevel)
	var w struct{ WorkerID, Token string }
	json.Unmarshal([]byte(b), &w)
	if c != 200 || w.Token == "" {
		t.Fatalf("pair %d %s", c, b)
	}

	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if _, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv"), SizeBefore: 1000}); err != nil {
		t.Fatal(err)
	}
	c, b = workerCall(t, srv, "lease", w.Token, `{"slots":1}`, WorkerAPILevel)
	var l struct{ Lease struct{ ID string } }
	json.Unmarshal([]byte(b), &l)
	if c != 200 || l.Lease.ID == "" {
		t.Fatalf("lease %d %s", c, b)
	}

	const words = "the new file isn't what the workflow asked for: the audio is missing."
	req := httptest.NewRequest("POST", "/api/worker/v1/leases/"+l.Lease.ID+"/report",
		strings.NewReader(`{"kept":"`+words+`","kept_code":"`+jobs.ReasonCheckDiffers+`"}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.Token)
	req.Header.Set(workerHeader, "test; api="+strconv.Itoa(WorkerAPILevel))
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("report %d %s, want 202", rec.Code, rec.Body.String())
	}
	select {
	case res := <-answers:
		if res.KeptCode != jobs.ReasonCheckDiffers || res.Kept != words {
			t.Errorf("the run got kept %q code %q, want %q and %q", res.Kept, res.KeptCode, words, jobs.ReasonCheckDiffers)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run never received the report's answer")
	}
}
