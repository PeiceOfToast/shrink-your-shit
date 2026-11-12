// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// Convert again (design 11:45Z, SCREENS V6 job drawer): a done job runs again, always from its
// original, never from our own output, with its library's workflow as it is now. A file that replaced
// its original runs from the original kept in .syp-trash (the new file replaces our earlier one once it
// passes its checks, and the original's days start again); one whose original stayed in place runs
// from it (our earlier output is replaced only after the new one passes, as any own output is).

// AgainCheck says whether a job can be converted again, and if not, why: Code and Message, and
// DeletedAt (RFC 3339) when its kept original was deleted. For a done job it also says what its run
// did, so the drawer can word the button (orch-lead 17:12Z): Policy (replace, separate_folder or
// beside), KeptUntil (replace: when the kept original leaves the trash) and OutputPath (the earlier new
// file Convert again replaces; "" for replace, where it is the file's own name).
type AgainCheck struct {
	Possible                 bool
	Code, Message, DeletedAt string
	Policy, KeptUntil        string
	OutputPath               string
	originalID               int64 // the kept original it runs from (0: the original in place)
}

// Codes of a refused Convert again.
const (
	AgainNotDone      = "not_done"      // only a converted file can be converted again
	AgainOriginalGone = "original_gone" // its original no longer exists
	AgainBusy         = "original_busy" // its original is being moved, or needs the person's attention
)

// ConvertAgainCheck says whether done job id can be converted again (ErrNotFound: no such job). keepDays
// is the keep-originals setting now: KeptUntil is the date the purge deletes the kept original on.
func (s *Store) ConvertAgainCheck(id int64, keepDays int) (AgainCheck, error) {
	c, err := s.convertAgainCheck(id)
	if err != nil || c.Code == AgainNotDone {
		return c, err
	}
	j, err := s.Get(id)
	if err != nil {
		return AgainCheck{}, err
	}
	// what the run did, from its own record (the workflow may have changed since): a kept original, or
	// a new file at the original's own name, is a replace; a new file in the same folder is beside
	var state, keptAt string
	err = s.db.QueryRow(`SELECT state, COALESCE(kept_at, '') FROM originals WHERE job_id=? ORDER BY id DESC LIMIT 1`, id).Scan(&state, &keptAt)
	switch {
	case err == nil || j.OutputPath == j.Path:
		c.Policy = "replace"
		if due, ok := KeptUntil(keptAt, keepDays); ok && state == OriginalKept {
			c.KeptUntil = due.UTC().Format(time.RFC3339)
		}
	case !errors.Is(err, sql.ErrNoRows):
		return AgainCheck{}, err
	case filepath.Dir(j.OutputPath) == filepath.Dir(j.Path):
		c.Policy, c.OutputPath = "beside", j.OutputPath
	default:
		c.Policy, c.OutputPath = "separate_folder", j.OutputPath
	}
	return c, nil
}

func (s *Store) convertAgainCheck(id int64) (AgainCheck, error) {
	j, err := s.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return AgainCheck{}, ErrNotFound
	}
	if err != nil {
		return AgainCheck{}, err
	}
	if j.Status != Done {
		return AgainCheck{Code: AgainNotDone, Message: "Only a converted file can be converted again."}, nil
	}
	gone := AgainCheck{Code: AgainOriginalGone, Message: "Can't convert again: the original is no longer there."}
	o, err := scanOriginal(s.db.QueryRow(`SELECT `+originalCols+` FROM originals WHERE job_id=? ORDER BY id DESC LIMIT 1`, id))
	switch {
	case errors.Is(err, sql.ErrNoRows): // the original stayed where it was (a folder for new files, beside)
		if j.OutputPath == j.Path { // replaced with nothing kept: the original is gone
			return gone, nil
		}
		if _, err := os.Lstat(j.Path); err != nil {
			return gone, nil
		}
		return AgainCheck{Possible: true}, nil
	case err != nil:
		return AgainCheck{}, err
	}
	switch o.State {
	case OriginalKept:
		if _, err := os.Lstat(o.Trash); err != nil {
			return gone, nil
		}
		return AgainCheck{Possible: true, originalID: o.ID}, nil
	case OriginalPurged:
		// since #163 QA, a purge records when it happened; one purged before that, earlier than it was due
		// (the setting was lowered), shows its due date only, and says so
		if t, err := time.Parse(time.RFC3339, o.DeleteOn); err == nil {
			if t.After(time.Now()) {
				gone.Message = "Can't convert again: the original has been deleted. The date wasn't recorded." // design: the due date would mislead
			} else {
				gone.DeletedAt = o.DeleteOn
				gone.Message = "Can't convert again: the original was deleted on " + t.Local().Format("2 January 2006") + "."
			}
		}
		return gone, nil
	case OriginalRestored: // the original is back at its name: it runs from there
		if _, err := os.Lstat(j.Path); err != nil {
			return gone, nil
		}
		return AgainCheck{Possible: true}, nil
	}
	return AgainCheck{Code: AgainBusy, Message: "Can't convert again yet: this file's original is being moved, or needs your attention first."}, nil
}

// AgainError is a refused Convert again.
type AgainError struct{ AgainCheck }

func (e AgainError) Error() string { return e.Message }

// ConvertAgain puts done job id back in the queue as a fresh start, to run from its original (an
// AgainError if it can't; ErrNotFound if there is no such job).
func (s *Store) ConvertAgain(id int64) error {
	c, err := s.convertAgainCheck(id) // only Possible and the original are needed here
	if err != nil {
		return err
	}
	if !c.Possible {
		return AgainError{c}
	}
	r, err := s.db.Exec(`UPDATE jobs SET status=?, progress=0, error='', error_code='', worker='', attempts=0, retry_at='', fallback='', again_from=?, updated=? WHERE id=? AND status=?`,
		Queued, c.originalID, s.stamp(), id, Done)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 { // it changed meanwhile
		return AgainError{AgainCheck{Code: AgainNotDone, Message: "Only a converted file can be converted again."}}
	}
	return nil
}

// AgainFrom is the kept original (its record's id) job id runs from when converted again; 0: none.
func (s *Store) AgainFrom(id int64) int64 {
	var o int64
	s.db.QueryRow(`SELECT again_from FROM jobs WHERE id=?`, id).Scan(&o)
	return o
}

// Reconverted records that kept original id's file was converted again (Convert again): the new file
// (at newPath, "" for the original's name) replaced our earlier one, and the original is kept for its
// full days from now (deleteOn).
func (s *Store) Reconverted(id int64, newPath string, newBytes, newMTime int64, newHash string, deleteOn time.Time) error {
	return s.setOriginal(id, `new_path=?, new_bytes=?, new_mtime=?, new_hash=?, kept_at=?, delete_on=?`,
		newPath, newBytes, newMTime, newHash, s.stamp(), deleteOn.UTC().Format(time.RFC3339))
}
