// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Getting an original back and deleting kept originals after their days (slice 18, QOL-SAFETY-PLAN.md
// S2). A restore uses the swap's journal row: it is marked "restoring" before anything is renamed, so
// RecoverSwaps can finish it after a crash.

// Store is what restoring and purging need from the job store.
type Store interface {
	Journal
	OriginalByID(id int64) (jobs.Original, error)
	PutFile(f jobs.File) error
}

// RestoreError is a restore that was refused: Code for the API, Message in plain words (what
// happened, what was not touched, what to do).
type RestoreError struct{ Code, Message string }

func (e *RestoreError) Error() string { return e.Message }

// asidePath is where the converted file waits while its original is put back.
func asidePath(o jobs.Original) string { return o.Trash + ".converted" }

// isOriginal reports whether the file at p is the kept original as recorded: size, modification time
// (renames keep it) and sampled hash (review 9cb9617: an edit between the samples changes the time).
func isOriginal(p string, o jobs.Original) bool {
	st, err := os.Stat(p)
	if err != nil || st.Size() != o.Bytes || st.ModTime().UnixNano() != o.ModTime {
		return false
	}
	h, err := SampleHash(p)
	return err == nil && h == o.Hash
}

// convertedUnchanged reports whether the file at p is still the converted file as it was put in place.
// Rows from before slice 18 recorded only its size.
func convertedUnchanged(st os.FileInfo, p string, o jobs.Original) bool {
	if st.Size() != o.NewBytes || o.NewMTime != 0 && st.ModTime().UnixNano() != o.NewMTime {
		return false
	}
	if o.NewHash == "" {
		return true
	}
	h, err := SampleHash(p)
	return err == nil && h == o.NewHash
}

// Restore puts kept original id back at its name and removes the converted file. It refuses (a
// *RestoreError, nothing moved) if the original is not kept any more, is not the file that was kept,
// or if the converted file was changed since it was made (unless force). After it, the file is marked
// restored so it is not converted again. The returned Original is the row as restored.
func Restore(st Store, id int64, force bool) (jobs.Original, error) {
	o, err := st.OriginalByID(id)
	if errors.Is(err, jobs.ErrNotFound) {
		return o, &RestoreError{"not_found", "There is no such kept original. Nothing was changed."}
	}
	if err != nil {
		return o, err
	}
	if o.State != jobs.OriginalKept {
		return o, &RestoreError{"not_kept", "This original is not kept any more: it was already put back or deleted. Nothing was changed."}
	}
	if !isOriginal(o.Trash, o) {
		return o, &RestoreError{"original_missing", fmt.Sprintf("The kept original (%s) is missing or is not the file that was kept, so it can't be put back safely. Nothing was changed; the converted file is still in place.", o.Trash)}
	}
	cst, err := os.Stat(o.Path)
	converted := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return o, err
	}
	if converted && !force && !convertedUnchanged(cst, o.Path, o) {
		return o, &RestoreError{"converted_changed", "The converted file was changed since it was made, so putting the original back would lose those changes. Nothing was changed. To put it back anyway, run shrink-your-shit restore --force in a terminal."}
	}
	if err := st.SetSwapState(id, jobs.Restoring); err != nil {
		return o, err
	}
	if err := hook("restore journal written"); err != nil {
		return o, err
	}
	aside := asidePath(o)
	if converted {
		if err := os.Rename(o.Path, aside); err != nil {
			st.SetSwapState(id, jobs.OriginalKept)
			return o, fmt.Errorf("could not move the converted file aside (%v); nothing was changed", err)
		}
		if err := hook("converted moved aside"); err != nil {
			return o, err
		}
	}
	if err := os.Rename(o.Trash, o.Path); err != nil {
		if converted {
			if rerr := os.Rename(aside, o.Path); rerr != nil {
				return o, fmt.Errorf("could not put the original back (%v), nor the converted file (%v); it is in %s and is put back at the next start", err, rerr, aside)
			}
		}
		st.SetSwapState(id, jobs.OriginalKept)
		return o, fmt.Errorf("could not put the original back (%v); nothing was changed", err)
	}
	if err := hook("original back"); err != nil {
		return o, err
	}
	return o, finishRestore(st, o)
}

// IdentityConfirmed reports whether the file now at the original's name has the size, modification
// time and sampled hash recorded when it was kept. That confirms its identity; a sampled hash cannot
// prove every byte, so the program says "identity confirmed", not "byte-identical".
func IdentityConfirmed(o jobs.Original) bool {
	st, err := os.Stat(o.Path)
	return err == nil && st.ModTime().UnixNano() == o.ModTime && isOriginal(o.Path, o)
}

