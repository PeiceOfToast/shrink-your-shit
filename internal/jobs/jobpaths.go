// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

// JobPaths are the files of every job there is, once each (the start-up clean-up looks next to them).
func (s *Store) JobPaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT path FROM jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
