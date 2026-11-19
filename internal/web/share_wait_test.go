// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// A library inside a saved share's mount point waits while the share does not answer, and not after it does.
func TestLibraryOnAShareWaitsUntilItAnswers(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	root, answers := shareRoot, shareAnswers
	shareRoot = t.TempDir()
	shareAnswers = func(p string) bool { _, err := os.Stat(p); return err == nil } // the fake mount: a folder that exists
	t.Cleanup(func() { shareRoot, shareAnswers = root, answers })

	sh, err := st.SaveShare(jobs.Share{Name: "Movies NAS", Type: "smb", Address: "//nas/movies"}, "")
	if err != nil {
		t.Fatal(err)
	}
	mp := mountPointFor(sh)
	films := filepath.Join(mp, "Films")
	other := t.TempDir() // a local library, not on the share
	// the share is not mounted: its mount point is not there
	waiting, err := WaitingMounts(st)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(waiting, mp) {
		t.Fatalf("waiting %v; want the mount point %s", waiting, mp)
	}
	if !Waits(films, waiting) || !Waits(mp, waiting) {
		t.Error("a library inside the unmounted share does not wait")
	}
	if Waits(mp+"2/Films", waiting) {
		t.Error("a sibling folder that only shares the name is treated as inside the share")
	}
	if Waits(other, waiting) {
		t.Error("a local library waits for a share it is not on")
	}

	// the share answers: nothing waits
	if err := os.MkdirAll(films, 0o755); err != nil {
		t.Fatal(err)
	}
	waiting, _ = WaitingMounts(st)
	if Waits(films, waiting) {
		t.Errorf("the library waits after the share answered: %v", waiting)
	}
}
