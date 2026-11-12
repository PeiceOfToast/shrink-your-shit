// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

// Freed and waiting bytes (UI-API-CONTRACT F1; V5c, design call 3). A conversion's saving (original minus
// new) counts as waiting while the original is still on the disk, and as freed once it is gone:
//   - replaced: freed at once, or when its kept copy (.syp-trash) is deleted;
//   - left where it was (beside, or in a folder for new files, output-first): freed once a scan finds the
//     original gone (original_gone_at).

// leftSQL is true for a done job (of table jobs) that left its original where it was.
const leftSQL = `(output_path<>'' AND output_path<>path AND NOT EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id))`

const keptNowSQL = `EXISTS (SELECT 1 FROM originals o WHERE o.job_id=jobs.id AND o.state='kept')`

// freedSQL and waitingSQL are a jobs row's freed and waiting bytes (NULL: none).
const (
	freedSQL   = `CASE WHEN status='done' AND size_after>0 AND ((` + replacedSQL + ` AND NOT ` + keptNowSQL + `) OR (` + leftSQL + ` AND original_gone_at<>'')) THEN size_before-size_after END`
	waitingSQL = `CASE WHEN status='done' AND size_after>0 AND ((` + replacedSQL + ` AND ` + keptNowSQL + `) OR (` + leftSQL + ` AND original_gone_at='')) THEN size_before-size_after END`
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
}

// LeftOriginals are those of the given libraries' done jobs.
func (s *Store) LeftOriginals(libraryIDs []int64) ([]LeftOriginal, error) {
	var out []LeftOriginal
	for _, id := range libraryIDs {
		rows, err := s.db.Query(`SELECT id, path FROM jobs WHERE library_id=? AND status='done' AND original_gone_at='' AND `+leftSQL, id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var o LeftOriginal
			if err := rows.Scan(&o.JobID, &o.Path); err != nil {
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
