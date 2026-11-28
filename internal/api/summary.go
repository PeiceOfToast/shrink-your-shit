// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// Attention is one row of GET /attention, the shape types.ts names. action and link are null: a replace row has no
// button yet.
type Attention struct {
	ID       string           `json:"id"`
	Level    string           `json:"level"`
	What     string           `json:"what"`
	Means    string           `json:"means"`
	Count    int              `json:"count"`
	LatestAt string           `json:"latestAt"`
	Action   *attentionAction `json:"action"`
	Link     *string          `json:"link"`
}

// attentionAction is a row's button (page 08): a route the UI calls and the body it sends as is, so the UI never
// builds a request of its own.
type attentionAction struct {
	Label  string `json:"label"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Body   any    `json:"body,omitempty"`
}

// attentionWindow is how far back the event groups look: one day, the span the Dashboard's "failed in the last 24
// hours" number covers.
const attentionWindow = 24 * time.Hour

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

// getAttention is the Dashboard's Needs attention list (page 08): one row per problem, built from the recent warn
// and error events grouped by source, plus a replace whose swap could not be settled or put back.
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
	groups, err := s.attentionGroups(r.Context(), rs)
	if err != nil {
		internalError(w)
		return
	}
	out = append(out, groups...)
	writeJSON(w, http.StatusOK, out)
}

// attentionGroup is one source's recent warn and error events before they become a row.
type attentionGroup struct {
	source string
	level  events.Level
	count  int                 // events that are not about a job
	latest time.Time           // the latest of those
	jobAt  map[int64]time.Time // each job's latest error event
}

// attentionGroups groups the last day's warn and error events by source: one row per source, the jobs' failures as
// their own row of the files still failed (page 08: "each row is a group of recent warn/error events on the same
// thing"). A job that failed and has since finished drops out, because nothing about it is waiting on the person.
func (s *Server) attentionGroups(ctx context.Context, rs *Deps) ([]Attention, error) {
	if rs.Events == nil {
		return nil, nil
	}
	stored, _, err := rs.Events.Query(ctx, events.Filter{MinLevel: events.LevelWarn, Since: rs.now().Add(-attentionWindow)},
		events.Page{Limit: 1000, Newest: true})
	if err != nil {
		return nil, err
	}
	groups := map[string]*attentionGroup{}
	var order []string
	for _, ev := range stored {
		source := ev.Source
		if source == "" {
			source = "app"
			if ev.JobID != 0 {
				source = "jobs" // a job's own step events carry no source of their own
			}
		}
		if ev.JobID != 0 && ev.Level != events.LevelError {
			continue // a retry's warning is not a failure waiting on the person
		}
		g := groups[source]
		if g == nil {
			g = &attentionGroup{source: source, level: events.LevelWarn}
			groups[source] = g
			order = append(order, source)
		}
		if ev.Level == events.LevelError {
			g.level = events.LevelError
		}
		if ev.JobID != 0 {
			if g.jobAt == nil {
				g.jobAt = map[int64]time.Time{}
			}
			if ev.TS.After(g.jobAt[ev.JobID]) {
				g.jobAt[ev.JobID] = ev.TS
			}
		} else {
			g.count++
			if ev.TS.After(g.latest) {
				g.latest = ev.TS
			}
		}
	}
	out := make([]Attention, 0, len(order))
	for _, source := range order {
		a, err := s.attentionRow(ctx, rs, groups[source])
		if err != nil {
			return nil, err
		}
		if a != nil {
			out = append(out, *a)
		}
	}
	// The worst rows first, then the latest: the card's tone follows the level.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level == "error"
		}
		if out[i].LatestAt != out[j].LatestAt {
			return out[i].LatestAt > out[j].LatestAt // RFC 3339 UTC sorts as text
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// attentionRow turns one source's group into a row, or nil when nothing in it still needs the person.
func (s *Server) attentionRow(ctx context.Context, rs *Deps, g *attentionGroup) (*Attention, error) {
	a := &Attention{ID: "events-" + g.source, Level: string(g.level), Count: g.count,
		LatestAt: g.latest.UTC().Format(time.RFC3339)}
	if g.source != "jobs" {
		a.What, a.Means, a.Link = attentionWords(g.source)
		return a, nil
	}
	if rs.Jobs == nil {
		return nil, nil
	}
	var ids []int64
	var latest time.Time
	var only string
	for id, at := range g.jobAt {
		j, err := rs.Jobs.Get(ctx, id)
		if errors.Is(err, jobs.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if j.State != jobs.Failed {
			continue
		}
		ids = append(ids, id)
		if at.After(latest) {
			latest = at
		}
		only = filepath.Base(j.Path)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	a.Count = len(ids)
	a.LatestAt = latest.UTC().Format(time.RFC3339)
	if len(ids) == 1 {
		a.What = only + " failed"
		a.Means = "The new file was not written, so the original is still in place. Retry runs it again, or open the job to see why."
	} else {
		a.What = fmt.Sprintf("%d files stopped before they were saved", len(ids))
		a.Means = "Their new files were not written, so the originals are still in place. Retry runs them again, or open a job to see why."
	}
	actionIDs := make([]string, len(ids))
	for i, id := range ids {
		actionIDs[i] = strconv.FormatInt(id, 10)
	}
	a.Action = &attentionAction{Label: "Retry", Method: http.MethodPost, Path: "/api/v2/jobs/actions",
		Body: map[string]any{"action": "retry", "ids": actionIDs}}
	link := "/jobs?state=failed"
	a.Link = &link
	return a, nil
}

// attentionWords is a source's row in plain words, and where its link opens: the page's "links open the jobs, or
// the System log filtered to that source". A source with no words of its own still gets a row that points at the log.
func attentionWords(source string) (what, means string, link *string) {
	logLink := func() *string {
		path := "/settings/log"
		if source != "" && source != "app" {
			path += "?source=" + url.QueryEscape(source)
		}
		return &path
	}
	switch source {
	case "hw":
		return "This computer's graphics encoder kept failing",
			"Files convert on the processor while it does, which is slower.", logLink()
	case "worker":
		return "The worker reported a problem",
			"A job may have stopped early. The System log has the details.", logLink()
	case "safety":
		return "A replaced original needs a look",
			"The original is in the library's " + safety.TrashDir + " folder and nothing was removed. Check it before you use the file again.",
			logLink()
	case "health":
		return "A health check failed",
			"Something the app relies on isn't working. The System log has the details.", logLink()
	case "library":
		libraries := "/libraries"
		return "A library had a problem",
			"Its files may not be converting. Check the library and the System log.", &libraries
	default:
		return "Something needs a look", "The System log has the details.", logLink()
	}
}
