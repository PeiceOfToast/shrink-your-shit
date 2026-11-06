// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import "context"

// FakeExec records calls and returns canned output; for tests in any package, so ffmpeg is never needed.
type FakeExec struct {
	Out    []byte
	Lines  []string
	Err    error
	Calls  [][]string
	OnCall func(name string, args []string)
}

func (f *FakeExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	return f.Out, f.Err
}

func (f *FakeExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) error {
	f.Calls = append(f.Calls, append([]string{name}, args...))
	if f.OnCall != nil {
		f.OnCall(name, args)
	}
	for _, l := range f.Lines {
		onLine(l)
	}
	return f.Err
}
