// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"os"
	"path/filepath"
)

// Output-first (V5a; UI-API-CONTRACT-v2.md decision 1, design 00:35Z and 01:04Z): a workflow whose
// Save step is "Next to each library" puts each library's new files in a folder beside it,
// "<library folder> (shrunk)" (/media/movies -> /media/movies (shrunk)), or in the library's own
// override folder. There is no default when the folder beside it can't be used: its parent can't be
// written to, is on another disk than the library (a mount point: Docker's /media bind mount), or is
// the container's own disk (/). The engine never writes there; the person picks a folder.

// NoDefaultFolder is the code of a library with no usable folder for new files.
const NoDefaultFolder = "no_default_folder"

// FolderProblem is why a library has no folder for new files.
type FolderProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Folder and Reason: the parent folder that can't hold it and why, as a code (not_writable |
	// different_disk | container_root), so first run can say it in its own line (IA.md First run).
	Folder string `json:"folder"`
	Reason string `json:"reason"`
}

// Why a library has no default folder for new files (FolderProblem.Reason).
const (
	ReasonNotWritable   = "not_writable"
	ReasonDifferentDisk = "different_disk"
	ReasonContainerRoot = "container_root"
)

// sameDisk reports whether a and b are on the same disk (filesystem); tests replace it.
var sameDisk = sameDiskOS

// NextToLibrary is library lib's default folder for new files, or why it has none. name is the
// library's name, for the words.
func NextToLibrary(name, lib string) (string, *FolderProblem) {
	lib = filepath.Clean(lib)
	dir := lib + " (shrunk)"
	parent := filepath.Dir(lib)
	at := parent // where the folder is made, or itself once it exists
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		at = dir
	}
	reason, code := "", ""
	switch {
	case parent == lib || parent == filepath.Dir(parent): // the library is a disk's root, or its parent is
		reason, code = "is the container's own disk", ReasonContainerRoot
	case !sameDisk(lib, at):
		reason, code = "is on a different disk", ReasonDifferentDisk
	case !writable(at):
		reason, code = "can't be written to", ReasonNotWritable
	}
	if reason != "" {
		return "", &FolderProblem{Code: NoDefaultFolder, Message: "No folder for new files for " + name + " yet: " + parent + " " + reason +
			". Pick a folder in the library's Output tab, or choose One folder for all.", Folder: parent, Reason: code}
	}
	return dir, nil
}

// writable reports whether a file can be made in dir (a test file, removed at once).
func writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".syp-check-*")
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(f.Name())
	return true
}

// OutputFolder is library id's folder for new files with "Next to each library": its override folder,
// or the default beside it (libPath, for a job made before libraries had ids).
func (s *Store) OutputFolder(id int64, libPath string) (string, *FolderProblem, error) {
	name, override := filepath.Base(libPath), ""
	if id > 0 {
		l, err := s.GetLibrary(id)
		if err != nil {
			return "", nil, err
		}
		name, libPath, override = l.Name, l.Path, l.OverrideFolder
	}
	if override != "" {
		return override, nil, nil
	}
	dir, p := NextToLibrary(name, libPath)
	return dir, p, nil
}

// SetOverrideFolder sets library id's own folder for new files ("" goes back to the default).
func (s *Store) SetOverrideFolder(id int64, dir string) error {
	r, err := s.db.Exec(`UPDATE libraries SET override_folder=? WHERE id=?`, dir, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.libraryChanged(id, false)
	return nil
}
