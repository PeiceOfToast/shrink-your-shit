// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
)

// LatestConvertedIn is the newest file that a library using flowID converted: the sample the workflow
// editor's variable list shows (design-joblog-vars section 3). false when no library using the flow
// has converted anything yet, so the server offers its built-in sample instead.
func (s *Store) LatestConvertedIn(flowID int64) (Job, bool, error) {
	j, err := scan(s.db.QueryRow(`SELECT `+cols+` FROM jobs
		WHERE library_id IN (SELECT id FROM libraries WHERE flow_id=?)
		  AND status='done' AND output_path<>''
		ORDER BY id DESC LIMIT 1`, flowID))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	return j, err == nil, err
}
