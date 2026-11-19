// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
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

// LibraryTotalsOf is library l's totals: its files' records by a path range, which the files' primary
// key serves (review ab99630 P2-2: substr() read every record per library).
func (s *Store) LibraryTotalsOf(l Library) (LibraryTotals, error) {
	var t LibraryTotals
	lo, hi := pathRange(l.Path)
	if err := s.db.QueryRow(`SELECT COALESCE(SUM(size),0), COUNT(*)
FROM files WHERE path >= ? AND path < ?`, lo, hi).Scan(&t.BytesBefore, &t.VideosRecorded); err != nil {
		return t, err
	}
	// Skipped is the count Jobs > Skipped shows for this library (the same filter, so they cannot disagree).
	sw, sa := FileFilter{LibraryID: l.ID}.skipFilters()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE `+sw, sa...).Scan(&t.Skipped); err != nil {
		return t, err
	}
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT path) FROM jobs WHERE library_id=? AND status=?`, l.ID, Done).Scan(&t.Done)
	return t, err
}

// pathRange is [lo, hi) holding every path inside dir: dir + separator up to dir + the next character.
func pathRange(dir string) (string, string) {
	sep := string(filepath.Separator)
	root := strings.TrimSuffix(dir, sep)
	return root + sep, root + string(rune(sep[0]+1))
}

// Day is one day's totals in the overview (server-local days).
type Day struct {
	Day                        string `json:"day"` // 2026-11-12
	Recorded                   bool   `json:"recorded"`
	Completed, Skipped, Failed int
	FreedBytes                 int64 // NET, freed that day (review ab99630 P2-1)
	SavedBytes                 int64 // NET, saved by the conversions that finished that day (their finish day, as 'completed')
	WaitingBytes               int64 // the part of SavedBytes whose original is still kept in the trash
	CouldFreeBytes             int64 // the part of SavedBytes whose original was left where it was
}

// latestEndsSQL is every file's latest end (done, skipped, cancelled, failed, kept) with rn 1 for the newest:
// a file is its path over all its job rows, so a requeued file that later passes has done as its latest end
// and no failure left in the counts. The history itself stays in events; only the counts read this. Skip and
// cancel delete the job row, so its end event names the path in its data: the file is still the same file.
// A scan's skip (a file decision 'skip: ...', dated decided_at) is an end too, so the newest of the two wins:
// a file counts once, on the day of its latest end. A still_changing look is not a skip (the file may still be
// downloading), so it is no end. Same-second ties go to the job event.
const latestEndsSQL = `WITH ends AS (SELECT at, to_status, ROW_NUMBER() OVER (PARTITION BY pkey ORDER BY at DESC, seq DESC) AS rn FROM (
SELECT e.at, e.to_status, e.seq, COALESCE(j.path, (SELECT json_extract(x.data, '$.path') FROM events x WHERE x.job_id=e.job_id AND json_extract(x.data, '$.path') IS NOT NULL ORDER BY x.seq DESC LIMIT 1), 'job:' || e.job_id) AS pkey
FROM events e LEFT JOIN jobs j ON j.id=e.job_id WHERE e.kind='job' AND e.to_status IN ('done','skipped','cancelled','failed','kept')
UNION ALL
SELECT decided_at, 'skipped', 0, path FROM files WHERE decision LIKE 'skip: %' AND reason<>'still_changing' AND decided_at IS NOT NULL))
`

// Days are the last n days' totals, oldest first: files finished, files skipped (also stopped), files failed
// (also kept by a check), each counted once on the day of its latest end, and the bytes freed, saved and
// waiting that day (NET, from t: TotalsOf's per-day maps). A day before the first recorded history is
// Recorded false (zeroes, not estimates).
func (s *Store) Days(n int, now time.Time, t Totals) ([]Day, error) {
	var first string
	s.db.QueryRow(`SELECT COALESCE(MIN(date(at, 'localtime')), '') FROM events WHERE kind='job'`).Scan(&first)
	from := now.AddDate(0, 0, -(n - 1)).Format("2006-01-02")
	by := map[string]*Day{}
	rows, err := s.db.Query(latestEndsSQL+`SELECT date(at, 'localtime') AS d, to_status, COUNT(*) FROM ends
WHERE rn=1 AND date(at, 'localtime') >= ? GROUP BY d, to_status`, from)
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
	out := make([]Day, 0, n)
	for i := n - 1; i >= 0; i-- {
		d := now.AddDate(0, 0, -i).Format("2006-01-02")
		day := Day{Day: d}
		if x := by[d]; x != nil {
			day = *x
		}
		day.FreedBytes = t.FreedByDay[d]
		day.SavedBytes = t.SavedByDay[d]
		day.WaitingBytes = t.WaitingByDay[d]
		day.CouldFreeBytes = t.CouldFreeByDay[d]
		day.Recorded = first != "" && d >= first
		out = append(out, day)
	}
	return out, nil
}

