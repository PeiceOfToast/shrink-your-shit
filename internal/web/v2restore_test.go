// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// backupUpload is a multipart body with one file field, and its content type.
func backupUpload(t *testing.T, name string, data []byte) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), mw.FormDataContentType()
}

func TestBackupRestoreThroughTheRoutes(t *testing.T) {
	srv, _, _ := doctorServer(t, false)
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"backupKeep":5}`); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	code, body := post(t, srv, "POST", "/api/v2/backups", "")
	var made jobs.BackupInfo
	if err := json.Unmarshal([]byte(body), &made); err != nil || code != 201 {
		t.Fatalf("make: %d %s", code, body)
	}
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"backupKeep":7}`); code != 200 {
		t.Fatalf("change: %d %s", code, body)
	}

	// Preview the stored backup.
	code, body = post(t, srv, "POST", "/api/v2/backups/preview", `{"backupId":"`+made.ID+`"}`)
	var prev struct {
		PreviewID     string              `json:"previewId"`
		FormatVersion int                 `json:"formatVersion"`
		Contents      jobs.BackupContents `json:"contents"`
		Warnings      []string            `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(body), &prev); err != nil || code != 200 {
		t.Fatalf("preview: %d %s", code, body)
	}
	if prev.PreviewID == "" || prev.FormatVersion != 1 || !prev.Contents.Settings || prev.Warnings == nil {
		t.Fatalf("preview %+v", prev)
	}

	// Apply: the setting comes back from the backup, and a safety backup is made first.
	code, body = post(t, srv, "POST", "/api/v2/backups/apply", `{"previewId":"`+prev.PreviewID+`"}`)
	var applied struct {
		Restored     jobs.BackupContents `json:"restored"`
		SafetyBackup jobs.BackupInfo     `json:"safetyBackup"`
	}
	if err := json.Unmarshal([]byte(body), &applied); err != nil || code != 200 {
		t.Fatalf("apply: %d %s", code, body)
	}
	if applied.SafetyBackup.Reason != jobs.ReasonBeforeImport || !applied.Restored.Settings {
		t.Fatalf("applied %+v", applied)
	}
	code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/backups"})
	if code != 200 || !strings.Contains(body, `"keep":5`) {
		t.Errorf("keep after restore: %d %s", code, body)
	}

	// A used preview is gone.
	if code, body := post(t, srv, "POST", "/api/v2/backups/apply", `{"previewId":"`+prev.PreviewID+`"}`); code != 404 || errCode(body) != "preview_expired" {
		t.Errorf("apply twice: %d %s", code, body)
	}

	// Upload the downloaded file as a multipart preview.
	code, file, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/backups/" + made.ID + "/download"})
	if code != 200 {
		t.Fatalf("download: %d", code)
	}
	upload, ctype := backupUpload(t, made.Name, []byte(file))
	code, body, _ = do(t, nil, srv, call{method: "POST", path: "/api/v2/backups/preview", body: upload, ctype: ctype})
	if code != 200 || !strings.Contains(body, `"previewId"`) {
		t.Errorf("upload preview: %d %s", code, body)
	}

	// Something that is not a backup is refused, and nothing is kept.
	junk, jtype := backupUpload(t, "notes.json.gz", []byte("hello"))
	code, body, _ = do(t, nil, srv, call{method: "POST", path: "/api/v2/backups/preview", body: junk, ctype: jtype})
	if code != 400 || errCode(body) != "not_a_backup" {
		t.Errorf("junk preview: %d %s", code, body)
	}
}

func TestPreviewsExpire(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.part")
	if err := os.WriteFile(old, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &previewBook{m: map[string]stagedPreview{}}
	b.m["old"] = stagedPreview{path: old, expires: time.Now().Add(-time.Second)}
	if _, ok := b.get("old"); ok {
		t.Fatal("an expired preview was still there")
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("the expired preview's file was kept: %v", err)
	}

	fresh := filepath.Join(dir, "fresh.part")
	if err := os.WriteFile(fresh, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := b.add(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := b.get(id); !ok || p.path != fresh {
		t.Fatalf("a fresh preview is missing: %+v %v", p, ok)
	}
	b.drop(id)
	if _, ok := b.get(id); ok {
		t.Error("a dropped preview is still there")
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Errorf("a dropped preview's file was kept: %v", err)
	}
}
