//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

func fileIs(p, s string) bool {
	b, err := os.ReadFile(p)
	return err == nil && string(b) == s
}

// noRun is the version read for recovery tests: recovery decides from the files and must not run <bin>, so this fails
// the test if it is asked about bin. (The previous program's version is still read after each operation.)
func noRun(t *testing.T, bin string) func(string) (string, error) {
	return func(p string) (string, error) {
		if p == bin {
			t.Error("recovery ran the program to decide")
			return "", errors.New("a broken program does not run")
		}
		return "0.1.0", nil
	}
}

// A crash after the swap (no .new left): the new program answers at startup, so it is kept and the record is removed.
func TestPendingNewProgramHealthyIsKept(t *testing.T) {
	var h *helper
	h, restarts := testHelper(t, "", func() bool { return fileIs(h.bin, "new") })
	h.version = noRun(t, h.bin)
	writeFile(t, h.bin, "new")
	writeFile(t, h.bin+".previous", "old")
	writeFile(t, h.bin+pendingSuffix, "0.2.0")
	h.recoverPending()
	if a := finish(t, h); a.State != "idle" || a.Error != "" {
		t.Fatalf("recovery: %+v", a)
	}
	if exists(h.bin+pendingSuffix) || read(t, h.bin) != "new" || read(t, h.bin+".previous") != "old" || *restarts != 1 {
		t.Errorf("pending kept %v, bin %q, previous %q, restarts %d", exists(h.bin+pendingSuffix), read(t, h.bin), read(t, h.bin+".previous"), *restarts)
	}
}

// A crash after the swap, and the new program is broken: it cannot even run (its version read fails), and it never
// answers. The previous one is put back, the copy before it becomes the previous one again, the record is removed,
// and the failure is what status shows.
func TestPendingNewProgramNeverHealthyIsRolledBack(t *testing.T) {
	var h *helper
	h, restarts := testHelper(t, "", func() bool { return fileIs(h.bin, "old") })
	h.version = noRun(t, h.bin)
	writeFile(t, h.bin, "new")
	writeFile(t, h.bin+".previous", "old")
	writeFile(t, h.bin+".previous.older", "older")
	writeFile(t, h.bin+pendingSuffix, "0.2.0")
	h.recoverPending()
	a := finish(t, h)
	if a.State != "failed" || !strings.Contains(a.Error, "rolled back to the previous version") {
		t.Fatalf("recovery: %+v", a)
	}
	if exists(h.bin+pendingSuffix) || read(t, h.bin) != "old" || read(t, h.bin+".previous") != "older" || exists(h.bin+".previous.older") {
		t.Errorf("pending kept %v, bin %q, previous %q, older kept %v", exists(h.bin+pendingSuffix), read(t, h.bin), read(t, h.bin+".previous"), exists(h.bin+".previous.older"))
	}
	if *restarts != 2 {
		t.Errorf("restarts %d, want 2 (the new program, then the previous one)", *restarts)
	}
}

// A crash before the swap: <bin>.new is still there, so the swap never happened and <bin> is the program the install
// started from. Nothing is run and nothing is rolled back (no restart, no health wait); the record and .new go.
func TestPendingWhoseSwapNeverHappenedIsOnlyRemoved(t *testing.T) {
	h, restarts := testHelper(t, "", func() bool { return false })
	h.version = noRun(t, h.bin)
	writeFile(t, h.bin, "old")
	writeFile(t, h.bin+".new", "new")
	writeFile(t, h.bin+pendingSuffix, "0.2.0")
	h.recoverPending()
	if a := finish(t, h); a.State != "idle" || a.Error != "" {
		t.Fatalf("recovery: %+v", a)
	}
	if exists(h.bin+pendingSuffix) || exists(h.bin+".new") || read(t, h.bin) != "old" || *restarts != 0 {
		t.Errorf("pending kept %v, .new kept %v, bin %q, restarts %d", exists(h.bin+pendingSuffix), exists(h.bin+".new"), read(t, h.bin), *restarts)
	}
}

// A normal install: the record exists while the new program is checked, and is gone once it answers.
func TestInstallRecordsItsPendingUntilTheNewProgramAnswers(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "new")
	var sawPending bool
	var h *helper
	h, restarts := testHelper(t, srv.URL, func() bool {
		sawPending = exists(h.bin + pendingSuffix)
		return true
	})
	h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"})
	if a := finish(t, h); a.State != "idle" || a.Error != "" {
		t.Fatalf("install: %+v", a)
	}
	if !sawPending || exists(h.bin+pendingSuffix) || read(t, h.bin) != "new" || *restarts != 1 {
		t.Errorf("pending seen while checked %v, pending left %v, bin %q, restarts %d", sawPending, exists(h.bin+pendingSuffix), read(t, h.bin), *restarts)
	}
}

// An install that is undone (the new program does not answer) leaves no record behind.
func TestUndoneInstallLeavesNoPending(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "new")
	var h *helper
	h, _ = testHelper(t, srv.URL, func() bool { return fileIs(h.bin, "old") })
	h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"})
	a := finish(t, h)
	if a.Error == "" || read(t, h.bin) != "old" {
		t.Fatalf("install: %+v, bin %q", a, read(t, h.bin))
	}
	if exists(h.bin + pendingSuffix) {
		t.Error("the record of an undone install was left behind")
	}
}