// finishRestore removes the converted file set aside, marks the file restored and the row restored.
func finishRestore(st Store, o jobs.Original) error {
	if err := os.Remove(asidePath(o)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Printf("restore %s: the converted file %s is left: %v", o.Path, asidePath(o), err)
	}
	rst, err := os.Stat(o.Path)
	if err != nil {
		return err
	}
	if err := st.PutFile(jobs.File{Path: o.Path, Size: rst.Size(), ModTime: rst.ModTime().UnixNano(), Decision: jobs.DecisionRestored, Reason: jobs.DecisionRestored}); err != nil {
		return fmt.Errorf("the original is back, but it could not be marked as restored, so it may be converted again: %w", err)
	}
	removeIfEmpty(filepath.Dir(o.Trash))
	return st.SetSwapState(o.ID, jobs.OriginalRestored)
}

// removeIfEmpty removes the .syp-trash folder once nothing is kept in it (Remove fails on a folder
// that is not empty, so nothing else is ever deleted).
func removeIfEmpty(dir string) {
	if filepath.Base(dir) == TrashDir {
		os.Remove(dir)
	}
}

// recoverRestores ends restores a crash interrupted (rows "restoring"): the original back at its name
// finishes it; the converted file still at its name means nothing moved (back to kept); the name empty
// with the original still in the trash means it was half done: the original is put back.
func recoverRestores(st Store, say func(string, ...any)) {
	rows, err := st.Originals(jobs.Restoring)
	if err != nil {
		say("cannot read the restore journal: %v", err)
		return
	}
	exists := func(p string) bool { _, err := os.Lstat(p); return err == nil }
	for _, o := range rows {
		switch {
		case isOriginal(o.Path, o):
			if err := finishRestore(st, o); err != nil {
				say("%s: %v", o.Path, err)
				continue
			}
			say("%s: finished putting the original back", o.Path)
		case exists(o.Path) && isOriginal(o.Trash, o):
			st.SetSwapState(o.ID, jobs.OriginalKept)
			say("%s: the restore was interrupted before anything moved; the original is still kept", o.Path)
		case !exists(o.Path) && isOriginal(o.Trash, o):
			if err := os.Rename(o.Trash, o.Path); err != nil {
				say("%s: could not put the original back: %v", o.Path, err)
				continue
			}
			if err := finishRestore(st, o); err != nil {
				say("%s: %v", o.Path, err)
				continue
			}
			say("%s: finished putting the original back after an interruption", o.Path)
		default:
			st.SetSwapState(o.ID, jobs.SwapAttention)
			say("%s: an interrupted restore could not be accounted for; nothing was moved. Please check this file", o.Path)
		}
	}
}

// Purge deletes kept originals whose time is up: kept for days or more (the setting now, so a lower
// setting applies at the next purge). Only the recorded file is deleted, and only if its size is the
// recorded one; anything else in .syp-trash is never touched. A library's record being deleted does
// not matter. Returns what it did, for the log.
func Purge(st Store, days int, now time.Time) []string {
	rows, err := st.Originals(jobs.OriginalKept)
	if err != nil {
		return []string{"cannot read the kept originals: " + err.Error()}
	}
	var said []string
	purged := 0
	for _, o := range rows {
		kept, err := time.Parse(time.RFC3339, o.KeptAt)
		if err != nil || now.Before(kept.Add(time.Duration(days)*24*time.Hour)) {
			continue
		}
		fi, err := os.Lstat(o.Trash)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			said = append(said, fmt.Sprintf("%s: its kept original was already gone", o.Path))
		case err != nil || !fi.Mode().IsRegular() || fi.Size() != o.Bytes:
			st.SetSwapState(o.ID, jobs.SwapAttention)
			said = append(said, fmt.Sprintf("%s: the file in %s is not the kept original, so it was left alone", o.Path, o.Trash))
			continue
		default:
			if err := os.Remove(o.Trash); err != nil {
				said = append(said, fmt.Sprintf("%s: could not delete its kept original: %v", o.Path, err))
				continue
			}
		}
		st.SetSwapState(o.ID, jobs.OriginalPurged)
		removeIfEmpty(filepath.Dir(o.Trash))
		purged++
	}
	if purged > 0 {
		said = append(said, fmt.Sprintf("deleted %d kept original(s) older than %d day(s)", purged, days))
	}
	return said
}
