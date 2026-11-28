// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The backup golden (page 11): the one a made backup answers. The id, the size and the time are not fixed, so each is
// swapped for a fixed word: the body keeps its shape, and the UI's types.ts names the type BackupItem.
func TestContractBackupMadeGolden(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodPost, "/api/v2/backups", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("make = %d", rec.Code)
	}
	var b struct {
		ID        string `json:"id"`
		CreatedAt string `json:"createdAt"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	writeGolden(t, "backup-made", "BackupItem", http.MethodPost, "/api/v2/backups", rec, b.ID, b.CreatedAt, b.Name)
}
