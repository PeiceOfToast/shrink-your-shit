//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o755); err != nil {
		t.Fatal(err)
	}
}

// A crash after previous -> older but before the link leaves previous missing: repair puts older back.
func TestRepairPutsBackAPreviousThatIsMissing(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "shrink-your-shit")
	writeFile(t, bin, "new")
	writeFile(t, bin+".previous.older", "older")
	if err := repairLeftovers(bin); err != nil {
		t.Fatal(err)
	}
	if read(t, bin+".previous") != "older" || exists(bin+".previous.older") || read(t, bin) != "new" {
		t.Errorf("previous %q, older kept %v, bin %q", read(t, bin+".previous"), exists(bin+".previous.older"), read(t, bin))
	}
}

// A crash between the link and the rename leaves previous as a second name for the program: repair removes it,
// and puts older back as previous, so the program is never reported as its own previous version.
func TestRepairRemovesAPreviousThatIsTheProgram(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "shrink-your-shit")
	writeFile(t, bin, "current")
	if err := os.Link(bin, bin+".previous"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, bin+".previous.older", "older")
	if err := repairLeftovers(bin); err != nil {
		t.Fatal(err)
	}
	if sameFile(bin+".previous", bin) || read(t, bin+".previous") != "older" || read(t, bin) != "current" {
		t.Errorf("previous %q (same as bin %v), bin %q", read(t, bin+".previous"), sameFile(bin+".previous", bin), read(t, bin))
	}
}

func TestRepairLeavesAGoodStateAlone(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "shrink-your-shit")
	writeFile(t, bin, "new")
	writeFile(t, bin+".previous", "old")
	if err := repairLeftovers(bin); err != nil {
		t.Fatal(err)
	}
	if read(t, bin) != "new" || read(t, bin+".previous") != "old" {
		t.Errorf("bin %q, previous %q", read(t, bin), read(t, bin+".previous"))
	}
}

// On a disk without hard links, install keeps the previous program as a copy, and it still installs.
func TestInstallCopiesThePreviousWhereHardLinksFail(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "new")
	h, restarts := testHelper(t, srv.URL, func() bool { return true })
	old := link
	link = func(string, string) error { return syscall.EPERM }
	t.Cleanup(func() { link = old })
	h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"})
	if a := finish(t, h); a.State != "idle" || a.Error != "" || !a.Previous {
		t.Fatalf("install without hard links: %+v", a)
	}
	prev := h.bin + ".previous"
	if read(t, h.bin) != "new" || read(t, prev) != "old" || *restarts != 1 {
		t.Errorf("bin %q, previous %q, restarts %d", read(t, h.bin), read(t, prev), *restarts)
	}
	if sameFile(prev, h.bin) || exists(prev+".copying") {
		t.Error("the previous program is still a second name for the new one, or a temp copy was left")
	}
}

// When even the copy fails, install stops before the program is touched.
func TestInstallStopsWhenTheCopyFailsToo(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "new")
	h, restarts := testHelper(t, srv.URL, func() bool { return true })
	old := link
	link = func(string, string) error { return syscall.EPERM }
	t.Cleanup(func() { link = old })
	if err := os.Mkdir(h.bin+".previous"+copyingSuffix, 0o755); err != nil { // the copy cannot be opened
		t.Fatal(err)
	}
	h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"})
	if a := finish(t, h); a.State != "failed" || read(t, h.bin) != "old" || *restarts != 0 {
		t.Errorf("after: %+v, bin %q, restarts %d", a, read(t, h.bin), *restarts)
	}
}

// Roll back is one exchange: the program is never missing, and nothing is left under a temp name.
func TestRollbackExchangesInOneStep(t *testing.T) {
	h, _ := testHelper(t, "", func() bool { return true })
	writeFile(t, h.bin+".previous", "prev")
	var calls int
	old := exchange
	exchange = func(a, b string) error {
		calls++
		if !exists(a) || !exists(b) {
			return errors.New("a name is missing during the exchange")
		}
		return old(a, b)
	}
	t.Cleanup(func() { exchange = old })
	h.handle(update.HelperRequest{Cmd: "rollback"})
	if a := finish(t, h); a.State != "idle" || read(t, h.bin) != "prev" || read(t, h.bin+".previous") != "old" || calls != 1 {
		t.Errorf("rollback: %+v, bin %q, previous %q, exchanges %d", a, read(t, h.bin), read(t, h.bin+".previous"), calls)
	}
	if exists(h.bin + ".swap") {
		t.Error("a temp name was left behind")
	}
}

// A disk that cannot exchange swaps by renames: the roll back still swaps both files.
func TestRollbackFallsBackToRenamesWhereTheDiskCannotExchange(t *testing.T) {
	h, _ := testHelper(t, "", func() bool { return true })
	writeFile(t, h.bin+".previous", "prev")
	old := renameExchange
	renameExchange = func(a, b string) error { return syscall.EINVAL }
	t.Cleanup(func() { renameExchange = old })
	h.handle(update.HelperRequest{Cmd: "rollback"})
	if a := finish(t, h); a.State != "idle" || read(t, h.bin) != "prev" || read(t, h.bin+".previous") != "old" {
		t.Errorf("rollback by renames: %+v, bin %q, previous %q", a, read(t, h.bin), read(t, h.bin+".previous"))
	}
	if exists(h.bin + ".swap") {
		t.Error("a temp name was left behind")
	}
}

// A previous that is a copy of the program (crash between the copy and the rename) is removed by repair.
func TestRepairRemovesAPreviousThatIsACopyOfTheProgram(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "shrink-your-shit")
	writeFile(t, bin, "current")
	writeFile(t, bin+".previous", "current")
	writeFile(t, bin+".previous.older", "older")
	if err := repairLeftovers(bin); err != nil {
		t.Fatal(err)
	}
	if read(t, bin+".previous") != "older" || read(t, bin) != "current" {
		t.Errorf("previous %q, bin %q", read(t, bin+".previous"), read(t, bin))
	}
}
