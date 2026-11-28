// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// testNow is the clock every catalog test starts from: 2026-11-27 09:10:00 UTC.
func testNow() time.Time {
	return time.Date(2026, 11, 27, 9, 10, 0, 0, time.UTC)
}

func newTestCatalog(t *testing.T) (*Catalog, *store.DB) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := NewCatalog(db)
	c.Now = testNow
	return c, db
}

// newFolder makes a folder outside the data folder, which is where a library may point.
func newFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "videos")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestAddLibraryDefaults(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)

	lib, err := c.AddLibrary(context.Background(), NewLibrary{Path: dir})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if lib.ID == 0 || lib.Name != "videos" || lib.Path != dir {
		t.Errorf("library = %+v, want id, name videos, path %s", lib, dir)
	}
	if lib.Watch != WatchDaily {
		t.Errorf("watch = %q, want daily", lib.Watch)
	}
	if lib.Paused || lib.WorkflowID != nil || lib.ShareID != nil {
		t.Errorf("library = %+v, want not paused, no workflow or share", lib)
	}
	if lib.NextScanAt == nil || !lib.NextScanAt.Equal(testNow()) {
		t.Errorf("next scan = %v, want %v (due at once)", lib.NextScanAt, testNow())
	}
	if lib.LastScan != nil {
		t.Errorf("last scan = %+v, want none", lib.LastScan)
	}
}

func TestAddLibraryRefusesAMissingFolder(t *testing.T) {
	c, _ := newTestCatalog(t)
	path := filepath.Join(t.TempDir(), "nope")

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: path})
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("error = %v, want a missing-folder refusal", err)
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("message = %q, want a sentence that says the folder does not exist", err)
	}
}

func TestAddLibraryRefusesAFile(t *testing.T) {
	c, _ := newTestCatalog(t)
	file := filepath.Join(t.TempDir(), "clip.mp4")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: file})
	if !errors.Is(err, ErrNotFolder) {
		t.Fatalf("error = %v, want a not-a-folder refusal", err)
	}
	if !strings.Contains(err.Error(), "is a file, not a folder") {
		t.Errorf("message = %q, want a sentence that says it is a file", err)
	}
}

func TestAddLibraryRefusesTheDataFolder(t *testing.T) {
	c, db := newTestCatalog(t)
	inside := filepath.Join(db.Dir(), "media")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: inside})
	if !errors.Is(err, ErrInsideData) {
		t.Fatalf("error = %v, want an inside-the-data-folder refusal", err)
	}
	if !strings.Contains(err.Error(), "inside the app's data folder") {
		t.Errorf("message = %q, want a sentence about the data folder", err)
	}
}

func TestAddLibraryRefusesTheSameFolderTwice(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: dir}); err != nil {
		t.Fatal(err)
	}

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: dir + string(filepath.Separator)})
	if !errors.Is(err, ErrPathAlreadyWatched) {
		t.Fatalf("error = %v, want a path-already-watched refusal", err)
	}
}

func TestAddLibraryRefusesAnUnknownWatch(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: dir, Watch: "monthly"})
	if !errors.Is(err, ErrInvalidWatch) {
		t.Fatalf("error = %v, want an invalid-watch refusal", err)
	}
}

func TestUpdateLibraryChangesTheSchedule(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	lib, err := c.AddLibrary(context.Background(), NewLibrary{Path: dir})
	if err != nil {
		t.Fatal(err)
	}

	watch := WatchHourly
	updated, err := c.UpdateLibrary(context.Background(), lib.ID, Patch{Watch: &watch})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Watch != WatchHourly {
		t.Errorf("watch = %q, want hourly", updated.Watch)
	}
	want := testNow().Add(time.Hour).Truncate(time.Hour).Add(time.Duration(lib.ID%60) * time.Minute)
	if updated.NextScanAt == nil || !updated.NextScanAt.Equal(want) {
		t.Errorf("next scan = %v, want %v", updated.NextScanAt, want)
	}

	paused := true
	updated, err = c.UpdateLibrary(context.Background(), lib.ID, Patch{Paused: &paused})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Paused || updated.NextScanAt != nil {
		t.Errorf("paused library = %+v, want paused with no next scan", updated)
	}

	paused = false
	updated, err = c.UpdateLibrary(context.Background(), lib.ID, Patch{Paused: &paused})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Paused || updated.NextScanAt == nil {
		t.Errorf("resumed library = %+v, want a next scan", updated)
	}
}

func TestUpdateLibrarySetsAndClearsTheWorkflow(t *testing.T) {
	c, _ := newTestCatalog(t)
	lib, err := c.AddLibrary(context.Background(), NewLibrary{Path: newFolder(t)})
	if err != nil {
		t.Fatal(err)
	}

	id := int64(7)
	updated, err := c.UpdateLibrary(context.Background(), lib.ID, Patch{WorkflowID: &id})
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkflowID == nil || *updated.WorkflowID != 7 {
		t.Fatalf("workflow = %v, want 7", updated.WorkflowID)
	}

	updated, err = c.UpdateLibrary(context.Background(), lib.ID, Patch{ClearWorkflow: true})
	if err != nil {
		t.Fatal(err)
	}
	if updated.WorkflowID != nil {
		t.Errorf("workflow = %v, want none", updated.WorkflowID)
	}
}

