// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

var logT0 = time.Date(2026, 11, 28, 6, 0, 0, 0, time.UTC)

// logSession is the signed-in cookie the log routes need; seedLog sets it before any line is emitted.
var logSession string

func logHeaders() map[string]string {
	return map[string]string{"Cookie": logSession, "X-SYP": "1", "Origin": testOrigin}
}

// seedLog signs in, emits app-wide lines at the given times and flushes the bus, so the routes can read them.
func seedLog(t *testing.T, e *env, lines ...events.Event) {
	t.Helper()
	logSession = e.setup().String()
	for _, l := range lines {
		if l.Kind == "" {
			l.Kind = events.KindNote
		}
		if l.Level == "" {
			l.Level = events.LevelInfo
		}
		if err := e.bus.Emit(l); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.closeBus(); err != nil {
		t.Fatal(err)
	}
}

type logBody struct {
	Items []struct {
		Seq     int64  `json:"seq"`
		Level   string `json:"level"`
		Source  string `json:"source"`
		Message string `json:"message"`
		Repeat  *struct {
			Count   int    `json:"count"`
			FirstAt string `json:"firstAt"`
			LastAt  string `json:"lastAt"`
		} `json:"repeat"`
	} `json:"items"`
	Counts struct {
		Info  int `json:"info"`
		Warn  int `json:"warn"`
		Error int `json:"error"`
	} `json:"counts"`
	NextBefore *int64 `json:"nextBefore"`
}

func getLog(t *testing.T, e *env, path string) logBody {
	t.Helper()
	rec := e.do("GET", path, "", logHeaders(), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, rec.Code, rec.Body.String())
	}
	var out logBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func messages(b logBody) []string {
	var out []string
	for _, it := range b.Items {
		out = append(out, it.Message)
	}
	return out
}

func wantInvalid(t *testing.T, e *env, path, field string) {
	t.Helper()
	rec := e.do("GET", path, "", logHeaders(), false)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("GET %s = %d, want 400", path, rec.Code)
	}
	var body struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "invalid_setting" || body.Error.Field != field {
		t.Errorf("GET %s: code %q field %q, want invalid_setting on %s", path, body.Error.Code, body.Error.Field, field)
	}
}

// The list is app-wide only, newest first, with the counts for the whole filter; a job line is not in it.
func TestLogListsAppLinesNewestFirstWithCounts(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e,
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "scan started", TS: logT0},
		events.Event{Source: "Web", Level: events.LevelWarn, Message: "sign-in failed", TS: logT0.Add(time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelError, Message: "disk full", TS: logT0.Add(2 * time.Minute)},
		events.Event{JobID: 7, Attempt: 1, Source: "Scanner", Level: events.LevelInfo, Message: "job line", TS: logT0},
		events.Event{Source: "Scanner", Level: events.LevelDebug, Message: "debug line", TS: logT0},
	)
	got := getLog(t, e, "/api/v2/log")
	want := []string{"disk full", "sign-in failed", "scan started"}
	if strings.Join(messages(got), "|") != strings.Join(want, "|") {
		t.Errorf("items = %v, want %v (no job line, no debug line)", messages(got), want)
	}
	if got.Counts.Info != 1 || got.Counts.Warn != 1 || got.Counts.Error != 1 {
		t.Errorf("counts = %+v, want 1 each", got.Counts)
	}
	if got.NextBefore != nil {
		t.Errorf("nextBefore = %d on a short page, want none", *got.NextBefore)
	}
}

// Debug lines show only with detail=1, and the level filter is a minimum.
func TestLogDetailGateAndLevelFilter(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e,
		events.Event{Source: "Scanner", Level: events.LevelDebug, Message: "debug line", TS: logT0},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "info line", TS: logT0.Add(time.Minute)},
		events.Event{Source: "Web", Level: events.LevelWarn, Message: "warn line", TS: logT0.Add(2 * time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelError, Message: "error line", TS: logT0.Add(3 * time.Minute)},
	)
	// Debug lines are stored only when the bus is opened with Debug, so this test cannot see one; the default hides them
	// and detail=1 is accepted.
	if m := messages(getLog(t, e, "/api/v2/log?detail=1")); len(m) != 3 {
		t.Errorf("detail=1 items = %v, want the three stored lines", m)
	}
	if m := messages(getLog(t, e, "/api/v2/log?level=warn")); strings.Join(m, "|") != "error line|warn line" {
		t.Errorf("level=warn items = %v, want warnings and errors", m)
	}
	if m := messages(getLog(t, e, "/api/v2/log?level=error")); strings.Join(m, "|") != "error line" {
		t.Errorf("level=error items = %v, want the error only", m)
	}
}

