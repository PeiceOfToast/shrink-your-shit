// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// fakePlaces scores each place by its start time, the same for every alignment, so the picture check's
// per-place result is exactly what the test says. A 60 s video has places at 0, 25 and 45 s.
func fakePlaces(scores map[float64]float64) scoreFunc {
	return func(_ context.Context, _, _ string, _, _ int, seg segment, _, _ float64, _ string) (float64, []frameScore, error) {
		return scores[seg.start], nil, nil
	}
}

func fakeCheckWorker(scores map[float64]float64) *Worker {
	return &Worker{
		MinSSIM:    0.90,
		filterMemo: map[string]bool{"ssim": true}, // the filter is there: no ffmpeg probe
		scorePlace: fakePlaces(scores),
	}
}

func fakeInfo() media.Info {
	return media.Info{DurationSec: 60, Streams: []media.Stream{{Index: 0, Type: "video", Width: 1920, Height: 1080}}}
}

// The check's job step shows the lowest passing place: 0.99, 0.91 and 0.98 pass at 0.90 and 0.91 is stored.
func TestSSIMCheckStoresTheLowestPassingPlace(t *testing.T) {
	w := fakeCheckWorker(map[float64]float64{0: 0.99, 25: 0.91, 45: 0.98})
	if err := w.ssimCheck(context.Background(), fakeInfo(), fakeInfo(), "src", "tmp", 0); err != nil {
		t.Fatalf("ssimCheck returned %v, want nil", err)
	}
	if w.check == nil || w.check.Score != 0.91 || !w.check.Passed || w.check.Min != 0.90 {
		t.Fatalf("check %+v, want score 0.91 passed at min 0.90", w.check)
	}
}

// A place that fails keeps the original, and its score is the one stored, as it was before.
func TestSSIMCheckFailingPlaceStoresItsScoreAndKeeps(t *testing.T) {
	w := fakeCheckWorker(map[float64]float64{0: 0.99, 25: 0.85, 45: 0.98})
	err := w.ssimCheck(context.Background(), fakeInfo(), fakeInfo(), "src", "tmp", 0)
	k, ok := err.(*KeptError)
	if !ok {
		t.Fatalf("ssimCheck returned %v, want a *KeptError", err)
	}
	if w.check == nil || w.check.Score != 0.85 || w.check.Passed {
		t.Fatalf("check %+v, want score 0.85 not passed", w.check)
	}
	if !strings.Contains(k.Reason, "similarity 0.850") {
		t.Fatalf("reason %q, want the failing place's similarity", k.Reason)
	}
}

// A place that errors after a passing place keeps the lowest passing score: the job still shows 0.99 passed.
func TestSSIMCheckErrorKeepsTheLowestScore(t *testing.T) {
	w := &Worker{
		MinSSIM:    0.90,
		filterMemo: map[string]bool{"ssim": true},
		scorePlace: func(_ context.Context, _, _ string, _, _ int, seg segment, _, _ float64, _ string) (float64, []frameScore, error) {
			if seg.start == 25 {
				return 0, nil, errors.New("ffmpeg died")
			}
			return 0.99, nil, nil
		},
	}
	err := w.ssimCheck(context.Background(), fakeInfo(), fakeInfo(), "src", "tmp", 0)
	if err == nil || !strings.Contains(err.Error(), "ffmpeg died") {
		t.Fatalf("ssimCheck returned %v, want the place's error", err)
	}
	if w.check == nil || w.check.Score != 0.99 || !w.check.Passed {
		t.Fatalf("check %+v, want score 0.99 passed", w.check)
	}
}
