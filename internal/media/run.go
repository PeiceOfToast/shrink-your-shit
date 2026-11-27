// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrFFmpegMissing means the program Run was asked to start is not on PATH.
var ErrFFmpegMissing = errors.New("ffmpeg not found")

// Result is how one run ended: the exit code, how long it took, the end of its stderr (the job
// card's key lines come from these later) and the process id.
type Result struct {
	ExitCode int
	Duration time.Duration
	KeyLines []string
	PID      int
}

// Progress is one ffmpeg progress block. OutTime and TotalSize are both here on every tick,
// because the worker's stall watch (L-Q-6, L-SAFE-14) counts output growth as progress too.
type Progress struct {
	OutTime   time.Duration
	TotalSize int64
	Frame     int64
	FPS       float64
	Speed     float64
}

// keyLineCount is how much of ffmpeg's stderr a Result keeps.
const keyLineCount = 50

// Run starts argv (the program first, then its arguments) with ffmpeg's progress on a pipe and
// calls onProgress once per block. On context cancellation the whole process tree is killed
// (L-Q-7): on Unix the command runs in its own process group, on Windows in a job object.
//
// Run owns the progress flags: it appends "-progress pipe:1 -nostats" itself, so callers build
// the encode command without them.
func Run(ctx context.Context, argv []string, onProgress func(Progress)) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("run: no program given")
	}
	program := argv[0]
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("run %s: %w", program, err)
	}
	if _, err := exec.LookPath(program); err != nil {
		return Result{}, fmt.Errorf("run %s: %w: %v", program, ErrFFmpegMissing, err)
	}

	args := append([]string(nil), argv[1:]...)
	args = append(args, "-progress", "pipe:1", "-nostats")
	cmd := exec.Command(program, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("run %s: read its progress: %w", program, err)
	}
	key := &lineRing{max: keyLineCount}
	cmd.Stderr = key

	res := Result{}
	started := time.Now()
	tree, err := startTree(cmd)
	if err != nil {
		return res, fmt.Errorf("run %s: %w", program, err)
	}
	res.PID = cmd.Process.Pid

	// The watcher is the only thing that kills the tree. Closing finished after Wait lets it stop;
	// if ctx fires in the same moment, killing an already-reaped group is a harmless no-op.
	finished := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			tree.kill()
		case <-finished:
		}
	}()

	readProgress(stdout, onProgress)
	waitErr := cmd.Wait()
	close(finished)
	tree.release()

	res.Duration = time.Since(started)
	res.KeyLines = key.Lines()
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		res.ExitCode = -1
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("run %s: %w", program, ctxErr)
	}
	if waitErr != nil {
		line := ""
		if lines := res.KeyLines; len(lines) > 0 {
			line = ": " + lines[len(lines)-1]
		}
		return res, fmt.Errorf("run %s: ffmpeg exited with code %d%s", program, res.ExitCode, line)
	}
	return res, nil
}

// readProgress parses ffmpeg's -progress stream and calls onProgress at the end of each block,
// when every field of the block has been read. It returns the last block seen.
func readProgress(r io.Reader, onProgress func(Progress)) Progress {
	var block, last Progress
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			if us, err := strconv.ParseInt(value, 10, 64); err == nil && us >= 0 {
				block.OutTime = time.Duration(us) * time.Microsecond
			}
		case "out_time":
			// Kept as a fallback for builds that omit out_time_us.
			if block.OutTime == 0 {
				if d, ok := parseClock(value); ok {
					block.OutTime = d
				}
			}
		case "total_size":
			block.TotalSize = parseCount(value)
		case "frame":
			block.Frame = parseCount(value)
		case "fps":
			block.FPS = parseNumber(value)
		case "speed":
			block.Speed = parseNumber(strings.TrimSuffix(value, "x"))
		case "progress":
			// "continue" or "end" ends the block; emit it whole.
			last = block
			if onProgress != nil {
				onProgress(block)
			}
		}
	}
	return last
}

// parseClock reads ffmpeg's "HH:MM:SS.ffffff".
func parseClock(value string) (time.Duration, bool) {
	hourText, rest, ok := strings.Cut(value, ":")
	if !ok {
		return 0, false
	}
	minuteText, secondText, ok := strings.Cut(rest, ":")
	if !ok {
		return 0, false
	}
	hour, err1 := strconv.Atoi(hourText)
	minute, err2 := strconv.Atoi(minuteText)
	second, err3 := strconv.ParseFloat(secondText, 64)
	if err1 != nil || err2 != nil || err3 != nil || hour < 0 || minute < 0 || second < 0 {
		return 0, false
	}
	return time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute +
		time.Duration(second*float64(time.Second)), true
}

// parseCount reads a whole number, and 0 for ffmpeg's "N/A".
func parseCount(value string) int64 {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// parseNumber reads a decimal number, and 0 for ffmpeg's "N/A".
func parseNumber(value string) float64 {
	v, err := strconv.ParseFloat(value, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

// lineRing keeps the last max non-empty stderr lines. It is written by the child's stderr pipe
// and read after Wait, so it needs a lock. A write may end mid-line, so the tail of one write is
// held until the rest of the line arrives.
type lineRing struct {
	max     int
	mu      sync.Mutex
	partial string
	ring    []string
}

func (l *lineRing) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lines := strings.Split(l.partial+string(p), "\n")
	l.partial = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		l.push(line)
	}
	return len(p), nil
}

// Lines is the ring; any partial last line that never got its newline is included once.
func (l *lineRing) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := append([]string(nil), l.ring...)
	if line := strings.TrimSpace(l.partial); line != "" {
		out = append(out, line)
		if len(out) > l.max {
			out = out[len(out)-l.max:]
		}
	}
	return out
}

func (l *lineRing) push(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	l.ring = append(l.ring, line)
	if len(l.ring) > l.max {
		l.ring = l.ring[len(l.ring)-l.max:]
	}
}
