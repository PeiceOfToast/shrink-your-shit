// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"strings"
)

// FakeExec records calls and returns canned output; for tests in any package, so ffmpeg is never needed.
// For simple tests set Out/Lines/Err (the same answer to every call). For tests that need different
// answers per command set Handler, which then answers every call instead.
type FakeExec struct {
	Out    []byte
	Lines  []string
	Err    error
	Calls  [][]string
	OnCall func(name string, args []string)
	// Handler, if set, answers Output, Stream and Capture: stdout, stderr and an error (nil = exit 0).
	Handler func(name string, args []string) (stdout, stderr string, err error)
}

func (f *FakeExec) Capture(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	if f.Handler != nil {
		o, e, err := f.Handler(name, args)
		return []byte(o), []byte(e), err
	}
	return f.Out, nil, f.Err
}

func (f *FakeExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	if f.Handler != nil {
		o, _, err := f.Handler(name, args)
		return []byte(o), err
	}
	return f.Out, f.Err
}

func (f *FakeExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) error {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	if f.Handler != nil {
		o, _, err := f.Handler(name, args)
		for _, l := range strings.Split(o, "\n") {
			if l != "" {
				onLine(l)
			}
		}
		return err
	}
	if f.OnCall != nil {
		f.OnCall(name, args)
	}
	for _, l := range f.Lines {
		onLine(l)
	}
	return f.Err
}
