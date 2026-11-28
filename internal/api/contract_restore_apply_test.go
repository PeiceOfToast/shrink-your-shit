// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The apply golden (page 11): a restore of a kept backup. The safety backup's id, time and name are not fixed, so each
// is swapped for a fixed word; the UI's types.ts names the type BackupRestored.
func TestContractBackupRestoredGolden(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
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
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	sb := out.SafetyBackup
	writeGolden(t, "backup-restored", "BackupRestored", http.MethodPost, "/api/v2/backups/apply", rec, sb.ID, sb.CreatedAt, sb.Name)
}
