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

// UserStopped turns job id into a file the person stopped ("Stop this file", DESIGN decision 7): the
// job record goes, and the file is recorded as skipped with reason user_stopped, which the scanner
// honours until Convert again. Only a queued or running job can be stopped (ErrNotStoppable otherwise).
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
	f := File{Path: j.Path, Decision: "skip: you stopped it", Reason: UserStoppedReason}
	if st, err := os.Stat(j.Path); err == nil {
		f.Size, f.ModTime = st.Size(), st.ModTime().UnixNano()
	}
	if err := s.PutFile(f); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM jobs WHERE id=?`, id)
	return err
}

// UserStoppedReason is the skip reason of a file the person stopped.
const UserStoppedReason = "user_stopped"

// ErrNotStoppable: the job is not queued or running.
var ErrNotStoppable = errors.New("this file is not waiting or converting")
