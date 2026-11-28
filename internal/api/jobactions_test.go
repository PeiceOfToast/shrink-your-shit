// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// exec runs one write, for a test that sets a column directly.
func (e *env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// countJobs counts the job rows, so a test can see that an action reused a row rather than adding one.
func (e *env) countJobs(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.db.Read().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// act posts one action for ids and returns the decoded answer.
func (f *jobsFixture) act(t *testing.T, action string, ids ...any) jobActionDone {
	t.Helper()
	list := ""
	for i, id := range ids {
		if i > 0 {
			list += ","
		}
		switch v := id.(type) {
		case string:
			list += fmt.Sprintf("%q", v)
		default:
			list += fmt.Sprint(v)
		}
	}
	rec := f.e.do(http.MethodPost, "/api/v2/jobs/actions",
		fmt.Sprintf(`{"action":%q,"ids":[%s]}`, action, list),
		write(map[string]string{"Cookie": f.cookie.String()}), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s %v: status %d, body %s", action, ids, rec.Code, rec.Body)
	}
	var out jobActionDone
	decodeInto(t, rec, &out)
	return out
}

// refusals lists the answer's refused entries by id.
func refusals(out jobActionDone) map[string]jobRefusal {
	m := map[string]jobRefusal{}
	for _, r := range out.Refused {
		m[r.ID] = r
	}
	return m
}

// TestGuardRetryFailedJob: Retry puts the failed job back to waiting on the same row, and a refused id comes back
// with its code instead of failing the whole request.
func TestRetryFailedJob(t *testing.T) {
	f := newJobsFixture(t)
	out := f.act(t, "retry", f.failed, f.done, "nope")
	if len(out.Done) != 1 || out.Done[0] != fmt.Sprint(f.failed) {
		t.Fatalf("done = %v, want the failed job %d", out.Done, f.failed)
	}
	refused := refusals(out)
	if r := refused[fmt.Sprint(f.done)]; r.Code != "not_retryable" || r.Message != "This job has already finished. Use Run again." {
		t.Fatalf("done job refusal = %+v", r)
	}
	if r := refused["nope"]; r.Code != "job_not_found" || r.Message != "That job doesn't exist." {
		t.Fatalf("bad id refusal = %+v", r)
	}
	got, err := f.q.Get(t.Context(), f.failed)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != jobs.Waiting || got.WorkflowRev != 0 {
		t.Fatalf("after retry: state %s revision %d; want waiting with the published workflow", got.State, got.WorkflowRev)
	}
	// The wake tells an idle worker to look now.
	select {
	case <-f.e.scans.wakes:
	default:
		t.Fatal("no wake after a retry")
	}
}

// TestGuardRetryRefusals: the three replace failures are refused with their sentence, and the job is left failed.
func TestRetryReplaceFailureIsRefused(t *testing.T) {
	f := newJobsFixture(t)
	// A failed job with a replace code, as the flow records it.
	f.e.exec(t, `UPDATE jobs SET failure_code = 'replace.put_back_failed' WHERE id = ?`, f.failed)
	out := f.act(t, "retry", f.failed)
	want, _ := jobs.RetryRefused("replace.put_back_failed")
	if r := refusals(out)[fmt.Sprint(f.failed)]; r.Code != "not_retryable" || r.Message != want {
		t.Fatalf("refusal = %+v, want %q", r, want)
	}
	if got, _ := f.q.Get(t.Context(), f.failed); got.State != jobs.Failed {
		t.Fatalf("refused retry moved the job to %s", got.State)
	}
}

// TestRetryFileBusy: a file with another job waiting refuses the retry with file_busy.
func TestRetryFileBusy(t *testing.T) {
	f := newJobsFixture(t)
	var lib, wf int64
	if err := f.e.db.Read().QueryRow(`SELECT library_id, workflow_id FROM jobs WHERE id = ?`, f.failed).Scan(&lib, &wf); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.q.Enqueue(t.Context(), 2, lib, wf, 1, f.videos+"/again.mkv", "test"); err != nil {
		t.Fatal(err)
	}
	out := f.act(t, "retry", f.failed)
	if r := refusals(out)[fmt.Sprint(f.failed)]; r.Code != "file_busy" || r.Message != "This file already has a job waiting or running." {
		t.Fatalf("refusal = %+v, want file_busy", r)
	}
}

// TestGuardCancel: a waiting job is cancelled and kept; a finished job is refused.
func TestCancelWaitingJob(t *testing.T) {
	f := newJobsFixture(t)
	out := f.act(t, "cancel", f.waiting, f.done)
	if len(out.Done) != 1 || out.Done[0] != fmt.Sprint(f.waiting) {
		t.Fatalf("done = %v, want the waiting job %d", out.Done, f.waiting)
	}
	if r := refusals(out)[fmt.Sprint(f.done)]; r.Code != "not_cancellable" || r.Message != "This job has already finished." {
		t.Fatalf("done job refusal = %+v", r)
	}
	if got, _ := f.q.Get(t.Context(), f.waiting); got.State != jobs.Cancelled {
		t.Fatalf("waiting job state = %s, want cancelled", got.State)
	}
	if n := f.e.countJobs(t); n != 3 {
		t.Fatalf("%d rows; a cancelled job is kept, not deleted", n)
	}
}

// TestCancelRunningJobStopsTheWorker: the row is cancelled first, then the worker is asked to stop the run.
func TestCancelRunningJobStopsTheWorker(t *testing.T) {
	f := newJobsFixture(t)
	var stopped []int64
	f.e.stopJob = func(id int64) { stopped = append(stopped, id) }
	f.e.ready()

	if _, err := f.q.Claim(t.Context(), "local", jobs.ClaimOptions{}); err != nil {
		t.Fatal(err)
	}
	// Retry of a running job is refused with its own sentence (pinned with the other refusal pairs).
	retried := f.act(t, "retry", f.waiting)
	if r := refusals(retried)[fmt.Sprint(f.waiting)]; r.Code != "not_retryable" || r.Message != "This job is running now." {
		t.Fatalf("running job retry refusal = %+v", r)
	}
	out := f.act(t, "cancel", f.waiting)
	if len(out.Done) != 1 {
		t.Fatalf("done = %v, want the running job %d", out.Done, f.waiting)
	}
	if len(stopped) != 1 || stopped[0] != f.waiting {
		t.Fatalf("stopped = %v, want [%d]", stopped, f.waiting)
	}
	if got, _ := f.q.Get(t.Context(), f.waiting); got.State != jobs.Cancelled {
		t.Fatalf("running job state = %s, want cancelled", got.State)
	}
}

// TestGuardTop: Move to the top marks the waiting job and wakes a worker; a finished job is refused as
// not_movable, its own code, not Retry's (ui-shop, 11-28).
func TestTopWaitingJob(t *testing.T) {
	f := newJobsFixture(t)
	out := f.act(t, "top", f.waiting, f.failed)
	if len(out.Done) != 1 || out.Done[0] != fmt.Sprint(f.waiting) {
		t.Fatalf("done = %v, want the waiting job %d", out.Done, f.waiting)
	}
	if r := refusals(out)[fmt.Sprint(f.failed)]; r.Code != "not_movable" || r.Message != "Only a waiting job can be moved to the top." {
		t.Fatalf("failed job refusal = %+v", r)
	}
	got, _ := f.q.Get(t.Context(), f.waiting)
	if !got.Top {
		t.Fatal("top not set")
	}
}

// TestActionValidation: an unknown action, an empty list and a bad body are refused before any job changes.
func TestJobActionValidation(t *testing.T) {
	f := newJobsFixture(t)
	for _, body := range []string{
		`{"action":"convert","ids":[1]}`,
		`{"action":"retry","ids":[]}`,
		`not json`,
	} {
		rec := f.e.do(http.MethodPost, "/api/v2/jobs/actions", body,
			write(map[string]string{"Cookie": f.cookie.String()}), false)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d, want 400; body %s", body, rec.Code, rec.Body)
		}
	}
}

// TestActionLabelForReplaceFailures: the three replace failures get no Retry button, every other failure does.
func TestActionLabelForReplaceFailures(t *testing.T) {
	f := newJobsFixture(t)
	for code, want := range map[string]string{
		"replace.record_unsettled":  "",
		"replace.put_back_failed":   "",
		"replace.stranger_in_trash": "",
		"encode.failed":             "Retry",
		"":                          "Retry",
	} {
		f.e.exec(t, `UPDATE jobs SET failure_code = NULLIF(?, '') WHERE id = ?`, code, f.failed)
		var got jobDetailJSON
		decodeInto(t, f.get(fmt.Sprintf("/api/v2/jobs/%d", f.failed)), &got)
		if got.Failure == nil || got.Failure.ActionLabel != want {
			t.Fatalf("code %q: failure = %+v, want action label %q", code, got.Failure, want)
		}
	}
}
