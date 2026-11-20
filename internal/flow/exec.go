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
	"time"
)

// RunProgram is Env.Exec for a real program (FB2b): started directly (no shell) in dir, stopped after
// timeout, its output (both streams) given line by line to onLine and returned, capped at MaxOutput
// plus a little. An exit code other than 0 is an error that names it; a program that can't start says so.
func RunProgram(program string, args []string, dir string, timeout time.Duration, onLine func(string)) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = dir
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	var out strings.Builder
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			l := sc.Text()
			if onLine != nil {
				onLine(l)
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
	if ctx.Err() == context.DeadlineExceeded {
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
