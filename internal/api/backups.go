// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/settings"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// The backup routes (page 11, Backup): the list, making one, deleting, and downloading. Each needs the person's session
// cookie. A backup is a copy of the whole database, which holds the password hash and the sealed token, so a bearer
// token is not enough to read or make one.

// requireBackupSession answers the request 401 signed_out unless it carries the person's live session cookie, and
// reports whether it did. A bearer token alone does not pass.
func requireBackupSession(w http.ResponseWriter, r *http.Request, rs *Deps) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		writeError(w, http.StatusUnauthorized, "signed_out", "Sign in first.", "", 0)
		return false
	}
	live, err := rs.Access.LookupSession(r.Context(), c.Value)
	if err != nil || !live {
		writeError(w, http.StatusUnauthorized, "signed_out", "Sign in first.", "", 0)
		return false
	}
	return true
}

// backupItem is the BackupItem shape in the UI's types.ts.
type backupItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	Reason     string `json:"reason"`
	AppVersion string `json:"appVersion"`
	SizeBytes  int64  `json:"sizeBytes"`
}

func backupItemOf(b store.Backup) backupItem {
	return backupItem{
		ID:         b.ID,
		Name:       "Backup from " + b.CreatedAt.Format("2 Jan 2006 15:04") + " UTC",
		CreatedAt:  b.CreatedAt.Format(time.RFC3339),
		Reason:     b.Reason,
		AppVersion: b.AppVersion,
		SizeBytes:  b.SizeBytes,
	}
}

// keepCount is how many backups are kept (page 11's backupKeep, 1 to 50).
func (s *Server) keepCount(r *http.Request, rs *Deps) (int, error) {
	doc, err := settings.Load(r.Context(), rs.DB, s.settingsOptions(rs))
	if err != nil {
		return 0, err
	}
	return doc.BackupKeep, nil
}

// getBackups is GET /api/v2/backups: the kept backups, newest first, and how many are kept.
func (s *Server) getBackups(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if !requireBackupSession(w, r, rs) {
		return
	}
	list, err := rs.DB.ListBackups()
	if err != nil {
		internalError(w)
		return
	}
	keep, err := s.keepCount(r, rs)
	if err != nil {
		internalError(w)
		return
	}
	items := make([]backupItem, 0, len(list))
	for _, b := range list {
		items = append(items, backupItemOf(b))
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": items, "keep": keep})
}

// postBackup is POST /api/v2/backups: makes a manual backup, then keeps only the newest keep of them. It answers 201
// with the backup.
func (s *Server) postBackup(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if !requireBackupSession(w, r, rs) {
		return
	}
	b, err := rs.DB.MakeBackup("manual", rs.AppVersion, time.Now())
	if err != nil {
		internalError(w)
		return
	}
	keep, err := s.keepCount(r, rs)
	if err != nil {
		internalError(w)
		return
	}
	if err := rs.DB.PruneBackups(keep, ""); err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusCreated, backupItemOf(b))
}

// deleteBackup is DELETE /api/v2/backups/{id}: 204, or 404 backup_not_found.
func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if !requireBackupSession(w, r, rs) {
		return
	}
	err := rs.DB.DeleteBackup(r.PathValue("id"))
	if errors.Is(err, store.ErrBackupNotFound) {
		writeError(w, http.StatusNotFound, "backup_not_found", "That backup doesn't exist.", "", 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// downloadBackup is GET /api/v2/backups/{id}/download: the backup's file, as an attachment.
func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if !requireBackupSession(w, r, rs) {
		return
	}
	path, err := rs.DB.BackupPath(r.PathValue("id"))
	if errors.Is(err, store.ErrBackupNotFound) {
		writeError(w, http.StatusNotFound, "backup_not_found", "That backup doesn't exist.", "", 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	// The stored copy keeps the auth, tokens and sessions rows (it is the copy an upgrade rolls back to). A download is a
	// copy with those rows removed, so the password hash and the token never leave the server.
	tmp := filepath.Join(rs.DB.BackupFolder(), "download-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".db")
	if err := store.StripSecrets(r.Context(), path, tmp); err != nil {
		internalError(w)
		return
	}
	defer removeFile(tmp)
	f, err := os.Open(tmp)
	if err != nil {
		internalError(w)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+r.PathValue("id")+`.db"`)
	http.ServeContent(w, r, "", time.Time{}, f)
}
