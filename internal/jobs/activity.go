// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"os"
)

// Activity's figures and lists (slice 21b, UI-API-CONTRACT.md "Activity"). They only read.

// Summary is the Home page's figures (contract F1: freed counts originals already gone, waiting those
// still kept; freed + waiting = all saved).
type Summary struct {
	FreedBytes, WaitingBytes   int64
	WorkingFiles, WaitingFiles int
	Skipped, Failed            int
}

// ActivitySummary adds up every library (and jobs from before libraries).
func (s *Store) ActivitySummary() (Summary, error) {
	var a Summary
	err := s.db.QueryRow(`SELECT
  COALESCE(SUM(CASE WHEN status=? AND size_after>0 AND output_path=path AND NOT `+keptSQL+` THEN size_before-size_after END),0),
  COALESCE(SUM(CASE WHEN status=? AND size_after>0 AND output_path=path AND `+keptSQL+` THEN size_before-size_after END),0),
  COALESCE(SUM(status=?),0), COALESCE(SUM(status=?),0), COALESCE(SUM(status IN (?,?)),0)
FROM jobs`, Done, OriginalKept, Done, OriginalKept, Running, Queued, Failed, Kept).Scan(&a.FreedBytes, &a.WaitingBytes, &a.WorkingFiles, &a.WaitingFiles, &a.Failed)
	if err != nil {
		return a, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE reason<>'' AND NOT ` + convertedSQL).Scan(&a.Skipped)
	return a, err
}

// Working is a running job with its library's name and when it started.
type Working struct {
	Job
	LibraryName string
	Started     string
}

// WorkingJobs lists the running jobs, oldest first.
func (s *Store) WorkingJobs() ([]Working, error) {
	rows, err := s.db.Query(`SELECT `+prefixedCols+`, COALESCE(l.name, j.library), COALESCE(j.started,'')
FROM jobs j LEFT JOIN libraries l ON l.id=j.library_id WHERE j.status=? ORDER BY j.id`, Running)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Working{}
	for rows.Next() {
		var w Working
		j := &w.Job
		if err := rows.Scan(&j.ID, &j.Library, &j.LibraryID, &j.Path, &j.Status, &j.Progress, &j.Reason, &j.Error, &j.Worker,
			&j.DurationSec, &j.SizeBefore, &j.SizeAfter, &j.OutputPath, &j.Plan, &j.PlanPrint, &j.Created, &j.Updated, &w.LibraryName, &w.Started); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// prefixedCols is cols for the jobs table under the alias j.
const prefixedCols = `j.id,j.library,COALESCE(j.library_id,0),j.path,j.status,j.progress,j.reason,j.error,j.worker,j.duration,j.size_before,j.size_after,j.output_path,COALESCE(j.plan,''),COALESCE(j.plan_fingerprint,''),j.created,j.updated`

// LibraryQueue is how many files of a library wait.
type LibraryQueue struct {
	Library string
	Count   int
}

// UpNext counts the queued jobs per library, in queue order of their first job.
func (s *Store) UpNext() ([]LibraryQueue, error) {
	// by library id (names need not be unique); jobs from before libraries by their folder
	rows, err := s.db.Query(`SELECT COALESCE(MAX(l.name), j.library), COUNT(*) FROM jobs j LEFT JOIN libraries l ON l.id=j.library_id
WHERE j.status=? GROUP BY COALESCE(j.library_id, 0), CASE WHEN j.library_id IS NULL THEN j.library END ORDER BY MIN(j.id)`, Queued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LibraryQueue{}
	for rows.Next() {
		var q LibraryQueue
		if err := rows.Scan(&q.Library, &q.Count); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// FailedJobs lists failed jobs and jobs whose original was kept by a check (status kept), newest
// first, with their library's name; and how many there are.
func (s *Store) FailedJobs(limit, offset int) ([]Working, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status IN (?,?)`, Failed, Kept).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT `+prefixedCols+`, COALESCE(l.name, j.library), ''
FROM jobs j LEFT JOIN libraries l ON l.id=j.library_id WHERE j.status IN (?,?) ORDER BY j.id DESC LIMIT ? OFFSET ?`, Failed, Kept, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Working{}
	for rows.Next() {
		var w Working
		j := &w.Job
		if err := rows.Scan(&j.ID, &j.Library, &j.LibraryID, &j.Path, &j.Status, &j.Progress, &j.Reason, &j.Error, &j.Worker,
			&j.DurationSec, &j.SizeBefore, &j.SizeAfter, &j.OutputPath, &j.Plan, &j.PlanPrint, &j.Created, &j.Updated, &w.LibraryName, &w.Started); err != nil {
			return nil, 0, err
		}
		out = append(out, w)
	}
	return out, total, rows.Err()
}

// convertedSQL is true for a files row that is the output of a done conversion: the scanner skips it
// afterwards (already the new format), but it is not "skipped" for the person, it is done.
// It is that output only while it is the file the job wrote: the size and modification time recorded
// when the job was done (jobs.output_mtime). 0 means unknown (not readable then, or not recoverable for
// a job done before it was recorded): such a job hides nothing, so a skip shows rather than wrongly
// hiding. A different file put at that path later is listed. A file whose original was put back
// (slice 18) is skipped as restored, even though a done job wrote it once.
const convertedSQL = `(files.reason<>'restored' AND EXISTS (SELECT 1 FROM jobs WHERE jobs.status='done' AND jobs.output_path=files.path AND jobs.size_after=files.size
  AND jobs.output_mtime<>0 AND jobs.output_mtime=files.mtime))`

// Skipped is a file the scanner decided not to convert, with the row id the Skipped list uses.
type Skipped struct {
	ID int64
	File
}

// SkippedFiles lists the skipped files (reason = "" lists every reason), by path; and how many there are.
func (s *Store) SkippedFiles(reason string, limit, offset int) ([]Skipped, int, error) {
	where, args := `reason<>'' AND NOT `+convertedSQL, []any{}
	if reason != "" {
		where, args = `reason=? AND NOT `+convertedSQL, []any{reason}
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE `+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.Query(`SELECT rowid,path,size,mtime,COALESCE(codec,''),COALESCE(decision,''),reason FROM files WHERE `+where+` ORDER BY path LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Skipped{}
	for rows.Next() {
		var k Skipped
		if err := rows.Scan(&k.ID, &k.Path, &k.Size, &k.ModTime, &k.Codec, &k.Decision, &k.Reason); err != nil {
			return nil, 0, err
		}
		out = append(out, k)
	}
	return out, total, rows.Err()
}

// ForgetSkipped removes a skipped file's record ("Convert again"): the next scan looks at it afresh.
// ErrNotFound if there is no such skipped file.
func (s *Store) ForgetSkipped(id int64) (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT path FROM files WHERE rowid=? AND reason<>''`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`DELETE FROM files WHERE rowid=?`, id)
	return path, err
}

