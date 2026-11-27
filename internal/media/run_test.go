// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readProgressFile plays one checked-in progress stream through the parser.
func readProgressFile(t *testing.T, name string) (blocks []Progress, last Progress) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "progress", name))
	if err != nil {
		t.Fatalf("open fixture %s: %v", name, err)
	}
	defer f.Close()
	last = readProgress(f, func(p Progress) { blocks = append(blocks, p) })
	return blocks, last
}

func TestReadProgressFromARealRun(t *testing.T) {
	blocks, last := readProgressFile(t, "typical.txt")

	if len(blocks) != 3 {
		t.Fatalf("progress blocks = %d, want 3", len(blocks))
	}
	if blocks[0].OutTime != 766667*time.Microsecond {
		t.Errorf("first out_time = %v, want 766.667ms", blocks[0].OutTime)
	}
	if last.Frame != 120 || last.TotalSize != 214835 {
		t.Errorf("last frame/total size = %d/%d, want 120/214835", last.Frame, last.TotalSize)
	}
	if last.OutTime != 3933333*time.Microsecond {
		t.Errorf("last out_time = %v, want 3.933333s", last.OutTime)
	}
	if last.Speed != 5.89 {
		t.Errorf("last speed = %v, want 5.89", last.Speed)
	}
	if last.OutTime < blocks[0].OutTime {
		t.Error("the media position went backwards")
	}
}

// TestReadProgressWithoutOutTimeUs covers builds of ffmpeg that only send the clock form.
func TestReadProgressWithoutOutTimeUs(t *testing.T) {
	_, last := readProgressFile(t, "seconds-only.txt")
	if last.OutTime != time.Hour+2*time.Minute+3*time.Second+500*time.Millisecond {
		t.Errorf("out_time = %v, want 1h2m3.5s", last.OutTime)
	}
	if last.TotalSize != 1234 || last.Frame != 42 {
		t.Errorf("total size/frame = %d/%d, want 1234/42", last.TotalSize, last.Frame)
	}
}

// TestReadProgressUnknownValues is ffmpeg's "N/A" (before it can size the output): every unknown
// field reads as zero and never as a negative number.
func TestReadProgressUnknownValues(t *testing.T) {
	blocks, last := readProgressFile(t, "na.txt")
	if len(blocks) != 1 {
		t.Fatalf("progress blocks = %d, want 1", len(blocks))
	}
	if last != (Progress{}) {
		t.Errorf("block = %+v, want all zero", last)
	}
}

func TestRunWithNoProgram(t *testing.T) {
	_, err := Run(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("Run with no program returned no error")
	}
}

func TestRunWithNoFFmpeg(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := Run(context.Background(), []string{"ffmpeg", "-i", "in.mp4", "out.mp4"}, nil)
	if !errors.Is(err, ErrFFmpegMissing) {
		t.Fatalf("error = %v, want ErrFFmpegMissing", err)
	}
}

func TestRunCancelledBeforeItStarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, []string{"ffmpeg"}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
