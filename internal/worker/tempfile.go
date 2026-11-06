// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The new file is written to a temp file next to the original. Its name is unpredictable
// (os.CreateTemp puts a random number where the "*" is), it is created exclusively, and the worker
// remembers which file it created, so a file swapped in under that name is never used. The name
// keeps the original extension, so ffmpeg picks the right container, and the part before the
// extension ends in ".syp-tmp", so the scanner skips it.

func tempPattern(src string) string {
	ext := filepath.Ext(src)
	return strings.TrimSuffix(filepath.Base(src), ext) + ".*.syp-tmp" + ext
}

// TempFiles lists the temp files next to src: ours from an earlier run that crashed, and the fixed
// name ("movie.syp-tmp.mkv") that versions before random names used.
func TempFiles(src string) ([]string, error) {
	dir := filepath.Dir(src)
	ext := filepath.Ext(src)
	stem := strings.TrimSuffix(filepath.Base(src), ext)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if n == stem+".syp-tmp"+ext {
			out = append(out, filepath.Join(dir, n))
			continue
		}
		mid, ok := strings.CutPrefix(n, stem+".")
		if !ok {
			continue
		}
		if mid, ok = strings.CutSuffix(mid, ".syp-tmp"+ext); ok && mid != "" && strings.Trim(mid, "0123456789") == "" {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out, nil
}

// removeLeftoverTemps removes temp files an earlier run left next to src. Any failure stops the job:
// leaving a file there that we cannot account for is safer than carrying on beside it. Removing a
// link removes the link, never the file it points to.
func removeLeftoverTemps(src string) error {
	old, err := TempFiles(src)
	if err != nil {
		return fmt.Errorf("looking for old temp files: %w", err)
	}
	for _, p := range old {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot remove the old temp file %s: %w", p, err)
		}
	}
	return nil
}

// newTemp creates the empty temp file for src, with the original's permissions perm, and returns its
// path and identity (device and inode, or the Windows file ID). ffmpeg then opens that path and
// truncates it, which keeps the same file and its permissions.
func newTemp(src string, perm fs.FileMode) (string, os.FileInfo, error) {
	f, err := os.CreateTemp(filepath.Dir(src), tempPattern(src))
	if err != nil {
		return "", nil, fmt.Errorf("cannot create the temp file next to the original: %w", err)
	}
	err = f.Chmod(perm | 0o200) // ffmpeg must be able to write it; see readOnlyAgain
	var id os.FileInfo
	if err == nil {
		id, err = f.Stat()
	}
	f.Close()
	if err != nil {
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("temp file: %w", err)
	}
	return f.Name(), id, nil
}

// tempIntact refuses the temp file if the name no longer refers to the regular file the worker
// created: then whatever is there now was not written, or not checked, by us.
func tempIntact(tmp string, id os.FileInfo) error {
	now, err := os.Lstat(tmp)
	if err != nil || !now.Mode().IsRegular() || !os.SameFile(now, id) {
		return kept("the new file was replaced by another file before it could be used, so it was not used")
	}
	return nil
}

// readOnlyAgain gives a checked temp file the original's permissions when the original was not
// writable by its owner (newTemp had to leave the temp file writable for ffmpeg).
func readOnlyAgain(tmp string, id os.FileInfo, perm fs.FileMode) error {
	if perm&0o200 != 0 {
		return nil
	}
	if err := tempIntact(tmp, id); err != nil {
		return err
	}
	return os.Chmod(tmp, perm)
}
