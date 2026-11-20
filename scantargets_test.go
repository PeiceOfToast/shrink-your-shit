// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A started library at a system folder, stored before slice 23's guard existed, is never a scan target
// (review f4c41fd).
func TestScanTargetsLeaveOutSystemFolders(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "syp.db")
	st, err := jobs.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ok, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	old, _ := st.AddLibrary("Old", t.TempDir(), rules.SliderTemplate(3), true)
	// seeded as an older version could have stored it
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE libraries SET path=? WHERE id=?`, jobs.SystemDirForTest(), old.ID); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	targets, err := scanTargets(st)()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].LibraryID != ok.ID {
		t.Errorf("targets %+v, want only Movies", targets)
	}
}

// A library with no workflow is never a scan target, whatever its started flag says: nothing is walked or
// queued for it until a workflow is assigned (the user's "add a library, assign its workflow later").
func TestScanTargetsLeaveOutLibrariesWithoutAWorkflow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "syp.db")
	st, err := jobs.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ok, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	later, _ := st.AddLibraryWithoutFlow("Later", t.TempDir(), false)
	// started by hand, as no path allows any more: the missing flow is the guard, not the flag
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE libraries SET started=1 WHERE id=?`, later.ID); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	targets, err := scanTargets(st)()
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].LibraryID != ok.ID {
		t.Errorf("targets %+v, want only Movies", targets)
	}
}
