// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

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

// getAttention is the Dashboard's Needs attention list. Stage 1 keeps no attention rows: the
// problems it will show (a failing share, an offline worker) do not exist yet.
func (s *Server) getAttention(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}
