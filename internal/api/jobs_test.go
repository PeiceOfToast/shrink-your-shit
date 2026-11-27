// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// jobsFixture is a signed-in server with three jobs in the store: the oldest done (smaller), the next failed, and
// the newest still waiting. Their files sit in one library that runs the Shrink workflow.
type jobsFixture struct {
	e       *env
	cookie  *http.Cookie
	done    int64 // the done job's id
	failed  int64
	waiting int64
}

func newJobsFixture(t *testing.T) *jobsFixture {
	t.Helper()
	e := newEnv(t, true)
	cookie := e.setup()
	ctx := context.Background()
	videos := t.TempDir() // a library may not sit inside the app's data folder
	lib, err := library.NewCatalog(e.db).AddLibrary(ctx, library.NewLibrary{Name: "Clips", Path: videos})
	if err != nil {
		t.Fatalf("add library: %v", err)
	}
	var fileIDs []int64
	for _, name := range []string{"one.mkv", "two.mkv", "three.mkv"} {
		var id int64
		if err := e.db.Tx(ctx, func(tx *sql.Tx) error {
			res, err := tx.Exec(`INSERT INTO files(library_id, rel_path, size, mtime, file_id) VALUES(?, ?, 1000, 1, ?)`,
				lib.ID, name, "fid-"+name)
			if err != nil {
				return err
			}
			id, err = res.LastInsertId()
			return err
		}); err != nil {
			t.Fatalf("insert file %s: %v", name, err)
		}
		fileIDs = append(fileIDs, id)
	}
	flows := flow.Open(e.db)
	wf, _, err := flows.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	q := jobs.New(jobs.Options{DB: e.db, Bus: e.bus, Clock: e.clock.Now})
	e.srv.Jobs(q, library.NewCatalog(e.db), flows, e.bus)

	f := &jobsFixture{e: e, cookie: cookie}
	var ids []int64
	for i, name := range []string{"one.mkv", "two.mkv", "three.mkv"} {
		id, _, err := q.Enqueue(ctx, fileIDs[i], lib.ID, wf, filepath.Join(videos, name), "test")
		if err != nil {
			t.Fatalf("enqueue %s: %v", name, err)
		}
		ids = append(ids, id)
	}
	// Ids are 1 (one.mkv), 2 (two.mkv), 3 (three.mkv). Claim hands out the newest first, so the first claim is
	// done, the second failed, and the oldest (one.mkv) stays waiting.
	j, err := q.Claim(ctx, "local", jobs.ClaimOptions{})
	if err != nil || j == nil {
		t.Fatalf("claim the newest job: %+v, %v", j, err)
	}
	if err := q.Finish(ctx, j.ID, j.Attempt, jobs.Result{
		Verdict: jobs.VerdictDone, Outcome: "smaller", SizeBefore: 1000, SizeAfter: 400, OutputPath: filepath.Join(videos, "three-shrunk.mkv"),
	}); err != nil {
		t.Fatalf("finish done: %v", err)
	}
	f.done = j.ID
	j, err = q.Claim(ctx, "local", jobs.ClaimOptions{})
	if err != nil || j == nil {
		t.Fatalf("claim the second job: %+v, %v", j, err)
	}
	if err := q.Finish(ctx, j.ID, j.Attempt, jobs.Result{
		Verdict: jobs.VerdictFail, Reason: "the encoder stopped", FailedStep: "encode",
	}); err != nil {
		t.Fatalf("finish failed: %v", err)
	}
	f.failed = j.ID
	f.waiting = ids[0]
	if f.done != ids[2] || f.failed != ids[1] {
		t.Fatalf("claim order: done %d failed %d; want %d and %d", f.done, f.failed, ids[2], ids[1])
	}
	return f
}

func (f *jobsFixture) get(path string) *httptest.ResponseRecorder {
	f.e.t.Helper()
	return f.e.do(http.MethodGet, path, "", map[string]string{"Cookie": f.cookie.String()}, false)
}

