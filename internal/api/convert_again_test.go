// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
)

// TestRunAgainDoneJobAnswersTheUpdatedJob: a done job runs again on its own row and the answer is that job, so the
// panel stays open on the new attempt (S3-6b).
func TestRunAgainDoneJobAnswersTheUpdatedJob(t *testing.T) {
	f := newJobsFixture(t)
	rec := f.e.do(http.MethodPost, fmt.Sprintf("/api/v2/jobs/%d/convert-again", f.done), "",
		write(map[string]string{"Cookie": f.cookie.String()}), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var body struct {
		Job struct {
			ID string `json:"id"`
		} `json:"job"`
	}
	decodeInto(t, rec, &body)
	if body.Job.ID != fmt.Sprint(f.done) {
		t.Errorf("answer job = %q, want the same job %d", body.Job.ID, f.done)
	}
	got, err := f.q.Get(t.Context(), f.done)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != jobs.Waiting {
		t.Errorf("state = %s, want waiting", got.State)
	}
}

// TestRunAgainRefusesAJobThatDidNotFinish: a failed job answers 409 with the plain sentence, and nothing changes.
func TestRunAgainRefusesAJobThatDidNotFinish(t *testing.T) {
	f := newJobsFixture(t)
	rec := f.e.do(http.MethodPost, fmt.Sprintf("/api/v2/jobs/%d/convert-again", f.failed), "",
		write(map[string]string{"Cookie": f.cookie.String()}), false)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409; body %s", rec.Code, rec.Body)
	}
	code, message, _ := errorBody(t, rec)
	if code != "not_runnable" || message != "This job did not finish. Use Retry." {
		t.Errorf("error = %q %q", code, message)
	}
	if got, _ := f.q.Get(t.Context(), f.failed); got.State != jobs.Failed {
		t.Errorf("state = %s, want the failed job left failed", got.State)
	}
}

// TestRunAgainUnknownJob: an id that does not exist is a 404.
func TestRunAgainUnknownJob(t *testing.T) {
	f := newJobsFixture(t)
	rec := f.e.do(http.MethodPost, "/api/v2/jobs/999999/convert-again", "",
		write(map[string]string{"Cookie": f.cookie.String()}), false)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404; body %s", rec.Code, rec.Body)
	}
}
