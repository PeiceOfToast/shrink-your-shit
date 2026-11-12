// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"strings"
	"time"
)

// The overview's numbers (user review 11-12 item 5, slices/v2-engine/review-items-shapes.md), from the
// jobs, their history and the scanner's file records. Nothing is estimated: what isn't recorded says so.

// LibraryTotals are a library's sizes and counts for the overview.
type LibraryTotals struct {
	// BytesBefore: its videos as the scanner first decided them (a converted file's record keeps its
	// original's size).
	BytesBefore    int64
	Done, Skipped  int
	VideosRecorded int
}

// LibraryTotalsOf is library l's totals.
func (s *Store) LibraryTotalsOf(l Library) (LibraryTotals, error) {
	var t LibraryTotals
	prefix := strings.TrimSuffix(l.Path, string(filepath.Separator)) + string(filepath.Separator)
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(size),0), COUNT(*), COALESCE(SUM(CASE WHEN decision LIKE 'skip:%' THEN 1 ELSE 0 END),0)
FROM files WHERE substr(path, 1, length(?)) = ?`, prefix, prefix).Scan(&t.BytesBefore, &t.VideosRecorded, &t.Skipped); err != nil {
		return t, err
	}
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE library_id=? AND status=?`, l.ID, Done).Scan(&t.Done)
	return t, err
}

// Day is one day's totals in the overview (server-local days).
type Day struct {
	Day                        string `json:"day"` // 2026-11-12
	Recorded                   bool   `json:"recorded"`
	Completed, Skipped, Failed int
	SavedBytes                 int64
}

// Days are the last n days' totals, oldest first: conversions finished, files skipped (also stopped),
// failed (also kept by a check), and the bytes saved by the conversions finished that day. A day
// before the first recorded history is Recorded false (zeroes, not estimates).
func (s *Store) Days(n int, now time.Time) ([]Day, error) {
	var first string
	s.db.QueryRow(`SELECT COALESCE(MIN(date(at, 'localtime')), '') FROM events WHERE kind='job'`).Scan(&first)
	from := now.AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	by := map[string]*Day{}
	rows, err := s.db.Query(`SELECT date(at, 'localtime') AS d, to_status, COUNT(*) FROM events
WHERE kind='job' AND to_status IN ('done','skipped','cancelled','failed','kept') AND date(at, 'localtime') >= ? GROUP BY d, to_status`, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d, st string
		var c int
		if err := rows.Scan(&d, &st, &c); err != nil {
			rows.Close()
			return nil, err
		}
		if by[d] == nil {
			by[d] = &Day{Day: d}
		}
		switch st {
		case "done":
			by[d].Completed += c
		case "skipped", "cancelled":
			by[d].Skipped += c
		default:
			by[d].Failed += c
		}
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT date(e.at, 'localtime') AS d, COALESCE(SUM(MAX(j.size_before - j.size_after, 0)),0) FROM events e JOIN jobs j ON j.id=e.job_id
WHERE e.kind='job' AND e.to_status='done' AND j.status='done' AND j.size_before>0 AND date(e.at, 'localtime') >= ? GROUP BY d`, from)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var d string
		var b int64
		if err := rows.Scan(&d, &b); err != nil {
			rows.Close()
			return nil, err
		}
		if by[d] == nil {
			by[d] = &Day{Day: d}
		}
		by[d].SavedBytes = b
	}
	rows.Close()
	out := make([]Day, 0, n)
	for i := n - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		day := Day{Day: d}
		if x := by[d]; x != nil {
			day = *x
		}
		day.Recorded = first != "" && d >= first
		out = append(out, day)
	}
	return out, nil
}

// Averages over the conversions done: the share saved, and the time per GB of original (only jobs
// whose start was recorded); ok false when there are none.
func (s *Store) Averages() (savingPercent float64, savingOK bool, secondsPerGB float64, timeOK bool, err error) {
	var before, after int64
	if err = s.db.QueryRow(`SELECT COALESCE(SUM(size_before),0), COALESCE(SUM(size_after),0) FROM jobs WHERE status=? AND size_before>0 AND size_after>0`, Done).
		Scan(&before, &after); err != nil {
		return
	}
	if before > 0 {
		savingPercent, savingOK = 100*float64(before-after)/float64(before), true
	}
	var secs float64
	var timedBytes int64
	if err = s.db.QueryRow(`SELECT COALESCE(SUM((julianday(updated)-julianday(started))*86400),0), COALESCE(SUM(size_before),0)
FROM jobs WHERE status=? AND started<>'' AND size_before>0`, Done).Scan(&secs, &timedBytes); err != nil {
		return
	}
	if timedBytes > 0 && secs > 0 {
		secondsPerGB, timeOK = secs/(float64(timedBytes)/1e9), true
	}
	return
}

// DoneCount is how many files were converted (also by jobs made before libraries were records).
func (s *Store) DoneCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status=?`, Done).Scan(&n)
	return n, err
}
