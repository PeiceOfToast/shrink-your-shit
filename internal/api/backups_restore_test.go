// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// upload posts a file as a backup preview, signed in.
func upload(t *testing.T, e *env, cookie *http.Cookie, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("file", "backup.db")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	headers := write(withSession(cookie))
	headers["Content-Type"] = mw.FormDataContentType()
	return e.do(http.MethodPost, "/api/v2/backups/preview", buf.String(), headers, false)
}

func makeBackup(t *testing.T, e *env, cookie *http.Cookie) string {
	t.Helper()
	rec := e.do(http.MethodPost, "/api/v2/backups", "", write(withSession(cookie)), false)
	var b struct {
		ID string `json:"id"`
	}
	decodeInto(t, rec, &b)
	return b.ID
}

func previewKept(t *testing.T, e *env, cookie *http.Cookie, backupID string) string {
	t.Helper()
	rec := e.do(http.MethodPost, "/api/v2/backups/preview", `{"backupId":"`+backupID+`"}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", rec.Code, rec.Body)
	}
	var p struct {
		PreviewID string `json:"previewId"`
	}
	decodeInto(t, rec, &p)
	return p.PreviewID
}

func applyPreview(e *env, cookie *http.Cookie, previewID string) *httptest.ResponseRecorder {
	return e.do(http.MethodPost, "/api/v2/backups/apply", `{"previewId":"`+previewID+`"}`, write(withSession(cookie)), false)
}

func setServerName(t *testing.T, e *env, cookie *http.Cookie, name string) {
	t.Helper()
	rec := e.do(http.MethodPatch, "/api/v2/settings", `{"general":{"serverName":"`+name+`"}}`, write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("set serverName = %d: %s", rec.Code, rec.Body)
	}
}

func serverNameOf(t *testing.T, e *env, cookie *http.Cookie) string {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/v2/settings", "", withSession(cookie), false)
	var doc struct {
		General struct {
			ServerName string `json:"serverName"`
		} `json:"general"`
	}
	decodeInto(t, rec, &doc)
	return doc.General.ServerName
}

// The download carries no password hash and no token: their bytes are not in the file.
func TestBackupDownloadHasNoSecrets(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	var hash, sealed []byte
	var hashText string
	if err := e.db.Read().QueryRow(`SELECT password_hash FROM auth WHERE id = 1`).Scan(&hashText); err != nil {
		t.Fatal(err)
	}
	hash = []byte(hashText)
	if err := e.db.Read().QueryRow(`SELECT sealed FROM tokens LIMIT 1`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	id := makeBackup(t, e, cookie)
	rec := e.do(http.MethodGet, "/api/v2/backups/"+id+"/download", "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d", rec.Code)
	}
	if bytes.Contains(rec.Body.Bytes(), hash) {
		t.Error("the download holds the password hash")
	}
	if bytes.Contains(rec.Body.Bytes(), sealed) {
		t.Error("the download holds the sealed access token")
	}
	stored, err := os.ReadFile(filepath.Join(e.db.BackupFolder(), id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stored, hash) {
		t.Error("the stored copy lost the hash; it must stay whole")
	}
}

// A preview and an apply bring the set-up back: the setting the person changed reads its backed-up value again, and the
// safety backup is listed.
func TestRestoreBringsTheSetUpBack(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	setServerName(t, e, cookie, "Den")
	id := makeBackup(t, e, cookie)
	setServerName(t, e, cookie, "Live")
	pid := previewKept(t, e, cookie, id)
	rec := applyPreview(e, cookie, pid)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Restored     map[string]any `json:"restored"`
		SafetyBackup struct {
			ID string `json:"id"`
		} `json:"safetyBackup"`
	}
	decodeInto(t, rec, &out)
	if got := serverNameOf(t, e, cookie); got != "Den" {
		t.Errorf("serverName = %q after the restore, want Den", got)
	}
	if out.SafetyBackup.ID == "" || out.Restored["settings"] != true {
		t.Errorf("apply = %+v", out)
	}
	list := e.do(http.MethodGet, "/api/v2/backups", "", withSession(cookie), false)
	if !strings.Contains(list.Body.String(), out.SafetyBackup.ID) {
		t.Error("the safety backup is not in the list")
	}
}

// An uploaded backup that holds a different password hash does not change the password, and you stay signed in.
func TestUploadedBackupCannotChangeThePasswordOrSignYouOut(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
	stored, err := os.ReadFile(filepath.Join(e.db.BackupFolder(), id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	// Give the copy a different hash, as an old backup of another password would hold.
	dir := t.TempDir()
	other := filepath.Join(dir, "other.db")
	if err := os.WriteFile(other, stored, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE auth SET password_hash = 'OTHER-HASH' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	data, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	rec := upload(t, e, cookie, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload preview = %d: %s", rec.Code, rec.Body)
	}
	var p struct {
		PreviewID string `json:"previewId"`
	}
	decodeInto(t, rec, &p)
	if rec := applyPreview(e, cookie, p.PreviewID); rec.Code != http.StatusOK {
		t.Fatalf("apply = %d", rec.Code)
	}
	var hash string
	if err := e.db.Read().QueryRow(`SELECT password_hash FROM auth WHERE id = 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == "OTHER-HASH" {
		t.Error("the restore changed the password hash")
	}
	if rec := e.do(http.MethodGet, "/api/v2/settings", "", withSession(cookie), false); rec.Code != http.StatusOK {
		t.Errorf("the session after the restore = %d, want still signed in", rec.Code)
	}
}

// A file that is not a backup is refused, and nothing is left in the backups folder.
func TestUploadOfANonBackupIsRefused(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := upload(t, e, cookie, []byte("just some text, not a database"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "not_a_backup") {
		t.Errorf("non-backup = %d %s, want 400 not_a_backup", rec.Code, rec.Body)
	}
	left, _ := filepath.Glob(filepath.Join(e.db.BackupFolder(), "upload-*"))
	if len(left) != 0 {
		t.Errorf("the refused upload is still on disk: %v", left)
	}
}

// A backup from a newer schema is refused.
func TestUploadOfANewerBackupIsRefused(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
	stored, err := os.ReadFile(filepath.Join(e.db.BackupFolder(), id+".db"))
	if err != nil {
		t.Fatal(err)
	}
	newer := filepath.Join(t.TempDir(), "newer.db")
	if err := os.WriteFile(newer, stored, 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+newer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	data, err := os.ReadFile(newer)
	if err != nil {
		t.Fatal(err)
	}
	rec := upload(t, e, cookie, data)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "backup_too_new") {
		t.Errorf("newer = %d %s, want 409 backup_too_new", rec.Code, rec.Body)
	}
}

// A preview that has run out, or an id that was never made, is preview_expired.
func TestApplyAfterExpiryOrUnknownIsRefused(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
	pid := previewKept(t, e, cookie, id)
	e.srv.restores.mu.Lock()
	p := e.srv.restores.items[pid]
	p.expires = time.Now().Add(-time.Minute)
	e.srv.restores.items[pid] = p
	e.srv.restores.mu.Unlock()
	for _, bad := range []string{pid, "not-a-preview"} {
		rec := applyPreview(e, cookie, bad)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "preview_expired") {
			t.Errorf("apply %q = %d %s, want 404 preview_expired", bad, rec.Code, rec.Body)
		}
	}
}

// A backup whose library folder another library already uses is refused as a conflict, and the set-up stays as it was.
func TestRestoreConflictChangesNothing(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	setServerName(t, e, cookie, "Den")
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO libraries(id, name, path) VALUES(1, 'Films', '/shared/folder')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	id := makeBackup(t, e, cookie)
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM libraries WHERE id = 1`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO libraries(id, name, path) VALUES(9, 'Other', '/shared/folder')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	setServerName(t, e, cookie, "Live")
	pid := previewKept(t, e, cookie, id)
	rec := applyPreview(e, cookie, pid)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "restore_conflict") {
		t.Fatalf("apply = %d %s, want 409 restore_conflict", rec.Code, rec.Body)
	}
	if got := serverNameOf(t, e, cookie); got != "Live" {
		t.Errorf("serverName = %q after a refused restore, want Live", got)
	}
}

// With backupKeep 1, the restore's safety backup is the one kept.
func TestApplyKeepsOnlyTheSafetyBackupAtKeepOne(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	if rec := e.do(http.MethodPatch, "/api/v2/settings", `{"backupKeep":1}`, write(withSession(cookie)), false); rec.Code != http.StatusOK {
		t.Fatalf("keep = %d", rec.Code)
	}
	id := makeBackup(t, e, cookie)
	pid := previewKept(t, e, cookie, id)
	rec := applyPreview(e, cookie, pid)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply = %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		SafetyBackup struct {
			ID string `json:"id"`
		} `json:"safetyBackup"`
	}
	decodeInto(t, rec, &out)
	list := e.do(http.MethodGet, "/api/v2/backups", "", withSession(cookie), false)
	var l struct {
		Backups []struct {
			ID string `json:"id"`
		} `json:"backups"`
	}
	decodeInto(t, list, &l)
	if len(l.Backups) != 1 || l.Backups[0].ID != out.SafetyBackup.ID {
		t.Errorf("kept backups = %+v, want only the safety backup %s", l.Backups, out.SafetyBackup.ID)
	}
}

// A download can be restored: the file a person saves is a backup the preview accepts.
func TestDownloadRoundTripRestores(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	setServerName(t, e, cookie, "Den")
	id := makeBackup(t, e, cookie)
	dl := e.do(http.MethodGet, "/api/v2/backups/"+id+"/download", "", withSession(cookie), false)
	if dl.Code != http.StatusOK {
		t.Fatalf("download = %d", dl.Code)
	}
	setServerName(t, e, cookie, "Live")
	rec := upload(t, e, cookie, dl.Body.Bytes())
	if rec.Code != http.StatusOK {
		t.Fatalf("upload of the download = %d: %s", rec.Code, rec.Body)
	}
	var p struct {
		PreviewID string `json:"previewId"`
	}
	decodeInto(t, rec, &p)
	if rec := applyPreview(e, cookie, p.PreviewID); rec.Code != http.StatusOK {
		t.Fatalf("apply = %d: %s", rec.Code, rec.Body)
	}
	if got := serverNameOf(t, e, cookie); got != "Den" {
		t.Errorf("serverName = %q after restoring the download, want Den", got)
	}
}

// A restore while a job runs is refused as jobs_running, and no safety backup is made for it.
func TestApplyRefusedWhileAJobRuns(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	id := makeBackup(t, e, cookie)
	pid := previewKept(t, e, cookie, id)
	if err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO jobs(file_id, library_id, path, state, queued_at) VALUES(1, 1, '/x.mkv', 'running', 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := countBackups(t, e, cookie)
	rec := applyPreview(e, cookie, pid)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "jobs_running") {
		t.Fatalf("apply = %d %s, want 409 jobs_running", rec.Code, rec.Body)
	}
	if after := countBackups(t, e, cookie); after != before {
		t.Errorf("backups = %d after a refused apply, want %d (no safety backup for it)", after, before)
	}
}

// A restore that fails midway is 500 restore_failed, names the safety backup made just before it, and leaves the set-up
// as it was.
func TestApplyFailureChangesNothingAndNamesTheSafetyBackup(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	setServerName(t, e, cookie, "Den")
	id := makeBackup(t, e, cookie)
	setServerName(t, e, cookie, "Live")
	pid := previewKept(t, e, cookie, id)
	e.srv.restoreHook = func(table string) error {
		if table == "libraries" {
			return errBoomForTest
		}
		return nil
	}
	defer func() { e.srv.restoreHook = nil }()
	rec := applyPreview(e, cookie, pid)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "restore_failed") {
		t.Fatalf("apply = %d %s, want 500 restore_failed", rec.Code, rec.Body)
	}
	list := e.do(http.MethodGet, "/api/v2/backups", "", withSession(cookie), false)
	var l struct {
		Backups []struct {
			ID string `json:"id"`
		} `json:"backups"`
	}
	decodeInto(t, list, &l)
	if len(l.Backups) == 0 || !strings.Contains(rec.Body.String(), l.Backups[0].ID) {
		t.Errorf("the message does not name the safety backup %v: %s", l.Backups, rec.Body)
	}
	if got := serverNameOf(t, e, cookie); got != "Live" {
		t.Errorf("serverName = %q after a failed restore, want Live (rolled back)", got)
	}
}

var errBoomForTest = errors.New("disk went away")

func countBackups(t *testing.T, e *env, cookie *http.Cookie) int {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/v2/backups", "", withSession(cookie), false)
	var l struct {
		Backups []json.RawMessage `json:"backups"`
	}
	decodeInto(t, rec, &l)
	return len(l.Backups)
}
