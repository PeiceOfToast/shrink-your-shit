// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"net/http"
	"strings"
	"testing"
)

// The backup routes need the person's session: the bearer token alone is refused, and so is no sign-in.
func TestBackupRoutesNeedASession(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	token := backupTestToken(t, e, cookie)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/v2/backups"},
		{http.MethodPost, "/api/v2/backups"},
		{http.MethodDelete, "/api/v2/backups/1"},
		{http.MethodGet, "/api/v2/backups/1/download"},
	} {
		if rec := e.do(c.method, c.path, "", write(map[string]string{"Authorization": "Bearer " + token}), false); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with only the bearer token = %d, want 401", c.method, c.path, rec.Code)
		}
		if rec := e.do(c.method, c.path, "", write(nil), false); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s with no session = %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

// Making a backup answers 201 with its details; the list shows it with the keep count (page 11's default 10).
func TestBackupMakeAndList(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodPost, "/api/v2/backups", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("make = %d: %s", rec.Code, rec.Body)
	}
	var made struct {
		ID         string `json:"id"`
		Reason     string `json:"reason"`
		AppVersion string `json:"appVersion"`
		SizeBytes  int64  `json:"sizeBytes"`
	}
	decodeInto(t, rec, &made)
	if made.ID == "" || made.Reason != "manual" || made.SizeBytes == 0 {
		t.Errorf("made = %+v", made)
	}
	rec = e.do(http.MethodGet, "/api/v2/backups", "", withSession(cookie), false)
	var list struct {
		Backups []struct {
			ID string `json:"id"`
		} `json:"backups"`
		Keep int `json:"keep"`
	}
	decodeInto(t, rec, &list)
	if len(list.Backups) != 1 || list.Backups[0].ID != made.ID || list.Keep != 10 {
		t.Errorf("list = %+v", list)
	}
}

// Making a backup keeps only the newest backupKeep of them: the setting is changed through the settings route.
func TestBackupKeepPrunesTheOldest(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	if rec := e.do(http.MethodPatch, "/api/v2/settings", `{"backupKeep":1}`, write(withSession(cookie)), false); rec.Code != http.StatusOK {
		t.Fatalf("set backupKeep = %d: %s", rec.Code, rec.Body)
	}
	var newest string
	for i := 0; i < 3; i++ {
		rec := e.do(http.MethodPost, "/api/v2/backups", "", write(withSession(cookie)), false)
		if rec.Code != http.StatusCreated {
			t.Fatalf("make %d = %d", i, rec.Code)
		}
		var b struct {
			ID string `json:"id"`
		}
		decodeInto(t, rec, &b)
		newest = b.ID
	}
	rec := e.do(http.MethodGet, "/api/v2/backups", "", withSession(cookie), false)
	var list struct {
		Backups []struct {
			ID string `json:"id"`
		} `json:"backups"`
	}
	decodeInto(t, rec, &list)
	if len(list.Backups) != 1 || list.Backups[0].ID != newest {
		t.Errorf("with keep 1 the list is %+v, want only the newest %s", list.Backups, newest)
	}
}

// Delete removes the backup (204), a second delete and a bad id are 404 backup_not_found.
func TestBackupDelete(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodPost, "/api/v2/backups", "", write(withSession(cookie)), false)
	var b struct {
		ID string `json:"id"`
	}
	decodeInto(t, rec, &b)
	if rec := e.do(http.MethodDelete, "/api/v2/backups/"+b.ID, "", write(withSession(cookie)), false); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d", rec.Code)
	}
	// The router cleans a path with ".." before any handler runs (a redirect), so only the id itself is tested here;
	// the store refuses any id that is not its own (TestBackupIDsCannotNameAPath).
	for _, id := range []string{b.ID, "abc", "0"} {
		rec := e.do(http.MethodDelete, "/api/v2/backups/"+id, "", write(withSession(cookie)), false)
		if rec.Code != http.StatusNotFound {
			t.Errorf("delete %q = %d, want 404", id, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "backup_not_found") {
			t.Errorf("delete %q body = %s", id, rec.Body)
		}
	}
}

// The download is the backup's own file: a SQLite database, sent as an attachment.
func TestBackupDownloadIsTheCopy(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	rec := e.do(http.MethodPost, "/api/v2/backups", "", write(withSession(cookie)), false)
	var b struct {
		ID        string `json:"id"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	decodeInto(t, rec, &b)
	rec = e.do(http.MethodGet, "/api/v2/backups/"+b.ID+"/download", "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d", rec.Code)
	}
	// The download is a copy without the secret rows, so its size can differ from the stored copy's; it must be a SQLite file.
	if got := rec.Body.Bytes(); !strings.HasPrefix(string(got), "SQLite format 3\x00") {
		t.Errorf("download (%d bytes, stored %d) does not start as a SQLite file", len(got), b.SizeBytes)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

// backupTestToken reads the access token through the session, as the access page does.
func backupTestToken(t *testing.T, e *env, cookie *http.Cookie) string {
	t.Helper()
	rec := e.do(http.MethodGet, "/api/v2/access/token", "", withSession(cookie), false)
	var body struct {
		Token string `json:"token"`
	}
	decodeInto(t, rec, &body)
	return body.Token
}
