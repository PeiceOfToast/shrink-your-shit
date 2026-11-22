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

// F7: the speed a remote worker measures reaches the run with its report, so the step records the encoder
// it ran and how fast it went (the run is stood in for by a reader of its answer, as the kept code's test).
func TestReportCarriesItsSpeed(t *testing.T) {
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

	// the fields are read the same for every report; a refusal stands in here so no upload has to arrive
	req := httptest.NewRequest("POST", "/api/worker/v1/leases/"+l.Lease.ID+"/report",
		strings.NewReader(`{"kept":"the picture differs","kept_code":"`+jobs.ReasonCheckDiffers+`","encoder":"hevc_qsv","speed":3.5,"fps":118}`))
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
		if res.Encoder != "hevc_qsv" || res.Speed != 3.5 || res.FPS != 118 {
			t.Errorf("the run got encoder %q speed %v fps %v, want hevc_qsv at 3.5x and 118 fps", res.Encoder, res.Speed, res.FPS)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run never received the report's answer")
	}
}
