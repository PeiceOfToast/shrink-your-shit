// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// overviewOf reads GET /overview as the signed-in person.
func overviewOf(t *testing.T, e *env, cookie string) map[string]any {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/v2/overview", "", map[string]string{"Cookie": cookie}, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("overview = %d %s", rec.Code, rec.Body)
	}
	var m map[string]any
	decodeInto(t, rec, &m)
	return m
}

func num(m map[string]any, path ...string) float64 {
	cur := any(m)
	for _, p := range path {
		cur = cur.(map[string]any)[p]
	}
	f, _ := cur.(float64)
	return f
}

// insertJob writes one job row for the overview's tests.
func insertJob(t *testing.T, e *env, state, outcome string, startedMS, finishedMS, before, after int64, fileID int64) {
	t.Helper()
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO jobs (file_id, library_id, workflow_id, path, state, outcome, queued_at, started_at, finished_at,
			size_before, size_after, queued_by, attempt, tries, top, preview)
			VALUES (?, 1, 1, ?, ?, ?, ?, ?, ?, ?, ?, 'test', 1, 1, 0, 0)`,
			fileID, fmt.Sprintf("/lib/f%d.mkv", fileID), state, outcome, startedMS-1000, startedMS, finishedMS, before, after)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// An install with nothing in it: every figure is its honest empty value and nothing errors.
func TestOverviewEmptyInstall(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	m := overviewOf(t, e, cookie.String())
	if num(m, "converted", "total") != 0 || num(m, "freed", "totalBytes") != 0 || num(m, "freed", "todayBytes") != 0 {
		t.Errorf("empty install: %+v", m)
	}
	if m["freed"].(map[string]any)["nextReleaseAt"] != nil || m["waiting"].(map[string]any)["nextScanAt"] != nil {
		t.Errorf("empty install should have null next-release and next-scan: %+v", m)
	}
	if m["converted"].(map[string]any)["perDay"] != float64(0) || len(m["series"].(map[string]any)["days"].([]any)) != 30 {
		t.Errorf("empty install: want perDay 0 and 30 series days, got %+v", m["converted"])
	}
	for _, d := range m["series"].(map[string]any)["days"].([]any) {
		if d.(map[string]any)["done"] != nil {
			t.Errorf("an empty day should be null: %+v", d)
			break
		}
	}
}

// wireJobs gives the test server its jobs queue, as the jobs routes' tests do, so the running jobs can be read.
func wireJobs(t *testing.T, e *env) {
	t.Helper()
	q := jobs.New(jobs.Options{DB: e.db, Bus: e.bus, Clock: e.clock.Now})
	e.srv.Jobs(q, library.NewCatalog(e.db), flow.Open(e.db), e.bus)
}

// Seeded jobs: all-time and today's savings, the 24-hour counts, the failed percent, and a running job.
func TestOverviewCountsFromTheJobRows(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	wireJobs(t, e)
	now := time.Now()
	nowMS := now.UnixMilli()
	insertJob(t, e, "done", "smaller", nowMS-60_000, nowMS-30_000, 1000, 600, 1) // saves 400, today
	insertJob(t, e, "done", "smaller", nowMS-120_000, nowMS-90_000, 500, 400, 2) // saves 100, today
	insertJob(t, e, "failed", "", nowMS-50_000, nowMS-40_000, 0, 0, 3)
	insertJob(t, e, "skipped", "", nowMS-50_000, nowMS-45_000, 0, 0, 4)
	insertJob(t, e, "running", "", nowMS-1000, 0, 0, 0, 5)
	m := overviewOf(t, e, cookie)
	if got := num(m, "converted", "total"); got != 2 {
		t.Errorf("converted.total = %v, want 2", got)
	}
	if got := num(m, "freed", "totalBytes"); got != 500 {
		t.Errorf("freed.totalBytes = %v, want 500", got)
	}
	if got := num(m, "freed", "todayBytes"); got != 500 {
		t.Errorf("freed.todayBytes = %v, want 500", got)
	}
	if num(m, "last24h", "done") != 2 || num(m, "last24h", "failed") != 1 || num(m, "last24h", "skipped") != 1 {
		t.Errorf("last24h = %+v", m["last24h"])
	}
	if got := num(m, "failed24h", "pct"); got != 100.0/4.0 {
		t.Errorf("failed24h.pct = %v, want 25 (a percent)", got)
	}
	if len(m["running"].([]any)) != 1 {
		t.Errorf("running = %+v, want the one running job", m["running"])
	}
}

// Local day: a job that finished a minute before local midnight counts toward yesterday, not today.
func TestOverviewLocalDayBoundary(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	midnight := time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), 0, 0, 0, 0, time.Local)
	yesterdayLate := midnight.Add(-time.Minute).UnixMilli()
	insertJob(t, e, "done", "smaller", yesterdayLate-10_000, yesterdayLate, 900, 300, 7)
	m := overviewOf(t, e, cookie)
	if got := num(m, "freed", "todayBytes"); got != 0 {
		t.Errorf("todayBytes = %v, want 0 (the job finished yesterday)", got)
	}
	days := m["series"].(map[string]any)["days"].([]any)
	yesterday := midnight.AddDate(0, 0, -1).Format("2006-01-02")
	found := false
	for _, d := range days {
		dm := d.(map[string]any)
		if dm["day"] == yesterday && dm["freedBytes"] == float64(600) && dm["done"] == float64(1) {
			found = true
		}
	}
	if !found {
		t.Errorf("series has no day %s with the job's 600 bytes", yesterday)
	}
}

// The trash: the saving still in it, and the next release day, from a real replace.
func TestOverviewTrashFromTheJournal(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	replacedFixture(t, e, false)
	m := overviewOf(t, e, cookie)
	// The fixture's converted file is bigger than its original, so the trash holds no saving; the figure is 0, not
	// negative. The next release still names the original's bytes.
	if got := num(m, "freed", "inTrashBytes"); got != 0 {
		t.Errorf("inTrashBytes = %v, want 0 for a converted file bigger than its original", got)
	}
	if num(m, "freed", "nextReleaseBytes") <= 0 {
		t.Errorf("nextReleaseBytes = %v, want the original's bytes", m["freed"])
	}
	if m["freed"].(map[string]any)["nextReleaseAt"] == nil || m["freed"].(map[string]any)["nextReleaseBytes"] == nil {
		t.Errorf("next release should be set while the trash holds an original: %+v", m["freed"])
	}
}

// The contract golden for ui-shop's Dashboard: the shape of an empty install's overview (S3-8a).
func TestContractOverview(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	rec := e.do(http.MethodGet, "/api/v2/overview", "", map[string]string{"Cookie": cookie}, false)
	writeGolden(t, "overview", "Overview", http.MethodGet, "/api/v2/overview", rec)
}

// #894 review: perDay is a whole number; 1 done in seven days is 0, 4 is 1.
func TestOverviewPerDayIsRoundedPerDay(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	now := time.Now().UnixMilli()
	insertJob(t, e, "done", "smaller", now-20_000, now-10_000, 1000, 500, 1)
	if got := num(overviewOf(t, e, cookie), "converted", "perDay"); got != 0 {
		t.Errorf("1 done in 7 days: perDay = %v, want 0", got)
	}
	for i := int64(2); i <= 4; i++ {
		insertJob(t, e, "done", "smaller", now-20_000, now-10_000, 1000, 500, i)
	}
	if got := num(overviewOf(t, e, cookie), "converted", "perDay"); got != 1 {
		t.Errorf("4 done in 7 days: perDay = %v, want 1", got)
	}
}

// #894 review: secPerGB divides by GB = 1e9 bytes, not GiB: 100 s over 2e9 bytes is 50.
func TestOverviewSecPerGBIsPerDecimalGB(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	now := time.Now().UnixMilli()
	insertJob(t, e, "done", "smaller", now-100_000, now, 2_000_000_000, 1_000_000_000, 1)
	if got := num(overviewOf(t, e, cookie), "avgShrink", "secPerGB"); got != 50 {
		t.Errorf("secPerGB = %v, want 50 (100 s over 2 GB)", got)
	}
}

// #894 review: avgShrink.pct reads the last seven local days, like secPerGB. An old saving of half does not count.
func TestOverviewShrinkPctIsTheSevenDayWindow(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	old := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	insertJob(t, e, "done", "smaller", old-20_000, old-10_000, 1000, 500, 1)
	now := time.Now().UnixMilli()
	insertJob(t, e, "done", "bigger_kept", now-20_000, now-10_000, 1000, 1000, 2)
	if got := num(overviewOf(t, e, cookie), "avgShrink", "pct"); got != 0 {
		t.Errorf("pct = %v, want 0 for the last 7 days (the old half saving is outside the window)", got)
	}
}

// #894 review: after the first finished job, a day with nothing is 0 in every figure; only days before it are null.
func TestOverviewSeriesZeroAfterTheFirstJob(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	twoDaysAgo := time.Now().Add(-48 * time.Hour).UnixMilli()
	insertJob(t, e, "done", "smaller", twoDaysAgo-20_000, twoDaysAgo-10_000, 1000, 600, 1)
	m := overviewOf(t, e, cookie)
	days := m["series"].(map[string]any)["days"].([]any)
	firstDay := time.UnixMilli(twoDaysAgo).In(time.Local).Format("2006-01-02")
	var sawFirst, sawLaterZero, sawEarlyNull bool
	for _, d := range days {
		dm := d.(map[string]any)
		day := dm["day"].(string)
		switch {
		case day < firstDay:
			sawEarlyNull = sawEarlyNull || dm["done"] == nil
		case day == firstDay:
			sawFirst = dm["done"] == float64(1)
		default:
			if dm["done"] == float64(0) && dm["freedBytes"] == float64(0) && dm["failed"] == float64(0) {
				sawLaterZero = true
			}
		}
	}
	if !sawFirst || !sawLaterZero || !sawEarlyNull {
		t.Errorf("series: first day ok=%v, later zero ok=%v, earlier null ok=%v", sawFirst, sawLaterZero, sawEarlyNull)
	}
}

// #894 review: a done job whose output came out bigger frees nothing, and freedBytes never goes negative.
func TestOverviewSavingIsClampedAtZero(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup().String()
	now := time.Now().UnixMilli()
	insertJob(t, e, "done", "smaller", now-20_000, now-10_000, 1000, 1500, 1)
	insertJob(t, e, "done", "smaller", now-20_000, now-10_000, 1000, 800, 2)
	m := overviewOf(t, e, cookie)
	if got := num(m, "freed", "totalBytes"); got != 200 {
		t.Errorf("totalBytes = %v, want 200 (the bigger output adds nothing)", got)
	}
}
