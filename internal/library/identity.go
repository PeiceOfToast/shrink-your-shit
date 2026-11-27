// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// folder is a directory as the watch rule sees it: the real folder, symlinks resolved and the path
// cleaned, and the identity the file system gives it. Two mounts of one folder have the same
// identity and different paths, so both are needed.
type folder struct {
	real string
	// id is the device and inode (the volume serial and file index on Windows). It is empty when
	// the file system could not say, and then only the real path compares.
	id string
}

// identifyFolder reads a folder's identity. The folder exists by the time this is called (the path
// check has just looked at it); a folder that has gone away since compares by its own path.
func identifyFolder(path string) folder {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	real = filepath.Clean(real)
	f := folder{real: real}
	if id, err := fsx.FileID(real); err == nil {
		f.id = id
	}
	return f
}

// same reports whether the two are one folder: the same identity when both have one, otherwise the
// same real path, compared case-insensitively as fsx.Inside compares.
func (f folder) same(o folder) bool {
	if f.id != "" && o.id != "" {
		return f.id == o.id
	}
	return strings.EqualFold(f.real, o.real)
}

// inside reports whether f is somewhere below o, so a file in f would belong to both libraries. The
// real path answers the ordinary case; walking f's ancestors by identity answers the case a path
// cannot: a library reached through a second mount of o's folder.
func (f folder) inside(o folder) bool {
	if fsx.Inside(f.real, o.real) {
		return true
	}
	if o.id == "" {
		return false
	}
	for p := filepath.Dir(f.real); ; {
		if id, err := fsx.FileID(p); err == nil && id == o.id {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// Overlap is a pair of stored libraries that watch one real folder, or folders nested in each
// other. An install that predates this rule can hold such a pair.
type Overlap struct {
	First, Second Library
	Same          bool
}

// Message is the one System log line an overlap gets, naming both libraries and their folders. It
// leaves the decision to the person: start-up reports the overlap and carries on.
func (o Overlap) Message() string {
	if o.Same {
		return fmt.Sprintf("Libraries %q (%s) and %q (%s) watch the same folder. Give each its own folder.", o.First.Name, o.First.Path, o.Second.Name, o.Second.Path)
	}
	return fmt.Sprintf("Library %q (%s) and library %q (%s) share a folder: one is inside the other. Give each its own folder.", o.First.Name, o.First.Path, o.Second.Name, o.Second.Path)
}

// Overlaps lists every pair of stored libraries whose folders are one folder, or one inside the
// other. Start-up reports them instead of failing: the person can still open the app and move one
// library, and the checks refuse further edits that keep the overlap.
func (c *Catalog) Overlaps(ctx context.Context) ([]Overlap, error) {
	libs, err := c.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	folders := make([]folder, len(libs))
	for i, lib := range libs {
		folders[i] = identifyFolder(lib.Path)
	}
	// An install can hold two mounts of one network share, which the identity misses: the same share compare the
	// checks use reports those too (S2-10a). No readable mount table skips it.
	mounts := readMounts()
	shares := make([]Share, len(libs))
	onShare := make([]bool, len(libs))
	for i := range libs {
		shares[i], onShare[i] = ShareOf(mounts, folders[i].real)
	}
	var out []Overlap
	for i := 0; i < len(libs); i++ {
		for j := i + 1; j < len(libs); j++ {
			sameShare := onShare[i] && onShare[j]
			switch {
			case folders[i].same(folders[j]), sameShare && shares[i].same(shares[j]):
				out = append(out, Overlap{First: libs[i], Second: libs[j], Same: true})
			case folders[i].inside(folders[j]), folders[j].inside(folders[i]),
				sameShare && (shares[i].inside(shares[j]) || shares[j].inside(shares[i])):
				out = append(out, Overlap{First: libs[i], Second: libs[j]})
			}
		}
	}
	return out, nil
}