// Each filter narrows the list: source, library, the search text (with % and _ taken literally), and time range.
func TestLogFilters(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e,
		events.Event{Source: "Scanner", LibraryID: "lib-a", Level: events.LevelInfo, Message: "scan 100% done", TS: logT0},
		events.Event{Source: "Web", Level: events.LevelInfo, Message: "sign-in ok", TS: logT0.Add(time.Hour)},
		events.Event{Source: "Scanner", WorkerID: "w-1", Level: events.LevelInfo, Message: "worker joined", TS: logT0.Add(2 * time.Hour)},
	)
	cases := map[string]string{
		"/api/v2/log?source=Web":    "sign-in ok",
		"/api/v2/log?library=lib-a": "scan 100% done",
		"/api/v2/log?worker=w-1":    "worker joined",
		"/api/v2/log?q=100%25":      "scan 100% done", // %25 is the percent sign
		"/api/v2/log?q=100_":        "",               // _ is literal: "100" then any one char is not in the text
		"/api/v2/log?from=2026-11-28T06:30:00Z&to=2026-11-28T07:30:00Z": "sign-in ok",
	}
	for path, want := range cases {
		got := strings.Join(messages(getLog(t, e, path)), "|")
		if got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
}

// before pages back by row id, since keeps only newer lines, and nextBefore points at the next page.
func TestLogPagingBeforeAndSince(t *testing.T) {
	e := newEnv(t, true)
	var lines []events.Event
	for i, m := range []string{"one", "two", "three", "four", "five"} {
		lines = append(lines, events.Event{Source: "Scanner", Level: events.LevelInfo, Message: m, TS: logT0.Add(time.Duration(i) * time.Hour)})
	}
	seedLog(t, e, lines...)
	first := getLog(t, e, "/api/v2/log?limit=2")
	if strings.Join(messages(first), "|") != "five|four" || first.NextBefore == nil {
		t.Fatalf("first page = %v next %v, want five|four with a nextBefore", messages(first), first.NextBefore)
	}
	second := getLog(t, e, "/api/v2/log?limit=2&before="+itoa(*first.NextBefore))
	if strings.Join(messages(second), "|") != "three|two" {
		t.Errorf("second page = %v, want three|two", messages(second))
	}
	last := getLog(t, e, "/api/v2/log?limit=2&before="+itoa(*second.NextBefore))
	if strings.Join(messages(last), "|") != "one" || last.NextBefore != nil {
		t.Errorf("last page = %v next %v, want one with no nextBefore", messages(last), last.NextBefore)
	}
	newer := getLog(t, e, "/api/v2/log?since="+itoa(*second.NextBefore))
	if strings.Join(messages(newer), "|") != "five|four|three" {
		t.Errorf("since %d = %v, want the three newer lines", *second.NextBefore, messages(newer))
	}
}

// Repeats within ten minutes of each other fold into the newest line with a count; a later run is its own line; every
// occurrence stays in the counts.
func TestLogFoldsRepeats(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e,
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "still scanning", TS: logT0},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "still scanning", TS: logT0.Add(time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "still scanning", TS: logT0.Add(2 * time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "still scanning", TS: logT0.Add(30 * time.Minute)},
	)
	got := getLog(t, e, "/api/v2/log")
	if len(got.Items) != 2 {
		t.Fatalf("items = %d, want 2: the 30-minute line, and the folded run", len(got.Items))
	}
	if got.Items[0].Repeat != nil {
		t.Errorf("the 30-minute line folded: %+v", got.Items[0].Repeat)
	}
	r := got.Items[1].Repeat
	if r == nil || r.Count != 3 {
		t.Fatalf("the run = %+v, want count 3", r)
	}
	if r.FirstAt != timeJSON2(logT0) || r.LastAt != timeJSON2(logT0.Add(2*time.Minute)) {
		t.Errorf("the run's firstAt %q lastAt %q", r.FirstAt, r.LastAt)
	}
	if got.Counts.Info != 4 {
		t.Errorf("info count = %d, want 4: every occurrence is counted", got.Counts.Info)
	}
}

