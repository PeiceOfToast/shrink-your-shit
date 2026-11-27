// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
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

// TestReadProgressClockOnlyBlocksAdvance: a build that sends only the clock form must not freeze at
// the first block's position. The second block's clock reads through, not the first's.
func TestReadProgressClockOnlyBlocksAdvance(t *testing.T) {
	blocks, last := readProgressFile(t, "seconds-two-blocks.txt")
	if len(blocks) != 2 {
		t.Fatalf("progress blocks = %d, want 2", len(blocks))
	}
	if blocks[0].OutTime != time.Second {
		t.Errorf("first out_time = %v, want 1s", blocks[0].OutTime)
	}
	if blocks[1].OutTime != 2*time.Second || last.OutTime != 2*time.Second {
		t.Errorf("second out_time = %v (last %v), want 2s", blocks[1].OutTime, last.OutTime)
	}
}

// TestReadProgressNAAfterAValuedBlock: ffmpeg can report N/A once it has reported numbers. Every
// unknown field still reads as zero, never as the previous block's value.
func TestReadProgressNAAfterAValuedBlock(t *testing.T) {
	blocks, last := readProgressFile(t, "na-after-value.txt")
	if len(blocks) != 2 {
		t.Fatalf("progress blocks = %d, want 2", len(blocks))
	}
	if blocks[0].OutTime != 400*time.Millisecond || blocks[0].TotalSize != 400 {
		t.Fatalf("first = %+v, want a 400ms position and size 400", blocks[0])
	}
	if blocks[1] != (Progress{}) || last != (Progress{}) {
		t.Errorf("second = %+v (last %+v), want all zero", blocks[1], last)
	}
}

// TestReadProgressStartsEachBlockEmpty: a field the next block leaves out must not repeat the
// block before it (ffmpeg omits fields it cannot fill yet).
func TestReadProgressStartsEachBlockEmpty(t *testing.T) {
	stream := "out_time_us=1000000\ntotal_size=100\nframe=10\nprogress=continue\n" +
		"out_time=00:00:02.000000\ntotal_size=200\nprogress=continue\n"
	var blocks []Progress
	readProgress(strings.NewReader(stream), func(p Progress) { blocks = append(blocks, p) })
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if blocks[0].Frame != 10 || blocks[0].OutTime != time.Second {
		t.Errorf("first = %+v, want frame 10 with a 1s position", blocks[0])
	}
	if blocks[1].Frame != 0 || blocks[1].OutTime != 2*time.Second || blocks[1].TotalSize != 200 {
		t.Errorf("second = %+v, want frame 0 (omitted), 2s, size 200", blocks[1])
	}
}

// TestReadProgressDrainsALineTooLong: a progress line over the scanner's buffer must not stop the
// reader, or the child blocks writing into a pipe nobody drains.
func TestReadProgressDrainsALineTooLong(t *testing.T) {
	pr, pw := io.Pipe()
	wrote := make(chan struct{})
	go func() {
		pw.Write([]byte(strings.Repeat("x", 2<<20) + "\n"))
		pw.Write([]byte("frame=7\nprogress=end\n"))
		pw.Close()
		close(wrote)
	}()
	readProgress(pr, nil)
	select {
	case <-wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer is still blocked: stdout was not drained")
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
