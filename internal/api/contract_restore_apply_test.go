// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"
)

// The apply golden (page 11): a restore of a kept backup that also keeps one library the backup lacks, so the golden
// pins a kept item (its id is a numeric string). The safety backup's id, time and name are not fixed, so each is swapped
// for a fixed word; the UI's types.ts names the type BackupRestored.
func TestContractBackupRestoredGolden(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO libraries(id, name, path) VALUES(7, 'Kept library', '/kept')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	pid := previewKept(t, e, cookie, id)
	rec := applyPreview(e, cookie, pid)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		SafetyBackup struct {
			ID        string `json:"id"`
			CreatedAt string `json:"createdAt"`
			Name      string `json:"name"`
		} `json:"safetyBackup"`
		Kept []struct {
			ID string `json:"id"`
		} `json:"keptNotInBackup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Kept) != 1 || out.Kept[0].ID != "7" {
		t.Fatalf("kept = %+v, want library 7 as the string \"7\"", out.Kept)
	}
	sb := out.SafetyBackup
	writeGolden(t, "backup-restored", "BackupRestored", http.MethodPost, "/api/v2/backups/apply", rec, sb.ID, sb.CreatedAt, sb.Name)
}
