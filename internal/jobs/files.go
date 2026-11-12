// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"strings"
)

// The Files view (S5, UI-API-CONTRACT.md "Workshop addition 21:00Z"): one list of files by card.
const (
	CardUnprocessed = "unprocessed" // waiting, or waiting to be tried again (DD-2)
	CardProcessing  = "processing"  // converting now
	CardProcessed   = "processed"   // done
	CardSkipped     = "skipped"     // left alone, with a reason
	CardFailed      = "failed"      // failed, or a check kept the original
)

// Cards are the cards in the order the page shows them.
var Cards = []string{CardUnprocessed, CardProcessing, CardProcessed, CardSkipped, CardFailed}

// FileFilter says which files to list: a card, and optionally a library, a name to look for (in the
// file's name, or its new name after a rename) and, on processed, only those whose original is kept.
type FileFilter struct {
	Show      string
	LibraryID int64
	Q         string
	Reasons   []string // skipped: only these reasons (none: every reason)
	Kept      bool
	// QueueOrder: waiting jobs in the order workers take them (Claim's: priority, then age), ready ones
	// first, then those waiting to retry (soonest first) or being stopped.
	QueueOrder    bool
	Limit, Offset int
}

// FileRow is one file of the list. A job card's rows have JobID; a skipped row has SkipID, and JobID
// when its worker skipped it (Store.Skip).
type FileRow struct {
	JobID, SkipID         int64
	LibraryID             int64
	Library               string
	Path, NewPath         string
	Status                Status
	Error                 string
	ErrorCode             string // Error's code (migration 23: jobs.error_code)
	Priority              int    // the job's place-in-queue priority (higher first)
	Attempts              int
	RetryAt               string
	SizeBefore, SizeAfter int64
	DurationSec           float64
	Updated               string
	Reason, Decision      string // skipped
	FlowID                int64
	FlowName              string
	OriginalID            int64  // the job's newest original record (0: none)
	OriginalState         string // OriginalKept, OriginalRestored, OriginalPurged, or ""
	OriginalDeleteOn      string
	OriginalKeptAt        string
	OutputPath            string
	Beside                bool // kept beside (the output is another file next to it)
}

// nameOf is SQL for the lower-case file name of a path column (no basename function in SQLite: the
// path less everything up to its last slash; Windows backslashes count as slashes).
func nameOf(col string) string {
	p := "replace(" + col + ",'\\','/')"
	return "lower(replace(" + p + ", rtrim(" + p + ", replace(" + p + ",'/','')), ''))"
}

// jobWhere is each job card's condition.
var jobWhere = map[string]string{
	CardUnprocessed: "j.status='queued'",
	CardProcessing:  "j.status='running'",
	CardProcessed:   "j.status='done'",
	CardFailed:      "j.status IN ('failed','kept')",
}

var jobOrder = map[string]string{
	CardUnprocessed: "(j.retry_at<>'') DESC, j.retry_at, j.id", // retrying first, then queue order
	CardProcessing:  "COALESCE(j.started,''), j.id",            // by start (review 43e6f66: updated moves with progress)
	CardProcessed:   "j.updated DESC, j.id DESC",               // newest first
	CardFailed:      "j.updated DESC, j.id DESC",
}

const jobFrom = `FROM jobs j LEFT JOIN libraries l ON l.id=j.library_id LEFT JOIN flows fl ON fl.id=l.flow_id
  LEFT JOIN originals o ON o.id=(SELECT MAX(id) FROM originals WHERE job_id=j.id)`

// filters is the condition and arguments for f's library and name (both cards' kinds).
func (f FileFilter) jobFilters() (string, []any) {
	where, args := "", []any{}
	if f.LibraryID > 0 {
		where += " AND j.library_id=?"
		args = append(args, f.LibraryID)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		where += " AND (instr(" + nameOf("j.path") + ", lower(?))>0 OR instr(" + nameOf("COALESCE(o.new_path,'')") + ", lower(?))>0)"
		args = append(args, q, q)
	}
	return where, args
}

