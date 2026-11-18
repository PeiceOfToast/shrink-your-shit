// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRenameRetry(t *testing.T) {
	calls := 0
	flaky := func(a, b string) error {
		calls++
		if calls < 3 {
			return errors.New("file in use")
		}
		return nil
	}
	if err := renameRetry(context.Background(), flaky, "a", "b", 5, 0); err != nil || calls != 3 {
		t.Errorf("calls=%d err=%v", calls, err)
	}
	calls = 0
	never := func(a, b string) error { calls++; return errors.New("locked") }
	if err := renameRetry(context.Background(), never, "a", "b", 4, 0); err == nil || calls != 4 {
		t.Errorf("should give up after 4 tries: calls=%d err=%v", calls, err)
	}
}

func TestPathsWithSpacesAndDots(t *testing.T) {
	if got := BesidePath(filepath.Join("a", "x.mp4")); filepath.Base(got) != "x.hevc.mp4" {
		t.Errorf("%q", got)
	}
}

func TestRenameRetryStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	locked := func(a, b string) error {
		calls++
		cancel()
		return errors.New("locked")
	}
	start := time.Now()
	err := renameRetry(ctx, locked, "a", "b", 10, time.Hour)
	if !errors.Is(err, context.Canceled) || calls != 1 || time.Since(start) > time.Second {
		t.Errorf("err=%v calls=%d after %v", err, calls, time.Since(start))
	}
}
