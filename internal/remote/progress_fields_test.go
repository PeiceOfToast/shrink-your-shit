// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// progressExec is an ffmpeg that prints these progress lines, then ends.
type progressExec struct{ lines []string }

func (x progressExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return nil, nil
}

func (x progressExec) Capture(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	return nil, nil, nil
}

func (x progressExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	if name == "ffmpeg" {
		for _, l := range x.lines {
			onLine(l)
		}
	}
	return "", nil
}

var _ media.Exec = progressExec{}

// The converting reports carry the frame and byte counts when ffmpeg has them (so the server can judge a frozen
// position by them), and omit them when it has not: an older server reads only the position, as before.
func TestConvertingReportsCarryFramesAndBytesWhenKnown(t *testing.T) {
	old := ProgressEvery
	ProgressEvery = 0 // every progress line is reported
	defer func() { ProgressEvery = old }()
	for _, c := range []struct {
		name      string
		lines     []string
		wantFrame bool
	}{
		{"with frames and bytes", []string{"frame=50", "total_size=20", "out_time_us=1000000"}, true},
		{"position only", []string{"out_time_us=1000000"}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, rec := recordingServer(t)
			cl := &Client{Server: srv.URL, Version: "test"}
			l := &Lease{ID: "L1"}
			cv := Converter{Exec: progressExec{lines: c.lines}, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir()}
			plan := rules.Plan{Video: rules.VideoEncode{Codec: "libx264", CRF: 20, Preset: "medium"}}
			out := filepath.Join(t.TempDir(), "out.mkv")
			if err := os.WriteFile(out, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := cl.convert(context.Background(), cv, l, rules.Snapshot{Plan: plan}, "/in.mkv", out); err != nil {
				t.Fatal(err)
			}
			rec.mu.Lock()
			defer rec.mu.Unlock()
			// the first converting report is the start (outTimeUs 0); the progress line is the next one
			if len(rec.converting) < 2 {
				t.Fatalf("converting reports %d, want the start and the progress", len(rec.converting))
			}
			body := rec.converting[len(rec.converting)-1]
			if _, ok := body["outTimeUs"]; !ok {
				t.Fatalf("the progress report has no position: %v", body)
			}
			frame, hasFrame := body["frame"]
			total, hasTotal := body["totalSize"]
			if c.wantFrame {
				if !hasFrame || frame != float64(50) || !hasTotal || total != float64(20) {
					t.Errorf("frame %v (%v), totalSize %v (%v): want 50 and 20", frame, hasFrame, total, hasTotal)
				}
			} else if hasFrame || hasTotal {
				t.Errorf("position-only worker sent frame %v and totalSize %v", frame, total)
			}
		})
	}
}
