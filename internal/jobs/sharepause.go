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
