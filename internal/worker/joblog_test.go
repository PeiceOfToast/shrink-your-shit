// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// recordedLines is a Queue that keeps the job's log lines and the job as it stands; the other methods are unused.
type recordedLines struct {
	Queue
	job   jobs.Job
	lines []recordedLine
}

type recordedLine struct{ section, level, text string }

func (q *recordedLines) AppendLogLine(id int64, step int, section, level, stream, text string) error {
	q.lines = append(q.lines, recordedLine{section, level, text})
	return nil
}

func (q *recordedLines) Get(id int64) (jobs.Job, error) { return q.job, nil }

// log-spec R1: the job's last line says the verdict in words, and that word is the verdict summary.result gives
// (converted, failed, kept). A job with no final result writes no summary line.
func TestSummaryLineSaysTheVerdict(t *testing.T) {
	cases := []struct {
		name          string
		status        jobs.Status
		before, after int64
		reason        string
		wantFirstWord string // "" when the job writes no summary line
	}{
		{"converted and smaller", jobs.Done, 1000, 400, "", "converted"},
		{"converted but not smaller", jobs.Done, 1000, 1200, "", "converted"},
		{"failed", jobs.Failed, 1000, 0, "ffmpeg exited with code 1", "failed"},
		{"kept", jobs.Kept, 1000, 0, "the new file does not look like the original", "kept"},
		{"still running", jobs.Running, 1000, 0, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := &recordedLines{job: jobs.Job{ID: 7, Status: c.status, SizeBefore: c.before, SizeAfter: c.after, Error: c.reason}}
			w := &Worker{ID: "w1", Queue: q}
			w.summaryLine(7)
			if c.wantFirstWord == "" {
				if len(q.lines) != 0 {
					t.Fatalf("wrote %+v, want no line", q.lines)
				}
				return
			}
			last := q.lines[len(q.lines)-1]
			if last.section != secSummary {
				t.Fatalf("last line is in section %q, want summary", last.section)
			}
			first := strings.ToLower(strings.TrimSuffix(strings.Fields(last.text)[0], "."))
			if first != c.wantFirstWord {
				t.Fatalf("last line %q says %q, want the verdict %q", last.text, first, c.wantFirstWord)
			}
		})
	}
}

// log-spec R7: one line per stream, in words: "#2 audio · German · AC-3 5.1 · 640 kbps · default".
func TestStreamLineInWords(t *testing.T) {
	got := streamLine(media.Stream{Index: 2, Type: "audio", Language: "German", Codec: "ac3", Channels: 6, BitrateKbps: 640, Default: true})
	if want := "#2 audio · German · AC-3 5.1 · 640 kbps · default"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestClockAndBitrateWords(t *testing.T) {
	if got := clockText(3725.4); got != "1:02:05" {
		t.Errorf("clockText = %q", got)
	}
	if got := bitrateText(8000); got != "8.0 Mbps" {
		t.Errorf("bitrateText(8000) = %q", got)
	}
	if got := bitrateText(640); got != "640 kbps" {
		t.Errorf("bitrateText(640) = %q", got)
	}
}