func TestLogSources(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e)
	rec := e.do("GET", "/api/v2/log/sources", "", logHeaders(), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET sources = %d", rec.Code)
	}
	var list []logSource
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, s := range list {
		have[s.Source] = true
	}
	for _, want := range []string{"Scanner", "Web", "Updates", "Notifications", "Health check"} {
		if !have[want] {
			t.Errorf("sources = %v, missing %s", list, want)
		}
	}
}

// The download is the matching lines, oldest first, as plain text, every occurrence included.
func TestLogDownloadIsPlainTextOldestFirst(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e,
		events.Event{Source: "Web", Level: events.LevelWarn, Message: "sign-in failed", TS: logT0.Add(time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "scan started", TS: logT0},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "scan started", TS: logT0.Add(time.Second)},
	)
	rec := e.do("GET", "/api/v2/log/download", "", logHeaders(), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, `filename="system-log.txt"`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	lines := strings.Split(strings.TrimRight(rec.Body.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("download has %d lines, want 3 (folding is for the list only):\n%s", len(lines), rec.Body.String())
	}
	if !strings.HasPrefix(lines[0], "2026-11-28 06:00:00.000  INFO") || !strings.HasSuffix(lines[0], "Scanner  scan started") {
		t.Errorf("first line = %q, want the 06:00:00 scan first", lines[0])
	}
	if !strings.HasPrefix(lines[2], "2026-11-28 06:01:00.000  WARN") || !strings.HasSuffix(lines[2], "Web  sign-in failed") {
		t.Errorf("last line = %q, want the 06:01 warning last", lines[2])
	}
}

// Bad input is a 400 invalid_setting that names the field.
func TestLogBadInput(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e)
	wantInvalid(t, e, "/api/v2/log?level=debug", "level")
	wantInvalid(t, e, "/api/v2/log?level=loud", "level")
	wantInvalid(t, e, "/api/v2/log?from=yesterday", "from")
	wantInvalid(t, e, "/api/v2/log?to=2026-11-28", "to")
	wantInvalid(t, e, "/api/v2/log?before=0", "before")
	wantInvalid(t, e, "/api/v2/log?before=x", "before")
	wantInvalid(t, e, "/api/v2/log?since=-1", "since")
	wantInvalid(t, e, "/api/v2/log?limit=0", "limit")
	wantInvalid(t, e, "/api/v2/log?detail=yes", "detail")
	wantInvalid(t, e, "/api/v2/log/download?level=nope", "level")
}

// The seen mark moves forward and the summary counts the errors after it as new errors.
func TestLogSeenFeedsTheSummaryNewErrors(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e,
		events.Event{Source: "Scanner", Level: events.LevelError, Message: "first failure", TS: logT0},
		events.Event{Source: "Scanner", Level: events.LevelError, Message: "second failure", TS: logT0.Add(time.Hour)},
		events.Event{Source: "Web", Level: events.LevelInfo, Message: "fine", TS: logT0.Add(2 * time.Hour)},
	)
	list := getLog(t, e, "/api/v2/log?level=error")
	if len(list.Items) != 2 {
		t.Fatalf("errors = %d, want 2", len(list.Items))
	}
	firstFailure := list.Items[1].Seq
	rec := e.do("POST", "/api/v2/log/seen", `{"seq":`+itoa(firstFailure)+`}`, logHeaders(), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("seen = %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"seenSeq":`+itoa(firstFailure)) {
		t.Errorf("seen body = %s", rec.Body.String())
	}
	// Moving back does nothing.
	_ = e.do("POST", "/api/v2/log/seen", `{"seq":1}`, logHeaders(), false)
	sum := e.do("GET", "/api/v2/summary", "", logHeaders(), false)
	var s struct {
		SeenSeq   int     `json:"seenSeq"`
		NewErrors int     `json:"newErrors"`
		SeenAt    *string `json:"seenAt"`
	}
	if err := json.Unmarshal(sum.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.SeenSeq != int(firstFailure) || s.NewErrors != 1 || s.SeenAt == nil {
		t.Errorf("summary seenSeq %d newErrors %d seenAt %v, want %d, 1, set", s.SeenSeq, s.NewErrors, s.SeenAt, firstFailure)
	}
}

func TestLogSeenBadInput(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e)
	for _, body := range []string{`{}`, `{"seq":-1}`, `not json`} {
		rec := e.do("POST", "/api/v2/log/seen", body, logHeaders(), false)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("seen %s = %d, want 400", body, rec.Code)
		}
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
