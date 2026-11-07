// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

// A Sidecar is a subtitle saved as an .srt file beside a converted file (slice 25b), recorded before it
// is put there: its path and what it is, and what the converted file at VideoPath is once the swap has
// happened. Only a file that is still exactly this one is ever removed.
type Sidecar struct {
	JobID      int64
	Path       string
	Bytes      int64
	MTime      int64 // UnixNano
	Hash       string
	VideoPath  string
	VideoBytes int64
	VideoHash  string
}

// AddSidecars records them, in one transaction, before they are put in place.
func (s *Store) AddSidecars(list []Sidecar) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range list {
		if _, err := tx.Exec(`INSERT INTO sidecars(job_id,path,bytes,mtime,hash,video_path,video_bytes,video_hash) VALUES(?,?,?,?,?,?,?,?)`,
			c.JobID, c.Path, c.Bytes, c.MTime, c.Hash, c.VideoPath, c.VideoBytes, c.VideoHash); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DropSidecars forgets job jobID's records: its swap is done, or its files are gone again.
func (s *Store) DropSidecars(jobID int64) error {
	_, err := s.db.Exec(`DELETE FROM sidecars WHERE job_id=?`, jobID)
	return err
}

// Sidecars are all the records left (a crash came between putting them in place and the swap's end).
func (s *Store) Sidecars() ([]Sidecar, error) {
	rows, err := s.db.Query(`SELECT job_id,path,bytes,mtime,hash,video_path,video_bytes,video_hash FROM sidecars ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sidecar
	for rows.Next() {
		var c Sidecar
		if err := rows.Scan(&c.JobID, &c.Path, &c.Bytes, &c.MTime, &c.Hash, &c.VideoPath, &c.VideoBytes, &c.VideoHash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
