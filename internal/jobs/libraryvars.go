// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"encoding/json"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// A library's overrides (W3, workflow-architecture.md 2.2 and 2.4): its own values for the settings
// its workflow lets a library change (the variables with TemplateParam). They live on the library row
// as JSON, and a job reads them when it starts, like the server's global variables.

// SetLibraryVariables saves a library's overrides. The caller checks them against the library's
// workflow (workflow.CheckLibraryOverrides): this writes what it is given. Like a global save it
// forgets no files and queues nothing, because the next job reads the values itself; the change is on
// the live stream like any other library change.
func (s *Store) SetLibraryVariables(id int64, vs []workflow.Variable) error {
	if vs == nil {
		vs = []workflow.Variable{}
	}
	b, err := json.Marshal(vs)
	if err != nil {
		return err
	}
	r, err := s.db.Exec(`UPDATE libraries SET variables=? WHERE id=?`, string(b), id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.libraryChanged(id, false)
	return nil
}