func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), v); err != nil {
		t.Fatalf("body is not the shape: %v\n%s", err, rec.Body.String())
	}
}

func TestJobsListNeedsSignIn(t *testing.T) {
	f := newJobsFixture(t)
	for _, path := range []string{"/api/v2/jobs", fmt.Sprintf("/api/v2/jobs/%d", f.done), fmt.Sprintf("/api/v2/jobs/%d/neighbours", f.done)} {
		rec := f.e.do(http.MethodGet, path, "", nil, false)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", path, rec.Code)
		}
	}
}

func TestJobsListShowsCountsAndRows(t *testing.T) {
	f := newJobsFixture(t)
	rec := f.get("/api/v2/jobs")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Counts map[string]int `json:"counts"`
		Items  []struct {
			ID, State, File, Library, Workflow string
			Result                             struct {
				SizeBefore int64   `json:"sizeBefore"`
				SizeAfter  *int64  `json:"sizeAfter"`
				Outcome    *string `json:"outcome"`
				FailedStep *string `json:"failedStep"`
				Reason     *string `json:"reason"`
				Warnings   int     `json:"warnings"`
			} `json:"result"`
		} `json:"items"`
		Next *string `json:"next"`
	}
	decodeInto(t, rec, &page)
	if page.Counts["all"] != 3 || page.Counts["waiting"] != 1 || page.Counts["done"] != 1 || page.Counts["failed"] != 1 {
		t.Errorf("counts = %v, want all 3, waiting 1, done 1, failed 1", page.Counts)
	}
	if len(page.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(page.Items))
	}
	// Newest first: done (three.mkv), failed (two.mkv), waiting (one.mkv).
	top := page.Items[0]
	if top.ID != fmt.Sprint(f.done) || top.State != "done" || top.File != "three.mkv" || top.Library != "Clips" || top.Workflow != "Shrink" {
		t.Errorf("top row = %+v; want the done three.mkv in Clips, Shrink", top)
	}
	if top.Result.SizeBefore != 1000 || top.Result.SizeAfter == nil || *top.Result.SizeAfter != 400 ||
		top.Result.Outcome == nil || *top.Result.Outcome != "smaller" {
		t.Errorf("done row result = %+v; want 1000 -> 400, smaller", top.Result)
	}
	failed := page.Items[1]
	if failed.ID != fmt.Sprint(f.failed) || failed.State != "failed" || failed.Result.FailedStep == nil ||
		*failed.Result.FailedStep != "encode" || failed.Result.Reason == nil {
		t.Errorf("failed row = %+v; want failed, failedStep encode, a reason", failed)
	}
	if page.Items[2].ID != fmt.Sprint(f.waiting) || page.Items[2].State != "waiting" {
		t.Errorf("last row = %+v; want the waiting one.mkv", page.Items[2])
	}
	if page.Next != nil {
		t.Errorf("next = %q on a short page; want null", *page.Next)
	}
}

func TestJobsListFiltersAndRefusesBadInput(t *testing.T) {
	f := newJobsFixture(t)
	rec := f.get("/api/v2/jobs?state=done")
	var page struct {
		Items []map[string]any `json:"items"`
	}
	decodeInto(t, rec, &page)
	if rec.Code != http.StatusOK || len(page.Items) != 1 {
		t.Fatalf("state=done: %d, %d items; want 200 with one", rec.Code, len(page.Items))
	}
	// A waiting job shows in the waiting list.
	decodeInto(t, f.get("/api/v2/jobs?state=waiting"), &page)
	if len(page.Items) != 1 {
		t.Errorf("state=waiting: %d items, want 1", len(page.Items))
	}
	for _, tc := range []struct{ path, field string }{
		{"/api/v2/jobs?state=cancelled", "state"},
		{"/api/v2/jobs?limit=0", "limit"},
		{"/api/v2/jobs?limit=501", "limit"},
		{"/api/v2/jobs?cursor=abc", "cursor"},
	} {
		rec := f.get(tc.path)
		code, _, raw := errorBody(t, rec)
		field, _ := raw["field"].(string)
		if rec.Code != http.StatusBadRequest || code != "invalid_request" || field != tc.field {
			t.Errorf("%s: %d %s field %q; want 400 invalid_request on %s", tc.path, rec.Code, code, field, tc.field)
		}
	}
}

