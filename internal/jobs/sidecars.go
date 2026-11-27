// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"

	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// SidecarJournal keeps the .srt files a job writes beside its new file, recorded before they are
// placed (L-AUD-25). It is internal/safety's SidecarStore over the database.
type SidecarJournal struct {
	DB *store.DB
}

var _ safety.SidecarStore = SidecarJournal{}

// AddSidecars records them, in one transaction, before they are put in place.
func (j SidecarJournal) AddSidecars(list []safety.Sidecar) error {
	return j.DB.Tx(context.Background(), func(tx *sql.Tx) error {
		for _, c := range list {
			if _, err := tx.Exec(`INSERT INTO sidecars(job_id,path,bytes,mtime,hash,video_path,video_bytes,video_hash) VALUES(?,?,?,?,?,?,?,?)`,
				c.JobID, c.Path, c.Bytes, c.MTime, c.Hash, c.VideoPath, c.VideoBytes, c.VideoHash); err != nil {
				return err
			}
		}
		return nil
	})
}

// FinishSidecars is the job's place done: the job keeps the names of the .srt files it saved (the
// job page lists them; none is []), and their records go. A run that saved none clears an earlier
// run's list.
func (j SidecarJournal) FinishSidecars(jobID int64) error {
	return j.DB.Tx(context.Background(), func(tx *sql.Tx) error {
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
		b, err := json.Marshal(names)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE jobs SET subtitle_files=? WHERE id=?`, string(b), jobID); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM sidecars WHERE job_id=?`, jobID)
		return err
	})
}

// SubtitleFiles are the names of the .srt files job id saved beside its new file; [] when none, or
// for a job from before the names were kept.
func (j SidecarJournal) SubtitleFiles(id int64) ([]string, error) {
	var raw sql.NullString
	if err := j.DB.Read().QueryRow(`SELECT subtitle_files FROM jobs WHERE id=?`, id).Scan(&raw); err != nil {
		return nil, err
	}
	names := []string{}
	if raw.Valid && raw.String != "" {
		if err := json.Unmarshal([]byte(raw.String), &names); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// DropSidecar forgets one record: that file is gone, is not ours, or was never placed.
func (j SidecarJournal) DropSidecar(jobID int64, path string) error {
	return j.DB.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM sidecars WHERE job_id=? AND path=?`, jobID, path)
		return err
	})
}

// Sidecars are all the records left (a crash came between putting them in place and placing the
// new file).
func (j SidecarJournal) Sidecars() ([]safety.Sidecar, error) {
	rows, err := j.DB.Read().Query(`SELECT job_id,path,bytes,mtime,hash,video_path,video_bytes,video_hash FROM sidecars ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []safety.Sidecar
	for rows.Next() {
		var c safety.Sidecar
		if err := rows.Scan(&c.JobID, &c.Path, &c.Bytes, &c.MTime, &c.Hash, &c.VideoPath, &c.VideoBytes, &c.VideoHash); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
