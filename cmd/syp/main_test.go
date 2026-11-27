// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
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
