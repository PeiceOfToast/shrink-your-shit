// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package media

import (
	"context"
	"strings"
	"testing"
)

// A run that prints more than the stderr budget of info lines before anything worth reading still keeps its
// error lines and ffmpeg's end-of-run report (review-588 round 1: the first 256 KiB were all info lines, so a
// damaged file's errors and report were dropped, and the job read the file as undamaged).
func TestStreamKeepsErrorsAndTheReportPastTheInfoLines(t *testing.T) {
	script := `i=0
while [ $i -lt 3000 ]; do
  printf '[info] %s\n' '................................ (an info line that fills the stderr budget) ................................' >&2
  i=$((i+1))
done
printf '[error] error while decoding MB 3 4, bytestream 17\n' >&2
printf '[in#0/matroska @ 0x1] [verbose]   Input stream #0:0 (video): 900 packets read (1 bytes); 900 frames decoded; 0 decode errors;\n' >&2`
	got, err := OSExec{}.Stream(context.Background(), func(string) {}, "sh", "-c", script)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[error] error while decoding MB 3 4, bytestream 17",
		"Input stream #0:0 (video): 900 packets read (1 bytes); 900 frames decoded;",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stderr lacks %q (kept %d bytes)", want, len(got))
		}
	}
	if strings.Contains(got, "[info]") {
		t.Errorf("info lines must not be what is kept (kept %d bytes)", len(got))
	}
	// What is kept still reads as -v error text: the marker comes off the error line.
	if text := ErrorLines([]byte(got)); !strings.Contains(text, "error while decoding MB 3 4") || strings.Contains(text, "[error]") {
		t.Errorf("the error line does not survive ErrorLines as -v error text:\n%s", text)
	}
}

// The report has its own budget: a flood of error lines cannot crowd it out.
func TestKeepBufferKeepsTheReportApartFromTheErrorLines(t *testing.T) {
	k := &keepBuffer{max: 100, maxReports: 4096}
	for i := 0; i < 100; i++ {
		k.Write([]byte("[error] one damaged picture\n"))
	}
	k.Write([]byte("[in#0/matroska @ 0x1] [verbose]   Input stream #0:0 (video): 9 packets read (1 bytes); 9 frames decoded; 0 decode errors;\n"))
	k.flush()
	got := string(k.lines) + string(k.reports)
	if !strings.Contains(got, "Input stream #0:0 (video): 9 packets read (1 bytes); 9 frames decoded;") {
		t.Errorf("the report was crowded out by the error lines:\n%s", got)
	}
	if len(k.lines) > 100 {
		t.Errorf("error lines kept %d bytes, over the 100-byte budget", len(k.lines))
	}
}

// A run at -v error has no level marker: its lines are kept whole (that is what the playback check reads).
func TestKeepBufferKeepsUnmarkedLines(t *testing.T) {
	k := &keepBuffer{max: 4096, maxReports: 4096}
	k.Write([]byte("[h264 @ 0x1] error while decoding MB 3 4\n"))
	k.Write([]byte("[info] ffmpeg version 7.1.5\n"))
	k.flush()
	got := string(k.lines)
	if !strings.Contains(got, "[h264 @ 0x1] error while decoding MB 3 4") {
		t.Errorf("the unmarked error line was dropped:\n%s", got)
	}
	if strings.Contains(got, "ffmpeg version") {
		t.Errorf("the marker-less rule let an info line in:\n%s", got)
	}
}