// Pause all (slice 24): a flag that survives a restart; workers take no job while it is set.

// Paused reports whether converting is paused.
func (s *Store) Paused() bool {
	v, _, _ := s.Meta("paused")
	return v == "1"
}

// SetPaused sets or clears the pause.
func (s *Store) SetPaused(on bool) error {
	v := "0"
	if on {
		v = "1"
	}
	return s.SetMeta("paused", v)
}

// RequestStop is "Stop this file" from the page (DESIGN decision 7), in one transaction, so no claim
// falls between the look and the change. A waiting job stops at once: UserStopped. A running job is only
// marked (stop_requested; running is true): its worker ends it as stopped, or, if the new file was
// already being put in place, finishes it. Its record is never removed while a worker may still finish.
func (s *Store) RequestStop(id int64) (running bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var path, status string
	err = tx.QueryRow(`SELECT path, status FROM jobs WHERE id=?`, id).Scan(&path, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	switch Status(status) {
	case Queued:
		if err := putFile(tx, userStoppedFile(path)); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`DELETE FROM jobs WHERE id=?`, id); err != nil {
			return false, err
		}
	case Running:
		if _, err := tx.Exec(`UPDATE jobs SET stop_requested=? WHERE id=? AND stop_requested=0`, StopByPerson, id); err != nil {
			return false, err
		}
		running = true
	default:
		return false, ErrNotStoppable
	}
	return running, tx.Commit()
}