func TestJobsListPages(t *testing.T) {
	f := newJobsFixture(t)
	var first struct {
		Items []map[string]any `json:"items"`
		Next  *string          `json:"next"`
	}
	decodeInto(t, f.get("/api/v2/jobs?limit=2"), &first)
	if len(first.Items) != 2 || first.Next == nil {
		t.Fatalf("first page = %d items, next %v; want 2 and a cursor", len(first.Items), first.Next)
	}
	var second struct {
		Items []map[string]any `json:"items"`
		Next  *string          `json:"next"`
	}
	decodeInto(t, f.get("/api/v2/jobs?limit=2&cursor="+*first.Next), &second)
	if len(second.Items) != 1 || second.Next != nil {
		t.Errorf("second page = %d items, next %v; want 1 and no cursor", len(second.Items), second.Next)
	}
}

func TestJobGetOneAndNotFound(t *testing.T) {
	f := newJobsFixture(t)
	rec := f.get(fmt.Sprintf("/api/v2/jobs/%d", f.failed))
	var j struct {
		Job struct {
			ID, State string
			Result    struct {
				FailedStep *string `json:"failedStep"`
			} `json:"result"`
		} `json:"job"`
		Failure *struct {
			Step string `json:"step"`
		} `json:"failure"`
		Steps []any `json:"steps"`
	}
	decodeInto(t, rec, &j)
	if rec.Code != http.StatusOK || j.Job.ID != fmt.Sprint(f.failed) || j.Job.State != "failed" {
		t.Errorf("get failed job = %d %+v; want 200 failed", rec.Code, j.Job)
	}
	if j.Failure == nil || j.Failure.Step != "encode" {
		t.Errorf("failure = %+v; want step encode", j.Failure)
	}
	for _, path := range []string{"/api/v2/jobs/999999", "/api/v2/jobs/abc", "/api/v2/jobs/0"} {
		rec := f.get(path)
		if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "job_not_found" {
			t.Errorf("%s: %d %s; want 404 job_not_found", path, rec.Code, code)
		}
	}
}

func TestJobNeighbours(t *testing.T) {
	f := newJobsFixture(t)
	var n struct {
		Prev *string `json:"prev"`
		Next *string `json:"next"`
	}
	// The failed job sits between the done job above it and the waiting job below.
	decodeInto(t, f.get(fmt.Sprintf("/api/v2/jobs/%d/neighbours", f.failed)), &n)
	if n.Prev == nil || *n.Prev != fmt.Sprint(f.done) || n.Next == nil || *n.Next != fmt.Sprint(f.waiting) {
		t.Errorf("neighbours of failed = prev %v next %v; want done above, waiting below", n.Prev, n.Next)
	}
	// In the done list the done job has no neighbours.
	decodeInto(t, f.get(fmt.Sprintf("/api/v2/jobs/%d/neighbours?state=done", f.done)), &n)
	if n.Prev != nil || n.Next != nil {
		t.Errorf("neighbours of done in the done list = %v %v; want none", n.Prev, n.Next)
	}
	// A job that is not in the list shown is a 404.
	rec := f.get(fmt.Sprintf("/api/v2/jobs/%d/neighbours?state=done", f.waiting))
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "job_not_found" {
		t.Errorf("waiting job in the done list: %d %s; want 404 job_not_found", rec.Code, code)
	}
}

