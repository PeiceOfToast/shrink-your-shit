// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Slice 16: the job log's Encode step returns the command the worker stored, so the Log screen can show what ran.
func TestJobLogEncodeStepReturnsTheStoredCommand(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	const cmd = "-c:v libx265 -crf 23 -maxrate:v:0 6000k -bufsize:v:0 12000k -c:a copy"
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Outcome: "ok", Command: "ffmpeg -i in.mkv " + cmd + " out.mkv"})
	st.RecordRunStep(id, jobs.RunStep{Index: 1, Name: "check", Outcome: "ok"})
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	var r struct {
		Steps []map[string]any
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 2 {
		t.Fatalf("log %s", body)
	}
	if got := r.Steps[0]["command"]; got != "ffmpeg -i in.mkv "+cmd+" out.mkv" {
		t.Errorf("encode step command %v, want the stored command", got)
	}
	if got := r.Steps[0]["name"]; got != "encode" {
		t.Errorf("first step %v, want encode", got)
	}
}
