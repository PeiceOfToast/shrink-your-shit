// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// ErrNoPermission means a leftover staging folder could not be removed for lack of permission. The job fails
// before encoding.
var ErrNoPermission = errors.New("safety: no permission to remove a leftover staging folder")

// ErrBusy means a leftover is in use right now. The job retries later.
var ErrBusy = errors.New("safety: a leftover staging folder is in use")

// stagingSuffix marks a folder Stage made.
const stagingSuffix = ".syp-tmp"

// subtitleSidecar is a sub.N.srt file a staging folder may hold (L-SAFE-11).
var subtitleSidecar = regexp.MustCompile(`^sub\.\d+\.srt$`)

// SweepReport says what Sweep did. Left holds the folders it kept, with the reason.
type SweepReport struct {
	Removed []string
	Left    []LeftBehind
}

// LeftBehind is one folder Sweep did not remove.
type LeftBehind struct {
	Path   string
	Reason string
}

// Sweep removes the staging folders that are certainly ours, in the direct children of each folder given. It
// never recurses into a library and never follows a link: a link named like ours is removed as a link. A folder
// that holds anything besides our temp file (and sub.N.srt) is left, and reported.
func Sweep(ctx context.Context, folders []string) (SweepReport, error) {
	var rep SweepReport
	var errs []error
	for _, dir := range folders {
		if err := ctx.Err(); err != nil {
			return rep, errors.Join(append(errs, err)...)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("sweep %s: %w", dir, err))
			continue
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), stagingSuffix) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			removed, err := sweepFolder(path)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if removed {
				rep.Removed = append(rep.Removed, path)
			} else {
				rep.Left = append(rep.Left, LeftBehind{Path: path, Reason: "holds something that is not ours"})
			}
		}
	}
	return rep, errors.Join(errs...)
}

// sweepFolder removes one staging folder when every child is ours. It returns false, with no error, when it leaves
// the folder alone.
func sweepFolder(path string) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("sweep %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		// A link named like ours: remove the link itself, never its target.
		return true, removeOne(path)
	}
	if !info.IsDir() {
		return false, nil
	}
	children, err := os.ReadDir(path)
	if err != nil {
		return false, classify(path, err)
	}
	for _, c := range children {
		if !ours(c.Name()) {
			return false, nil
		}
	}
	for _, c := range children {
		if err := removeOne(filepath.Join(path, c.Name())); err != nil {
			return false, err
		}
	}
	return true, removeOne(path)
}

// ours reports whether a child name is one Stage or the subtitle step makes: out.<ext> or sub.N.srt.
func ours(name string) bool {
	return (strings.HasPrefix(name, "out.") && len(name) > len("out.")) || subtitleSidecar.MatchString(name)
}

func removeOne(path string) error {
	if err := os.Remove(path); err != nil {
		return classify(path, err)
	}
	return nil
}

// classify turns a remove error into the typed errors the job acts on.
func classify(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("sweep %s: %w: %v", path, ErrNoPermission, err)
	case errors.Is(err, syscall.EBUSY), errors.Is(err, syscall.ETXTBSY):
		return fmt.Errorf("sweep %s: %w: %v", path, ErrBusy, err)
	}
	return fmt.Errorf("sweep %s: %w", path, err)
}
