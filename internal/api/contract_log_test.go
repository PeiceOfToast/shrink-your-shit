// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
)

// The System log goldens (page 12). The lines carry fixed times, so the goldens do not move with the date. The UI's
// types.ts names the types AppLogPage and LogSeen; the sources are a string array.
func TestContractLogGoldens(t *testing.T) {
	e := newEnv(t, true)
	seedLog(t, e,
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "scan started", TS: logT0},
		events.Event{Source: "Web", Level: events.LevelWarn, Message: "sign-in failed", TS: logT0.Add(time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelError, Message: "disk full", TS: logT0.Add(2 * time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "still scanning", TS: logT0.Add(3 * time.Minute)},
		events.Event{Source: "Scanner", Level: events.LevelInfo, Message: "still scanning", TS: logT0.Add(4 * time.Minute)},
	)
	rec := e.do("GET", "/api/v2/log", "", logHeaders(), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET log = %d", rec.Code)
	}
	writeGolden(t, "log", "AppLogPage", "GET", "/api/v2/log", rec)

	rec = e.do("GET", "/api/v2/log/sources", "", logHeaders(), false)
	writeGolden(t, "log-sources", "string[]", "GET", "/api/v2/log/sources", rec)

	seq := getLog(t, e, "/api/v2/log?level=error").Items[0].Seq
	rec = e.do("POST", "/api/v2/log/seen", `{"seq":`+itoa(seq)+`}`, logHeaders(), false)
	writeGolden(t, "log-seen", "LogSeen", "POST", "/api/v2/log/seen", rec)
}
