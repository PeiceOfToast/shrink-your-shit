// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A made-up job, with a line in each section the download shows. Times are fixed, so the golden file is stable.
func sampleJob() (jobs.Job, []jobs.LogLine) {
	j := jobs.Job{ID: 42, Path: "/m/Holiday Trip 2026.mkv", Status: jobs.Done, Attempts: 0,
		SizeBefore: 4012345678, SizeAfter: 1500000000, OutputPath: "/m/Holiday Trip 2026.mkv"}
	lines := []jobs.LogLine{
		{Seq: 1, At: "2026-11-20T05:00:00.000Z", Section: "start", Level: jobs.LevelInfo, Stream: "app", Text: "Job 42 started"},
		{Seq: 2, At: "2026-11-20T05:00:01.250Z", Section: "input", Level: jobs.LevelInfo, Stream: "app", Text: "Size: 4,012,345,678 bytes (3.7 GiB)"},
		{Seq: 3, At: "2026-11-20T05:00:02.000Z", Section: "encode", Level: jobs.LevelInfo, Stream: "app", Text: "Encode started"},
		{Seq: 4, At: "2026-11-20T05:01:10.500Z", Section: "encode", Level: jobs.LevelInfo, Stream: "stderr", Text: "frame= 100 fps=25"},
		{Seq: 5, At: "2026-11-20T05:02:00.000Z", Section: "output", Level: jobs.LevelWarning, Stream: "app", Text: "a little damage in the output"},
		{Seq: 6, At: "2026-11-20T05:02:05.000Z", Section: "replace", Level: jobs.LevelInfo, Stream: "app", Text: "Moved the original aside"},
		{Seq: 7, At: "2026-11-20T05:02:06.000Z", Section: "summary", Level: jobs.LevelInfo, Stream: "app", Text: "Converted. Saved 62.6% (2,512,345,678 bytes)."},
	}
	return j, lines
}

func TestLogFileName(t *testing.T) {
	if got := logFileName(42, "/m/Holiday Trip 2026.mkv"); got != "syp-job-42-Holiday-Trip-2026.log" {
		t.Fatalf("got %q", got)
	}
	if got := logFileName(7, "/m/.mkv"); got != "syp-job-7-job.log" {
		t.Fatalf("an empty title: got %q", got)
	}
}

// The download is the log-spec section 5 layout; compared with a golden file written once and read by eye.
// Regenerate with UPDATE_GOLDEN=1.
func TestJobLogDownloadGolden(t *testing.T) {
	j, lines := sampleJob()
	got := jobLogText(j, j.ID, lines)
	path := filepath.Join("testdata", "joblog_download.golden")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("download differs from %s:\n%s", path, got)
	}
}

// log-spec R1: summary.result is the only verdict; null while the job has none.
func TestSummaryNullWhileRunning(t *testing.T) {
	j, lines := sampleJob()
	j.Status = jobs.Running
	if s := summaryJSON(j, lines); s != nil {
		t.Fatalf("summary while running = %v, want null", s)
	}
	j.Status = jobs.Done
	if s := summaryJSON(j, lines); s == nil || s["result"] != "converted" {
		t.Fatalf("summary when done = %v", s)
	}
}

// Sections come in the order the log-spec gives, with lines in their own order; old lines (section "") come last.
func TestSectionsInOrder(t *testing.T) {
	_, lines := sampleJob()
	lines = append([]jobs.LogLine{{Seq: 0, At: "2026-11-20T04:59:00.000Z", Section: "", Level: jobs.LevelInfo, Text: "old"}}, lines...)
	var names []string
	for _, s := range sectionsJSON(lines) {
		names = append(names, s["name"].(string))
	}
	want := []string{"start", "input", "encode", "output", "replace", "summary", ""}
	if len(names) != len(want) {
		t.Fatalf("sections %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("sections %v, want %v", names, want)
		}
	}
}

// The job.log event gives section, step and level on every line; a line without a level reads as info.
func TestStreamLinesCarryFields(t *testing.T) {
	out := streamLines([]jobs.LogLine{{At: "2026-11-20T05:00:00.000Z", Section: "start", Step: 0, Stream: "app", Text: "x"}})
	if len(out) != 1 {
		t.Fatalf("%d lines", len(out))
	}
	l := out[0]
	if l["section"] != "start" || l["level"] != jobs.LevelInfo || l["text"] != "x" {
		t.Fatalf("line %v", l)
	}
}
