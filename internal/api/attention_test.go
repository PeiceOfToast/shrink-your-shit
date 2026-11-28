// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// attentionItem is the part of a Needs attention row these tests read.
type attentionItem struct {
	ID       string `json:"id"`
	Level    string `json:"level"`
	What     string `json:"what"`
	Means    string `json:"means"`
	Count    int    `json:"count"`
	LatestAt string `json:"latestAt"`
	Action   *struct {
		Label  string `json:"label"`
		Method string `json:"method"`
		Path   string `json:"path"`
		Body   struct {
			Action string   `json:"action"`
			IDs    []string `json:"ids"`
		} `json:"body"`
	} `json:"action"`
	Link *string `json:"link"`
}

// mustAttentionLibrary adds the one library every attention test starts from.
func mustAttentionLibrary(t *testing.T, e *env) library.Library {
	t.Helper()
	wfID, _, err := e.flows.CreateFromStarter(context.Background(), "shrink-videos", "Test")
	if err != nil {
		t.Fatal(err)
	}
	return mustAddLibraryWithWorkflow(t, e, 1, wfID, t.TempDir())
}

// mustFailedJob leaves one job of the library failed, and returns its id.
func mustFailedJob(t *testing.T, e *env, lib library.Library, rel string) int64 {
	t.Helper()
	ctx := context.Background()
	id := mustEnqueueWaiting(t, e, lib, rel)[0]
	job, err := e.queue.Claim(ctx, "worker-1", jobs.ClaimOptions{})
	if err != nil || job == nil {
		t.Fatalf("claim %s: %+v, %v", rel, job, err)
	}
	if err := e.queue.Finish(ctx, id, job.Attempt, jobs.Result{
		Verdict: jobs.VerdictFail, Reason: "The new file was shorter than the original.",
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

// emitAttention emits one event with the time the test picked. The grouping tests leave the server's clock at its
// default (the wall clock) and stamp their events then; only the contract golden fixes the clock.
func emitAttention(t *testing.T, e *env, ev events.Event) {
	t.Helper()
	if err := e.bus.Emit(ev); err != nil {
		t.Fatal(err)
	}
}

// attentionSince is the start of the window when the server has no fixed clock: the last day of the wall clock.
func attentionSince() time.Time { return time.Now().Add(-attentionWindow) }

// waitAttentionEvents waits until the bus's database sink has written n warn or error events at or after since. The
// sink batches, so the wait is on its condition, not on a sleep sized to it.
func waitAttentionEvents(t *testing.T, e *env, since time.Time, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got int
		if err := e.db.Read().QueryRow(`SELECT count(*) FROM events WHERE level IN ('warn', 'error') AND ts >= ?`,
			since.UnixMilli()).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("warn/error events in the window = %d, want %d", got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// getAttentionItems reads GET /attention.
func getAttentionItems(t *testing.T, e *env, cookie string) []attentionItem {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/v2/attention", "", map[string]string{"Cookie": cookie}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	var items []attentionItem
	decodeInto(t, rec, &items)
	return items
}

// TestAttentionGroupsFailedJobsIntoOneRow: several failed files are one row with the count, the latest time and a
// Retry that names exactly those jobs (page 08's Needs attention group).
func TestAttentionGroupsFailedJobsIntoOneRow(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	lib := mustAttentionLibrary(t, e)
	first := mustFailedJob(t, e, lib, "first.mkv")
	second := mustFailedJob(t, e, lib, "second.mkv")
	now := time.Now().Truncate(time.Second)
	emitAttention(t, e, events.Event{JobID: first, Attempt: 1, Level: events.LevelError, Kind: events.KindFailed,
		Source: "jobs", Message: "The new file was shorter than the original.", TS: now.Add(-2 * time.Minute)})
	emitAttention(t, e, events.Event{JobID: second, Attempt: 1, Level: events.LevelError, Kind: events.KindFailed,
		Source: "jobs", Message: "The new file was shorter than the original.", TS: now})
	waitAttentionEvents(t, e, attentionSince(), 2)

	items := getAttentionItems(t, e, cookie.String())
	if len(items) != 1 {
		t.Fatalf("attention = %d rows, want the one jobs group", len(items))
	}
	a := items[0]
	if a.ID != "events-jobs" || a.Level != "error" || a.Count != 2 {
		t.Fatalf("row = %+v; want the jobs group, error, count 2", a)
	}
	if a.What != "2 files stopped before they were saved" {
		t.Errorf("what = %q", a.What)
	}
	if a.LatestAt != now.UTC().Format(time.RFC3339) {
		t.Errorf("latestAt = %q, want the second failure's time %q", a.LatestAt, now.UTC().Format(time.RFC3339))
	}
	if a.Action == nil || a.Action.Label != "Retry" || a.Action.Method != http.MethodPost || a.Action.Path != "/api/v2/jobs/actions" {
		t.Fatalf("action = %+v, want Retry on /api/v2/jobs/actions", a.Action)
	}
	if a.Action.Body.Action != "retry" {
		t.Errorf("action body action = %q, want retry", a.Action.Body.Action)
	}
	want := []string{strconv.FormatInt(first, 10), strconv.FormatInt(second, 10)}
	if len(a.Action.Body.IDs) != 2 || a.Action.Body.IDs[0] != want[0] || a.Action.Body.IDs[1] != want[1] {
		t.Errorf("retry ids = %v, want %v", a.Action.Body.IDs, want)
	}
	if a.Link == nil || *a.Link != "/jobs?state=failed" {
		t.Errorf("link = %v, want /jobs?state=failed", a.Link)
	}
}

// TestAttentionLevelIsTheHighestInTheGroup: a group with one error is an error row, and a group of warnings alone
// keeps the warn tone. Each source is a row of its own, and an event older than the window is not in it.
func TestAttentionLevelIsTheHighestInTheGroup(t *testing.T) {
	e := newEnv(t, true)
	useWallClock(e)
	cookie := e.setup()
	now := time.Now().Truncate(time.Second)
	for _, ev := range []events.Event{
		{Level: events.LevelWarn, Kind: events.KindWarning, Source: "library", Message: "The folder is not reachable.", TS: now},
		{Level: events.LevelWarn, Kind: events.KindWarning, Source: "hw", Message: "hevc paused for 60 min", TS: now},
		{Level: events.LevelError, Kind: events.KindError, Source: "hw", Message: "The graphics encoder stopped.", TS: now},
		{Level: events.LevelError, Kind: events.KindError, Source: "hw", Message: "The graphics encoder stopped.", TS: now.Add(-25 * time.Hour)},
	} {
		emitAttention(t, e, ev)
	}
	waitAttentionEvents(t, e, attentionSince(), 3)

	items := getAttentionItems(t, e, cookie.String())
	if len(items) != 2 {
		t.Fatalf("attention = %d rows, want one per source", len(items))
	}
	// The worst first.
	if items[0].ID != "events-hw" || items[0].Level != "error" || items[0].Count != 2 {
		t.Errorf("first row = %+v, want the hw group as an error of 2 events inside the window", items[0])
	}
	if items[0].Action != nil {
		t.Errorf("hw action = %+v; there is no encoder test route yet, so it must be null", items[0].Action)
	}
	if items[1].ID != "events-library" || items[1].Level != "warn" {
		t.Errorf("second row = %+v, want the library group as a warning", items[1])
	}
	if items[1].Link == nil || *items[1].Link != "/libraries" {
		t.Errorf("library link = %v, want /libraries", items[1].Link)
	}
}

// TestAttentionRetryListsOnlyJobsStillFailed: a failed file and a job that failed and has since finished are not
// one problem. Only the job still failed is counted and retried.
func TestAttentionRetryListsOnlyJobsStillFailed(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, true)
	cookie := e.setup()
	lib := mustAttentionLibrary(t, e)

	failed := mustFailedJob(t, e, lib, "broken.mkv")

	// A job that failed once and was then run to done: its old error event is still inside the window.
	done := mustEnqueueWaiting(t, e, lib, "finish.mkv")[0]
	job, err := e.queue.Claim(ctx, "worker-1", jobs.ClaimOptions{})
	if err != nil || job == nil {
		t.Fatalf("claim: %+v, %v", job, err)
	}
	if err := e.queue.Finish(ctx, done, job.Attempt, jobs.Result{Verdict: jobs.VerdictDone, Outcome: "smaller"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	for _, id := range []int64{failed, done} {
		emitAttention(t, e, events.Event{JobID: id, Attempt: job.Attempt, Level: events.LevelError,
			Kind: events.KindFailed, Source: "jobs", Message: "The new file was shorter than the original.", TS: now})
	}
	waitAttentionEvents(t, e, attentionSince(), 2)

	items := getAttentionItems(t, e, cookie.String())
	if len(items) != 1 {
		t.Fatalf("attention = %d rows, want the jobs group", len(items))
	}
	a := items[0]
	if a.Count != 1 || a.What != "broken.mkv failed" {
		t.Errorf("row = %+v; want the one file still failed", a)
	}
	if a.Action == nil || len(a.Action.Body.IDs) != 1 || a.Action.Body.IDs[0] != strconv.FormatInt(failed, 10) {
		t.Errorf("retry ids = %+v, want only the failed job %d", a.Action, failed)
	}
}

// TestAttentionIsEmptyWithNothingToShow: a fresh install has an empty list, not null, so the card can hide itself.
func TestAttentionIsEmptyWithNothingToShow(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodGet, "/api/v2/attention", "", map[string]string{"Cookie": cookie.String()}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Errorf("body = %s, want []", body)
	}
}

// TestContractAttention pins the filled list at a fixed clock (S3-8b): the jobs row with its Retry action, and a
// source's row from the log. The clock is the server's seam, so the golden's times are the same on every day. The
// failed job is a row written straight into the table, so the queue's own events do not join the golden.
func TestContractAttention(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	insertJob(t, e, "failed", "", now.Add(-10*time.Minute).UnixMilli(), now.Add(-9*time.Minute).UnixMilli(), 0, 0, 1)
	d := e.deps()
	d.Now = func() time.Time { return now }
	e.srv.Ready(d)

	emitAttention(t, e, events.Event{JobID: 1, Attempt: 1, Level: events.LevelError, Kind: events.KindFailed,
		Source: "jobs", Message: "The new file was shorter than the original.", TS: now.Add(-3 * time.Minute)})
	emitAttention(t, e, events.Event{Level: events.LevelError, Kind: events.KindError, Source: "hw",
		Message: "The graphics encoder stopped.", TS: now.Add(-5 * time.Minute)})
	waitAttentionEvents(t, e, now.Add(-attentionWindow), 2)

	items := getAttentionItems(t, e, cookie)
	if len(items) != 2 || items[0].ID != "events-jobs" || items[1].ID != "events-hw" {
		t.Fatalf("rows = %+v, want the jobs row and the hw row", items)
	}
	rec := e.do(http.MethodGet, "/api/v2/attention", "", map[string]string{"Cookie": cookie}, false)
	writeGolden(t, "attention", "Attention[]", http.MethodGet, "/api/v2/attention", rec)
}
