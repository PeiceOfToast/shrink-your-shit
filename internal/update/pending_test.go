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

// watcherDead makes the record's watcher look gone for the rest of the test: the kill tests model a watcher
// that was killed, so recovery is allowed to act.
func watcherDead(t *testing.T) {
	t.Helper()
	old := pidAlive
	pidAlive = func(int) bool { return false }
	t.Cleanup(func() { pidAlive = old })
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
	watcherDead(t)
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
	watcherDead(t)
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
	watcherDead(t)
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
	watcherDead(t)
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

// While the watcher runs, recovery leaves the record and both installers alone, whatever the app's version: the
// new app starts inside the installer's run, and the old app can be relaunched during it. (Findings 1 and 2.)
func TestRecoverPendingLeavesAnUpdateItsWatcherIsRunning(t *testing.T) {
	old := pidAlive
	pidAlive = func(int) bool { return true }
	t.Cleanup(func() { pidAlive = old })
	for _, running := range []string{"0.2.0", "0.1.0"} {
		f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
		f.w.RunSetup = func(string) error { panic(killed{}) }
		runKilled(t, func() { f.w.Watch() })
		if err := RecoverPending(f.w.DataDir, running, time.Now()); err != nil {
			t.Fatal(err)
		}
		if readPending(f.w.DataDir) == nil || !exists(f.w.NewSetup) || !exists(f.w.OldSetup) {
			t.Fatalf("running %s: recovery cleared the record or an installer while the watcher runs", running)
		}
		if LastResult(f.w.DataDir) != nil {
			t.Fatalf("running %s: recovery wrote a result while the watcher runs", running)
		}
	}
}

// A live PID is not proof the watcher still runs: the record is older than any watcher run, so the PID now belongs
// to something else, and recovery goes on.
func TestRecoverPendingProceedsWhenAnOldRecordsPIDIsAlive(t *testing.T) {
	old := pidAlive
	pidAlive = func(int) bool { return true }
	t.Cleanup(func() { pidAlive = old })
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	f.w.RunSetup = func(string) error { panic(killed{}) }
	runKilled(t, func() { f.w.Watch() })
	p := readPending(f.w.DataDir)
	if p == nil {
		t.Fatal("no record after the kill")
	}

	if err := RecoverPending(f.w.DataDir, "0.2.0", p.At.Add(watchMaxLife)); err != nil {
		t.Fatal(err)
	}
	if readPending(f.w.DataDir) != nil || exists(f.w.NewSetup) || exists(f.w.OldSetup) {
		t.Fatal("an old record under a live PID was left in place")
	}
}

// A fresh record whose PID is alive is the watcher's own run: recovery leaves it alone.
func TestRecoverPendingLeavesAFreshRecordWhosePIDIsAlive(t *testing.T) {
	old := pidAlive
	pidAlive = func(int) bool { return true }
	t.Cleanup(func() { pidAlive = old })
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	f.w.RunSetup = func(string) error { panic(killed{}) }
	runKilled(t, func() { f.w.Watch() })
	p := readPending(f.w.DataDir)
	if p == nil {
		t.Fatal("no record after the kill")
	}

	if err := RecoverPending(f.w.DataDir, "0.2.0", p.At.Add(watchMaxLife-time.Second)); err != nil {
		t.Fatal(err)
	}
	if readPending(f.w.DataDir) == nil || !exists(f.w.NewSetup) || !exists(f.w.OldSetup) {
		t.Fatal("a fresh record under a live PID was cleared while its watcher may run")
	}
}

// The record's paths are read back from disk: recovery removes only installers in the update folder.
func TestRecoverPendingWontRemoveFilesOutsideTheUpdateFolder(t *testing.T) {
	watcherDead(t)
	dir := t.TempDir()
	victim := filepath.Join(dir, "syp.db")
	if err := os.WriteFile(victim, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writePending(dir, pendingUpdate{From: "0.1.0", To: "0.2.0", NewSetup: victim, OldSetup: filepath.Join(dir, updateDir, "old.exe"), At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := RecoverPending(dir, "0.2.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !exists(victim) {
		t.Fatal("recovery removed a file outside the update folder")
	}
}

// When the record cannot be written the update never starts: the installers go with it, and the app is not stopped.
func TestWatchRemovesTheInstallersWhenTheRecordCannotBeWritten(t *testing.T) {
	f := newWatchFixture(t, func(int) bool { return true }, nil, nil)
	// A folder where the record file goes makes its save fail.
	if err := os.Mkdir(filepath.Join(f.w.DataDir, updateDir, pendingFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.w.Watch(); err == nil {
		t.Fatal("no error when the record could not be written")
	}
	if f.stops != 0 {
		t.Fatalf("the app was stopped %d times without a record", f.stops)
	}
	if exists(f.w.NewSetup) || exists(f.w.OldSetup) {
		t.Fatal("the installers were left behind after the update did not start")
	}
}
