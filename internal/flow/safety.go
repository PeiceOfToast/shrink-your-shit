// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow

import (
	"context"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
)

// Safety is the file boundary a run gets: stage the output somewhere private, check it, and put
// it in place without ever overwriting. The tests replace it so the engine can be run without a
// file system surprise; RealSafety is the real thing.
type Safety interface {
	Stage(dst string) (safety.Staged, error)
	Verify(ctx context.Context, p media.Prober, rec safety.Recorder, st safety.Staged, src media.Info, want safety.Want, timeout time.Duration) (safety.Verdict, error)
	Place(ctx context.Context, st safety.Staged, final string) (string, error)
}

// RealSafety is internal/safety, unchanged.
type RealSafety struct{}

func (RealSafety) Stage(dst string) (safety.Staged, error) { return safety.Stage(dst) }

func (RealSafety) Verify(ctx context.Context, p media.Prober, rec safety.Recorder, st safety.Staged, src media.Info, want safety.Want, timeout time.Duration) (safety.Verdict, error) {
	return safety.Verify(ctx, p, rec, st, src, want, timeout)
}

func (RealSafety) Place(ctx context.Context, st safety.Staged, final string) (string, error) {
	return safety.Place(ctx, st, final)
}

var _ Safety = RealSafety{}
