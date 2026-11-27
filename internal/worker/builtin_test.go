// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// storedJob is a job in a real store: a migrated database, a library over root with one file row, the shrink-videos
// workflow saved, and one queued job for that file.
type storedJob struct {
	ctx   context.Context
	db    *store.DB
	q     *jobs.Queue
	flows *flow.Store
	root  string
	libID int64
	wfID  int64
	jobID int64
	mtime int64
	bus   *events.Bus
}

func newStoredJob(t *testing.T, mtime int64) storedJob {
	t.Helper()
	root := t.TempDir()
	return newStoredJobFor(t, root, "fid-1", 4321, mtime, filepath.Join(root, "holiday.mkv"))
}

// newStoredJobFor is newStoredJob over a file the caller made: its file row carries fid, size and mtime, and the job
// is queued for src.
func newStoredJobFor(t *testing.T, root, fid string, size, mtime int64, src string) storedJob {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	bus, err := events.New(events.Options{Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bus.Close(context.Background()) })

	lib, err := library.NewCatalog(db).AddLibrary(ctx, library.NewLibrary{Path: root})
	if err != nil {
		t.Fatalf("add library: %v", err)
	}
	var fileID int64
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO files(library_id, rel_path, size, mtime, file_id)
			VALUES(?, ?, ?, ?, ?)`, lib.ID, filepath.Base(src), size, mtime, fid)
		if err != nil {
			return err
		}
		fileID, err = res.LastInsertId()
		return err
	}); err != nil {
		t.Fatalf("insert file row: %v", err)
	}
	flows := flow.Open(db)
	wfID, _, err := flows.CreateFromStarter(ctx, "shrink-videos", "Shrink")
	if err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	q := jobs.New(jobs.Options{DB: db, Bus: bus})
	jobID, _, err := q.Enqueue(ctx, fileID, lib.ID, wfID, src, "test")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return storedJob{ctx: ctx, db: db, q: q, flows: flows, root: root, libID: lib.ID, wfID: wfID, jobID: jobID, mtime: mtime, bus: bus}
}

// TestBuiltinClaimBuildsTheRunFromTheStore takes a job through the real store and checks the run input Claim builds
// from the rows, not from memory.
func TestBuiltinClaimBuildsTheRunFromTheStore(t *testing.T) {
	const mtime = int64(1_700_000_000_123_456_789)
	s := newStoredJob(t, mtime)
	b := Builtin{Queue: s.q, DB: s.db, Flows: s.flows}
	w, err := b.Claim(s.ctx, "local")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if w == nil {
		t.Fatal("claim returned nothing; want the queued job")
	}
	if w.Err != nil {
		t.Fatalf("claim could not build the run: %v", w.Err)
	}
	if w.Job.ID != s.jobID {
		t.Errorf("job id = %d, want %d", w.Job.ID, s.jobID)
	}
	in := w.In
	want := flow.RunInput{
		JobID: s.jobID, Attempt: w.Job.Attempt, LibraryID: strconv.FormatInt(s.libID, 10), WorkerID: "local",
		WorkflowID: s.wfID, Revision: 1,
		LibraryRoot: s.root, Source: filepath.Join(s.root, "holiday.mkv"),
		Size: 4321, FileID: "fid-1",
	}
	if in.JobID != want.JobID || in.LibraryID != want.LibraryID || in.WorkerID != want.WorkerID ||
		in.WorkflowID != want.WorkflowID || in.Revision != want.Revision || in.LibraryRoot != want.LibraryRoot ||
		in.Source != want.Source || in.Size != want.Size || in.FileID != want.FileID || in.Attempt != w.Job.Attempt {
		t.Errorf("run input = %+v\nwant          %+v", in, want)
	}
	if !in.MTime.Equal(time.Unix(0, mtime)) {
		t.Errorf("mtime = %v, want %v", in.MTime, time.Unix(0, mtime))
	}
}

// A database read that fails for now is transient: the run input is not built, and the job is marked to retry.
// A file that is gone is not transient.
func TestBuiltinMarksADatabaseReadErrorTransient(t *testing.T) {
	s := newStoredJob(t, 1)
	// Break the workflow read: Claim does not read workflows, but the run input does, so only input fails.
	if err := s.db.Tx(s.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(s.ctx, `ALTER TABLE workflows RENAME TO workflows_gone`)
		return err
	}); err != nil {
		t.Fatalf("break the read: %v", err)
	}
	b := Builtin{Queue: s.q, DB: s.db, Flows: s.flows}
	w, err := b.Claim(s.ctx, "local")
	if err != nil || w == nil {
		t.Fatalf("claim = %v, %v; want the job with a read error", w, err)
	}
	if w.Err == nil || !w.Transient {
		t.Fatalf("work = err %v transient %v; want a transient read error", w.Err, w.Transient)
	}
}

// A file that is no longer in its library is permanent: the job fails, it is not retried.
func TestBuiltinFileGoneIsNotTransient(t *testing.T) {
	s := newStoredJob(t, 1)
	if err := s.db.Tx(s.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(s.ctx, `DELETE FROM files`)
		return err
	}); err != nil {
		t.Fatalf("delete the file row: %v", err)
	}
	b := Builtin{Queue: s.q, DB: s.db, Flows: s.flows}
	w, err := b.Claim(s.ctx, "local")
	if err != nil || w == nil {
		t.Fatalf("claim = %v, %v", w, err)
	}
	if w.Err == nil || w.Transient || w.Err.Error() != "the file is no longer in its library" {
		t.Fatalf("work = err %v transient %v; want the permanent file-gone error", w.Err, w.Transient)
	}
}
