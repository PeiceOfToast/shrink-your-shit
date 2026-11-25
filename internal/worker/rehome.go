// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// U-B: a share re-added at another mount leaves kept originals whose recorded paths name the old mount. A purge
// finds them again under the library folders and the share mount points, by the tail of their trash path, and
// re-homes the row only when exactly one place proves to be ours. What it cannot reach is reported; nothing is
// deleted without proof.

// Codes for an Unreachable entry (the API sends them as they are).
const (
	CodeNotFound  = "not_found" // the original is not under any library or mount folder
	CodeAmbiguous = "ambiguous" // more than one place is ours: nothing is chosen
	CodeChanged   = "changed"   // a file is at the name, but it is not the kept original
	CodeInUse     = "in_use"    // a queued or running job uses the file
)

// Unreachable is a kept original that a purge left in place, and why. Path is as recorded.
type Unreachable struct {
	ID     int64  `json:"id"`
	Path   string `json:"path"`
	Trash  string `json:"-"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

// rehomed is one place a kept original's files were found, as the row would name them.
type rehomed struct {
	path, newPath, trash string
}

// findMoved looks for kept original o under roots. For each suffix of o.Trash (leading components dropped one
// at a time, at least one kept), the suffix is joined onto each root. The part of o.Trash that was dropped is
// the old prefix, and the root's part before the suffix is the new one; o.Path, o.NewPath and o.Dest() must
// start with the old prefix, and are swapped to the new one. A place is ours only if the original there passes
// isOriginal and the converted file at the swapped Dest passes isConvertedFile. found reports whether a regular
// file was at any candidate name (that is what tells "changed" from "not found").
func findMoved(o jobs.Original, roots []string) (matches []rehomed, found, changed, orphan bool) {
	trash := filepath.Clean(o.Trash)
	parts := strings.Split(trash, string(filepath.Separator))
	seen := map[string]bool{}
	for _, root := range roots {
		root = filepath.Clean(root)
		for i := 1; i < len(parts); i++ {
			suffix := filepath.Join(parts[i:]...)
			oldPrefix := trash[:len(trash)-len(suffix)]
			candidate := filepath.Join(root, suffix)
			if seen[candidate] {
				continue
			}
			newPrefix := candidate[:len(candidate)-len(suffix)]
			path, ok := swapPrefix(o.Path, oldPrefix, newPrefix)
			if !ok {
				continue
			}
			newPath := ""
			if o.NewPath != "" {
				if newPath, ok = swapPrefix(o.NewPath, oldPrefix, newPrefix); !ok {
					continue
				}
			}
			dest, ok := swapPrefix(o.Dest(), oldPrefix, newPrefix)
			if !ok {
				continue
			}
			st, err := os.Stat(candidate)
			if err != nil || !st.Mode().IsRegular() {
				continue
			}
			seen[candidate] = true
			found = true
			if !isOriginal(candidate, o) {
				changed = true
				continue
			}
			if !isConvertedFile(dest, o) {
				orphan = true
				continue
			}
			matches = append(matches, rehomed{path: path, newPath: newPath, trash: candidate})
		}
	}
	return matches, found, changed, orphan
}

// swapPrefix replaces oldPrefix by newPrefix at the start of p; false if p doesn't start with oldPrefix.
func swapPrefix(p, oldPrefix, newPrefix string) (string, bool) {
	if !strings.HasPrefix(p, oldPrefix) {
		return "", false
	}
	return newPrefix + p[len(oldPrefix):], true
}

// searchRoots are the folders a moved original may be under now: the library folders, and the mount points
// the shares are recorded at (a re-added share does not change its library's path).
func searchRoots(st Store) ([]string, error) {
	libs, err := st.Libraries()
	if err != nil {
		return nil, err
	}
	var roots []string
	for _, l := range libs {
		if l.Path != "" {
			roots = append(roots, l.Path)
		}
	}
	mounts, err := st.MountRoots()
	if err != nil {
		return roots, err
	}
	return append(roots, mounts...), nil
}

// rehomeOrReport finds o's files after their share moved. It re-homes the row when exactly one place is ours and
// returns the row as it now is; otherwise it returns o unchanged and the unreachable entry to report.
func rehomeOrReport(st Store, o jobs.Original, roots []string, say *[]string) (jobs.Original, *Unreachable) {
	unreachable := func(code, reason string) (jobs.Original, *Unreachable) {
		return o, &Unreachable{ID: o.ID, Path: o.Path, Trash: o.Trash, Code: code, Reason: reason}
	}
	matches, found, changed, orphan := findMoved(o, roots)
	switch {
	case len(matches) == 1:
		m := matches[0]
		if err := st.RehomeOriginal(o.ID, m.path, m.newPath, m.trash); err != nil {
			return unreachable(CodeNotFound, "its files were found, but the record could not be moved to them: "+err.Error())
		}
		*say = append(*say, fmt.Sprintf("%s: its files were found at %s after the share moved; the record now names them", o.Path, m.path))
		o.Path, o.NewPath, o.Trash = m.path, m.newPath, m.trash
		return o, nil
	case len(matches) > 1:
		return unreachable(CodeAmbiguous, fmt.Sprintf("its file is in %d places under the library folders, so none was chosen. Nothing was deleted.", len(matches)))
	case changed:
		return unreachable(CodeChanged, "a file is under the library folders where its original was, but it is not the kept original, so nothing was deleted.")
	case orphan:
		return unreachable(CodeNotFound, "its original is under the library folders, but the converted file is not beside it, so nothing was deleted.")
	case found:
		return unreachable(CodeNotFound, "its file was not found under the library folders.")
	}
	return unreachable(CodeNotFound, "its folder and file were not found under the library folders.")
}

// ForgetError is a forget that was refused: Code for the API, Message in plain words.
type ForgetError struct{ Code, Message string }

func (e *ForgetError) Error() string { return e.Message }

// Forget marks kept original id gone (its days end now) and deletes nothing. It is done only when the file can't
// be found anywhere the purge would look: a file found there is a refusal (a *ForgetError, 409 in the API).
// An unknown id is jobs.ErrNotFound.
func Forget(st Store, id int64, now time.Time) error {
	o, err := st.OriginalByID(id)
	if err != nil {
		return err
	}
	if o.State != jobs.OriginalKept {
		return &ForgetError{Code: "not_kept", Message: "This original is not kept any more, so there is nothing to forget. Nothing was changed."}
	}
	if _, err := os.Lstat(o.Trash); err == nil {
		return &ForgetError{Code: "file_found", Message: "Its kept original is still in the trash folder, so it isn't gone. Empty trash or restore it instead. Nothing was changed."}
	}
	roots, err := searchRoots(st)
	if err != nil {
		return err
	}
	matches, found, changed, orphan := findMoved(o, roots)
	if len(matches) > 0 || found || changed || orphan {
		return &ForgetError{Code: "file_found", Message: "A file is still under the library or mount folders where its original was, so it isn't gone. Nothing was changed."}
	}
	return st.SetKeptGone(id, now)
}
