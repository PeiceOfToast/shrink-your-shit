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

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
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

// stagingDirName is a staging folder's name: "." + the file's name + "." + digits + ".syp-tmp".
var stagingDirName = regexp.MustCompile(`^\..+\.[0-9]+\.syp-tmp$`)

// stagedNew is our temp file's name in a staging folder: "new." and the new file's type.
var stagedNew = regexp.MustCompile(`^new\.[A-Za-z0-9]+$`)

// SweepStaging removes our staging folders that a server killed mid-convert left anywhere under the
// folders for new files, dirs (review-697332b P2: the per-file clean-up only looks next to an original,
// and a file whose job never runs again would keep its folder for ever). Only ours: a folder named
// exactly like ours that holds nothing but our temp file and extracted subtitles. Run at start, before any
// worker: then every staging folder is a leftover. It says what it removed.
func SweepStaging(dirs []string) []string {
	var said []string
	for _, root := range dirs {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() || p == root {
				return nil
			}
			if !stagingDirName.MatchString(d.Name()) {
				return nil
			}
			if sweepOne(p) {
				said = append(said, "removed an unfinished conversion's temp folder: "+p)
			}
			return fs.SkipDir
		})
	}
	return said
}

// OutputFolders are the folders for new files of every library whose workflow saves them in another
// folder (one folder for all, or next to each library): where SweepStaging looks.
func OutputFolders(store *jobs.Store) []string {
	libs, err := store.Libraries()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range libs {
		f, err := store.GetFlow(l.FlowID)
		if err != nil {
			continue
		}
		for _, part := range f.Template.Parts() { // each path of a workflow with checks (V6a)
			s := part.Selectors
			if s == nil || s.AfterConverting != "folder" {
				continue
			}
			dir := s.OutputDir
			if s.FolderMode == rules.NextToLibrary {
				d, problem, err := store.OutputFolder(l.ID, l.Path)
				if err != nil || problem != nil {
					continue
				}
				dir = d
			}
			if dir != "" && !seen[dir] {
				seen[dir] = true
				out = append(out, dir)
			}
		}
	}
	return out
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

// sweepOne removes the staging folder p if it holds nothing but our temp file and extracted subtitles
// (else it is left alone, and logged); true when it was removed.
func sweepOne(p string) bool {
	entries, err := os.ReadDir(p)
	if err != nil {
		return false
	}
	news := 0
	for _, e := range entries {
		switch {
		case !e.Type().IsRegular():
			log.Printf("leaving %s alone: it holds files shrink-your-shit did not put there", p)
			return false
		case stagedNew.MatchString(e.Name()):
			news++
		case !stagedSrt.MatchString(e.Name()):
			log.Printf("leaving %s alone: it holds files shrink-your-shit did not put there", p)
			return false
		}
	}
	if news > 1 {
		return false
	}
	for _, e := range entries {
		os.Remove(filepath.Join(p, e.Name()))
	}
	return os.Remove(p) == nil
}

// SweepNextTo removes the staging folders a killed conversion left next to the files of paths (the
// jobs' files: a replace stages beside its original, in the library), each folder read once (#163 QA:
// a conversion killed mid-encode kept its folder after the restart, until its own job ran again). Only
// a folder named for one of those files ("." + its name + "." + digits + ".syp-tmp") and holding only
// ours. Run at start, before any worker.
func SweepNextTo(paths []string) []string {
	byDir := map[string]map[string]bool{}
	for _, p := range paths {
		dir := filepath.Dir(p)
		if byDir[dir] == nil {
			byDir[dir] = map[string]bool{}
		}
		byDir[dir][strings.TrimSuffix(stagingPattern(p), "*.syp-tmp")] = true // "." + stem + "."
	}
	var said []string
	for dir, prefixes := range byDir {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || !stagingDirName.MatchString(e.Name()) {
				continue
			}
			for prefix := range prefixes {
				rest, ok := strings.CutPrefix(e.Name(), prefix) // "<digits>.syp-tmp": this file's, not "a.b.mkv"'s for "a.mkv"
				if !ok || !digits.MatchString(strings.TrimSuffix(rest, ".syp-tmp")) {
					continue
				}
				if p := filepath.Join(dir, e.Name()); sweepOne(p) {
					said = append(said, "removed an unfinished conversion's temp folder: "+p)
				}
				break
			}
		}
	}
	return said
}

var digits = regexp.MustCompile(`^[0-9]+$`)
