// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// addLibrary adds a library at path and returns what the catalog stacked it with, failing the test.
func addLibrary(t *testing.T, c *Catalog, path string) Library {
	t.Helper()
	lib, err := c.AddLibrary(context.Background(), NewLibrary{Path: path})
	if err != nil {
		t.Fatalf("add %s: %v", path, err)
	}
	return lib
}

// TestAddLibraryRefusesASymlinkToAWatchedFolder: two names that are one real folder are one folder.
func TestAddLibraryRefusesASymlinkToAWatchedFolder(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	addLibrary(t, c, dir)
	link := filepath.Join(t.TempDir(), "shortcut")
	if err := os.Symlink(dir, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this Windows account cannot make a symlink: %v", err)
		}
		t.Fatalf("make the link: %v", err)
	}

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: link})
	if !errors.Is(err, ErrPathAlreadyWatched) {
		t.Fatalf("error = %v, want the same-folder refusal", err)
	}
}

// TestAddLibraryRefusesAFolderInsideAWatchedFolder: a folder inside another library's folder would
// hand one original to two libraries (and, with replace, two jobs).
func TestAddLibraryRefusesAFolderInsideAWatchedFolder(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	lib := addLibrary(t, c, dir)
	inside := filepath.Join(dir, "sub", "deeper")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: inside})
	var nested *NestedError
	if !errors.Is(err, ErrPathNested) || !errors.As(err, &nested) {
		t.Fatalf("error = %v, want the nested-folder refusal", err)
	}
	if nested.Library != lib.Name {
		t.Errorf("refusal names %q, want the library %q", nested.Library, lib.Name)
	}

	// The check the add page runs answers the same way, with its own code and sentence.
	check, err := c.CheckFolder(context.Background(), inside)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(check.Problems) != 1 || check.Problems[0].Code != ProblemNestedFolder {
		t.Fatalf("problems = %+v, want one %s", check.Problems, ProblemNestedFolder)
	}
	message := check.Problems[0].Message
	if !strings.Contains(message, lib.Name) || !strings.Contains(message, "A file can belong to one library only.") {
		t.Errorf("message = %q, want the library's name and the one-library sentence", message)
	}
}

// TestAddLibraryRefusesAFolderContainingAWatchedFolder: the other direction of the same rule.
func TestAddLibraryRefusesAFolderContainingAWatchedFolder(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	inner := filepath.Join(dir, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	addLibrary(t, c, inner)

	_, err := c.AddLibrary(context.Background(), NewLibrary{Path: dir})
	if !errors.Is(err, ErrPathNested) {
		t.Fatalf("error = %v, want the nested-folder refusal", err)
	}

	check, err := c.CheckFolder(context.Background(), dir)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(check.Problems) != 1 || check.Problems[0].Code != ProblemNestedFolder {
		t.Fatalf("problems = %+v, want one %s", check.Problems, ProblemNestedFolder)
	}
}

// TestASiblingFolderWithASharedPrefixIsAllowed: "Movies" and "Movies 2" are two folders, not one
// inside the other.
func TestASiblingFolderWithASharedPrefixIsAllowed(t *testing.T) {
	c, _ := newTestCatalog(t)
	base := t.TempDir()
	first := filepath.Join(base, "Movies")
	second := filepath.Join(base, "Movies 2")
	for _, dir := range []string{first, second} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	addLibrary(t, c, first)
	addLibrary(t, c, second)

	overlaps, err := c.Overlaps(context.Background())
	if err != nil {
		t.Fatalf("overlaps: %v", err)
	}
	if len(overlaps) != 0 {
		t.Errorf("overlaps = %+v, want none for two sibling folders", overlaps)
	}
}

// TestEditingALibraryToItsOwnPathIsAllowed: a library compared with itself is not an overlap.
func TestEditingALibraryToItsOwnPathIsAllowed(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	lib := addLibrary(t, c, dir)
	own := dir
	if _, err := c.UpdateLibrary(context.Background(), lib.ID, Patch{Path: &own}); err != nil {
		t.Fatalf("editing a library to its own path: %v", err)
	}
}

// TestEditingALibraryIntoAnotherIsRefused: the edit's path check follows the same rule.
func TestEditingALibraryIntoAnotherIsRefused(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	addLibrary(t, c, dir)
	other := newFolder(t)
	lib := addLibrary(t, c, other)
	into := filepath.Join(dir, "sub")
	if err := os.MkdirAll(into, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateLibrary(context.Background(), lib.ID, Patch{Path: &into}); !errors.Is(err, ErrPathNested) {
		t.Fatalf("error = %v, want the nested-folder refusal", err)
	}
}

// TestOverlapsReportsWhatAnOldInstallHolds: an install that stored two overlapping libraries
// before this rule is reported, pair by pair, and start-up does not fail over it.
func TestOverlapsReportsWhatAnOldInstallHolds(t *testing.T) {
	c, _ := newTestCatalog(t)
	dir := newFolder(t)
	addLibrary(t, c, dir)
	inside := filepath.Join(dir, "sub")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	// Written straight to the store, the way an older version left it.
	if err := c.db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`INSERT INTO libraries(name, path, workflow_id, watch, paused, next_scan_at) VALUES(?, ?, NULL, 'daily', 0, ?)`,
			"Old", inside, testNow().UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}

	overlaps, err := c.Overlaps(context.Background())
	if err != nil {
		t.Fatalf("overlaps: %v", err)
	}
	if len(overlaps) != 1 {
		t.Fatalf("overlaps = %+v, want the one nested pair", overlaps)
	}
	if overlaps[0].Same {
		t.Errorf("the pair is reported as one folder, want nested")
	}
	message := overlaps[0].Message()
	if !strings.Contains(message, "inside the other") || !strings.Contains(message, "Old") {
		t.Errorf("message = %q, want it to say one is inside the other and name the libraries", message)
	}

	// And the checks refuse a new library that would keep the overlap.
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: other}); !errors.Is(err, ErrPathNested) {
		t.Fatalf("error = %v, want the new folder refused as inside %q", err, dir)
	}
}

// TestNestedThroughASecondMountIsRefused: a library reached through a second mount of a folder is
// still that folder. Bind mounts need root, so where the test cannot make one it says so.
func TestNestedThroughASecondMountIsRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bind mounts are a Linux arrangement")
	}
	if _, err := exec.LookPath("mount"); err != nil {
		t.Skip("this system has no mount command")
	}
	real := newFolder(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Mkdir(alias, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("mount", "--bind", real, alias).CombinedOutput(); err != nil {
		t.Skipf("this account cannot bind-mount, so the second-mount case is not tried: %v %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("umount", alias).Run() })

	c, _ := newTestCatalog(t)
	addLibrary(t, c, real)

	// The same folder under another name is the same-folder refusal, though neither path says so.
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: alias}); !errors.Is(err, ErrPathAlreadyWatched) {
		t.Fatalf("the alias was not recognised as the same folder: %v", err)
	}

	sub := filepath.Join(alias, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: sub}); !errors.Is(err, ErrPathNested) {
		t.Fatalf("a folder below the alias was not refused as nested: %v", err)
	}
}
