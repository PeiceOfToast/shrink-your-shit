// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// killed is what a simulated kill of the watcher panics with: the process is gone at that point, so nothing
// after it runs (no defers that clean up are simulated; Watch's own bookkeeping is what is under test).
type killed struct{}

// runKilled runs f and fails the test unless f was killed.
func runKilled(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("the watcher was not killed at the step the test names")
		} else if _, ok := r.(killed); !ok {
			panic(r)
		}
	}()
	f()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// The record is written before the app is stopped, so a kill at that stop still leaves it to find.
func TestWatchWritesTheRecordBeforeStoppingTheApp(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	f.w.StopApp = func(int) error {
		p := readPending(f.w.DataDir)
		if p == nil || p.From != "0.1.0" || p.To != "0.2.0" || p.NewSetup != f.w.NewSetup || p.OldSetup != f.w.OldSetup {
			t.Fatalf("at the stop the record is %+v, want the update 0.1.0 -> 0.2.0 with both installers", p)
		}
		panic(killed{})
	}
	runKilled(t, func() { f.w.Watch() })
}

// A kill while the app is stopped leaves the record and the old installer; the next start puts it right.
func TestWatchKilledAfterTheAppStoppedRecoversToTheOldVersion(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	f.w.RunSetup = func(string) error { panic(killed{}) }
	runKilled(t, func() { f.w.Watch() })
	if readPending(f.w.DataDir) == nil || !exists(f.w.OldSetup) {
		t.Fatal("a kill mid-update must leave the record and the old installer for the next start")
	}

	// The app is still the old version and its marker is there: Settle reports the roll back, so recovery
	// writes no result of its own, but clears the record and the installers.
	if err := RecoverPending(f.w.DataDir, "0.1.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if readPending(f.w.DataDir) != nil || exists(f.w.NewSetup) || exists(f.w.OldSetup) {
		t.Fatal("recovery did not clear the record and the installers")
	}
	if LastResult(f.w.DataDir) != nil {
		t.Fatal("recovery wrote a result while the marker is there; Settle must write it")
	}
}

// A kill with no marker (the app's own record of the update is gone) is a cut-off update: the result says so.
func TestRecoverPendingCutOffWritesAResult(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	f.w.RunSetup = func(string) error { panic(killed{}) }
	runKilled(t, func() { f.w.Watch() })
	if err := os.Remove(filepath.Join(f.w.DataDir, markerFile)); err != nil {
		t.Fatal(err)
	}

	if err := RecoverPending(f.w.DataDir, "0.1.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	r := LastResult(f.w.DataDir)
	if r == nil || r.OK || !r.RolledBack || r.From != "0.1.0" || r.To != "0.2.0" || !strings.Contains(r.Message, "cut off") {
		t.Fatalf("result = %+v, want a cut-off failure that is still on 0.1.0", r)
	}
	if readPending(f.w.DataDir) != nil || exists(f.w.NewSetup) || exists(f.w.OldSetup) {
		t.Fatal("recovery did not clear the record and the installers")
	}
}

// The new version is serving when the start finds the record: the update is done, nothing is reported as failed.
func TestRecoverPendingNewVersionFinishesQuietly(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	f.w.RunSetup = func(string) error { panic(killed{}) }
	runKilled(t, func() { f.w.Watch() })

	if err := RecoverPending(f.w.DataDir, "0.2.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if LastResult(f.w.DataDir) != nil {
		t.Fatal("a new version that is serving must not write a failed result")
	}
	if readPending(f.w.DataDir) != nil || exists(f.w.NewSetup) || exists(f.w.OldSetup) {
		t.Fatal("recovery did not clear the record and the installers")
	}
}

// When the failed result cannot be saved, the record and the installers stay, so the next start can try again.
func TestRecoverPendingKeepsTheRecordWhenTheResultCannotBeSaved(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	f.w.RunSetup = func(string) error { panic(killed{}) }
	runKilled(t, func() { f.w.Watch() })
	if err := os.Remove(filepath.Join(f.w.DataDir, markerFile)); err != nil {
		t.Fatal(err)
	}
	// A directory where the result file goes makes the save fail.
	if err := os.Mkdir(filepath.Join(f.w.DataDir, resultFile), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := RecoverPending(f.w.DataDir, "0.1.0", time.Now()); err == nil {
		t.Fatal("recovery must report that the result could not be saved")
	}
	if readPending(f.w.DataDir) == nil || !exists(f.w.OldSetup) {
		t.Fatal("the record and the old installer must stay when the result is not saved")
	}
}

// A failed result is saved before the record is cleared, so a failure the watcher reports is never lost.
func TestWatchKeepsTheRecordWhenItsFailedResultCannotBeSaved(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return false }, map[string]error{"old.exe": os.ErrPermission}, nil)
	if err := os.Mkdir(filepath.Join(f.w.DataDir, resultFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Watch(); err == nil {
		t.Fatal("a failed update must return an error")
	}
	if readPending(f.w.DataDir) == nil || !exists(f.w.OldSetup) {
		t.Fatal("the record and installers must stay when the failed result is not saved")
	}
}

// A clean update clears the record and removes both installers.
func TestWatchClearsTheRecordOnceAProgramIsServing(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	if err := f.w.Watch(); err != nil {
		t.Fatal(err)
	}
	if readPending(f.w.DataDir) != nil || exists(f.w.NewSetup) || exists(f.w.OldSetup) {
		t.Fatal("a serving program must leave no record and no installers")
	}
}
