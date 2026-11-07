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

// The new file is written into a private staging folder next to the original: created fresh with
// os.MkdirTemp (unpredictable name, mode 0700), holding one file created exclusively at mode 0600.
// No other user can reach or replace the file while it is written and checked, and the worker
// remembers which file it created. The folder name starts with "." so the scanner skips it; the file
// keeps the original's extension so ffmpeg picks the right container. The original's permissions are
// put on the file only after the checks, just before the rename, and the folder is removed on every
// path.

type staged struct {
	dir, path string
	id        os.FileInfo // device and inode, or the Windows file ID, of the file we created
}

func stagingPattern(src string) string {
	ext := filepath.Ext(src)
	return "." + strings.TrimSuffix(filepath.Base(src), ext) + ".*.syp-tmp"
}

// TempFiles lists what earlier runs left next to src when they crashed: staging folders, and the
// fixed temp file name ("movie.syp-tmp.mkv") that versions before staging folders used.
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
		mid, ok := strings.CutPrefix(n, "."+stem+".")
		if !ok {
			continue
		}
		if mid, ok = strings.CutSuffix(mid, ".syp-tmp"); ok && mid != "" && strings.Trim(mid, "0123456789") == "" {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out, nil
}

// removeLeftoverTemps removes what earlier runs left next to src. Any failure stops the job: carrying
// on beside something we cannot account for is not safe. Removing a link removes the link, never
// what it points to.
func removeLeftoverTemps(src string) error {
	old, err := TempFiles(src)
	if err != nil {
		return fmt.Errorf("looking for old temp files: %w", err)
	}
	for _, p := range old {
		if err := os.RemoveAll(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("cannot remove the old temp file %s: %w", p, err)
		}
	}
	return nil
}

// newStaged creates the staging folder and the empty file ffmpeg will write into. ffmpeg opens that
// path and truncates it, which keeps the same file.
func newStaged(src string) (*staged, error) {
	dir, err := os.MkdirTemp(filepath.Dir(src), stagingPattern(src))
	if err != nil {
		return nil, fmt.Errorf("cannot create the temp folder next to the original: %w", err)
	}
	s := &staged{dir: dir, path: filepath.Join(dir, "new"+filepath.Ext(src))}
	f, err := os.OpenFile(s.path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		s.id, err = f.Stat()
		f.Close()
	}
	if err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("temp file: %w", err)
	}
	return s, nil
}

// intact refuses the staged file if its name no longer refers to the regular file the worker
// created: then whatever is there now was not written, or not checked, by us.
func (s *staged) intact() error {
	now, err := os.Lstat(s.path)
	if err != nil || !now.Mode().IsRegular() || !os.SameFile(now, s.id) {
		return kept("the new file was replaced by another file before it could be used, so it was not used")
	}
	return nil
}

// remove deletes the staging folder and whatever is left in it.
func (s *staged) remove() { os.RemoveAll(s.dir) }
