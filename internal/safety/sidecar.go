// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// Sidecar is one subtitle saved as an .srt file beside a converted file, recorded before it is
// placed: its path and what it is, and what the new file at VideoPath is once it is in place.
// Only a file that is still exactly this one is ever removed (L-AUD-25).
type Sidecar struct {
	JobID      int64
	Path       string
	Bytes      int64
	MTime      int64  // UnixNano
	Hash       string // the full SHA-256 of the .srt, which is small
	VideoPath  string
	VideoBytes int64
	VideoHash  string
}

// SidecarStore keeps the sidecar records across runs and restarts.
type SidecarStore interface {
	// AddSidecars records them, in one transaction, before they are placed.
	AddSidecars(list []Sidecar) error
	// FinishSidecars is the job's swap done: the job keeps the names of its .srt files, and their
	// records go.
	FinishSidecars(jobID int64) error
	// DropSidecar forgets one record: its file is gone, is not ours, or was never placed.
	DropSidecar(jobID int64, path string) error
	// Sidecars are every record left, for the check a start makes.
	Sidecars() ([]Sidecar, error)
}

// Hooks for tests: the check and the removal a recovery makes.
var (
	sidecarLstat  = os.Lstat
	sidecarHash   = fsx.SHA256
	sidecarRemove = os.Remove
)

// sidecarOwner is what sidecarIsOurs found out about a recorded .srt file.
type sidecarOwner int

const (
	sidecarNotOurs sidecarOwner = iota // gone, or not exactly the file we wrote: its record can go
	sidecarOurs                        // still exactly the file we wrote
	sidecarUnknown                     // could not check (Lstat or reading failed): keep the record
)

// sidecarIsOurs compares c's file with what we wrote: regular file, size, time and full SHA-256.
func sidecarIsOurs(c Sidecar) (sidecarOwner, error) {
	st, err := sidecarLstat(c.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return sidecarNotOurs, nil
	case err != nil:
		return sidecarUnknown, err
	case !st.Mode().IsRegular() || st.Size() != c.Bytes || st.ModTime().UnixNano() != c.MTime:
		return sidecarNotOurs, nil
	}
	h, err := sidecarHash(c.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return sidecarNotOurs, nil
	case err != nil:
		return sidecarUnknown, err
	case h != c.Hash:
		return sidecarNotOurs, nil
	}
	return sidecarOurs, nil
}

// RemoveSidecarIfOurs removes c's file if it is still exactly ours. removed says it was; a nil
// error means the record can go (the file is gone, someone else's, or removed). When we could not
// tell whether the file is ours, it is left alone and the error keeps the record, so a later start
// checks again.
func RemoveSidecarIfOurs(c Sidecar) (removed bool, err error) {
	switch o, err := sidecarIsOurs(c); o {
	case sidecarUnknown:
		return false, fmt.Errorf("could not tell whether it is ours: %w", err)
	case sidecarNotOurs:
		return false, nil
	}
	if err := sidecarRemove(c.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	return true, nil
}

// RecoverSidecars ends what a crash left between putting .srt files beside a new file and placing
// the file (run at start). Where the new video is in place (its recorded size and sample hash at
// VideoPath), its subtitles stay and only their records go; otherwise each .srt that is still
// exactly ours is removed, so a retry finds no name taken by our own file. A record stays only when
// its file could not be checked or removed; the next start tries again.
func RecoverSidecars(store SidecarStore) []string {
	list, err := store.Sidecars()
	if err != nil {
		return []string{fmt.Sprintf("subtitle files: %v", err)}
	}
	var said []string
	for _, c := range list {
		swapped := false
		if st, err := os.Stat(c.VideoPath); err == nil && st.Size() == c.VideoBytes {
			if h, err := fsx.SampleHash(c.VideoPath); err == nil && h == c.VideoHash {
				swapped = true // the new file was placed: the subtitles belong to it
			}
		}
		if !swapped {
			removed, err := RemoveSidecarIfOurs(c)
			if err != nil {
				said = append(said, fmt.Sprintf("job %d: could not remove %s, kept on record to try again: %v", c.JobID, c.Path, err))
				continue // its record stays
			}
			if removed {
				said = append(said, fmt.Sprintf("job %d: removed %s (put there before a swap that did not happen)", c.JobID, c.Path))
			}
		}
		if err := store.DropSidecar(c.JobID, c.Path); err != nil {
			said = append(said, fmt.Sprintf("job %d: %s: %v", c.JobID, c.Path, err))
		}
	}
	return said
}

// FreeName is the name Place would use for final if it ran now: final itself when it is free, else
// "name (2).ext" and so on (L-SAFE-10). A run asks before it encodes, so the names of the .srt
// files it writes match the file the place puts them beside.
func FreeName(final string) (string, error) {
	for n := 1; n <= maxPlaceNames; n++ {
		cand := placeCandidate(final, n)
		_, err := os.Lstat(cand)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return cand, nil
		case err != nil:
			return "", err
		}
	}
	return "", fmt.Errorf("no free name for %s in %d tries", final, maxPlaceNames)
}
