// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRenameNoReplaceMovesToAFreeName(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "my film.v1.tmp")
	dst := filepath.Join(dir, "my film.v1.mkv")
	if err := os.WriteFile(src, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenameNoReplace(src, dst); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "data" {
		t.Fatalf("destination = %q, %v; want the moved data", got, err)
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still there after the move: %v", err)
	}
}

// The check and the rename are separate steps, so a racing creator can take the name between them. The rename
// itself must still refuse: pre-create the target and call it directly.
func TestRenameNoReplaceRefusesAnExistingTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "staged.syp-tmp")
	dst := filepath.Join(dir, "out.mkv")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := RenameNoReplace(src, dst)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v; want one matching os.ErrExist", err)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "theirs" {
		t.Fatalf("existing target was overwritten: %q", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source must stay when the move is refused: %v", err)
	}
}

func TestLinkRenameRefusesAnExistingTarget(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a b.c")
	dst := filepath.Join(dir, "d e.f")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := linkRename(src, dst); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v; want os.ErrExist", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "theirs" {
		t.Fatalf("existing target was overwritten: %q", got)
	}
}
