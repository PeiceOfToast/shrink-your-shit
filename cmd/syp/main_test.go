// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/worker"
)

// A scan that enqueues a job wakes the worker, so the first encode starts before the scan ends. The
// second attempt at the same file is already queued and must not wake it again.
func TestScanEnqueueWakesTheWorker(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	flows := flow.Open(db)
	wfID, _, err := flows.CreateFromStarter(ctx, "shrink-videos", "Test")
	if err != nil {
		t.Fatalf("create the workflow: %v", err)
	}
	catalog := library.NewCatalog(db)
	lib, err := catalog.AddLibrary(ctx, library.NewLibrary{Path: t.TempDir(), WorkflowID: &wfID})
	if err != nil {
		t.Fatalf("add the library: %v", err)
	}
	queue := jobs.New(jobs.Options{DB: db})

	wakes := 0
	sc := newScanner(catalog, flows, queue, nil, func() { wakes++ })
	file := library.File{LibraryID: lib.ID, RelPath: "clip.mp4"}
	if err := sc.Enqueue(ctx, lib, file); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if wakes != 1 {
		t.Fatalf("wakes after one enqueue = %d, want 1", wakes)
	}

	if err := sc.Enqueue(ctx, lib, file); !errors.Is(err, library.ErrAlreadyQueued) {
		t.Fatalf("second enqueue = %v, want ErrAlreadyQueued", err)
	}
	if wakes != 1 {
		t.Fatalf("wakes after an already-queued file = %d, want 1", wakes)
	}
}

// Every migration number from 1 to the newest must be registered. A module registers its migration
// from init, so a module this binary forgets to import leaves a hole; a database that migrates past
// the hole can never fill it. Migration 7 (internal/hw) was exactly this until the blank import in
// main.go went in.
func TestTheBinaryRegistersEveryMigration(t *testing.T) {
	versions := store.MigrationVersions()
	if len(versions) == 0 {
		t.Fatal("no migrations are registered")
	}
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("migration %d is missing (registered: %v)", i+1, versions)
		}
	}
}