func TestUpdateLibraryRefusesABadPath(t *testing.T) {
	c, _ := newTestCatalog(t)
	lib, err := c.AddLibrary(context.Background(), NewLibrary{Path: newFolder(t)})
	if err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(t.TempDir(), "nope")
	_, err = c.UpdateLibrary(context.Background(), lib.ID, Patch{Path: &missing})
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("error = %v, want a missing-folder refusal", err)
	}
	if current, err := c.GetLibrary(context.Background(), lib.ID); err != nil || current.Path != lib.Path {
		t.Errorf("path after the refusal = %q (%v), want the old one", current.Path, err)
	}
}

// TestUpdateLibraryPathChangeDropsTheOldFiles: a library's files rows describe the folder it watched.
// When that folder changes, they no longer mean anything: they are cleared, and the next scan counts
// the new folder. A workflow set in the same save therefore queues nothing that is not there
// (review-890 item 3).
func TestUpdateLibraryPathChangeDropsTheOldFiles(t *testing.T) {
	ctx := context.Background()
	c, _ := newTestCatalog(t)
	lib, err := c.AddLibrary(ctx, NewLibrary{Path: newFolder(t)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SaveFile(ctx, File{LibraryID: lib.ID, RelPath: "a.mp4"}, Skipped, "needs_workflow", nil); err != nil {
		t.Fatal(err)
	}

	// A name change is not a path change: the rows still describe the same folder.
	name := "Renamed"
	if _, err := c.UpdateLibrary(ctx, lib.ID, Patch{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if rows, err := c.Files(ctx, lib.ID); err != nil || len(rows) != 1 {
		t.Fatalf("after a name change: %d rows (%v), want the one", len(rows), err)
	}

	next := newFolder(t)
	workflow := int64(7)
	if _, err := c.UpdateLibrary(ctx, lib.ID, Patch{Path: &next, WorkflowID: &workflow}); err != nil {
		t.Fatal(err)
	}
	if rows, err := c.Files(ctx, lib.ID); err != nil || len(rows) != 0 {
		t.Fatalf("after a path change: %d rows (%v), want none", len(rows), err)
	}
}

func TestDeleteLibraryRemovesItsFiles(t *testing.T) {
	c, _ := newTestCatalog(t)
	lib, err := c.AddLibrary(context.Background(), NewLibrary{Path: newFolder(t)})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO files(library_id, rel_path) VALUES(?, 'a.mp4')`, lib.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := c.DeleteLibrary(context.Background(), lib.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetLibrary(context.Background(), lib.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("get after delete = %v, want ErrNotFound", err)
	}
	if err := c.DeleteLibrary(context.Background(), lib.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
	files, err := c.Files(context.Background(), lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Errorf("files after delete = %+v, want none (the cascade)", files)
	}
}

func TestListLibrariesIsOldestFirst(t *testing.T) {
	c, _ := newTestCatalog(t)
	first, err := c.AddLibrary(context.Background(), NewLibrary{Path: newFolder(t), Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.AddLibrary(context.Background(), NewLibrary{Path: newFolder(t), Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}

	list, err := c.ListLibraries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("list = %+v, want the two libraries in add order", list)
	}
}

func TestDueLibraries(t *testing.T) {
	c, _ := newTestCatalog(t)
	due, err := c.AddLibrary(context.Background(), NewLibrary{Path: newFolder(t), Name: "Due"})
	if err != nil {
		t.Fatal(err)
	}
	pausedLib, err := c.AddLibrary(context.Background(), NewLibrary{Path: newFolder(t), Name: "Paused"})
	if err != nil {
		t.Fatal(err)
	}
	paused := true
	if _, err := c.UpdateLibrary(context.Background(), pausedLib.ID, Patch{Paused: &paused}); err != nil {
		t.Fatal(err)
	}

	got, err := c.DueLibraries(context.Background(), testNow().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != due.ID {
		t.Fatalf("due = %+v, want only %d", got, due.ID)
	}

	got, err = c.DueLibraries(context.Background(), testNow().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("due an hour ago = %+v, want none", got)
	}
}

func TestParseWatch(t *testing.T) {
	for value, want := range map[string]Watch{
		"":        WatchDaily,
		"Daily":   WatchDaily,
		"hourly":  WatchHourly,
		" weekly": WatchWeekly,
	} {
		got, err := ParseWatch(value)
		if err != nil || got != want {
			t.Errorf("ParseWatch(%q) = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := ParseWatch("monthly"); !errors.Is(err, ErrInvalidWatch) {
		t.Errorf("ParseWatch(monthly) = %v, want ErrInvalidWatch", err)
	}
}
