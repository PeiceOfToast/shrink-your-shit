// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"database/sql"
	"math"
	"net/http"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// overviewJSON is GET /overview, the Dashboard's numbers (page 08, Data and API). Every figure is read from the job rows,
// the trash journal and the libraries; a figure with no data is its honest empty value (0, or null where the spec says).
type overviewJSON struct {
	Freed struct {
		TotalBytes       int64   `json:"totalBytes"`
		TodayBytes       int64   `json:"todayBytes"`
		InTrashBytes     int64   `json:"inTrashBytes"`
		NextReleaseBytes *int64  `json:"nextReleaseBytes"`
		NextReleaseAt    *string `json:"nextReleaseAt"`
	} `json:"freed"`
	Converted struct {
		Total  int `json:"total"`
		PerDay int `json:"perDay"` // done jobs per day over the last 7 local days, rounded to a whole number
	} `json:"converted"`
	Last24h struct {
		Done    int `json:"done"`
		Skipped int `json:"skipped"`
		Failed  int `json:"failed"`
	} `json:"last24h"`
	Waiting struct {
		Count      int     `json:"count"`
		NextScanAt *string `json:"nextScanAt"`
	} `json:"waiting"`
	Failed24h struct {
		Count int     `json:"count"`
		Pct   float64 `json:"pct"`
	} `json:"failed24h"`
	AvgShrink struct {
		Pct      float64 `json:"pct"`
		SecPerGB float64 `json:"secPerGB"`
	} `json:"avgShrink"`
	Series struct {
		Days []seriesDay `json:"days"`
	} `json:"series"`
	Running   []jobJSON    `json:"running"`
	Libraries []libraryRow `json:"libraries"`
	WorkersOn bool         `json:"workersOn"`
}

// seriesDay is one day of the charts. Before the first finished job a day is null in every figure; from then on a day
// with nothing is 0 (types.ts: "null = no data that day", and the first job is the first data).
type seriesDay struct {
	Day        string `json:"day"`
	FreedBytes *int64 `json:"freedBytes"`
	TrashBytes *int64 `json:"trashBytes"`
	Done       *int   `json:"done"`
	Skipped    *int   `json:"skipped"`
	Failed     *int   `json:"failed"`
}

// finishedRow is one finished job, the columns the overview reads.
type finishedRow struct {
	fileID     int64
	state      string
	outcome    string
	startedMS  int64
	finishedMS int64
	before     int64
	after      int64
	hasSizes   bool
}

// savedOf is what a finished job freed: the source minus its output, never below zero (a job whose output came out
// bigger frees nothing, so such jobs add 0 to every saving).
func savedOf(row finishedRow) int64 {
	if !row.hasSizes {
		return 0
	}
	if s := row.before - row.after; s > 0 {
		return s
	}
	return 0
}

// localDay is the server's local calendar day of a millisecond time, as YYYY-MM-DD.
func localDay(ms int64) string { return time.UnixMilli(ms).In(time.Local).Format("2006-01-02") }

// dayCounts is what one local day adds up to in the series.
type dayCounts struct {
	freed, done, skipped, failed int64
}

