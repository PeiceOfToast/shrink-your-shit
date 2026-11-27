// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux || darwin

package media

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestProbeOfAFIFOIsNotMedia covers review 801 (N2): a named pipe named like a video must be
// refused without opening it. open() on a pipe waits for a writer, and that wait ignores the
// context, so before the fix a media folder holding a FIFO wedged a scan for good.
func TestProbeOfAFIFOIsNotMedia(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Probe(context.Background(), path)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotMedia) {
			t.Fatalf("probe = %v, want ErrNotMedia", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Probe blocked on a named pipe; opening it waits for a writer the context cannot cancel")
	}
}
