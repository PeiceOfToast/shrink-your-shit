// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// FolderCheck is what "Check this folder" answers (page 09): how many videos it holds, their total
// size, and what Add would refuse. Nothing is saved and nothing is probed.
type FolderCheck struct {
	Videos    int
	SizeBytes int64
	Problems  []FolderProblem
}

// FolderProblem is one reason Add would refuse the folder, in the shape page 09 shows.
type FolderProblem struct {
	Code    string
	Message string
}

const (
	// ProblemAlreadyWatched is a folder another library already has.
	ProblemAlreadyWatched = "already_watched"
	// ProblemUnreadable is a part of the folder the check could not read.
	ProblemUnreadable = "unreadable"
	// ProblemUnwritable is a folder the app cannot write in, so converted videos could not be saved.
	ProblemUnwritable = "unwritable"
)

// FileInLibrary reports whether the file sits in a folder a library watches, judged by the real
// path: the folder's existing part is resolved through symlinks, so a link inside a library that
// points outside it is not inside the library. A path that does not exist yet still compares by
// where it would be, so the caller's own "could not read" refusal stays the one a person sees.
func (c *Catalog) FileInLibrary(ctx context.Context, path string) (bool, error) {
	libs, err := c.ListLibraries(ctx)
	if err != nil {
		return false, err
	}
	for _, lib := range libs {
		if fsx.Inside(path, lib.Path) {
			return true, nil
		}
	}
	return false, nil
}

// checkWritable proves the app can write in the folder by creating and removing a temp file. A folder
// it can read but not write is refused up front rather than at the first conversion.
func checkWritable(path string) error {
	f, err := os.CreateTemp(path, ".syp-check-*")
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

// videoExts are the file names the library treats as video. The list is the scanner's: a folder
// check counts what a scan would find.
var videoExts = map[string]bool{
	".3g2": true, ".3gp": true, ".asf": true, ".avi": true, ".divx": true, ".f4v": true,
	".flv": true, ".m2ts": true, ".m4v": true, ".mkv": true, ".mov": true, ".mp4": true,
	".mpeg": true, ".mpg": true, ".mts": true, ".mxf": true, ".ogv": true, ".rm": true,
	".rmvb": true, ".ts": true, ".vob": true, ".webm": true, ".wmv": true,
}

// IsVideoName reports whether a file name has a video extension.
func IsVideoName(name string) bool {
	return videoExts[strings.ToLower(filepath.Ext(name))]
}

// CheckFolder counts the videos a folder holds and reports what Add would refuse about it. A missing
// folder or a file is a PathError, as Add answers; an unreadable part of the folder is a problem, so
// the person can still add the rest.
func (c *Catalog) CheckFolder(ctx context.Context, path string) (FolderCheck, error) {
	abs, err := c.checkPath(path)
	if err != nil {
		return FolderCheck{}, err
	}
	out := FolderCheck{}
	if err := c.refuseWatched(ctx, abs, 0); err != nil {
		if errors.Is(err, ErrPathAlreadyWatched) {
			out.Problems = append(out.Problems, FolderProblem{
				Code:    ProblemAlreadyWatched,
				Message: "Another library already watches this folder. Choose a different folder.",
			})
		} else {
			return FolderCheck{}, err
		}
	}
	if err := checkWritable(abs); err != nil {
		out.Problems = append(out.Problems, FolderProblem{
			Code:    ProblemUnwritable,
			Message: "The app can't write in this folder, so converted videos couldn't be saved here. It runs as the user syp: give syp access (for example add syp to this folder's group), or choose a folder outside your home.",
		})
	}
	walkErr := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if p == abs {
				return err
			}
			out.Problems = append(out.Problems, FolderProblem{
				Code:    ProblemUnreadable,
				Message: fmt.Sprintf("Part of this folder could not be read: %s.", p),
			})
			return nil
		}
		if p == abs {
			return nil
		}
		if d.IsDir() {
			if d.Type()&fs.ModeSymlink != 0 || skipName(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || skipName(d.Name()) || !IsVideoName(d.Name()) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			out.Problems = append(out.Problems, FolderProblem{
				Code:    ProblemUnreadable,
				Message: fmt.Sprintf("Part of this folder could not be read: %s.", p),
			})
			return nil
		}
		out.Videos++
		out.SizeBytes += info.Size()
		return nil
	})
	if walkErr != nil {
		return FolderCheck{}, walkErr
	}
	return out, nil
}
