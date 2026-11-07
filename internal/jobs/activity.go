// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
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
	err = s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE reason<>''`).Scan(&a.Skipped)
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
	rows, err := s.db.Query(`SELECT COALESCE(l.name, j.library), COUNT(*) FROM jobs j LEFT JOIN libraries l ON l.id=j.library_id
WHERE j.status=? GROUP BY COALESCE(l.name, j.library) ORDER BY MIN(j.id)`, Queued)
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

// Skipped is a file the scanner decided not to convert, with the row id the Skipped list uses.
type Skipped struct {
	ID int64
	File
}

// SkippedFiles lists the skipped files (reason = "" lists every reason), by path; and how many there are.
func (s *Store) SkippedFiles(reason string, limit, offset int) ([]Skipped, int, error) {
	where, args := `reason<>''`, []any{}
	if reason != "" {
		where, args = `reason=?`, []any{reason}
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
