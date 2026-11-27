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
	// Stage makes a private folder for dst. An empty base means beside dst; otherwise the folder
	// is made in base (a try stages in the app's own temp folder, never in the library).
	Stage(base, dst string) (safety.Staged, error)
	Verify(ctx context.Context, c safety.Checker, rec safety.Recorder, st safety.Staged, src media.Info, want safety.Want, timeout time.Duration) (safety.Verdict, error)
	// CheckSource decodes the original before it is converted (L-SAFE-4).
	CheckSource(ctx context.Context, c safety.Checker, rec safety.Recorder, path string, src media.Info, timeout time.Duration) (safety.Verdict, error)
	Place(ctx context.Context, st safety.Staged, final string) (string, error)
}

// RealSafety is internal/safety, unchanged.
type RealSafety struct{}

func (RealSafety) Stage(base, dst string) (safety.Staged, error) {
	if base == "" {
		return safety.Stage(dst)
	}
	return safety.StageIn(base, dst)
}

func (RealSafety) Verify(ctx context.Context, c safety.Checker, rec safety.Recorder, st safety.Staged, src media.Info, want safety.Want, timeout time.Duration) (safety.Verdict, error) {
	return safety.Verify(ctx, c, rec, st, src, want, timeout)
}

func (RealSafety) CheckSource(ctx context.Context, c safety.Checker, rec safety.Recorder, path string, src media.Info, timeout time.Duration) (safety.Verdict, error) {
	return safety.SourceCheck(ctx, c, rec, path, src, timeout)
}

func (RealSafety) Place(ctx context.Context, st safety.Staged, final string) (string, error) {
	return safety.Place(ctx, st, final)
}

var _ Safety = RealSafety{}
