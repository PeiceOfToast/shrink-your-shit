// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"time"
)

// An Original is a converted file's original, kept in the .syp-trash folder beside it (slice 17,
// QOL-SAFETY-PLAN.md S2). Its row is also the swap's journal: it is written, and committed, before
// either rename, so a restart can always tell what was in the middle of happening (RecoverSwaps).
type Original struct {
	ID        int64
	JobID     int64
	LibraryID int64  // 0: the job had none
	Path      string // where the original was; the new file is there once the swap is done
	Trash     string // where the original is kept
	Staged    string // the checked new file, until it is moved to Path
	Bytes     int64  // the original's size
	ModTime   int64  // the original's modification time (UnixNano)
	Hash      string // the original's sampled hash (worker.SampleHash): slice 18 checks a restore with it
	NewBytes  int64
	State     string
	Started   string
	KeptAt    string // when the swap was done
	DeleteOn  string // when it may be purged (slice 18)
}

// Original states.
const (
	SwapMoving     = "moving"          // journal written; the renames may be partly done
	OriginalKept   = "kept"            // the original is in Trash, the new file at Path
	SwapRolledBack = "rolled_back"     // a restart put the original back; the job runs again
	SwapAttention  = "needs_attention" // neither file could be found where expected; nothing was moved
)

// BeginSwap writes the journal row for a swap about to start (state moving) and returns its id.
func (s *Store) BeginSwap(o Original) (int64, error) {
	r, err := s.db.Exec(`INSERT INTO originals(job_id,library_id,path,trash,staged,original_bytes,original_mtime,original_hash,new_bytes,state,started)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`, o.JobID, nullID(o.LibraryID), o.Path, o.Trash, o.Staged, o.Bytes, o.ModTime, o.Hash, o.NewBytes, SwapMoving, s.stamp())
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

// FinishSwap marks a swap done: the original is kept until deleteOn.
func (s *Store) FinishSwap(id int64, deleteOn time.Time) error {
	return s.setOriginal(id, `state=?, kept_at=?, delete_on=?`, OriginalKept, s.stamp(), deleteOn.UTC().Format(time.RFC3339))
}

// SetSwapState records how a restart ended an unfinished swap (rolled_back or needs_attention).
func (s *Store) SetSwapState(id int64, state string) error {
	return s.setOriginal(id, `state=?`, state)
}

func (s *Store) setOriginal(id int64, set string, args ...any) error {
	res, err := s.db.Exec(`UPDATE originals SET `+set+` WHERE id=?`, append(args, id)...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

const originalCols = `id,job_id,COALESCE(library_id,0),path,trash,staged,original_bytes,original_mtime,original_hash,new_bytes,state,started,COALESCE(kept_at,''),COALESCE(delete_on,'')`

func scanOriginal(r interface{ Scan(...any) error }) (Original, error) {
	var o Original
	err := r.Scan(&o.ID, &o.JobID, &o.LibraryID, &o.Path, &o.Trash, &o.Staged, &o.Bytes, &o.ModTime, &o.Hash, &o.NewBytes, &o.State, &o.Started, &o.KeptAt, &o.DeleteOn)
	return o, err
}

// Originals returns the rows in state, oldest first.
func (s *Store) Originals(state string) ([]Original, error) {
	rows, err := s.db.Query(`SELECT `+originalCols+` FROM originals WHERE state=? ORDER BY id`, state)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Original{}
	for rows.Next() {
		o, err := scanOriginal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// OriginalOfJob returns the kept original of a job (ErrNotFound if it has none).
func (s *Store) OriginalOfJob(jobID int64) (Original, error) {
	o, err := scanOriginal(s.db.QueryRow(`SELECT `+originalCols+` FROM originals WHERE job_id=? AND state=? ORDER BY id DESC LIMIT 1`, jobID, OriginalKept))
	if errors.Is(err, sql.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// CompleteKeptJobs marks done every job still "running" whose swap was finished (its original is kept,
// the new file in place): the process stopped between the swap and recording the job. Without this
// the job would be queued again and the new file converted a second time.
func (s *Store) CompleteKeptJobs() (int64, error) {
	r, err := s.db.Exec(`UPDATE jobs SET status=?, progress=100, updated=?,
  output_path=(SELECT o.path FROM originals o WHERE o.job_id=jobs.id AND o.state=? ORDER BY o.id DESC LIMIT 1),
  size_after=(SELECT o.new_bytes FROM originals o WHERE o.job_id=jobs.id AND o.state=? ORDER BY o.id DESC LIMIT 1)
WHERE status=? AND EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id AND o.state=?)`,
		Done, s.stamp(), OriginalKept, OriginalKept, Running, OriginalKept)
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

// keptSQL is true for a jobs row whose original is kept (the state is the next argument).
const keptSQL = `EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id AND o.state=?)`

// DoneItem is a finished conversion, as the Done list shows it.
type DoneItem struct {
	JobID       int64
	Library     string // the library's name, or its folder for jobs from before libraries
	Path        string
	BeforeBytes int64
	AfterBytes  int64
	Beside      bool  // the new file was written beside the original, which was never touched
	KeptID      int64 // 0: the original is not in .syp-trash
	DeleteOn    string
}

// DoneItems lists done jobs, newest first, with what happened to their original; and how many there are.
func (s *Store) DoneItems(limit, offset int) ([]DoneItem, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status=?`, Done).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT j.id, COALESCE(l.name, j.library), j.path, j.size_before, j.size_after, j.output_path<>j.path,
  COALESCE(o.id,0), COALESCE(o.delete_on,'')
FROM jobs j LEFT JOIN libraries l ON l.id=j.library_id
  LEFT JOIN originals o ON o.id=(SELECT MAX(id) FROM originals WHERE job_id=j.id AND state=?)
WHERE j.status=? ORDER BY j.id DESC LIMIT ? OFFSET ?`, OriginalKept, Done, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []DoneItem{}
	for rows.Next() {
		var d DoneItem
		if err := rows.Scan(&d.JobID, &d.Library, &d.Path, &d.BeforeBytes, &d.AfterBytes, &d.Beside, &d.KeptID, &d.DeleteOn); err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// KeptItem is an original kept in .syp-trash, as the Kept originals list shows it.
type KeptItem struct {
	Original
	Library string
}

// KeptItems lists the kept originals, the soonest to be deleted first.
func (s *Store) KeptItems() ([]KeptItem, error) {
	rows, err := s.db.Query(`SELECT o.id,o.job_id,COALESCE(o.library_id,0),o.path,o.trash,o.staged,o.original_bytes,o.original_mtime,o.original_hash,o.new_bytes,o.state,o.started,COALESCE(o.kept_at,''),COALESCE(o.delete_on,''),
  COALESCE(l.name, j.library, '')
FROM originals o LEFT JOIN jobs j ON j.id=o.job_id LEFT JOIN libraries l ON l.id=o.library_id
WHERE o.state=? ORDER BY o.delete_on, o.id`, OriginalKept)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KeptItem{}
	for rows.Next() {
		var k KeptItem
		o := &k.Original
		if err := rows.Scan(&o.ID, &o.JobID, &o.LibraryID, &o.Path, &o.Trash, &o.Staged, &o.Bytes, &o.ModTime, &o.Hash, &o.NewBytes, &o.State, &o.Started, &o.KeptAt, &o.DeleteOn, &k.Library); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}
