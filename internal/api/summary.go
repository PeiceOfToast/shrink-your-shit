// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// Attention is one row of GET /attention, the shape types.ts names. action and link are null: a replace row has no
// button yet.
type Attention struct {
	ID       string `json:"id"`
	Level    string `json:"level"`
	What     string `json:"what"`
	Means    string `json:"means"`
	Count    int    `json:"count"`
	LatestAt string `json:"latestAt"`
	Action   any    `json:"action"`
	Link     any    `json:"link"`
}

// The shell's counts (GET /summary) and the Dashboard's Needs attention list (GET /attention).

// summaryRow is the Summary shape in types.ts: the counts the shell shows without loading a page.
// Stage 1 has one built-in worker and no all-workers switch, so workers is 1 and paused is false.
type summaryRow struct {
	Converting struct {
		Running int `json:"running"`
		Workers int `json:"workers"`
	} `json:"converting"`
	Queue          int     `json:"queue"`
	LastDoneAt     *string `json:"lastDoneAt"`
	NextScanAt     *string `json:"nextScanAt"`
	DiskFreeBytes  uint64  `json:"diskFreeBytes"`
	Failed24h      int     `json:"failed24h"`
	WorkersOffline int     `json:"workersOffline"`
	NewErrors      int     `json:"newErrors"`
	SeenSeq        int     `json:"seenSeq"`
	SeenAt         *string `json:"seenAt"`
	HealthFailing  bool    `json:"healthFailing"`
	Workers        struct {
		Paused    bool `json:"paused"`
		Finishing int  `json:"finishing"`
	} `json:"workers"`
}

func (s *Server) getSummary(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	ctx := r.Context()
	tally, err := rs.Jobs.Tally(ctx, 0)
	if err != nil {
		internalError(w)
		return
	}
	libs, err := rs.Catalog.ListLibraries(ctx)
	if err != nil {
		internalError(w)
		return
	}
	var out summaryRow
	out.Converting.Running = tally.Running
	out.Converting.Workers = 1
	out.Queue = tally.Waiting
	if tally.LastDoneAt != nil {
		at := rfc3339(*tally.LastDoneAt)
		out.LastDoneAt = &at
	}
	if next := earliestScan(libs); next != nil {
		at := rfc3339(*next)
		out.NextScanAt = &at
	}
	if rs.DiskFree != nil {
		if free, err := rs.DiskFree(rs.DB.Dir()); err == nil {
			out.DiskFreeBytes = free
		}
	}
	out.Failed24h = tally.Failed24h
	seen, err := readLogSeen(ctx, rs.DB)
	if err != nil {
		internalError(w)
		return
	}
	out.SeenSeq = int(seen.Seq)
	if seen.At != "" {
		at := seen.At
		out.SeenAt = &at
	}
	if rs.Events != nil {
		counts, err := rs.Events.Counts(ctx, events.Filter{AppOnly: true, AfterRowID: seen.Seq})
		if err != nil {
			internalError(w)
			return
		}
		out.NewErrors = counts[events.LevelError]
	}
	out.Workers.Paused = false
	out.Workers.Finishing = 0
	writeJSON(w, http.StatusOK, out)
}

// earliestScan is the next scan any watched library has, or nil when none is watched.
func earliestScan(libs []library.Library) *time.Time {
	var out *time.Time
	for _, lib := range libs {
		if lib.Paused || lib.NextScanAt == nil {
			continue
		}
		if out == nil || lib.NextScanAt.Before(*out) {
			at := *lib.NextScanAt
			out = &at
		}
	}
	return out
}

// getAttention is the Dashboard's Needs attention list. A replace whose swap could not be settled or put back is one
// row: the original is in the library's trash, and nothing was removed.
func (s *Server) getAttention(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	rows, err := safety.NewJournal(rs.DB).Attention(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	out := make([]Attention, 0, len(rows))
	for _, a := range rows {
		out = append(out, Attention{
			ID:       "replace-" + strconv.FormatInt(a.Row.ID, 10),
			Level:    "warn",
			What:     filepath.Base(a.Row.Original) + " could not be finished",
			Means:    "The original is in the library's " + safety.TrashDir + " folder and nothing was removed. Check the folder before you run this file again.",
			Count:    1,
			LatestAt: a.When.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
