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
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
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

	// Take the schema back to 6: remove what migrations 7 through 13 add, and say the database is
	// at 6. All the data above stays, as it would on a real stage 1 install.
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		for _, s := range []string{
			`DROP TABLE IF EXISTS encoder_state`,
			`ALTER TABLE workflows DROP COLUMN draft_rev`,
			`DROP TABLE IF EXISTS sidecars`,
			`DROP TABLE IF EXISTS replace_journal`,
			`ALTER TABLE jobs DROP COLUMN subtitle_files`,
			`ALTER TABLE jobs DROP COLUMN failure_code`,
			`DROP TABLE IF EXISTS app_settings`,
			`DROP TABLE IF EXISTS workers`,
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
	// The newest migration is 14 (the journal's kind, #877 M2); 13 is the workers (S3-4), 12 the app settings (S2-10c),
	// 11 the failure code (S3-6a), 10 the replace journal, 9 the subtitles change (#854).
	if version != 15 {
		t.Errorf("schema version after the upgrade = %d, want 15", version)
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
	var failureCode int
	if err := db.Read().QueryRow(`SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'failure_code'`).
		Scan(&failureCode); err != nil {
		t.Fatal(err)
	}
	if failureCode != 1 {
		t.Error("migration 11 did not run: jobs.failure_code is missing")
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

// TestAnInstallAtSchemaNineUpgradesToThirteen: an install at schema 9 (the subtitles change) upgrades through the
// program's own open-and-migrate path to 13, with the replace journal, the failure code, the app settings and the
// workers' settings made and the data kept (review #865 add-on, then S3-6a, S2-10c, S3-4).
func TestAnInstallAtSchemaNineUpgradesToThirteen(t *testing.T) {
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
	flows := flow.Open(db)
	wfID, _, err := flows.CreateFromStarter(ctx, "shrink-videos", "Everyday")
	if err != nil {
		db.Close()
		t.Fatalf("create the workflow: %v", err)
	}
	catalog := library.NewCatalog(db)
	if _, err := catalog.AddLibrary(ctx, library.NewLibrary{Name: "Clips", Path: t.TempDir(), WorkflowID: &wfID}); err != nil {
		db.Close()
		t.Fatalf("add the library: %v", err)
	}

	// Take the schema back to 9: remove what migrations 10 through 13 add and say the database is at 9.
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		for _, s := range []string{
			`DROP TABLE IF EXISTS replace_journal`,
			`ALTER TABLE jobs DROP COLUMN failure_code`,
			`DROP TABLE IF EXISTS app_settings`,
			`DROP TABLE IF EXISTS workers`,
			`PRAGMA user_version = 9`,
		} {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.Close()
		t.Fatalf("make the database a schema 9 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.Open(dir)
	if err != nil {
		t.Fatalf("open the schema 9 install: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("upgrade the schema 9 install: %v", err)
	}

	var version, journal int
	if err := db.Read().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 15 {
		t.Errorf("schema version after the upgrade = %d, want 15", version)
	}
	if err := db.Read().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'replace_journal'`).
		Scan(&journal); err != nil {
		t.Fatal(err)
	}
	if journal != 1 {
		t.Errorf("replace_journal tables = %d, want 1", journal)
	}
	var failureCode int
	if err := db.Read().QueryRow(`SELECT count(*) FROM pragma_table_info('jobs') WHERE name = 'failure_code'`).
		Scan(&failureCode); err != nil {
		t.Fatal(err)
	}
	if failureCode != 1 {
		t.Errorf("jobs.failure_code columns = %d, want 1", failureCode)
	}
	libs, err := library.NewCatalog(db).ListLibraries(ctx)
	if err != nil || len(libs) != 1 {
		t.Errorf("libraries after the upgrade = %d (%v), want the one kept", len(libs), err)
	}
}

// TestAnInstallAtSchemaTwelveUpgradesToThirteen: an install at schema 12 (the app settings) upgrades through the
// program's own open-and-migrate path to 13, which makes the workers table and its built-in row (S3-4).
func TestAnInstallAtSchemaTwelveUpgradesToThirteen(t *testing.T) {
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
	// A person who had already set page 11's files at once: the built-in worker row is created with
	// it (the setting is the default for a new worker).
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO app_settings(key, value) VALUES ('processing.parallelJobs', '4')
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
		return err
	}); err != nil {
		db.Close()
		t.Fatal(err)
	}
	// Take the schema back to 12: the number before the workers' settings.
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		for _, s := range []string{
			`DROP TABLE IF EXISTS workers`,
			`ALTER TABLE replace_journal DROP COLUMN kind`,
			`ALTER TABLE replace_journal DROP COLUMN trashed_at`,
			`ALTER TABLE replace_journal DROP COLUMN left_at`,
			`PRAGMA user_version = 12`,
		} {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.Close()
		t.Fatalf("make the database a schema 12 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.Open(dir)
	if err != nil {
		t.Fatalf("open the schema 12 install: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("upgrade the schema 12 install: %v", err)
	}

	var version int
	if err := db.Read().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 15 {
		t.Errorf("schema version after the upgrade = %d, want 15", version)
	}
	var workersTable int
	if err := db.Read().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'workers'`).
		Scan(&workersTable); err != nil {
		t.Fatal(err)
	}
	if workersTable != 1 {
		t.Error("migration 13 did not run: workers is missing")
	}
	var builtin int
	if err := db.Read().QueryRow(`SELECT count(*) FROM workers WHERE id = 'local'`).Scan(&builtin); err != nil {
		t.Fatal(err)
	}
	if builtin != 1 {
		t.Errorf("the built-in worker rows = %d, want 1", builtin)
	}
	var slots int
	if err := db.Read().QueryRow(`SELECT slots FROM workers WHERE id = 'local'`).Scan(&slots); err != nil {
		t.Fatal(err)
	}
	if slots != 4 {
		t.Errorf("the built-in worker's files at once = %d, want the page 11 value 4", slots)
	}
}

// TestAnInstallAtSchemaThirteenUpgradesThroughFourteen: an install at schema 13 (the workers, after #896) upgrades through 14. The
// replace journal gets its kind column, and the rows an older install already holds take the default kind. Recovery
// still runs on them, and no row is left in "moving".
func TestAnInstallAtSchemaThirteenUpgradesThroughFourteen(t *testing.T) {
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
	// Take the schema back to 13: the workers exist, the journal has no kind column yet. Two rows are from before the
	// kind column: one a crash left "moving", one already "done".
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		for _, s := range []string{
			`ALTER TABLE replace_journal DROP COLUMN kind`,
			`ALTER TABLE replace_journal DROP COLUMN trashed_at`,
			`ALTER TABLE replace_journal DROP COLUMN left_at`,
			`PRAGMA user_version = 13`,
			`INSERT INTO replace_journal (job_id, original, trash, staged, final, state,
				orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
				new_file_id, new_size, new_mtime, new_hash, created_at, updated_at)
				VALUES (1, '/lib/a.mkv', '/lib/.syp-trash/a', '/lib/a.staged.mkv', '/lib/a.mkv', 'moving',
				'1:1', 100, 1, 1, 'h1', '2:2', 50, 1, 'h2', 1, 1)`,
			`INSERT INTO replace_journal (job_id, original, trash, staged, final, state,
				orig_file_id, orig_size, orig_mtime, orig_ctime, orig_hash,
				new_file_id, new_size, new_mtime, new_hash, created_at, updated_at)
				VALUES (2, '/lib/b.mkv', '/lib/.syp-trash/b', '/lib/b.staged.mkv', '/lib/b.mkv', 'done',
				'3:3', 100, 1, 1, 'h3', '4:4', 50, 1, 'h4', 1, 1)`,
		} {
			if _, err := tx.Exec(s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		db.Close()
		t.Fatalf("make the database a schema 13 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = store.Open(dir)
	if err != nil {
		t.Fatalf("open the schema 13 install: %v", err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatalf("upgrade the schema 13 install: %v", err)
	}

	var version int
	if err := db.Read().QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 15 {
		t.Errorf("schema version after the upgrade = %d, want 15", version)
	}
	rows, err := db.Read().Query(`SELECT job_id, kind FROM replace_journal ORDER BY job_id`)
	if err != nil {
		t.Fatalf("read the journal kinds after the upgrade: %v", err)
	}
	defer rows.Close()
	kinds := map[int]string{}
	for rows.Next() {
		var job int
		var kind string
		if err := rows.Scan(&job, &kind); err != nil {
			t.Fatal(err)
		}
		kinds[job] = kind
	}
	if kinds[1] != "replace" || kinds[2] != "replace" || len(kinds) != 2 {
		t.Errorf("journal kinds after the upgrade = %v, want both rows \"replace\"", kinds)
	}

	if _, err := safety.Recover(ctx, safety.NewJournal(db)); err != nil {
		t.Fatalf("recovery on the upgraded journal: %v", err)
	}
	var moving int
	if err := db.Read().QueryRow(`SELECT count(*) FROM replace_journal WHERE state = 'moving'`).Scan(&moving); err != nil {
		t.Fatal(err)
	}
	if moving != 0 {
		t.Errorf("rows left in moving after recovery = %d, want 0", moving)
	}
}
