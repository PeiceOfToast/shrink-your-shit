// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// Staged is a private output file that has not been placed yet. Folder holds only the file; Path is where the
// encoder writes; id is the file's identity (fsx.FileID), checked again before the move (L-SAFE-12).
type Staged struct {
	Folder string
	Path   string
	id     string
}

// Stage makes a private folder next to dst and creates the output file in it, exclusively, mode 0600. Nothing
// appears at dst until Place. The folder name is unpredictable (MkdirTemp), so another program cannot guess it.
func Stage(dst string) (Staged, error) {
	return StageIn(filepath.Dir(dst), dst)
}

// StageIn is Stage with the private folder made in base instead of next to dst. A try uses it so a
// killed process leaves nothing in the original's folder.
func StageIn(base, dst string) (Staged, error) {
	dir := base
	name := filepath.Base(dst)
	ext := filepath.Ext(name)
	// MkdirTemp replaces the first "*" in the pattern, so a stray "*" in the name must not take its place.
	stem := strings.ReplaceAll(strings.TrimSuffix(name, ext), "*", "_")
	folder, err := os.MkdirTemp(dir, "."+stem+".*.syp-tmp")
	if err != nil {
		return Staged{}, fmt.Errorf("stage %s: %w", dst, err)
	}
	path := filepath.Join(folder, "out"+ext)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		os.RemoveAll(folder)
		return Staged{}, fmt.Errorf("stage %s: %w", dst, err)
	}
	if err := f.Close(); err != nil {
		os.RemoveAll(folder)
		return Staged{}, fmt.Errorf("stage %s: %w", dst, err)
	}
	id, err := fsx.FileID(path)
	if err != nil {
		os.RemoveAll(folder)
		return Staged{}, fmt.Errorf("stage %s: %w", dst, err)
	}
	return Staged{Folder: folder, Path: path, id: id}, nil
}
