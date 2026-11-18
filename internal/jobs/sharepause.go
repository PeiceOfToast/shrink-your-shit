// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

// D4b: a library whose share stopped answering is paused by the share. Its waiting jobs are not taken
// (libraryOnSQL) until the share answers again. The person's own pause (started) is never touched.

// PauseLibrariesForShare pauses, for share id, each library whose folder is mountPath or inside it, and
// that is not already paused by a share. It returns the libraries it paused.
func (s *Store) PauseLibrariesForShare(shareID int64, mountPath string) ([]int64, error) {
	libs, err := s.Libraries()
	if err != nil {
		return nil, err
	}
	var paused []int64
	for _, l := range libs {
		if l.Path != mountPath && !Within(l.Path, mountPath) {
			continue
		}
		r, err := s.db.Exec(`UPDATE libraries SET share_paused_id=?, share_paused_since=? WHERE id=? AND share_paused_id IS NULL`,
			shareID, s.stamp(), l.ID)
		if err != nil {
			return paused, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			paused = append(paused, l.ID)
			s.libraryChanged(l.ID, false)
		}
	}
	return paused, nil
}

// ShareDropped puts a running job back in the queue because its share stopped answering. It is not a
// failure: no try is counted, and the libraries in that folder are paused by the share at once, so no
// claim takes their jobs until it answers. words are the plain reason the page shows. A shareID of 0
// means the share answers again (it had an outage during the convert): the job is requeued, nothing paused.
func (s *Store) ShareDropped(id, shareID int64, mountPath, words string) error {
	if err := s.transition(id, Queued, `, progress=0, worker='', error=?, error_code=?, retry_at=''`, words, ReasonShareDropped); err != nil {
		return err
	}
	s.AddStep(id, "share", false, words)
	if shareID == 0 {
		return nil
	}
	_, err := s.PauseLibrariesForShare(shareID, mountPath)
	return err
}

// JobStarted is when job id was claimed (RFC3339, UTC; "" when it was never claimed).
func (s *Store) JobStarted(id int64) string {
	var started string
	s.db.QueryRow(`SELECT COALESCE(started,'') FROM jobs WHERE id=?`, id).Scan(&started)
	return started
}

// ResumeSharePause clears the share pause of every library share id paused. The libraries stay as the
// person left them (started or not).
func (s *Store) ResumeSharePause(shareID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM libraries WHERE share_paused_id=?`, shareID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if _, err := s.db.Exec(`UPDATE libraries SET share_paused_id=NULL, share_paused_since='' WHERE share_paused_id=?`, shareID); err != nil {
		return nil, err
	}
	for _, id := range ids {
		s.libraryChanged(id, false)
	}
	return ids, nil
}
