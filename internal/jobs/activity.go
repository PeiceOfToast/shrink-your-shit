// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"os"
	"sync"
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
// SkippedPath is the path of skipped record id (ErrNotFound if there is none).
func (s *Store) SkippedPath(id int64) (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT path FROM files WHERE rowid=? AND reason<>''`, id).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return path, err
}

// RenamedTo reports where a finished conversion moved the file at path when it changed its type in
// replace mode (25a-2): the converted file's name.
func (s *Store) RenamedTo(path string) (string, bool) {
	var to string
	err := s.db.QueryRow(`SELECT o.new_path FROM originals o JOIN jobs j ON j.id=o.job_id
WHERE o.path=? AND o.new_path<>'' AND o.state IN (?,?) AND j.status=? ORDER BY o.id DESC LIMIT 1`, path, OriginalKept, OriginalPurged, Done).Scan(&to)
	return to, err == nil
}

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

// StopByPerson is jobs.stop_requested for "Stop this file": the job ends as skipped, user_stopped.
const StopByPerson = 1

// StopRequested reports whether "Stop this file" was asked for job id (false if it has no record).
func (s *Store) StopRequested(id int64) (bool, error) {
	var v int
	err := s.db.QueryRow(`SELECT stop_requested FROM jobs WHERE id=?`, id).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return v != 0, err
}

// RunningJobsOf lists the running jobs of library id.
func (s *Store) RunningJobsOf(id int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM jobs WHERE library_id=? AND status=?`, id, Running)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var j int64
		if err := rows.Scan(&j); err != nil {
			return nil, err
		}
		ids = append(ids, j)
	}
	return ids, rows.Err()
}

// BeginRemove starts removing library id (DELETE /api/libraries): until release is called, no job of it
// is claimed and it cannot be started. Nothing is written, so nothing needs undoing or recovering: a
// failed or given-up removal just calls release. ErrLibraryBusy if a removal of it is under way already.
func (s *Store) BeginRemove(id int64) (release func(), err error) {
	if _, err := s.GetLibrary(id); err != nil {
		return nil, err
	}
	s.removeMu.Lock()
	defer s.removeMu.Unlock()
	if s.removing[id] {
		return nil, ErrLibraryBusy
	}
	if s.removing == nil {
		s.removing = map[int64]bool{}
	}
	s.removing[id] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			s.removeMu.Lock()
			delete(s.removing, id)
			s.removeMu.Unlock()
		})
	}, nil
}

// ErrLibraryBusy: a job of the library still runs, so it cannot be removed yet.
var ErrLibraryBusy = errors.New("a file in this library is still being converted")

// finishRequestedStops ends, as stopped, the jobs whose stop was requested but whose worker is gone (the
// program stopped first): run at startup, before the queue runs.
func (s *Store) finishRequestedStops() error {
	rows, err := s.db.Query(`SELECT id FROM jobs WHERE stop_requested!=0 AND status IN (?,?)`, Queued, Running)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := s.UserStopped(id); err != nil {
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
