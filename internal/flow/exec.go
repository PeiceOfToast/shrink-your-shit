// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// What a program's output may put in the job log: the first MaxLogLines lines, each at most MaxLogLine runes.
const (
	MaxLogLines = 200
	MaxLogLine  = 1000
)

// RunProgram is Env.Exec for a real program (FB2b): started directly (no shell) in dir, stopped after
// timeout, its output (both streams) given line by line to onLine and returned, capped at MaxOutput
// plus a little. An exit code other than 0 is an error that names it; a program that can't start says so.
func RunProgram(program string, args []string, dir string, timeout time.Duration, onLine func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	cmd.WaitDelay = timeout // review2-539 P2a: a child the program leaves behind holding its output can't hold the worker
	var killed atomic.Bool  // set only when the timeout had to stop the program itself
	cmd.Cancel = func() error { killed.Store(true); return cmd.Process.Kill() }
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var out strings.Builder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		lines := 0
		for sc.Scan() {
			l := sc.Text()
			if r := []rune(l); len(r) > MaxLogLine {
				l = string(r[:MaxLogLine]) + "…"
			}
			lines++
			if onLine != nil && lines <= MaxLogLines { // review2-539 P3a: the job log takes the first lines, then one that says so
				onLine(l)
			} else if onLine != nil && lines == MaxLogLines+1 {
				onLine("… more output not shown in the log")
			}
			if out.Len() < MaxOutput+1024 {
				out.WriteString(l)
				out.WriteByte('\n')
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		pw.Close()
		wg.Wait()
		return "", fmt.Errorf("%s could not start: %v", program, startWords(err))
	}
	err := cmd.Wait()
	pw.Close()
	wg.Wait()
	if errors.Is(err, exec.ErrWaitDelay) { // the program itself finished; something it started kept its output open
		err = nil
		if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
			err = &exec.ExitError{ProcessState: cmd.ProcessState}
		}
	}
	if killed.Load() {
		return out.String(), fmt.Errorf("%s was stopped after %s", program, timeout)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), fmt.Errorf("%s exited with code %d", program, ee.ExitCode())
	}
	if err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// startWords keeps the useful part of a start error (the system's words, without the program repeated).
func startWords(err error) string {
	var pe *exec.Error
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}
