// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The restore preview golden (page 11): a kept backup previewed. The preview id and the backup's time are not fixed, so
// each is swapped for a fixed word; the UI's types.ts names the type BackupPreview.
func TestContractBackupPreviewGolden(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
	rec := e.do(http.MethodPost, "/api/v2/backups/preview", `{"backupId":"`+id+`"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body)
	}
	var p struct {
		PreviewID string `json:"previewId"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	writeGolden(t, "backup-preview", "BackupPreview", http.MethodPost, "/api/v2/backups/preview", rec, p.PreviewID, p.CreatedAt)
}