// Why a job's stop was requested (jobs.stop_requested; 0: it was not).
const (
	StopByPerson       = 1 // "Stop this file": it ends as skipped, user_stopped
	StopLibraryRemoved = 2 // its library is being removed: its record is dropped
)

// StopRequested is why a stop was asked for job id (StopByPerson, StopLibraryRemoved), 0 if none or it
// has no record.
func (s *Store) StopRequested(id int64) (int, error) {
	var v int
	err := s.db.QueryRow(`SELECT stop_requested FROM jobs WHERE id=?`, id).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// MarkLibraryStop is the first step of removing library id (UI-API-CONTRACT.md, DELETE): every job of
// it that waits or runs is marked, so none is claimed any more and a running one stops at its last
// check before the swap (one already inside its swap finishes). It returns the running ones.
func (s *Store) MarkLibraryStop(id int64) ([]int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM libraries WHERE id=?`, id).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	if _, err := tx.Exec(`UPDATE jobs SET stop_requested=? WHERE library_id=? AND status IN (?,?) AND stop_requested=0`,
		StopLibraryRemoved, id, Queued, Running); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT id FROM jobs WHERE library_id=? AND status=?`, id, Running)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var j int64
		if err := rows.Scan(&j); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, j)
	}
	rows.Close()
	return ids, tx.Commit()
}

// LibraryRunning is how many jobs of library id are running.
func (s *Store) LibraryRunning(id int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE library_id=? AND status=?`, id, Running).Scan(&n)
	return n, err
}

// ClearLibraryStop undoes MarkLibraryStop when the library is not removed after all.
func (s *Store) ClearLibraryStop(id int64) error {
	_, err := s.db.Exec(`UPDATE jobs SET stop_requested=0 WHERE library_id=? AND stop_requested=?`, id, StopLibraryRemoved)
	return err
}

// finishRequestedStops ends, as stopped, the jobs whose stop was requested but whose worker is gone (the
// program stopped first): run at startup, before the queue runs.
func (s *Store) finishRequestedStops() error {
	rows, err := s.db.Query(`SELECT id, stop_requested FROM jobs WHERE stop_requested!=0 AND status IN (?,?)`, Queued, Running)
	if err != nil {
		return err
	}
	why := map[int64]int{}
	for rows.Next() {
		var id int64
		var v int
		if err := rows.Scan(&id, &v); err != nil {
			rows.Close()
			return err
		}
		why[id] = v
	}
	rows.Close()
	for id, v := range why {
		var err error
		if v == StopLibraryRemoved {
			err = s.Drop(id)
		} else {
			err = s.UserStopped(id)
		}
		if err != nil {
			return err
		}
	}
	return rows.Err()
}

func userStoppedFile(path string) File {
	f := File{Path: path, Decision: "skip: you stopped it", Reason: UserStoppedReason}
	if st, err := os.Stat(path); err == nil {
		f.Size, f.ModTime = st.Size(), st.ModTime().UnixNano()
	}
	return f
}

// UserStopped ends job id as stopped by the person, by its worker (or at startup): the job record goes,
// and the file is recorded as skipped with reason user_stopped, which the scanner honours until Convert
// again. Only a queued or running job can be stopped (ErrNotStoppable otherwise).
func (s *Store) UserStopped(id int64) error {
	j, err := s.Get(id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if j.Status != Queued && j.Status != Running {
		return ErrNotStoppable
	}
	if err := s.PutFile(userStoppedFile(j.Path)); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	return err
}

// UserStoppedReason is the skip reason of a file the person stopped.
const UserStoppedReason = "user_stopped"

// ErrNotStoppable: the job is not queued or running.
var ErrNotStoppable = errors.New("this file is not waiting or converting")
