// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// revOfFlowSQL is the id of a flow's newest published workflow revision (0: none yet) for the flow id in %s.
// It is the revision a job queued now runs with, and what a job records as workflow_rev.
const revOfFlowSQL = `COALESCE((SELECT r.id FROM workflow_revisions r WHERE r.workflow_id=%s AND r.status='published' ORDER BY r.number DESC LIMIT 1),0)`

var (
	queuedRevSQL = fmt.Sprintf(revOfFlowSQL, "libraries.flow_id")                                        // in an INSERT ... SELECT FROM libraries
	jobRevSQL    = fmt.Sprintf(revOfFlowSQL, "(SELECT flow_id FROM libraries WHERE id=jobs.library_id)") // the job's own library's
)

// currentRevision is library id's current workflow revision (0: none yet).
func (s *Store) currentRevision(id int64) (int64, error) {
	var cur int64
	err := s.db.QueryRow(`SELECT `+fmt.Sprintf(revOfFlowSQL, "(SELECT flow_id FROM libraries WHERE id=?)"), id).Scan(&cur)
	return cur, err
}

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
	AgainNotDone      = "not_done"         // only a converted file can be converted again
	AgainOriginalGone = "original_gone"    // its original no longer exists
	AgainBusy         = "original_busy"    // its original is being moved, or needs the person's attention
	AgainCurrent      = "current_workflow" // it was converted with the library's current workflow already
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
	var ran, cur int64
	if err := s.db.QueryRow(`SELECT workflow_rev, `+jobRevSQL+` FROM jobs WHERE id=?`, id).Scan(&ran, &cur); err != nil {
		return AgainCheck{}, err
	}
	if cur > 0 && ran == cur { // the library's workflow hasn't changed since: nothing to gain
		return AgainCheck{Code: AgainCurrent, Message: "Already converted with this workflow."}, nil
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
	// a fresh run: it runs with the library's workflow as it is now, so it records that revision
	n, err := s.moveAlong(s.db, Done, Queued,
		`, progress=0, error='', error_code='', worker='', attempts=0, retry_at='', fallback='', again_from=?, workflow_rev=`+jobRevSQL,
		[]any{c.originalID}, `id=?`, id)
	if err != nil {
		return err
	}
	if n == 0 { // it changed meanwhile
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

// AgainLibrary is a library's Convert again for the files converted with an earlier workflow: Earlier counts
// them, Possible those that can run again, and NotPossible the rest grouped by code (first one's words).
type AgainLibrary struct {
	Earlier, Possible int
	NotPossible       []AgainGroup
	ids               []int64 // the possible ones, in id order
}

// AgainGroup is the files refused for one code.
type AgainGroup struct {
	Code, Message string
	Count         int
}

// addRefused counts one more file refused for code, keeping the groups in the order they first came.
func addRefused(gs []AgainGroup, code, msg string) []AgainGroup {
	for i := range gs {
		if gs[i].Code == code {
			gs[i].Count++
			return gs
		}
	}
	return append(gs, AgainGroup{Code: code, Message: msg, Count: 1})
}

// LibraryAgain is library id's earlier files and what Convert again can do for them (ErrNotFound: no such
// library). A file is earlier when its newest converted job ran a workflow other than the library's current one.
// Only files inside the library's current folder count: after its folder is changed, the jobs under the old
// folder are not this library's files any more, so they are never counted or queued.
func (s *Store) LibraryAgain(id int64, keepDays int) (AgainLibrary, error) {
	lib, err := s.GetLibrary(id)
	if err != nil {
		return AgainLibrary{}, err
	}
	cur, err := s.currentRevision(id)
	if err != nil || cur == 0 { // no revision yet: nothing ran with an earlier one
		return AgainLibrary{}, err
	}
	rows, err := s.db.Query(`SELECT id, path FROM jobs WHERE library_id=? AND `+convertedNow("jobs")+` AND `+newestSQL+` AND workflow_rev<>? ORDER BY id`, id, cur)
	if err != nil {
		return AgainLibrary{}, err
	}
	var jobIDs []int64
	for rows.Next() {
		var jid int64
		var path string
		if err := rows.Scan(&jid, &path); err != nil {
			rows.Close()
			return AgainLibrary{}, err
		}
		if Within(path, lib.Path) {
			jobIDs = append(jobIDs, jid)
		}
	}
	rows.Close()
	var a AgainLibrary
	for _, jid := range jobIDs {
		c, err := s.ConvertAgainCheck(jid, keepDays)
		if err != nil {
			return AgainLibrary{}, err
		}
		a.Earlier++
		if c.Possible {
			a.Possible++
			a.ids = append(a.ids, jid)
		} else {
			a.NotPossible = addRefused(a.NotPossible, c.Code, c.Message)
		}
	}
	return a, nil
}

// ConvertLibraryAgain queues Convert again for every possible earlier file of library id (the same queue as
// ConvertAgain) and says how many it queued and which it refused. Once queued a file is no longer converted, so
// a second call queues nothing new.
func (s *Store) ConvertLibraryAgain(id int64, keepDays int) (queued int, notPossible []AgainGroup, err error) {
	a, err := s.LibraryAgain(id, keepDays)
	if err != nil {
		return 0, nil, err
	}
	notPossible = a.NotPossible
	for _, jid := range a.ids {
		err := s.ConvertAgain(jid)
		var ae AgainError
		switch {
		case errors.As(err, &ae): // it changed since the summary: refused, as the per-file action would
			notPossible = addRefused(notPossible, ae.Code, ae.Message)
		case err != nil:
			return queued, notPossible, err
		default:
			queued++
		}
	}
	if notPossible == nil {
		notPossible = []AgainGroup{}
	}
	return queued, notPossible, nil
}
