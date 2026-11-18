// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
)

// A second worker on a --work folder that a live worker holds refuses to start, and does not sweep
// the live worker's lease folder (sweepLeases runs only once the lock is held).
func TestSecondWorkerOnTheSameWorkFolderRefuses(t *testing.T) {
	data, work := t.TempDir(), t.TempDir()
	if err := (remote.State{Server: "http://127.0.0.1:1", Name: "w", Token: "t"}).Save(data); err != nil {
		t.Fatal(err)
	}
	lease := filepath.Join(work, "0123456789abcdef01234567") // the first worker's live lease folder
	if err := os.MkdirAll(lease, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(lease, "movie.part"), []byte("x"), 0o644)
	first, err := takeWorkFolder(work) // the first worker is running
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	var out, errOut bytes.Buffer
	if code := workerCommand([]string{"--data", data, "--work", work}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1: %s", code, errOut.String())
	}
	if want := "Another worker is already using " + work + ". Give each worker its own --work folder."; !strings.Contains(errOut.String(), want) {
		t.Errorf("message %q", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(lease, "movie.part")); err != nil {
		t.Errorf("the first worker's lease folder was touched: %v", err)
	}
}
