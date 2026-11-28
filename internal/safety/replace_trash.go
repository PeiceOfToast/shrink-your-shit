// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TrashDir is the folder a replaced original moves to. It sits in the original's own folder, so the move is a rename
// on the same file system (L-SAFE-6).
const TrashDir = ".syp-trash"

// maxTrashNames bounds the ".2", ".3" loop.
const maxTrashNames = 1000

// Refusals of a replace. Each one leaves the original where it was, and the job is kept, not failed.
var (
	// ErrNoTrash means the trash folder cannot be created, so the original stays in place (L-SAFE-6).
	ErrNoTrash = errors.New("safety: the trash folder cannot be created")
	// ErrReadOnlyLibrary means the library's folder cannot be written, so nothing moves.
	ErrReadOnlyLibrary = errors.New("safety: the library folder is read-only")
	// ErrSourceChanged means the original is not the file that was read at the start, so it is kept (L-SAFE-5).
	ErrSourceChanged = errors.New("safety: the original changed while it was being replaced")
	// ErrSharedSource means the original has more than one name (a hard link), so it is skipped as shared.
	ErrSharedSource = errors.New("safety: the original is hard-linked, so it is shared and not replaced")
)

// trashPath is the first free name in the trash folder: "<stem>.<jobid><ext>", then "<stem>.<jobid>.2<ext>" and on.
// A name that exists is skipped; the move itself is a no-replace rename, so a race cannot overwrite either.
func trashPath(trashDir, original string, jobID int64) (string, error) {
	name := filepath.Base(original)
	ext := filepath.Ext(name)
	stem := name[:len(name)-len(ext)]
	for n := 1; n <= maxTrashNames; n++ {
		cand := filepath.Join(trashDir, fmt.Sprintf("%s.%d%s", stem, jobID, ext))
		if n > 1 {
			cand = filepath.Join(trashDir, fmt.Sprintf("%s.%d.%d%s", stem, jobID, n, ext))
		}
		if _, err := os.Lstat(cand); errors.Is(err, os.ErrNotExist) {
			return cand, nil
		}
	}
	return "", fmt.Errorf("safety: no free trash name for %s in %d tries", original, maxTrashNames)
}

// ensureTrash makes the trash folder in the library's folder. A folder that cannot be made is ErrNoTrash; a folder
// that is read-only is ErrReadOnlyLibrary, and nothing moves in either case.
func ensureTrash(libDir string) (string, error) {
	if err := checkWritable(libDir); err != nil {
		return "", ErrReadOnlyLibrary
	}
	dir := filepath.Join(libDir, TrashDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", ErrNoTrash
	}
	// An existing trash folder the app cannot write in is the same refusal: the original stays and the job is kept
	// (L-SAFE-6, review #865).
	if err := checkWritable(dir); err != nil {
		return "", ErrNoTrash
	}
	return dir, nil
}

// checkWritable proves the folder takes a new file, by creating and removing one.
func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".syp-replace-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}
