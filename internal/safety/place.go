// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// ErrIdentityChanged means the staged file is no longer the file we wrote. Nothing is moved.
var ErrIdentityChanged = errors.New("safety: the staged file changed since it was made")

// ErrSourceLeftBehind means the output is placed but the staged copy could not be removed (the link fallback).
var ErrSourceLeftBehind = errors.New("safety: placed, but the source could not be removed")

// maxPlaceNames bounds the "name (2)", "name (3)" loop.
const maxPlaceNames = 1000

// Hooks for tests: a sync that fails with EINVAL must be ignored, so tests replace these.
var (
	syncFile = func(f *os.File) error { return f.Sync() }
	syncDir  = func(d *os.File) error { return d.Sync() }
)

// Place moves the staged file to final without ever overwriting: if final exists it tries "name (2).ext",
// "name (3).ext" and so on, each with no-replace rename. It returns the name it used. The staged file is fsynced
// before the move and the folder after it. EINVAL and ENOTSUP from fsync are ignored, and the folder sync is
// skipped on Windows (L-SAFE-12).
func Place(ctx context.Context, st Staged, final string) (string, error) {
	fi, err := os.Lstat(st.Path)
	if err != nil || !os.SameFile(fi, st.info) {
		return "", ErrIdentityChanged
	}
	if err := syncPath(st.Path); err != nil {
		return "", fmt.Errorf("place: sync staged file: %w", err)
	}

	ext := filepath.Ext(final)
	stem := final[:len(final)-len(ext)]
	var used string
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if n > maxPlaceNames {
			return "", fmt.Errorf("place %s: no free name in %d tries", final, maxPlaceNames)
		}
		cand := final
		if n > 1 {
			cand = fmt.Sprintf("%s (%d)%s", stem, n, ext)
		}
		err := fsx.RenameNoReplace(st.Path, cand)
		if err == nil {
			used = cand
			break
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if errors.Is(err, fsx.ErrNotRemoved) {
			used = cand
			if err := finishPlace(st, used); err != nil {
				return used, err
			}
			return used, ErrSourceLeftBehind
		}
		return "", fmt.Errorf("place %s: %w", final, err)
	}
	if err := finishPlace(st, used); err != nil {
		return used, err
	}
	return used, nil
}

// finishPlace syncs the destination folder and removes the staging folder once the file has left it.
func finishPlace(st Staged, used string) error {
	if runtime.GOOS != "windows" {
		if err := syncDirOf(used); err != nil {
			return fmt.Errorf("place: sync folder: %w", err)
		}
	}
	if err := os.Remove(st.Folder); err != nil {
		return fmt.Errorf("place: placed, but the staging folder is left: %w", err)
	}
	return nil
}

// syncPath fsyncs a file, ignoring the errors that mean the filesystem cannot sync.
func syncPath(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	return ignoreUnsupported(syncFile(f))
}

func syncDirOf(path string) error {
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return ignoreUnsupported(syncDir(d))
}

func ignoreUnsupported(err error) error {
	if err == nil || errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOTSUP) {
		return nil
	}
	return err
}
