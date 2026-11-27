// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// Output is what one captured run produced: everything it wrote to stdout (a -progress stream, the
// ssim filter's stats file) and to stderr (its errors and its end-of-run report), with the Result
// Run would have reported.
type Output struct {
	Stdout []byte
	Stderr []byte
	Result Result
}

// Capture runs argv to completion and keeps the whole of its stdout and stderr. It adds no flags of
// its own: the caller asks for "-progress pipe:1" or "stats_file=-" and reads the answer from
// Stdout. On context cancellation the whole process tree is killed, exactly as Run does.
//
// Run is the encode path: it wants progress while the run is going and only the tail of stderr.
// Capture is the check path: the result check judges ffmpeg's report and errors after the run, and
// a sampled playback of a long file can print more than Run's 50-line ring keeps.
func Capture(ctx context.Context, argv []string) (Output, error) {
	if len(argv) == 0 {
		return Output{}, errors.New("capture: no program given")
	}
	program := argv[0]
	if err := ctx.Err(); err != nil {
		return Output{}, fmt.Errorf("capture %s: %w", program, err)
	}
	bin := tool(program)
	if _, err := exec.LookPath(bin); err != nil {
		return Output{}, fmt.Errorf("capture %s: %w: %v", program, ErrFFmpegMissing, err)
	}

	// The same pipe handling as Run, for the same reasons: we own both ends, cancel kills the tree,
	// and a leftover child holding a pipe cannot hold us in a read.
	cmd := toolCommand(context.Background(), program, argv[1:]...)
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		return Output{}, fmt.Errorf("capture %s: make an output pipe: %w", program, err)
	}
	stderr, stderrW, err := os.Pipe()
	if err != nil {
		stdout.Close()
		stdoutW.Close()
		return Output{}, fmt.Errorf("capture %s: make an error pipe: %w", program, err)
	}
	defer stdout.Close()
	defer stderr.Close()
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	key := &lineRing{max: keyLineCount}

	var res Output
	var outBuf, errBuf bytes.Buffer
	started := time.Now()
	tree, err := startTree(cmd)
	if err != nil {
		stdoutW.Close()
		stderrW.Close()
		return res, fmt.Errorf("capture %s: %w", program, err)
	}
	res.Result.PID = cmd.Process.Pid
	stdoutW.Close()
	stderrW.Close()

	finished := make(chan struct{})
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			tree.kill()
		case <-finished:
		}
	}()

	stdoutDone := make(chan struct{})
	go func() {
		defer close(stdoutDone)
		_, _ = io.Copy(&outBuf, stdout)
	}()
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		_, _ = io.Copy(io.MultiWriter(&errBuf, key), stderr)
	}()
	waitErr := cmd.Wait()
	// Anything still holding a pipe after the program exited is a leftover child.
	tree.kill()
	waitRead(stdoutDone, stdout.Close)
	waitRead(stderrDone, stderr.Close)
	close(finished)
	<-watcherDone
	tree.release()

	res.Stdout = outBuf.Bytes()
	res.Stderr = errBuf.Bytes()
	res.Result.Duration = time.Since(started)
	res.Result.KeyLines = key.Lines()
	if cmd.ProcessState != nil {
		res.Result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		res.Result.ExitCode = -1
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("capture %s: %w", program, ctxErr)
	}
	if waitErr != nil {
		line := ""
		if lines := res.Result.KeyLines; len(lines) > 0 {
			line = ": " + lines[len(lines)-1]
		}
		return res, fmt.Errorf("capture %s: ffmpeg exited with code %d%s", program, res.Result.ExitCode, line)
	}
	return res, nil
}
