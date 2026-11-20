// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bytes"
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
		{Seq: 3, At: "2026-11-20T05:00:01.500Z", Section: "step:1:Convert video", Level: jobs.LevelInfo, Stream: "app", Text: "Convert video: H.264 becomes HEVC at CRF 23."},
		{Seq: 4, At: "2026-11-20T05:00:01.600Z", Section: "step:2:Audio", Level: jobs.LevelInfo, Stream: "app", Text: "Keep 2 tracks as they are."},
		{Seq: 5, At: "2026-11-20T05:00:01.700Z", Section: "step:3:Audio (2)", Level: jobs.LevelInfo, Stream: "app", Text: "Convert 1 to AAC."},
		{Seq: 6, At: "2026-11-20T05:00:02.000Z", Section: "encode", Level: jobs.LevelInfo, Stream: "app", Text: "Encode started"},
		{Seq: 7, At: "2026-11-20T05:01:10.500Z", Section: "encode", Level: jobs.LevelInfo, Stream: "stderr", Text: "frame= 100 fps=25"},
		{Seq: 8, At: "2026-11-20T05:02:00.000Z", Section: "output", Level: jobs.LevelWarning, Stream: "app", Text: "a little damage in the output"},
		{Seq: 9, At: "2026-11-20T05:02:05.000Z", Section: "replace", Level: jobs.LevelInfo, Stream: "app", Text: "Moved the original aside"},
		{Seq: 10, At: "2026-11-20T05:02:06.000Z", Section: "summary", Level: jobs.LevelInfo, Stream: "app", Text: "Converted. Saved 62.6% (2,512,345,678 bytes)."},
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
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")) // a Windows checkout may turn the file's line endings into CRLF
	if got != string(want) {
		t.Fatalf("download differs from %s:\n%s", path, got)
	}
}

// log-spec R1: summary.result is the only verdict; null while the job has none.
func TestSummaryNullWhileRunning(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j, lines := sampleJob()
	j.Status = jobs.Running
	if s := summaryJSON(st, j, lines); s != nil {
		t.Fatalf("summary while running = %v, want null", s)
	}
	j.Status = jobs.Done
	if s := summaryJSON(st, j, lines); s == nil || s["result"] != "converted" {
		t.Fatalf("summary when done = %v", s)
	}
}

// L2: passes, bytesWritten and slowestStep come from what the job recorded; null when it recorded none (a job
// from before L2) and slowestStep null when it recorded no steps.
func TestSummaryRunCountersAndSlowestStep(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j, lines := sampleJob()
	s := summaryJSON(st, j, lines)
	if s["passes"] != nil || s["bytesWritten"] != nil || s["slowestStep"] != nil {
		t.Fatalf("a job with no counters: %v", s)
	}
	if err := st.RecordJobStats(j.ID, 4, 1_500_000_000); err != nil {
		t.Fatal(err)
	}
	st.RecordRunStep(j.ID, jobs.RunStep{Index: 0, Name: "encode", TookMs: 65000, Outcome: "ok"})
	st.RecordRunStep(j.ID, jobs.RunStep{Index: 1, Name: "check", TookMs: 12000, Outcome: "ok"})
	s = summaryJSON(st, j, lines)
	if s["passes"] != 4 || s["bytesWritten"] != int64(1_500_000_000) {
		t.Fatalf("counters: %v", s)
	}
	slow, _ := s["slowestStep"].(map[string]any)
	if slow["name"] != "encode" || slow["tookMs"] != int64(65000) {
		t.Fatalf("slowestStep %v", s["slowestStep"])
	}
}

// L2/design-joblog-vars section 1: a workflow step's section is {id: "step", step: N, title: "Step N: Label"},
// and it comes after the input, before the encode.
func TestStepSectionsHaveTheirNumberAndTitle(t *testing.T) {
	_, lines := sampleJob()
	secs := sectionsJSON(lines)
	var steps []map[string]any
	for _, s := range secs {
		if s["id"] == "step" {
			steps = append(steps, s)
		}
	}
	if len(steps) != 3 {
		t.Fatalf("%d step sections: %v", len(steps), secs)
	}
	want := []struct {
		n     int
		title string
	}{{1, "Step 1: Convert video"}, {2, "Step 2: Audio"}, {3, "Step 3: Audio (2)"}}
	for i, w := range want {
		if steps[i]["step"] != w.n || steps[i]["title"] != w.title {
			t.Fatalf("step section %d = %v, want %d %q", i, steps[i], w.n, w.title)
		}
	}
	for i, s := range secs {
		if s["id"] == "encode" {
			if secs[i-1]["id"] != "step" {
				t.Fatalf("encode follows %v: the steps must come first", secs[i-1]["id"])
			}
			return
		}
	}
	t.Fatal("no encode section")
}

// Sections come in the order the log-spec gives, with lines in their own order; old lines (section "") come last.
func TestSectionsInOrder(t *testing.T) {
	_, lines := sampleJob()
	lines = append([]jobs.LogLine{{Seq: 0, At: "2026-11-20T04:59:00.000Z", Section: "", Level: jobs.LevelInfo, Text: "old"}}, lines...)
	var names []string
	for _, s := range sectionsJSON(lines) {
		names = append(names, s["id"].(string))
	}
	want := []string{"start", "input", "step", "step", "step", "encode", "output", "replace", "summary", ""}
	if len(names) != len(want) {
		t.Fatalf("sections %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("sections %v, want %v", names, want)
		}
	}
}

// Contract v2 (slices/design-joblog-vars.md section 1): each section is {id, title, lines}; the viewer reads id and
// title, so name and heading must not appear. start.workflow carries "name" (null: not stored yet) and revision.
func TestSectionKeysAndWorkflowNameAreContractV2(t *testing.T) {
	_, lines := sampleJob()
	secs := sectionsJSON(lines)
	if len(secs) == 0 {
		t.Fatal("no sections")
	}
	for _, s := range secs {
		for _, k := range []string{"id", "title", "lines"} {
			if _, ok := s[k]; !ok {
				t.Fatalf("section %v lacks %q", s, k)
			}
		}
		if _, ok := s["name"]; ok {
			t.Fatalf("section still has name: %v", s)
		}
		if _, ok := s["heading"]; ok {
			t.Fatalf("section still has heading: %v", s)
		}
	}
	if secs[0]["id"] != "start" || secs[0]["title"] != "Start" {
		t.Fatalf("first section %v", secs[0])
	}
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	wf, ok := startJSON(st, 0, jobs.Job{}, lines)["workflow"].(map[string]any)
	if !ok {
		t.Fatal("start has no workflow object")
	}
	if v, ok := wf["name"]; !ok || v != nil {
		t.Fatalf("workflow.name = %v (present %v), want null", v, ok)
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
