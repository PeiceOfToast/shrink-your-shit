// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// When the watcher's result could not be saved, the next start says so in the app's log (the detail is only in
// update/watch.log). A rollback with no failure to save logs nothing.
func TestSettleLogsWhenTheWatcherLeftNoResult(t *testing.T) {
	at := time.Date(2026, 11, 19, 7, 0, 0, 0, time.UTC)
	m := &Marker{Kind: "update", From: "0.1.0", To: "0.2.0", At: at}
	cases := []struct {
		name, watchLog string
		want           bool
	}{
		{"result could not be saved", "did not come up; putting 0.1.0 back\nthe result above could not be saved, so the app shows a generic message at its next start: access denied\n", true},
		{"rolled back with no failure to save", "did not come up; putting 0.1.0 back\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, updateDir, "watch.log")
			if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(logPath, []byte(c.watchLog), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(logPath, at.Add(time.Minute), at.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			old := log.Writer()
			log.SetOutput(&out)
			defer log.SetOutput(old)
			r, err := Settle(dir, m, "0.1.0", at.Add(2*time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if want := "Update to 0.2.0 failed; rolled back to 0.1.0"; r.Message != want {
				t.Errorf("message %q, want the generic %q", r.Message, want)
			}
			got := strings.Contains(out.String(), "left no result") && strings.Contains(out.String(), "update/watch.log")
			if got != c.want {
				t.Errorf("logged %q; want the no-result line %v", out.String(), c.want)
			}
		})
	}
}

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
	if r.RolledBack {
		t.Errorf("rolledBack true, want false: the watcher says the previous version did not start")
	}
	if got := LastResult(dir); got == nil || got.Message != msg {
		t.Errorf("stored result %+v, want the watcher's message", got)
	}
}

// A manual roll back says rolledBack only when the previous version is running.
func TestSettleRollbackSaysWhetherItSwitched(t *testing.T) {
	at := time.Date(2026, 11, 19, 7, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		running string
		want    bool
	}{{"0.1.0", true}, {"0.2.0", false}} {
		m := &Marker{Kind: "rollback", From: "0.2.0", To: "0.1.0", At: at}
		r, err := Settle(t.TempDir(), m, c.running, at.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if r.RolledBack != c.want || r.OK != c.want {
			t.Errorf("running %s: rolledBack %v ok %v, want %v", c.running, r.RolledBack, r.OK, c.want)
		}
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
