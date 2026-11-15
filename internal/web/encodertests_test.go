// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Q1: an accepted remote report's encoder test run is kept for the worker's computer, once per run.
func TestReportedEncoderTestsAreKeptPerRun(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	caps := `[{"key":"encoder:hevc_nvenc","kind":"encoder","test":"passed","testedAt":"2026-11-15T10:00:00Z","detail":"10-bit too (HDR)."},` +
		`{"key":"encoder:h264_vaapi","kind":"encoder","test":"failed","testedAt":"2026-11-15T10:00:00Z","detail":"no card"},` +
		`{"key":"tool:ffprobe","kind":"tool","test":"passed","testedAt":"2026-11-15T10:00:00Z"}]`
	for i := 0; i < 2; i++ { // the same report on two heartbeats
		if err := recordReportedTests(st, "w1", caps); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.EncoderTests("worker:w1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("two encoders, one run: got %+v", got)
	}
	byEncoder := map[string]jobs.EncoderTest{}
	for _, r := range got {
		byEncoder[r.Encoder] = r
	}
	if n := byEncoder["hevc_nvenc"]; !n.Passed || n.Detail != "10-bit too (HDR)." {
		t.Errorf("hevc_nvenc: %+v", n)
	}
	if v := byEncoder["h264_vaapi"]; v.Passed || v.Detail != "no card" {
		t.Errorf("h264_vaapi: %+v", v)
	}
	if err := recordReportedTests(st, "w3", "{}"); err != nil { // an older worker's empty report: nothing, no error
		t.Fatal(err)
	}
	// no test time yet (a worker that has not tested): nothing kept
	if err := recordReportedTests(st, "w2", `[{"key":"encoder:libx265","kind":"encoder","test":"passed","testedAt":null}]`); err != nil {
		t.Fatal(err)
	}
	if none, _ := st.EncoderTests("worker:w2"); len(none) != 0 {
		t.Errorf("no test time, no rows: %+v", none)
	}
}
