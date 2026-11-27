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

// Hooks for tests: a sync that fails with EINVAL must be ignored, and a source left behind needs a rename that
// fails after the link, so tests replace these.
var (
	syncFile        = func(f *os.File) error { return f.Sync() }
	syncDir         = func(d *os.File) error { return d.Sync() }
	renameNoReplace = fsx.RenameNoReplace
)

// placeCandidate is the nth name Place tries: final itself, then "name (2).ext", "name (3).ext" and
// so on. FreeName asks with it too, so the .srt files a run writes are named for the file the place
// will put them beside (L-SAFE-10).
func placeCandidate(final string, n int) string {
	if n <= 1 {
		return final
	}
	ext := filepath.Ext(final)
	return fmt.Sprintf("%s (%d)%s", final[:len(final)-len(ext)], n, ext)
}

// Place moves the staged file to final without ever overwriting: if final exists it tries "name (2).ext",
// "name (3).ext" and so on, each with no-replace rename. It returns the name it used. The staged file is fsynced
// before the move and the folder after it. EINVAL and ENOTSUP from fsync are ignored, and the folder sync is
// skipped on Windows (L-SAFE-12).
func Place(ctx context.Context, st Staged, final string) (string, error) {
	id, err := fsx.FileID(st.Path)
	if err != nil || id != st.id {
		return "", ErrIdentityChanged
	}
	if err := syncPath(st.Path); err != nil {
		return "", fmt.Errorf("place: sync staged file: %w", err)
	}

	var used string
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if n > maxPlaceNames {
			return "", fmt.Errorf("place %s: no free name in %d tries", final, maxPlaceNames)
		}
		cand := placeCandidate(final, n)
		err := renameNoReplace(st.Path, cand)
		if err == nil {
			used = cand
			break
		}
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if errors.Is(err, fsx.ErrNotRemoved) {
			// The file is at its final name and the staged copy is still in the staging folder. Keep the folder: it
			// holds the leftover, and the caller reports it. Only the destination folder is synced here.
			used = cand
			if err := syncDestination(used); err != nil {
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

// syncDestination syncs the folder the placed file is in, where the platform can (L-SAFE-12).
func syncDestination(used string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := syncDirOf(used); err != nil {
		return fmt.Errorf("place: sync folder: %w", err)
	}
	return nil
}

// finishPlace syncs the destination folder and removes the staging folder once the file has left it.
func finishPlace(st Staged, used string) error {
	if err := syncDestination(used); err != nil {
		return err
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