// An installed database from stage 1 is at schema 6. Opening it with this binary must take it to 8
// and run hw's migration 7 on the way. Without that import, 7 is not registered, the upgrade skips
// it, and encoder_state never exists on an existing install.
func TestAnInstallAtSchemaSixUpgradesThroughSeven(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		db.Close()
		t.Fatal(err)
	}

	// A stage 1 install: the setup-made workflow at its only revision, a library, and a job queued
	// before jobs recorded a revision (NULL workflow_rev).
	flows := flow.Open(db)
	wfID, _, err := flows.CreateFromStarter(ctx, "shrink-videos", "Everyday")
	if err != nil {
		db.Close()
		t.Fatalf("create the workflow: %v", err)
	}
	catalog := library.NewCatalog(db)
	lib, err := catalog.AddLibrary(ctx, library.NewLibrary{Name: "Clips", Path: t.TempDir(), WorkflowID: &wfID})
	if err != nil {
		db.Close()
		t.Fatalf("add the library: %v", err)
	}
	fileID, err := catalog.SaveFile(ctx, library.File{LibraryID: lib.ID, RelPath: "clip.mp4"}, library.Queued, "", nil)
	if err != nil {
		db.Close()
		t.Fatalf("save the file: %v", err)
	}
	jobID, _, err := jobs.New(jobs.Options{DB: db}).Enqueue(ctx, fileID, lib.ID, wfID, 0, filepath.Join(lib.Path, "clip.mp4"), "scan")
	if err != nil {
		db.Close()
		t.Fatalf("queue the old job: %v", err)
	}

	// Take the schema back to 6: remove what migrations 7, 8 and 9 add, and say the database is at
	// 6. All the data above stays, as it would on a real stage 1 install.
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		for _, s := range []string{
			`DROP TABLE IF EXISTS encoder_state`,
			`ALTER TABLE workflows DROP COLUMN draft_rev`,
			`DROP TABLE IF EXISTS sidecars`,
			`ALTER TABLE jobs DROP COLUMN subtitle_files`,
			`PRAGMA user_version = 6`,
		} {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.Close()
		t.Fatalf("make the database a schema 6 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Open it again the way the program does.
	db, err = store.Open(dir)
	if err != nil {
		t.Fatalf("open the schema 6 install: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("upgrade the schema 6 install: %v", err)
	}

	var version, encoderState int
	if err := db.Read().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 9 {
		t.Errorf("schema version after the upgrade = %d, want 9", version)
	}
	if err := db.Read().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'encoder_state'`).
		Scan(&encoderState); err != nil {
		t.Fatal(err)
	}
	if encoderState != 1 {
		t.Error("migration 7 did not run: encoder_state is missing")
	}
	var sidecarTable int
	if err := db.Read().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'sidecars'`).
		Scan(&sidecarTable); err != nil {
		t.Fatal(err)
	}
	if sidecarTable != 1 {
		t.Error("migration 9 did not run: sidecars is missing")
	}
	// The job queued before subtitle files were recorded keeps its NULL, not an invented list.
	var subtitleFiles sql.NullString
	if err := db.Read().QueryRow(`SELECT subtitle_files FROM jobs WHERE id = ?`, jobID).Scan(&subtitleFiles); err != nil {
		t.Fatal(err)
	}
	if subtitleFiles.Valid {
		t.Errorf("the old job's subtitle_files = %q, want NULL", subtitleFiles.String)
	}

	flows = flow.Open(db)
	wf, err := flows.Get(ctx, wfID)
	if err != nil {
		t.Fatalf("read the workflow after the upgrade: %v", err)
	}
	if wf.Name != "Everyday" || wf.CurrentRev != 1 {
		t.Fatalf("the workflow after the upgrade = %q at revision %d, want Everyday at revision 1", wf.Name, wf.CurrentRev)
	}
	draft, err := flows.Draft(ctx, wfID)
	if err != nil {
		t.Fatalf("read the workflow's draft: %v", err)
	}
	if _, err := flows.SaveDraft(ctx, wfID, draft.Flow, draft.Rev); err != nil {
		t.Fatalf("save a draft after the upgrade: %v", err)
	}
	rev, err := flows.Publish(ctx, wfID, nil, flow.ValidateContext{})
	if err != nil {
		t.Fatalf("publish after the upgrade: %v", err)
	}
	if rev != 2 {
		t.Fatalf("published revision = %d, want 2", rev)
	}

	// The job queued before workflow_rev was recorded still runs: the worker falls back to the
	// workflow's current revision.
	work, err := worker.Builtin{Queue: jobs.New(jobs.Options{DB: db}), DB: db, Flows: flows}.Claim(ctx, "upgrade-test")
	if err != nil {
		t.Fatalf("claim the old job: %v", err)
	}
	if work == nil {
		t.Fatal("the old job was not claimed")
	}
	if work.Err != nil {
		t.Fatalf("build the old job's input: %v", work.Err)
	}
	if work.Job.ID != jobID {
		t.Fatalf("claimed job %d, want the old job %d", work.Job.ID, jobID)
	}
	if work.In.Revision != 2 {
		t.Errorf("the old job runs revision %d, want the current revision 2", work.In.Revision)
	}
}

// TestTheStartupSweepClearsOlderTriesOnly: everything in the app's temp folder is ours, but an
// entry newer than this start may be a try another process is running right now, so it stays.
func TestTheStartupSweepClearsOlderTriesOnly(t *testing.T) {
	dir := t.TempDir()
	started := time.Now()
	old := filepath.Join(dir, "try-1234", "sample.mkv")
	newer := filepath.Join(dir, "try-5678", "sample.mkv")
	for _, path := range []string{old, newer} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("sample"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	long := time.Hour
	if err := os.Chtimes(filepath.Dir(old), started.Add(-long), started.Add(-long)); err != nil {
		t.Fatal(err)
	}

	var warned []string
	sweepTryFolder(context.Background(), dir, started, func(message string) { warned = append(warned, message) })

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an old try's folder is still there: %v", err)
	}
	if _, err := os.Stat(newer); err != nil {
		t.Errorf("a newer try's folder was removed: %v", err)
	}
	if len(warned) != 0 {
		t.Errorf("warnings = %q, want none", warned)
	}

	// A fresh install has no temp folder yet, and that is not a problem to report.
	sweepTryFolder(context.Background(), filepath.Join(dir, "not-yet"), started, func(message string) {
		t.Errorf("a missing temp folder was reported: %s", message)
	})
}
