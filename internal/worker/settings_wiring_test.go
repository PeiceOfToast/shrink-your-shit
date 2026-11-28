// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
)

// TestQueueOrderSettingChoosesTheClaim: with queueOrder "smallest" the claim takes the smallest waiting file, not the
// newest (the default), so the setting changes what runs next.
func TestQueueOrderSettingChoosesTheClaim(t *testing.T) {
	s := newStoredJob(t, 1) // a 4321-byte file, queued first
	ctx := context.Background()
	if err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO files(library_id, rel_path, size, mtime, file_id) VALUES(?, 'big.mkv', 999999, 1, 'fid-2')`, s.libID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var fileID int64
	if err := s.db.Read().QueryRow(`SELECT id FROM files WHERE rel_path = 'big.mkv'`).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.q.Enqueue(ctx, fileID, s.libID, s.wfID, 1, filepath.Join(s.root, "big.mkv"), "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := settings.Patch(ctx, s.db, settings.Options{}, map[string]json.RawMessage{
		"processing": json.RawMessage(`{"queueOrder":"smallest"}`),
	}); err != nil {
		t.Fatal(err)
	}
	b := Builtin{Queue: s.q, DB: s.db, Flows: s.flows}
	w, err := b.Claim(ctx, "local")
	if err != nil || w == nil {
		t.Fatalf("claim = %v, %v", w, err)
	}
	if w.Job.ID != s.jobID {
		t.Errorf("claimed job %d, want the smallest file's job %d (queueOrder smallest)", w.Job.ID, s.jobID)
	}
}
