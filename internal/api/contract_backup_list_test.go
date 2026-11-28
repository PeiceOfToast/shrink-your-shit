// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The backup list golden (page 11): one kept backup and the keep count. The id, the time and the name are not fixed,
// so each is swapped for a fixed word; the UI's types.ts names the type BackupList.
func TestContractBackupListGolden(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
	rec := e.do(http.MethodGet, "/api/v2/backups", "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d", rec.Code)
	}
	var l struct {
		Backups []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"createdAt"`
			Name      string `json:"name"`
		} `json:"backups"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &l); err != nil || len(l.Backups) != 1 || l.Backups[0].ID != id {
		t.Fatalf("list body = %s: %v", rec.Body, err)
	}
	b := l.Backups[0]
	writeGolden(t, "backup-list", "BackupList", http.MethodGet, "/api/v2/backups", rec, b.ID, b.CreatedAt, b.Name)
}