func (f FileFilter) skipFilters() (string, []any) {
	where, args := `files.reason<>'' AND NOT `+convertedSQL, []any{}
	if len(f.Reasons) > 0 {
		where += ` AND files.reason IN (?` + strings.Repeat(",?", len(f.Reasons)-1) + `)`
		for _, r := range f.Reasons {
			args = append(args, r)
		}
	}
	if f.LibraryID > 0 { // inside the library's folder, whatever the separator (Windows: review 43e6f66)
		where += ` AND EXISTS (SELECT 1 FROM libraries l WHERE l.id=? AND
  substr(replace(files.path,'\','/'),1,length(rtrim(replace(l.path,'\','/'),'/'))+1)=rtrim(replace(l.path,'\','/'),'/')||'/')`
		args = append(args, f.LibraryID)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		where += " AND instr(" + nameOf("files.path") + ", lower(?))>0"
		args = append(args, q)
	}
	return where, args
}

// FileList lists one card's files (f.Show), a page of them, with the card's total and every card's
// count under the same library and name filters (keptOriginals: processed files whose original is
// still kept).
func (s *Store) FileList(f FileFilter) (rows []FileRow, total int, counts map[string]int, err error) {
	counts = map[string]int{}
	jw, ja := f.jobFilters()
	for card, cond := range jobWhere {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) `+jobFrom+` WHERE `+cond+jw, ja...).Scan(&n); err != nil {
			return nil, 0, nil, err
		}
		counts[card] = n
	}
	var kept int
	if err := s.db.QueryRow(`SELECT COUNT(*) `+jobFrom+` WHERE j.status='done' AND o.state=?`+jw, append([]any{OriginalKept}, ja...)...).Scan(&kept); err != nil {
		return nil, 0, nil, err
	}
	counts["keptOriginals"] = kept
	sw, sa := f.skipFilters()
	var skipped int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE `+sw, sa...).Scan(&skipped); err != nil {
		return nil, 0, nil, err
	}
	counts[CardSkipped] = skipped

	rows = []FileRow{}
	switch {
	case f.Show == CardSkipped:
		total = skipped
		r, err := s.db.Query(`SELECT files.rowid, files.path, files.size, COALESCE(files.decision,''), files.reason, files.job_id FROM files WHERE `+sw+` ORDER BY files.path LIMIT ? OFFSET ?`,
			append(sa, f.Limit, f.Offset)...)
		if err != nil {
			return nil, 0, nil, err
		}
		defer r.Close()
		for r.Next() {
			var row FileRow
			if err := r.Scan(&row.SkipID, &row.Path, &row.SizeBefore, &row.Decision, &row.Reason, &row.JobID); err != nil {
				return nil, 0, nil, err
			}
			rows = append(rows, row)
		}
		return rows, total, counts, r.Err()
	case jobWhere[f.Show] != "":
		cond, args := jobWhere[f.Show]+jw, ja
		total = counts[f.Show]
		if f.Show == CardProcessed && f.Kept {
			cond += " AND o.state=?"
			args = append(append([]any{}, ja...), OriginalKept)
			total = kept
		}
		order, orderArgs := jobOrder[f.Show], []any{}
		if f.QueueOrder && f.Show == CardUnprocessed {
			order, orderArgs = `CASE WHEN `+notReadySQL+` THEN 1 ELSE 0 END, CASE WHEN `+notReadySQL+` THEN j.retry_at END, j.priority DESC, j.id`, []any{s.stamp(), s.stamp()}
		}
		r, err := s.db.Query(`SELECT j.id, COALESCE(j.library_id,0), COALESCE(l.name, j.library), j.path, COALESCE(o.new_path,''), j.status,
  COALESCE(j.error,''), COALESCE(j.error_code,''), j.priority, j.attempts, COALESCE(j.retry_at,''), j.size_before, COALESCE(j.size_after,0), j.duration, j.updated,
  COALESCE(fl.id,0), COALESCE(fl.name,''), COALESCE(o.id,0), COALESCE(o.state,''), COALESCE(o.delete_on,''), COALESCE(o.kept_at,''), j.output_path,
  j.output_path<>'' AND j.output_path<>j.path AND o.id IS NULL `+jobFrom+` WHERE `+cond+` ORDER BY `+order+` LIMIT ? OFFSET ?`,
			append(append(args, orderArgs...), f.Limit, f.Offset)...)
		if err != nil {
			return nil, 0, nil, err
		}
		defer r.Close()
		for r.Next() {
			var row FileRow
			if err := r.Scan(&row.JobID, &row.LibraryID, &row.Library, &row.Path, &row.NewPath, &row.Status, &row.Error, &row.ErrorCode, &row.Priority, &row.Attempts,
				&row.RetryAt, &row.SizeBefore, &row.SizeAfter, &row.DurationSec, &row.Updated, &row.FlowID, &row.FlowName,
				&row.OriginalID, &row.OriginalState, &row.OriginalDeleteOn, &row.OriginalKeptAt, &row.OutputPath, &row.Beside); err != nil {
				return nil, 0, nil, err
			}
			rows = append(rows, row)
		}
		return rows, total, counts, r.Err()
	}
	return nil, 0, nil, fmt.Errorf("show must be one of %s", strings.Join(Cards, ", "))
}

