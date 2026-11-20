// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"encoding/json"
	"path/filepath"
)

// A Sidecar is a subtitle saved as an .srt file beside a converted file (slice 25b), recorded before it
// is put there: its path and what it is, and what the converted file at VideoPath is once the swap has
// happened. Only a file that is still exactly this one is ever removed.
type Sidecar struct {
	JobID      int64
	Path       string
	Bytes      int64
	MTime      int64  // UnixNano
	Hash       string // the full SHA-256 of the .srt (they are small)
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

// FinishSidecars is job jobID's swap done: the job keeps the names of the .srt files it saved (the job
// page lists them; none is []), and their records go. A run that saved none clears an earlier run's list.
func (s *Store) FinishSidecars(jobID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT path FROM sidecars WHERE job_id=? ORDER BY id`, jobID)
	if err != nil {
		return err
	}
	names := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		names = append(names, filepath.Base(p))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	b, _ := json.Marshal(names)
	if _, err := tx.Exec(`UPDATE jobs SET subtitle_files=? WHERE id=?`, string(b), jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sidecars WHERE job_id=?`, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// SubtitleFiles are the names of the .srt files job id saved beside its new file; [] when none, or for a
// job from before they were kept (migration 52).
func (s *Store) SubtitleFiles(id int64) ([]string, error) {
	var raw string
	if err := s.db.QueryRow(`SELECT subtitle_files FROM jobs WHERE id=?`, id).Scan(&raw); err != nil {
		return nil, err
	}
	names := []string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &names); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// DropSidecar forgets one record: that file is gone, or it is not ours (any more).
func (s *Store) DropSidecar(jobID int64, path string) error {
	_, err := s.db.Exec(`DELETE FROM sidecars WHERE job_id=? AND path=?`, jobID, path)
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
