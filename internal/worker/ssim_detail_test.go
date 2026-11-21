// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

func TestParseFrameScores(t *testing.T) {
	got := parseFrameScores([]byte("n:1 Y:0.991 U:0.998 V:0.997 All:0.993000 (21.5)\n" +
		"noise line\nn:2 Y:0.4 U:0.4 V:0.4 All:0.412000 (3.8)\n"))
	if len(got) != 2 || got[0] != (frameScore{1, 0.993}) || got[1] != (frameScore{2, 0.412}) {
		t.Fatalf("parsed %+v", got)
	}
}

// A 20-minute file is compared at 0:00:00, 0:09:55 and 0:19:45. The second place is too different; the first
// passed, the third was never measured.
func TestSSIMDetailThreePlaces(t *testing.T) {
	segs := sampleSegments(1200)
	done := []placeScore{{0, 0.987}, {595, 0.869}}
	frames := []frameScore{{1, 0.9}, {2, 0.4}, {3, 0.6}, {4, 0.3}}
	got := ssimDetail(1200, segs, done, frames, 5, 0.95)
	want := []string{
		"Check: picture comparison (SSIM) at 3 places, 10 s each: 0:00:00, 0:09:55, 0:19:45",
		"Scores: 0:00:00 0.987, 0:09:55 0.869 (0.95 required); 0:19:45 not measured",
		"Alignment: the new file was also tried one frame earlier and later; the best score counts",
		"Worst frames: 0:09:56 (0.300), 0:09:55 (0.400), 0:09:55 (0.600)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("detail:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestSSIMDetailWholeFileAndNoFrameRate(t *testing.T) {
	got := ssimDetail(12, sampleSegments(12), []placeScore{{0, 0.5}}, []frameScore{{2, 0.3}, {1, 0.9}}, 0, 0.95)
	if got[0] != "Check: picture comparison (SSIM) of the whole file" {
		t.Errorf("check line %q", got[0])
	}
	if got[1] != "Scores: 0:00:00 0.500 (0.95 required)" {
		t.Errorf("scores line %q", got[1])
	}
	if got[3] != "Worst frames: frame 2 (0.300), frame 1 (0.900)" {
		t.Errorf("worst line %q", got[3])
	}
}

// The fake comparison scores every place 0.50 and gives per-frame stats: the job log's step-1 lines say where.
func TestSSIMRefusalLogsItsEvidence(t *testing.T) {
	s := newScenario(t)
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.50 U:0.50 V:0.50 All:0.500000 (3.0)"
	s.ssimStats = "n:1 Y:0.5 U:0.5 V:0.5 All:0.500000 (3.0)\nn:2 Y:0.2 U:0.2 V:0.2 All:0.200000 (1.0)\n"
	s.run(Replace)
	s.assertKept("too different")
	lines, _ := s.store.LogLines(s.id)
	var check []string
	for _, l := range lines {
		if l.Step == 1 && l.Stream == "check" {
			check = append(check, l.Text)
		}
	}
	joined := strings.Join(check, "\n")
	for _, want := range []string{"Check: picture comparison (SSIM) at 3 places", "Scores: 0:00:00 0.500 (0.95 required); ", "Worst frames: "} {
		if !strings.Contains(joined, want) {
			t.Errorf("check lines lack %q:\n%s", want, joined)
		}
	}
}

// An earlier attempt's failure must not show on a later kept attempt.
func TestKeptAttemptClearsAnEarlierFailure(t *testing.T) {
	s := newScenario(t)
	if err := s.store.SetFailureSummary(s.id, jobs.FailureSummary{Step: 2, StepCount: 3, KeyLines: []string{"encode died"}}); err != nil {
		t.Fatal(err)
	}
	s.ssimStderr = "[Parsed_ssim_0] SSIM Y:0.50 U:0.50 V:0.50 All:0.500000 (3.0)"
	s.run(Replace)
	s.assertKept("too different")
	if f, err := s.store.FailureOf(s.id); err != nil || f != nil {
		t.Errorf("kept attempt shows an earlier failure %+v (err %v)", f, err)
	}
}

// A real bad picture (scaled to 16x12 and back) is refused, and the job log lists its worst frames.
func TestRealBadPictureRefusalListsWorstFrames(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	orig := filepath.Join(dir, "orig.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-c:v", "libx264", orig)
	bad := filepath.Join(dir, "bad.mkv")
	runFFmpeg(t, "-i", orig, "-vf", "scale=16:12,scale=320:240", "-c:v", "libx264", "-crf", "51", bad)
	st, _ := realJob(t, orig, 10)
	w := realWorker(st, media.OSExec{})
	x := media.OSExec{}
	src, err := media.Probe(context.Background(), x, "ffprobe", orig)
	if err != nil {
		t.Fatal(err)
	}
	out, err := media.Probe(context.Background(), x, "ffprobe", bad)
	if err != nil {
		t.Fatal(err)
	}
	err = w.ssimCheck(context.Background(), src, out, orig, bad, 0, 0)
	var k *KeptError
	if !errors.As(err, &k) {
		t.Fatalf("ssimCheck returned %v, want a *KeptError", err)
	}
	joined := strings.Join(k.Detail, "\n")
	if !strings.Contains(joined, "Worst frames: ") {
		t.Fatalf("no Worst frames line:\n%s", joined)
	}
	t.Logf("real SSIM detail:\n%s", joined)
}
