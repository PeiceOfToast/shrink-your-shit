// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

func TestV2BackupsListMakeDownloadDelete(t *testing.T) {
	srv, _, _ := doctorServer(t, false)

	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/backups"})
	var list struct {
		Backups []jobs.BackupInfo `json:"backups"`
		Keep    int               `json:"keep"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil || code != 200 {
		t.Fatalf("list: %d %s", code, body)
	}
	if list.Keep != 10 || len(list.Backups) != 0 || !strings.Contains(body, `"backups":[]`) {
		t.Fatalf("empty list: %s", body)
	}

	code, body = post(t, srv, "POST", "/api/v2/backups", "")
	var made jobs.BackupInfo
	if err := json.Unmarshal([]byte(body), &made); err != nil || code != 201 {
		t.Fatalf("create: %d %s", code, body)
	}
	if made.ID == "" || made.Reason != jobs.ReasonManual || !strings.HasSuffix(made.Name, ".json.gz") || made.SizeBytes <= 0 {
		t.Fatalf("created: %+v", made)
	}

	code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/backups"})
	if !strings.Contains(body, made.ID) {
		t.Fatalf("list after create: %d %s", code, body)
	}

	code, body, resp := do(t, nil, srv, call{method: "GET", path: "/api/v2/backups/" + made.ID + "/download"})
	if code != 200 || resp.Header.Get("Content-Type") != "application/gzip" ||
		!strings.Contains(resp.Header.Get("Content-Disposition"), made.Name) || len(body) == 0 || body[0] != 0x1f || body[1] != 0x8b {
		t.Fatalf("download: %d %v %q", code, resp.Header, body[:min(len(body), 8)])
	}

	if code, body, _ = do(t, nil, srv, call{method: "DELETE", path: "/api/v2/backups/" + made.ID, ctype: "application/json"}); code != 204 {
		t.Fatalf("delete: %d %s", code, body)
	}
	if code, body, _ = do(t, nil, srv, call{method: "DELETE", path: "/api/v2/backups/" + made.ID, ctype: "application/json"}); code != 404 || errCode(body) != "not_found" {
		t.Errorf("delete again: %d %s", code, body)
	}
	if code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/backups/" + made.ID + "/download"}); code != 404 || errCode(body) != "not_found" {
		t.Errorf("download after delete: %d %s", code, body)
	}
}

func TestBackupKeepSetting(t *testing.T) {
	srv, _, _ := doctorServer(t, false)
	for _, bad := range []string{`{"backupKeep":0}`, `{"backupKeep":51}`} {
		if code, body := post(t, srv, "PATCH", "/api/settings", bad); code != 400 || errCode(body) != "invalid_setting" {
			t.Errorf("%s: %d %s", bad, code, body)
		}
	}
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"backupKeep":5}`); code != 200 {
		t.Fatalf("valid: %d %s", code, body)
	}
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/backups"})
	if code != 200 || !strings.Contains(body, `"keep":5`) {
		t.Errorf("keep after PATCH: %d %s", code, body)
	}
}

func TestSystemBackupStubIsGone(t *testing.T) {
	srv, _, _ := doctorServer(t, false)
	if code, body := post(t, srv, "POST", "/api/system/backup", ""); code == 501 || code == 200 {
		t.Errorf("the 501 stub is back: %d %s", code, body)
	}
}