// KeptBeside reports whether done job id wrote its new file next to the original (Beside), which it kept.
func (s *Store) KeptBeside(id int64) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM jobs j WHERE j.id=? AND j.status='done' AND j.output_path<>'' AND j.output_path<>j.path
  AND NOT EXISTS (SELECT 1 FROM originals WHERE job_id=j.id)`, id).Scan(&n)
	return n > 0
}

// FlowShapes are the distinct video shapes (ladder row, HDR, 50-60 fps) of the probed files in the
// libraries using flow flowID: what the rough-picture warning looks at (S6). At most 20,000 probes read.
func (s *Store) FlowShapes(flowID int64) ([]rules.Shape, error) {
	rows, err := s.db.Query(`SELECT p.info FROM probes p JOIN libraries l ON l.flow_id=?
  AND substr(replace(p.path,'\','/'),1,length(rtrim(replace(l.path,'\','/'),'/'))+1)=rtrim(replace(l.path,'\','/'),'/')||'/'
  ORDER BY p.rowid DESC LIMIT 20000`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[rules.Shape]bool{}
	var out []rules.Shape
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var info media.Info
		if json.Unmarshal([]byte(raw), &info) != nil {
			continue
		}
		if v := info.Video(); v != nil && v.Height > 0 {
			if sh := rules.ShapeOf(v); !seen[sh] {
				seen[sh] = true
				out = append(out, sh)
			}
		}
	}
	return out, rows.Err()
}

// notReadySQL is true for a waiting job (alias j) Claim would not take now (one placeholder: now):
// waiting to retry, being stopped, or in a paused library (the same condition as Claim's).
var notReadySQL = `(j.stop_requested=1 OR (j.retry_at<>'' AND j.retry_at>?) OR NOT ` + libraryOn("j") + `)` // also a paused library's job (V5b)

// QueuePositions are the given waiting jobs' places in the queue (1: taken next), counted over every
// job Claim would take, in its order (priority, then age); a job Claim wouldn't take now (waiting to
// retry, being stopped, or not waiting) has none.
func (s *Store) QueuePositions(ids []int64) (map[int64]int, error) {
	out := map[int64]int{}
	now := s.stamp()
	for _, id := range ids {
		var pos sql.NullInt64
		err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM jobs k WHERE k.status=? AND k.stop_requested=0 AND (k.retry_at='' OR k.retry_at<=?) AND `+libraryOn("k")+`
  AND (k.priority>j.priority OR k.priority=j.priority AND k.id<j.id)) + 1
FROM jobs j WHERE j.id=? AND j.status=? AND NOT `+notReadySQL, Queued, now, id, Queued, now).Scan(&pos)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = int(pos.Int64)
	}
	return out, nil
}
