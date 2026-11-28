// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// The System log page (page 12): the app-wide events, their sources, a text download, and the seen mark that the
// status line's new errors count from. The lines are the events table's app-wide rows. Their "seq" is the row id.

const (
	logSeenKey     = "log.seen"
	logPageDefault = 100
	logPageMax     = 200
	logDownloadMax = 50000
	logFoldWindow  = 10 * time.Minute
)

// logSources is the list the page's source filter offers, in the names a person reads. A line's source is shown as its
// display name, and the filter matches the stored names behind it. A new worker is added here when the workers land (#896).
var logSources = []string{"Scanner", "Web", "Updates", "Notifications", "Health check"}

// storedToDisplay maps a stored source (the lowercase names the emitters write today) to the name the page shows.
// A stored name with no entry is shown as it is.
var storedToDisplay = map[string]string{
	"library":  "Scanner",
	"safety":   "Scanner",
	"settings": "Web",
	"api":      "Web", // page 12: API errors are under Web
}

func displayOf(stored string) string {
	if d, ok := storedToDisplay[stored]; ok {
		return d
	}
	return stored
}

// storedFor is every stored source that shows as display, plus display itself.
func storedFor(display string) []string {
	out := []string{display}
	for stored, d := range storedToDisplay {
		if d == display {
			out = append(out, stored)
		}
	}
	return out
}

// appLine is one app-wide line. Seq is the row id; before, since and seen all use it.
type appLine struct {
	Seq       int64      `json:"seq"`
	TS        string     `json:"ts"`
	Level     string     `json:"level"`
	Kind      string     `json:"kind"`
	Source    string     `json:"source"`
	Message   string     `json:"message"`
	JobID     *string    `json:"jobId"`
	LibraryID *string    `json:"libraryId"`
	WorkerID  *string    `json:"workerId"`
	Repeat    *logRepeat `json:"repeat,omitempty"`
}

// logRepeat is a folded run: count includes the shown line, firstAt is the oldest in the run, lastAt the newest.
type logRepeat struct {
	Count   int    `json:"count"`
	FirstAt string `json:"firstAt"`
	LastAt  string `json:"lastAt"`
}

type logPage struct {
	Items      []appLine `json:"items"`
	Counts     logCounts `json:"counts"`
	NextBefore *int64    `json:"nextBefore,omitempty"`
}

type logCounts struct {
	Info  int `json:"info"`
	Warn  int `json:"warn"`
	Error int `json:"error"`
}

// logSeen is what the seen mark stores: the newest line the person has seen, and when they marked it.
type logSeen struct {
	Seq int64  `json:"seq"`
	At  string `json:"at,omitempty"`
}

// logQuery is the parsed filter and paging of a request.
type logQuery struct {
	filter events.Filter
	before int64
	limit  int
}

// logFilter parses the filters shared by the list and the download. It writes the 400 itself and returns false on a
// bad value. A level is info, warn or error; debug lines show only with detail=1.
func logFilter(w http.ResponseWriter, r *http.Request) (logQuery, bool) {
	q := r.URL.Query()
	var out logQuery
	f := events.Filter{AppOnly: true, Library: q.Get("library"), Worker: q.Get("worker"), Text: q.Get("q")}
	if src := q.Get("source"); src != "" {
		f.Sources = storedFor(src)
	}
	for field, v := range map[string]string{"source": q.Get("source"), "library": f.Library, "worker": f.Worker} {
		if len(v) > 64 {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Keep this filter under 64 characters.", field, 0)
			return out, false
		}
	}
	if len(f.Text) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_setting", "Keep the search under 200 characters.", "q", 0)
		return out, false
	}
	detail := q.Get("detail")
	if detail != "" && detail != "0" && detail != "1" {
		writeError(w, http.StatusBadRequest, "invalid_setting", "Use detail=1 to show debug lines, or leave it out.", "detail", 0)
		return out, false
	}
	// Empty is every level but debug (info and up). A named level is exactly that level, as the page's tabs are.
	switch q.Get("level") {
	case "":
		if detail != "1" {
			f.MinLevel = events.LevelInfo
		}
	case "info":
		f.Level = events.LevelInfo
	case "warn":
		f.Level = events.LevelWarn
	case "error":
		f.Level = events.LevelError
	default:
		writeError(w, http.StatusBadRequest, "invalid_setting", "Pick info, warnings or errors.", "level", 0)
		return out, false
	}
	for field, dst := range map[string]*time.Time{"from": &f.Since, "to": &f.Until} {
		v := q.Get(field)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Use a time like 2026-11-28T06:00:00Z.", field, 0)
			return out, false
		}
		*dst = t
	}
	if v := q.Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Use the seq of a line you have seen.", "since", 0)
			return out, false
		}
		f.AfterRowID = n
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Use the nextBefore from the last page.", "before", 0)
			return out, false
		}
		out.before = n
	}
	out.limit = logPageDefault
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "invalid_setting", "Pick from 1 to 200 lines a page.", "limit", 0)
			return out, false
		}
		out.limit = min(n, logPageMax)
	}
	out.filter = f
	return out, true
}