// getOverview is GET /overview.
func (s *Server) getOverview(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	ctx := r.Context()
	now := time.Now()
	nowMS := now.UnixMilli()
	todayStr := now.In(time.Local).Format("2006-01-02")
	rows, err := finishedRows(ctx, rs.DB)
	if err != nil {
		internalError(w)
		return
	}
	var out overviewJSON
	out.WorkersOn = true // Stage 1 has one worker and no all-workers switch (summary.go), so it is on.

	// The 30 series days, oldest first, and the seven perDay days.
	var series []seriesDay
	seriesIdx := map[string]int{}
	counts := make([]dayCounts, 30)
	for i := 29; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).In(time.Local).Format("2006-01-02")
		seriesIdx[d] = len(series)
		series = append(series, seriesDay{Day: d})
	}
	dayDone := map[string]int{}
	weekAgo := nowMS - 7*24*60*60*1000
	var weekBefore, weekSaved, weekMS int64
	var firstMS int64 = -1
	for _, row := range rows {
		day := localDay(row.finishedMS)
		if firstMS < 0 || row.finishedMS < firstMS {
			firstMS = row.finishedMS
		}
		if row.state == "done" {
			out.Converted.Total++
			dayDone[day]++
		}
		if row.state == "done" && row.outcome == "smaller" {
			saved := savedOf(row)
			out.Freed.TotalBytes += saved
			if day == todayStr {
				out.Freed.TodayBytes += saved
			}
		}
		if i, ok := seriesIdx[day]; ok {
			c := &counts[i]
			switch row.state {
			case "done":
				c.done++
				if row.outcome == "smaller" {
					c.freed += savedOf(row)
				}
			case "skipped":
				c.skipped++
			case "failed":
				c.failed++
			}
		}
		if row.state == "done" && row.finishedMS >= weekAgo {
			weekBefore += row.before
			weekSaved += savedOf(row)
			weekMS += row.finishedMS - row.startedMS
		}
	}
	// perDay: done jobs per day over the last seven local days, rounded to a whole number.
	var sevenDone int
	for i := 6; i >= 0; i-- {
		sevenDone += dayDone[now.AddDate(0, 0, -i).In(time.Local).Format("2006-01-02")]
	}
	out.Converted.PerDay = int(math.Round(float64(sevenDone) / 7))
	// secPerGB and pct read the same window: the done jobs of the last seven local days. GB is 1e9 bytes.
	if weekBefore > 0 {
		out.AvgShrink.SecPerGB = (float64(weekMS) / 1000) / (float64(weekBefore) / 1e9)
		out.AvgShrink.Pct = 100 * float64(weekSaved) / float64(weekBefore)
	}

	// From the first finished job's day on, a day with nothing is 0; days before it are null, and with no job at all
	// every day is null. trashBytes stays null: the journal keeps no per-day source for it (documented in the PR).
	if firstMS >= 0 {
		firstDay := localDay(firstMS)
		for i := range series {
			if series[i].Day < firstDay {
				continue
			}
			c := counts[i]
			freed := c.freed
			done, skipped, failed := int(c.done), int(c.skipped), int(c.failed)
			series[i].FreedBytes = &freed
			series[i].Done, series[i].Skipped, series[i].Failed = &done, &skipped, &failed
		}
	}
	out.Series.Days = series

	// The last 24 hours, each file counted once by its latest end (as the page does).
	latest := map[int64]finishedRow{}
	for _, row := range rows {
		if prev, ok := latest[row.fileID]; !ok || row.finishedMS > prev.finishedMS {
			latest[row.fileID] = row
		}
	}
	dayAgo := nowMS - 24*60*60*1000
	for _, row := range latest {
		if row.finishedMS < dayAgo {
			continue
		}
		switch row.state {
		case "done":
			out.Last24h.Done++
		case "skipped":
			out.Last24h.Skipped++
		case "failed":
			out.Last24h.Failed++
		}
	}
	out.Failed24h.Count = out.Last24h.Failed
	if ended := out.Last24h.Done + out.Last24h.Skipped + out.Last24h.Failed; ended > 0 {
		out.Failed24h.Pct = 100 * float64(out.Last24h.Failed) / float64(ended)
	}

	// Waiting, and the running jobs in the Jobs list's shape.
	waiting, err := countWaiting(ctx, rs.DB)
	if err != nil {
		internalError(w)
		return
	}
	out.Waiting.Count = waiting
	libs, err := rs.Catalog.ListLibraries(ctx)
	if err != nil {
		internalError(w)
		return
	}
	if next := earliestScan(libs); next != nil {
		at := rfc3339(*next)
		out.Waiting.NextScanAt = &at
	}
	if d := s.jobs.Load(); d != nil {
		running, err := runningIDs(ctx, rs.DB)
		if err != nil {
			internalError(w)
			return
		}
		for _, id := range running {
			j, err := d.queue.Get(ctx, id)
			if err != nil {
				continue
			}
			out.Running = append(out.Running, s.view(ctx, d, j))
		}
	}
	if out.Running == nil {
		out.Running = []jobJSON{}
	}

	// The trash: what is still waiting, and when the next release is.
	j := safety.NewJournal(rs.DB)
	entries, err := j.Trash(ctx)
	if err != nil {
		internalError(w)
		return
	}
	keep, err := j.KeepDays(ctx)
	if err != nil {
		internalError(w)
		return
	}
	var next *time.Time
	for _, e := range entries {
		// A saving is never negative: a converted file bigger than its original saves nothing.
		if saving := e.OrigBytes - e.NewBytes; saving > 0 {
			out.Freed.InTrashBytes += saving
		}
		until := e.When.Add(time.Duration(keep) * 24 * time.Hour)
		if next == nil || until.Before(*next) {
			next = &until
		}
	}
	if next != nil {
		nextDay := next.In(time.Local).Format("2006-01-02")
		var nextBytes int64
		for _, e := range entries {
			until := e.When.Add(time.Duration(keep) * 24 * time.Hour)
			if until.In(time.Local).Format("2006-01-02") == nextDay {
				nextBytes += e.OrigBytes
			}
		}
		at := rfc3339(*next)
		out.Freed.NextReleaseAt = &at
		out.Freed.NextReleaseBytes = &nextBytes
	}

	// The Libraries page's rows, the same as GET /libraries.
	counts2, err := rs.Catalog.CountFiles(ctx)
	if err != nil {
		internalError(w)
		return
	}
	out.Libraries = []libraryRow{}
	for _, lib := range libs {
		row, err := s.libraryRowOf(r, lib, counts2[lib.ID])
		if err != nil {
			internalError(w)
			return
		}
		out.Libraries = append(out.Libraries, row)
	}
	writeJSON(w, http.StatusOK, out)
}

// finishedRows reads every finished job the overview counts: done, skipped and failed.
func finishedRows(ctx context.Context, db *store.DB) ([]finishedRow, error) {
	rs, err := db.Read().QueryContext(ctx, `SELECT file_id, state, COALESCE(outcome, ''), COALESCE(started_at, finished_at),
		finished_at, size_before, size_after FROM jobs WHERE finished_at IS NOT NULL AND state IN ('done', 'skipped', 'failed')`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []finishedRow
	for rs.Next() {
		var r finishedRow
		var before, after sql.NullInt64
		if err := rs.Scan(&r.fileID, &r.state, &r.outcome, &r.startedMS, &r.finishedMS, &before, &after); err != nil {
			return nil, err
		}
		r.before, r.after = before.Int64, after.Int64
		r.hasSizes = before.Valid && after.Valid
		out = append(out, r)
	}
	return out, rs.Err()
}

// countWaiting is the jobs waiting or waiting to retry.
func countWaiting(ctx context.Context, db *store.DB) (int, error) {
	var n int
	err := db.Read().QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE state IN ('waiting', 'retry_wait')`).Scan(&n)
	return n, err
}

// runningIDs are the jobs running now.
func runningIDs(ctx context.Context, db *store.DB) ([]int64, error) {
	rs, err := db.Read().QueryContext(ctx, `SELECT id FROM jobs WHERE state = 'running' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var ids []int64
	for rs.Next() {
		var id int64
		if err := rs.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rs.Err()
}
