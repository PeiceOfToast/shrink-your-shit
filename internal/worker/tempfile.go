// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The new file is written into a private staging folder next to the original: created fresh with
// os.MkdirTemp (unpredictable name, mode 0700), holding one file created exclusively at mode 0600.
// On Linux and macOS no other user can reach or replace the file while it is written and checked.
// On Windows the mode is ignored and the folder inherits the media folder's access list (ACL), so
// it is only as private as that folder. Either way the worker remembers which file it created. The folder name starts with "." so the scanner skips it; the file
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

// removeFile is os.Remove; tests make it fail on every OS (Windows ignores POSIX permission bits).
var removeFile = os.Remove

// stagedSrt is an extracted subtitle in a staging folder (rules.StagedSrt).
var stagedSrt = regexp.MustCompile(`^sub\.[0-9]+\.srt$`)

// removeLeftoverTemps removes what earlier runs left next to src: a file under the old fixed temp
// name, and staging folders that hold nothing but our one temp file, a regular file (that file is
// removed, then the empty folder). Anything else with a matching name, such as a folder holding other
// files or a link, is left alone and logged: it may not be ours. A removal that fails stops the job. Removing a link removes
// the link, never what it points to.
func removeLeftoverTemps(src string) error {
	old, err := TempFiles(src)
	if err != nil {
		return fmt.Errorf("looking for old temp files: %w", err)
	}
	// our one temp file: new.<the original's type>, or new.mkv / new.mp4 when the flow changes the type
	isStaged := func(name string) bool {
		return name == "new"+filepath.Ext(src) || name == "new.mkv" || name == "new.mp4"
	}
	for _, p := range old {
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("cannot check the old temp file %s: %w", p, err)
		}
		var remove []string
		switch {
		case !strings.HasPrefix(filepath.Base(p), ".") && !fi.IsDir(): // the old fixed temp file name
			remove = []string{p}
		case strings.HasPrefix(filepath.Base(p), ".") && fi.IsDir():
			entries, err := os.ReadDir(p)
			if err != nil {
				return fmt.Errorf("cannot check the old temp folder %s: %w", p, err)
			}
			ours := true
			news := 0
			for _, e := range entries { // our temp file and, for extracted subtitles, sub.N.srt
				switch {
				case !e.Type().IsRegular():
					ours = false
				case isStaged(e.Name()):
					news++
				case !stagedSrt.MatchString(e.Name()):
					ours = false
				}
			}
			ours = ours && news <= 1
			if !ours {
				log.Printf("leaving %s alone: it holds files shrink-your-shit did not put there", p)
				continue
			}
			for _, e := range entries {
				remove = append(remove, filepath.Join(p, e.Name()))
			}
			remove = append(remove, p) // the folder last, once it is empty
		default:
			log.Printf("leaving %s alone: it is not a temp file or folder shrink-your-shit made", p)
			continue
		}
		for _, r := range remove {
			if err := removeFile(r); err != nil && !errors.Is(err, fs.ErrNotExist) {
				ferr := failed(fmt.Sprintf("An old temp file next to this file (%s) could not be removed, so converting did not start. Your original was not touched. Delete it, or let shrink-your-shit write in this folder, then press Retry.", filepath.Base(r)),
					fmt.Errorf("cannot remove the old temp file %s: %w", r, err))
				if errors.Is(err, fs.ErrPermission) { // the person must let it write there
					return ferr
				}
				return retryable(ferr, retryGeneral) // busy (a player or scanner holds it)
			}
		}
	}
	return nil
}

// newStaged creates the staging folder and the empty file ffmpeg will write into. ffmpeg opens that
// path and truncates it, which keeps the same file.
func newStaged(src, ext string) (*staged, error) {
	dir, err := os.MkdirTemp(filepath.Dir(src), stagingPattern(src))
	if err != nil {
		return nil, fmt.Errorf("cannot create the temp folder next to the original: %w", err)
	}
	s := &staged{dir: dir, path: filepath.Join(dir, "new"+ext)}
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
