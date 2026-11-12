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

// leftSQL is true for a done job (of table jobs) that left its original where it was.
const leftSQL = `(output_path<>'' AND output_path<>path AND NOT EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id))`

const keptNowSQL = `EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id AND o.state='kept')`

// freedSQL, waitingSQL and couldFreeSQL are a jobs row's freed, waiting and could-free bytes (NULL: none).
const (
	freedSQL     = `CASE WHEN status='done' AND size_after>0 AND ((` + replacedSQL + ` AND NOT ` + keptNowSQL + `) OR (` + leftSQL + ` AND original_gone_at<>'' AND original_changed=0)) THEN ` + savedSQL + ` END`
	waitingSQL   = `CASE WHEN status='done' AND size_after>0 AND ` + replacedSQL + ` AND ` + keptNowSQL + ` THEN ` + savedSQL + ` END`
	couldFreeSQL = `CASE WHEN status='done' AND size_after>0 AND ` + leftSQL + ` AND original_gone_at='' AND original_changed=0 THEN ` + savedSQL + ` END`
	// savedSQL is one file's saving, never below 0: a new file bigger than its original (kept by design,
	// always-standardize 11-07) saves nothing, and its growth counts in grewSQL instead, so a total never
	// hides it nor goes negative (orch-lead 18:15Z)
	savedSQL = `MAX(size_before-size_after, 0)`
	// grewSQL is how much bigger a done job's new file is than its original (NULL: not bigger)
	grewSQL = `CASE WHEN status='done' AND size_after>size_before AND size_before>0 THEN size_after-size_before END`
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
}

// LeftOriginals are those of the given libraries' done jobs.
func (s *Store) LeftOriginals(libraryIDs []int64) ([]LeftOriginal, error) {
	var out []LeftOriginal
	for _, id := range libraryIDs {
		rows, err := s.db.Query(`SELECT id, path, size_before, original_disk FROM jobs WHERE library_id=? AND status='done' AND original_gone_at='' AND original_changed=0 AND `+leftSQL, id)
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