// Totals are the overview's job figures from one pass over the jobs: what ActivitySummary, Counts and
// Days' freed bytes give, which each scan the table on its own (the overview recomputes after every
// change, so one scan instead of three).
type Totals struct {
	Summary    Summary
	Counts     map[int64]LibraryCounts
	FreedByDay map[string]int64 // NET freed bytes by local day, dated as Days dates them
	// SavedByDay and WaitingByDay are NET saved bytes (and the part still waiting in the trash) by the
	// local day of each conversion's 'done' event; each file counts once, by its newest conversion.
	SavedByDay, WaitingByDay map[string]int64
	CouldFreeByDay           map[string]int64
}

// TotalsOf is the overview's figures in one pass: jobs grouped by library and freed day.
func (s *Store) TotalsOf() (Totals, error) {
	t := Totals{Counts: map[int64]LibraryCounts{}, FreedByDay: map[string]int64{}, SavedByDay: map[string]int64{}, WaitingByDay: map[string]int64{}, CouldFreeByDay: map[string]int64{}}
	rows, err := s.db.Query(`SELECT library_id, date(`+freedAtSQL+`, 'localtime') AS d,
  COALESCE(SUM(status=?),0), COALESCE(SUM(status=?),0), COALESCE(SUM(status IN (?,?)),0),
  COALESCE(SUM(`+freedSQL+`),0), COALESCE(SUM(`+waitingSQL+`),0), COALESCE(SUM(`+couldFreeSQL+`),0), COALESCE(SUM(`+grewSQL+`),0)
FROM jobs GROUP BY library_id, d`, Running, Queued, Failed, Kept)
	if err != nil {
		return t, err
	}
	defer rows.Close()
	for rows.Next() {
		var lib sql.NullInt64
		var day sql.NullString
		var working, waiting, failed int
		var freed, waitingB, couldFree, grew int64
		if err := rows.Scan(&lib, &day, &working, &waiting, &failed, &freed, &waitingB, &couldFree, &grew); err != nil {
			return t, err
		}
		a := &t.Summary
		a.WorkingFiles += working
		a.WaitingFiles += waiting
		a.Failed += failed
		a.FreedBytes += freed
		a.WaitingBytes += waitingB
		a.CouldFreeBytes += couldFree
		a.GrewBytes += grew
		if day.Valid {
			t.FreedByDay[day.String] += freed
		}
		if lib.Valid { // Counts leaves out jobs from before libraries
			c := t.Counts[lib.Int64]
			c.Working += working
			c.Waiting += waiting
			c.Failed += failed
			c.FreedBytes += freed
			c.WaitingBytes += waitingB
			c.CouldFreeBytes += couldFree
			c.GrewBytes += grew
			t.Counts[lib.Int64] = c
		}
	}
	if err := rows.Err(); err != nil {
		return t, err
	}
	if err := s.savedByDay(&t); err != nil {
		return t, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE reason<>'' AND NOT ` + convertedSQL).Scan(&t.Summary.Skipped)
	return t, err
}

// savedByDay fills t's saved and waiting bytes by day in one GROUP BY: each done job's day is its latest
// 'done' event (the same day the events table gives it), and the rows are the newest conversion per file.
func (s *Store) savedByDay(t *Totals) error {
	rows, err := s.db.Query(`SELECT (SELECT date(e.at, 'localtime') FROM events e WHERE e.kind='job' AND e.job_id=jobs.id AND e.to_status=? ORDER BY e.seq DESC LIMIT 1) AS d,
  COALESCE(SUM(`+savedSQL+`),0), COALESCE(SUM(`+waitingSQL+`),0), COALESCE(SUM(`+couldFreeSQL+`),0)
FROM jobs WHERE status=? AND size_after>0 AND `+newestSQL+` GROUP BY d`, Done, Done)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var day sql.NullString
		var saved, waiting, could int64
		if err := rows.Scan(&day, &saved, &waiting, &could); err != nil {
			return err
		}
		if day.Valid {
			t.SavedByDay[day.String] += saved
			t.WaitingByDay[day.String] += waiting
			t.CouldFreeByDay[day.String] += could
		}
	}
	return rows.Err()
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

// DoneCount is how many files were converted: a file converted twice counts once (also by jobs made before
// libraries were records).
func (s *Store) DoneCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT path) FROM jobs WHERE status=?`, Done).Scan(&n)
	return n, err
}
