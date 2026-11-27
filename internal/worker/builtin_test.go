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

// TestBuiltinClaimBuildsTheRunFromTheStore takes a job through the real store: a library, a scanned file row, a
// workflow saved from the starter, and a queued job. Claim builds the flow input from those rows, not from memory.
func TestBuiltinClaimBuildsTheRunFromTheStore(t *testing.T) {
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

	root := t.TempDir()
	lib, err := library.NewCatalog(db).AddLibrary(ctx, library.NewLibrary{Path: root})
	if err != nil {
		t.Fatalf("add library: %v", err)
	}
	const mtime = int64(1_700_000_000_123_456_789)
	var fileID int64
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.Exec(`INSERT INTO files(library_id, rel_path, size, mtime, file_id)
			VALUES(?, 'holiday.mkv', 4321, ?, 'fid-1')`, lib.ID, mtime)
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
	jobID, _, err := q.Enqueue(ctx, fileID, lib.ID, wfID, filepath.Join(root, "holiday.mkv"), "test")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	b := Builtin{Queue: q, DB: db, Flows: flows}
	w, err := b.Claim(ctx, "local")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if w == nil {
		t.Fatal("claim returned nothing; want the queued job")
	}
	if w.Err != nil {
		t.Fatalf("claim could not build the run: %v", w.Err)
	}
	if w.Job.ID != jobID {
		t.Errorf("job id = %d, want %d", w.Job.ID, jobID)
	}
	in := w.In
	want := flow.RunInput{
		JobID: jobID, Attempt: w.Job.Attempt, LibraryID: strconv.FormatInt(lib.ID, 10), WorkerID: "local",
		WorkflowID: wfID, Revision: 1,
		LibraryRoot: root, Source: filepath.Join(root, "holiday.mkv"),
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
