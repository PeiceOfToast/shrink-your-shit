// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

// Saved bytes (UI-API-CONTRACT F1; V5c, design call 3 and 01:55Z). A conversion's saving (original minus
// new) is one of:
//   - freed: the original is gone (replaced and nothing kept, its kept copy deleted, or, left where it
//     was, found gone by a scan);
//   - waiting to free: a replaced original kept in .syp-trash, which goes on its own on its day;
//   - could free: an original left where it was (beside, or with a folder for new files), which never
//     goes by itself: the person can remove it. Once a scan finds it gone, it is freed; if it changed
//     instead (another size), it is a new file and the old saving counts nowhere.

// newestSQL is true for a jobs row with no later done job for the same file: a file is counted once,
// by its newest conversion (checked last in each bucket: a row reaches it in one bucket at most, so
// it costs one lookup by the jobs_path index, not four). Republishing a workflow queues every file again, and each earlier done job
// would otherwise count the same original's saving once more (ui-shop review d10d17e, orch-lead 18:29Z).
const newestSQL = `NOT EXISTS (SELECT 1 FROM jobs later WHERE later.path=jobs.path AND later.status='done' AND later.id>jobs.id)`

// notRestoredSQL is true when the job whose id is col was not restored. A restored original is the original
// file again, so its job counts nowhere as converted.
func notRestoredSQL(col string) string {
	return `NOT EXISTS (SELECT 1 FROM originals o WHERE o.job_id=` + col + ` AND o.state='restored')`
}

// convertedNow is true for a done job (table alias t) that was not restored: the one condition every converted
// count and saving reads (the overview, library Done, daily bars, per-workflow counts).
func convertedNow(t string) string {
	return t + `.status='done' AND ` + notRestoredSQL(t+`.id`)
}

// leftSQL is true for a done job (of table jobs) that left its original where it was.
const leftSQL = `(output_path<>'' AND output_path<>path AND NOT EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id))`

const keptNowSQL = `EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id AND o.state='kept')`

// freedSQL, waitingSQL and couldFreeSQL are a jobs row's freed, waiting and could-free bytes (NULL: none).
var (
	freedSQL   = `CASE WHEN ` + convertedNow("jobs") + ` AND size_after>0 AND ((` + replacedSQL + ` AND NOT ` + keptNowSQL + `) OR (` + leftSQL + ` AND original_gone_at<>'' AND original_changed=0)) AND ` + newestSQL + ` THEN ` + savedSQL + ` END`
	waitingSQL = `CASE WHEN ` + convertedNow("jobs") + ` AND size_after>0 AND ` + replacedSQL + ` AND ` + keptNowSQL + ` AND output_gone_at='' AND ` + newestSQL + ` THEN ` + savedSQL + ` END`
	// goneSQL is what a replaced file's bytes still counted in the overview's bytesNow once its output is gone
	// from the library (the original's size, less the saving already freed, plus the growth that bytesNow adds
	// back): the file is no longer there.
	goneSQL      = `CASE WHEN ` + convertedNow("jobs") + ` AND size_after>0 AND ` + replacedSQL + ` AND output_gone_at<>'' AND ` + newestSQL + ` THEN size_before-COALESCE(` + freedSQL + `,0)+COALESCE(` + grewSQL + `,0) END`
	couldFreeSQL = `CASE WHEN ` + convertedNow("jobs") + ` AND size_after>0 AND ` + leftSQL + ` AND original_gone_at='' AND original_changed=0 AND ` + newestSQL + ` THEN ` + savedSQL + ` END`
	// savedSQL is one file's saving, never below 0: a new file bigger than its original (kept by design,
	// always-standardize 11-07) saves nothing, and its growth counts in grewSQL instead, so a total never
	// hides it nor goes negative (orch-lead 18:15Z)
	savedSQL = `MAX(size_before-size_after, 0)`
	// grewSQL is how much bigger a done job's new file is than its original (NULL: not bigger)
	grewSQL = `CASE WHEN ` + convertedNow("jobs") + ` AND size_after>size_before AND size_before>0 AND ` + newestSQL + ` THEN size_after-size_before END`
	// grewNowSQL is the growth bytesNow adds: only a replaced file's, since the new file then stands where the
	// library's record (the original's size) is. A left output is not in the library's bytes, so it adds none (#403).
	grewNowSQL = `CASE WHEN ` + convertedNow("jobs") + ` AND size_after>size_before AND size_before>0 AND ` + replacedSQL + ` AND ` + newestSQL + ` THEN size_after-size_before END`
	// leftNowSQL is what a left job adds to its library's bytesNow, on top of the recorded bytes (LibraryTotalsOf
	// binds the library's path range for leftInSQL, twice). The original's record stays in the library's bytes
	// until it is gone, so while it is there the new file adds its size if it is in the library and not gone.
	// Once the original is gone, freedSQL has already taken the saving off (original less new, never below 0):
	// the file's bytes left are the growth, if the new file is bigger, and the library holds nothing of it when
	// the new file is not in it, so that saving is taken back off.
	leftNowSQL = `CASE WHEN ` + leftSQL + ` AND ` + convertedNow("jobs") + ` AND size_after>0 AND ` + newestSQL + ` THEN
  CASE WHEN original_gone_at<>'' AND original_changed=0 THEN
    CASE WHEN ` + leftInSQL + ` THEN MAX(size_after-size_before,0) ELSE -MIN(size_before,size_after) END
  ELSE
    CASE WHEN ` + leftInSQL + ` THEN size_after ELSE 0 END
  END END`
	// leftInSQL is a left output in its library now: inside the library's path range (two binds) and not known gone.
	leftInSQL = `(output_path>=? AND output_path<? AND output_gone_at='')`
	// freedAtSQL is when a freed saving was freed: the original found gone, its kept copy's delete day,
	// or the conversion's end (replaced with nothing kept).
	freedAtSQL = `CASE WHEN ` + leftSQL + ` THEN original_gone_at
  ELSE COALESCE((SELECT o.delete_on FROM originals o WHERE o.job_id=jobs.id AND o.state='purged' ORDER BY o.id DESC LIMIT 1), updated) END`
)