func TestJobLogIsOldestFirstAndHistoryKeepsLifecycle(t *testing.T) {
	f := newJobsFixture(t)
	var log []struct {
		Seq   int64   `json:"seq"`
		Kind  string  `json:"kind"`
		Level string  `json:"level"`
		JobID *string `json:"jobId"`
	}
	// The event store commits on the sink's own flush, so the log can trail an Emit by a moment. Poll for it.
	var rec *httptest.ResponseRecorder
	deadline := time.Now().Add(3 * time.Second)
	for {
		rec = f.get(fmt.Sprintf("/api/v2/jobs/%d/events", f.done))
		log = nil
		decodeInto(t, rec, &log)
		if len(log) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.Code != http.StatusOK || len(log) == 0 {
		t.Fatalf("job log: %d with %d lines; want 200 with lines", rec.Code, len(log))
	}
	sawQueued := false
	for i, e := range log {
		if e.JobID == nil || *e.JobID != fmt.Sprint(f.done) {
			t.Errorf("line %d jobId = %v; want %d", i, e.JobID, f.done)
		}
		if i > 0 && e.Seq <= log[i-1].Seq {
			t.Errorf("line %d seq %d is not after %d", i, e.Seq, log[i-1].Seq)
		}
		if e.Kind == "queued" {
			sawQueued = true
		}
	}
	if !sawQueued {
		t.Error("the log has no queued line")
	}

	var hist []struct {
		Time, Level, Text string
	}
	rec = f.get(fmt.Sprintf("/api/v2/jobs/%d/history", f.done))
	decodeInto(t, rec, &hist)
	if rec.Code != http.StatusOK || len(hist) == 0 {
		t.Fatalf("history: %d with %d lines; want 200 with lines", rec.Code, len(hist))
	}
	for _, h := range hist {
		if h.Level == "debug" || h.Time == "" {
			t.Errorf("history line %+v; want no debug lines and a time", h)
		}
	}

	for _, path := range []string{"/api/v2/jobs/999999/events", "/api/v2/jobs/999999/history", "/api/v2/jobs/abc/events"} {
		rec := f.get(path)
		if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "job_not_found" {
			t.Errorf("%s: %d %s; want 404 job_not_found", path, rec.Code, code)
		}
	}
	if rec := f.e.do(http.MethodGet, fmt.Sprintf("/api/v2/jobs/%d/events", f.done), "", nil, false); rec.Code != http.StatusUnauthorized {
		t.Errorf("log without a session: %d, want 401", rec.Code)
	}
}

// A step's duration is its end time minus its start time, and the worker is the one that ran the job's attempt.
func TestJobDetailHasStepDurationsAndWorker(t *testing.T) {
	f := newJobsFixture(t)
	bus := f.e.bus
	if err := bus.Emit(events.Event{JobID: f.done, Attempt: 1, Kind: events.KindStepStart, Level: events.LevelInfo,
		StepIndex: 1, StepName: "Video", Message: "1. Video"}); err != nil {
		t.Fatal(err)
	}
	f.e.clock.Advance(2 * time.Second)
	if err := bus.Emit(events.Event{JobID: f.done, Attempt: 1, Kind: events.KindStepEnd, Level: events.LevelInfo,
		StepIndex: 1, StepName: "Video"}); err != nil {
		t.Fatal(err)
	}
	var d struct {
		Job    struct{ Worker string } `json:"job"`
		Worker string                  `json:"worker"`
		Steps  []struct {
			Name       string `json:"name"`
			DurationMs *int64 `json:"durationMs"`
		} `json:"steps"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		decodeInto(t, f.get(fmt.Sprintf("/api/v2/jobs/%d", f.done)), &d)
		if len(d.Steps) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(d.Steps) != 1 || d.Steps[0].DurationMs == nil || *d.Steps[0].DurationMs != 2000 {
		t.Fatalf("steps = %+v; want one step of 2000 ms", d.Steps)
	}
	if d.Worker != "local" || d.Job.Worker != "local" {
		t.Errorf("worker = %q (job %q); want local", d.Worker, d.Job.Worker)
	}
}
