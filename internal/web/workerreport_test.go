// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
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

// A report whose lease runs out after the server has checked it writes nothing: its steps and failure
// summary must not reach the job's record, where they could overwrite or clear the attempt that holds the job now.
func TestLateReportFromLostLeaseWritesNothing(t *testing.T) {
	_, st, a, _ := libServer(t, true)
	// the server runs a remote job until the test ends, so the job's run (and the report's route to it) exists
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
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv"), SizeBefore: 1000})
	c, b = workerCall(t, srv, "lease", w.Token, `{"slots":1}`, WorkerAPILevel)
	var l struct{ Lease struct{ ID string } }
	json.Unmarshal([]byte(b), &l)
	if c != 200 || l.Lease.ID == "" {
		t.Fatalf("lease %d %s", c, b)
	}

	// the current attempt's failure is on record before the late report arrives
	current := jobs.FailureSummary{Step: 1, StepCount: 1, Hint: "the attempt that holds the job"}
	if err := st.SetFailureSummary(id, current); err != nil {
		t.Fatal(err)
	}

	// The late report. Its body is read only after the handler has passed the lease check and the run lookup,
	// so the first read is the moment the lease runs out and the sweeper takes the job back.
	late := &expireOnFirstRead{
		data: `{"ok":false,"message":"late","steps":[{"Name":"late step","Outcome":"failed"}],"failure":{"Step":2,"StepCount":2,"Hint":"late"}}`,
		before: func() {
			old := jobs.LeaseFor
			jobs.LeaseFor = -time.Second
			lease, _ := st.LeaseOf(id)
			st.RenewLease(id, lease)
			jobs.LeaseFor = old
			st.RequeueExpiredLeases(time.Minute)
		},
	}
	req := httptest.NewRequest("POST", "/api/worker/v1/leases/"+l.Lease.ID+"/report", late)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.Token)
	req.Header.Set(workerHeader, "test; api="+strconv.Itoa(WorkerAPILevel))
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, req)
	if !late.fired {
		t.Fatalf("the report never reached its body: %d %s", rec.Code, rec.Body.String())
	}
	if j, _ := st.Get(id); j.Status != jobs.Queued {
		t.Fatalf("the sweeper should have taken the job back: %s", j.Status)
	}
	if rec.Code != 409 || errCode(rec.Body.String()) != "lease_gone" {
		t.Fatalf("a late report: %d %s", rec.Code, rec.Body.String())
	}
	if steps, _ := st.RunSteps(id); len(steps) != 0 {
		t.Errorf("the late report wrote steps: %+v", steps)
	}
	if f, _ := st.FailureOf(id); f == nil || f.Hint != current.Hint {
		t.Errorf("the late report changed the failure summary: %+v", f)
	}
}

// expireOnFirstRead hands the report its body; the first read runs before, so the lease expiry lands
// between the handler's lease check and its final hold.
type expireOnFirstRead struct {
	data   string
	before func()
	fired  bool
	r      *strings.Reader
}

func (e *expireOnFirstRead) Read(p []byte) (int, error) {
	if e.r == nil {
		e.fired = true
		e.before()
		e.r = strings.NewReader(e.data)
	}
	return e.r.Read(p)
}
