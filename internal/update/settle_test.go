// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The watcher's failure detail is what the next start shows, not the generic line Settle writes.
func TestSettleKeepsTheWatchersFailure(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 19, 7, 0, 0, 0, time.UTC)
	m := &Marker{Kind: "update", From: "0.1.0", To: "0.2.0", At: at}
	msg := "Update to 0.2.0 failed; rolled back to 0.1.0, but it did not start either"
	if err := WriteResult(dir, Result{Kind: "update", From: "0.1.0", To: "0.2.0", Message: msg, At: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	r, err := Settle(dir, m, "0.1.0", at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if r.OK || r.Message != msg {
		t.Errorf("settled as %+v, want the watcher's message %q", r, msg)
	}
	if got := LastResult(dir); got == nil || got.Message != msg {
		t.Errorf("stored result %+v, want the watcher's message", got)
	}
}

// A failure from an earlier attempt (before this update started) is not shown for this one.
func TestSettleIgnoresAnEarlierFailure(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 19, 7, 0, 0, 0, time.UTC)
	m := &Marker{Kind: "update", From: "0.1.0", To: "0.2.0", At: at}
	if err := WriteResult(dir, Result{Kind: "update", From: "0.1.0", To: "0.2.0", Message: "old failure", At: at.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	r, err := Settle(dir, m, "0.1.0", at.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	want := "Update to 0.2.0 failed; rolled back to 0.1.0"
	if r.Message != want {
		t.Errorf("message %q, want %q", r.Message, want)
	}
}

// A result that cannot be saved is logged with what was lost, and the error is returned.
func TestWatchLogsAResultThatCannotBeSaved(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, errors.New("access denied"))
	f.w.DataDir = filepath.Join(t.TempDir(), "missing")
	var logs []string
	f.w.Logf = func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }
	if err := f.w.Watch(); err == nil {
		t.Fatal("no error when the result could not be saved")
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "could not be stopped") || !strings.Contains(joined, "could not be saved") {
		t.Errorf("log %q, want the failure and that its result could not be saved", joined)
	}
}