// FreedSince is the bytes freed since at (RFC3339): the summary's freedLast7d.
func (s *Store) FreedSince(at string) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COALESCE(SUM(`+freedSQL+`),0) FROM jobs WHERE `+freedAtSQL+` >= ?`, at).Scan(&n)
	return n, err
}

// LeftOriginal is a converted file's original that was left where it was and not yet found gone.
type LeftOriginal struct {
	JobID int64
	Path  string
	Size  int64  // the original's size when it was converted
	Disk  string // the disk its folder was on then ("": not known: a job done before it was recorded)
	Gone  bool   // ReplacedOutputs: the output is already known gone
}

// LeftOriginals are those of the given libraries' done jobs.
func (s *Store) LeftOriginals(libraryIDs []int64) ([]LeftOriginal, error) {
	var out []LeftOriginal
	for _, id := range libraryIDs {
		rows, err := s.db.Query(`SELECT id, path, size_before, original_disk FROM jobs WHERE library_id=? AND `+convertedNow("jobs")+` AND original_gone_at='' AND original_changed=0 AND `+leftSQL, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o LeftOriginal
			if err := rows.Scan(&o.JobID, &o.Path, &o.Size, &o.Disk); err != nil {
				rows.Close()
				return nil, err
			}
			out = append(out, o)
		}
		rows.Close()
	}
	return out, nil
}

// SetOriginalGone records that job id's original, left where it was, is gone (now).
func (s *Store) SetOriginalGone(id int64) error {
	_, err := s.db.Exec(`UPDATE jobs SET original_gone_at=? WHERE id=? AND original_gone_at=''`, s.stamp(), id)
	return err
}

// ReplacedOutputs are the converted files (replaced in their own place, the newest conversion of each file)
// of the given libraries that a scan can check: the output's path and the disk its folder was on.
func (s *Store) ReplacedOutputs(libraryIDs []int64) ([]LeftOriginal, error) {
	var out []LeftOriginal
	for _, id := range libraryIDs {
		rows, err := s.db.Query(`SELECT id, output_path, size_after, original_disk, output_gone_at FROM jobs WHERE library_id=? AND `+convertedNow("jobs")+` AND output_path<>'' AND size_after>0 AND `+replacedSQL+` AND `+newestSQL, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o LeftOriginal
			var gone string
			if err := rows.Scan(&o.JobID, &o.Path, &o.Size, &o.Disk, &gone); err != nil {
				rows.Close()
				return nil, err
			}
			o.Gone = gone != ""
			out = append(out, o)
		}
		rows.Close()
	}
	return out, nil
}

// LeftOutputs are the left outputs (the original left where it was, the new file beside it or in a folder)
// of the given libraries that are inside the library, the newest conversion of each file, that a scan can
// check. Their output_gone_at says the scan found them gone (leftNowSQL counts them until then).
func (s *Store) LeftOutputs(libraryIDs []int64) ([]LeftOriginal, error) {
	var out []LeftOriginal
	for _, id := range libraryIDs {
		l, err := s.GetLibrary(id)
		if err != nil {
			return nil, err
		}
		lo, hi := pathRange(l.Path)
		rows, err := s.db.Query(`SELECT id, output_path, size_after, original_disk, output_gone_at FROM jobs WHERE library_id=? AND `+convertedNow("jobs")+` AND size_after>0 AND `+leftSQL+` AND `+newestSQL+` AND output_path>=? AND output_path<?`, id, lo, hi)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o LeftOriginal
			var gone string
			if err := rows.Scan(&o.JobID, &o.Path, &o.Size, &o.Disk, &gone); err != nil {
				rows.Close()
				return nil, err
			}
			o.Gone = gone != ""
			out = append(out, o)
		}
		rows.Close()
	}
	return out, nil
}

// SetOutputGone records that job id's converted file is gone from the library (now); SetOutputBack that it
// is there again.
func (s *Store) SetOutputGone(id int64) error {
	_, err := s.db.Exec(`UPDATE jobs SET output_gone_at=? WHERE id=? AND output_gone_at=''`, s.stamp(), id)
	return err
}

func (s *Store) SetOutputBack(id int64) error {
	_, err := s.db.Exec(`UPDATE jobs SET output_gone_at='' WHERE id=? AND output_gone_at<>''`, id)
	return err
}

// SetOriginalChanged records that job id's original, left where it was, changed (another size): its
// saving counts nowhere any more (design call 3); the new file is converted on its own.
func (s *Store) SetOriginalChanged(id int64) error {
	_, err := s.db.Exec(`UPDATE jobs SET original_changed=1 WHERE id=? AND original_gone_at=''`, id)
	return err
}

// DiskOf is the disk folder p is on (false: it can't be read), as Complete records it for an original.
func DiskOf(p string) (string, bool) { return diskOf(p) }

// SetOriginalDisk records the disk job id's original's folder is on now, seen there by a scan (a job
// done before Complete recorded it, or a disk whose id changed).
func (s *Store) SetOriginalDisk(id int64, disk string) error {
	_, err := s.db.Exec(`UPDATE jobs SET original_disk=? WHERE id=?`, disk, id)
	return err
}