// getLog is GET /api/v2/log: one page of app-wide lines, newest first, with the counts for the whole filter.
func (s *Server) getLog(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil || rs.Events == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	lq, ok := logFilter(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	evs, next, err := rs.Events.Query(ctx, lq.filter, events.Page{Limit: lq.limit, Newest: true, Cursor: lq.before})
	if err != nil {
		internalError(w)
		return
	}
	counts, err := rs.Events.Counts(ctx, lq.filter)
	if err != nil {
		internalError(w)
		return
	}
	// The page reads top to bottom with new lines last, so the newest page comes back oldest first. The query walks
	// newest first to find it; before pages back from the oldest line on it.
	items := foldRepeats(evs)
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	out := logPage{
		Items:  items,
		Counts: logCounts{Info: counts[events.LevelInfo], Warn: counts[events.LevelWarn], Error: counts[events.LevelError]},
	}
	if len(evs) == lq.limit {
		n := next.Cursor
		out.NextBefore = &n
	}
	writeJSON(w, http.StatusOK, out)
}

// foldRepeats folds the same source and message within logFoldWindow of each other into the newest line, with a count.
// It walks newest first and works on one page, so a run that spans two pages shows as two lines. Every row stays stored.
func foldRepeats(evs []events.Event) []appLine {
	type key struct{ source, message string }
	type run struct {
		idx    int
		count  int
		oldest time.Time
		newest time.Time
	}
	out := make([]appLine, 0, len(evs))
	last := map[key]*run{}
	var runs []*run
	for _, e := range evs {
		k := key{e.Source, e.Message}
		if g, ok := last[k]; ok && g.oldest.Sub(e.TS) <= logFoldWindow {
			g.count++
			g.oldest = e.TS
			continue
		}
		out = append(out, lineOf(e))
		g := &run{idx: len(out) - 1, count: 1, oldest: e.TS, newest: e.TS}
		last[k] = g
		runs = append(runs, g)
	}
	for _, g := range runs {
		if g.count >= 2 {
			out[g.idx].Repeat = &logRepeat{
				Count:   g.count,
				FirstAt: timeJSON2(g.oldest),
				LastAt:  timeJSON2(g.newest),
			}
		}
	}
	return out
}

func lineOf(e events.Event) appLine {
	return appLine{
		Seq: e.RowID, TS: timeJSON2(e.TS), Level: string(e.Level), Kind: string(e.Kind), Source: displayOf(e.Source),
		Message: e.Message, JobID: idOrNil(e.JobID), LibraryID: strOrNil(e.LibraryID), WorkerID: strOrNil(e.WorkerID),
	}
}

// getLogSources is GET /api/v2/log/sources: the names the source filter offers, as a string array.
func (s *Server) getLogSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, logSources)
}

// logDownload is GET /api/v2/log/download: the lines the filter matches, oldest first, as plain text. Every line is in
// the file; the list folds repeats, the download does not.
func (s *Server) logDownload(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil || rs.Events == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	lq, ok := logFilter(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	var all []events.Event
	cursor := lq.before
	truncated := false
	for {
		evs, next, err := rs.Events.Query(ctx, lq.filter, events.Page{Limit: logPageMax, Newest: true, Cursor: cursor})
		if err != nil {
			internalError(w)
			return
		}
		all = append(all, evs...)
		if len(evs) < logPageMax {
			break
		}
		if len(all) >= logDownloadMax {
			truncated = true
			break
		}
		cursor = next.Cursor
	}
	// The download is in time order: the rows are in insertion order, which is not always time order.
	sort.SliceStable(all, func(i, j int) bool { return all[i].TS.Before(all[j].TS) })
	var b strings.Builder
	for _, e := range all {
		fmt.Fprintf(&b, "%s  %-5s  %s  %s\n", e.TS.Format("2006-01-02 15:04:05.000"), strings.ToUpper(string(e.Level)), displayOf(e.Source), e.Message)
	}
	if truncated {
		fmt.Fprintf(&b, "Download stopped at %d lines. Narrow the filters to get the rest.\n", logDownloadMax)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="system-log.txt"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(b.String()))
}

// postLogSeen is POST /api/v2/log/seen {seq}: the newest line the person has seen. It only moves forward.
func (s *Server) postLogSeen(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	var body struct {
		Seq *int64 `json:"seq"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Seq == nil || *body.Seq < 0 {
		writeError(w, http.StatusBadRequest, "invalid_setting", "Send the seq of the newest line you saw.", "seq", 0)
		return
	}
	ctx := r.Context()
	seen, err := readLogSeen(ctx, rs.DB)
	if err != nil {
		internalError(w)
		return
	}
	if *body.Seq > seen.Seq {
		seen = logSeen{Seq: *body.Seq, At: time.Now().UTC().Format(time.RFC3339)}
		if err := writeLogSeen(ctx, rs.DB, seen); err != nil {
			internalError(w)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int64{"seenSeq": seen.Seq})
}

// readLogSeen is the stored seen mark, or zero before any was stored.
func readLogSeen(ctx context.Context, db *store.DB) (logSeen, error) {
	var raw string
	err := db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, logSeenKey).Scan(&raw)
	if err == sql.ErrNoRows {
		return logSeen{}, nil
	}
	if err != nil {
		return logSeen{}, err
	}
	var seen logSeen
	if err := json.Unmarshal([]byte(raw), &seen); err != nil {
		return logSeen{}, nil
	}
	return seen, nil
}

func writeLogSeen(ctx context.Context, db *store.DB, seen logSeen) error {
	raw, err := json.Marshal(seen)
	if err != nil {
		return err
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, logSeenKey, string(raw))
		return err
	})
}
