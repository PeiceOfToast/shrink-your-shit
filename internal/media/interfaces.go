// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
)

// Prober is what a scanner, the flow engine or a test needs from Probe.
type Prober interface {
	Probe(ctx context.Context, path string) (Info, error)
}

// Runner is what the flow engine or a test needs from Run.
type Runner interface {
	Run(ctx context.Context, argv []string, onProgress func(Progress)) (Result, error)
}

// Media is the pair the packages above take, so the real implementation and media/fake are
// interchangeable in unit tests.
type Media interface {
	Prober
	Runner
}

var _ Media = MediaFuncs{}

// MediaFuncs turns two functions into a Media, for a test that needs only one of them.
type MediaFuncs struct {
	ProbeFunc func(ctx context.Context, path string) (Info, error)
	RunFunc   func(ctx context.Context, argv []string, onProgress func(Progress)) (Result, error)
}

func (m MediaFuncs) Probe(ctx context.Context, path string) (Info, error) {
	if m.ProbeFunc == nil {
		// A mis-wired test double is a mistake to fix, not a file that turned out not to be media.
		return Info{}, errors.New("media: no Probe function")
	}
	return m.ProbeFunc(ctx, path)
}

func (m MediaFuncs) Run(ctx context.Context, argv []string, onProgress func(Progress)) (Result, error) {
	if m.RunFunc == nil {
		return Result{}, errors.New("media: no Run function")
	}
	return m.RunFunc(ctx, argv, onProgress)
}
