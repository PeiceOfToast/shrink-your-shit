// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// watchFixture is a Watcher whose outside world is recorded: setups run in order, and healthy answers by
// healthOK(number of setups run so far).
type watchFixture struct {
	w     *Watcher
	ran   []string
	stops int
}

func newWatchFixture(t *testing.T, healthOK func(ran int) bool, runErr map[string]error, stopErr error) *watchFixture {
	t.Helper()
	dir := t.TempDir()
	f := &watchFixture{}
	f.w = &Watcher{PID: 4242, From: "0.1.0", To: "0.2.0", DataDir: dir, Health: "http://unused/healthz",
		NewSetup: filepath.Join(dir, "new.exe"), OldSetup: filepath.Join(dir, "old.exe"),
		Wait: 20 * time.Millisecond, Poll: time.Millisecond,
		StopApp: func(pid int) error {
			f.stops++
			if pid != 4242 {
				t.Errorf("stopped pid %d", pid)
			}
			return stopErr
		},
		RunSetup: func(setup string) error {
			f.ran = append(f.ran, filepath.Base(setup))
			return runErr[filepath.Base(setup)]
		},
		Healthy: func(string) bool { return healthOK(len(f.ran)) },
		Logf:    func(string, ...any) {},
	}
	for _, p := range []string{f.w.NewSetup, f.w.OldSetup} {
		if err := os.WriteFile(p, []byte("setup"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteMarker(dir, Marker{Kind: "update", From: "0.1.0", To: "0.2.0", Backup: "b", Resume: true}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *watchFixture) result(t *testing.T) *Result {
	t.Helper()
	return LastResult(f.w.DataDir)
}

// The new program answers: the watcher ends and leaves the marker for that program to settle.
func TestWatchHealthyKeepsTheNewProgram(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	if err := f.w.Watch(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.ran, ",") != "new.exe" {
		t.Errorf("setups run %v", f.ran)
	}
	if f.result(t) != nil || ReadMarker(f.w.DataDir) == nil {
		t.Error("a result was written, or the marker removed, before the new program settled it")
	}
	if _, err := os.Stat(f.w.NewSetup); !os.IsNotExist(err) {
		t.Error("the installer was kept after the update")
	}
}

// The new program does not answer: the previous installer is run, and that program settles it as rolled back.
func TestWatchUnhealthyRunsThePreviousSetup(t *testing.T) {
	f := newWatchFixture(t, func(ran int) bool { return ran >= 2 }, nil, nil)
	if err := f.w.Watch(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.ran, ",") != "new.exe,old.exe" {
		t.Errorf("setups run %v", f.ran)
	}
	if f.result(t) != nil {
		t.Error("a result was written; the previous program should settle the roll back itself")
	}
	if ReadMarker(f.w.DataDir) == nil {
		t.Error("the marker went; the previous program needs it to settle")
	}
}

// Neither program answers: the failure is written in the same words the app shows, and the marker stays.
func TestWatchWritesTheFailureWhenThePreviousDoesNotStart(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return false }, nil, nil)
	if err := f.w.Watch(); err == nil {
		t.Fatal("no error when neither program started")
	}
	if strings.Join(f.ran, ",") != "new.exe,old.exe" {
		t.Errorf("setups run %v", f.ran)
	}
	r := f.result(t)
	want := "Update to 0.2.0 failed; rolled back to 0.1.0, but it did not start either"
	if r == nil || r.OK || r.Message != want || r.From != "0.1.0" || r.To != "0.2.0" {
		b, _ := json.Marshal(r)
		t.Errorf("result %s, want message %q", b, want)
	}
}

// The new installer fails: the previous one is run at once, and a healthy previous program is the outcome.
func TestWatchPutsThePreviousBackWhenTheNewInstallerFails(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, map[string]error{"new.exe": errors.New("exit 1")}, nil)
	if err := f.w.Watch(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.ran, ",") != "new.exe,old.exe" {
		t.Errorf("setups run %v", f.ran)
	}
}

// The app cannot be stopped: nothing is installed, and the failure is written.
func TestWatchStopsWhenTheAppCannotBeStopped(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, errors.New("access denied"))
	if err := f.w.Watch(); err == nil {
		t.Fatal("no error")
	}
	if len(f.ran) != 0 {
		t.Errorf("setups run %v", f.ran)
	}
	if r := f.result(t); r == nil || r.OK || !strings.Contains(r.Message, "could not be stopped") {
		t.Errorf("result %+v", r)
	}
}
