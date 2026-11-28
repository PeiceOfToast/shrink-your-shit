// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Preview then apply (page 11, Backup). A preview reads a backup, either an upload or a kept one, and holds it for 15
// minutes under an id. An apply restores the set-up from that preview: a safety backup first, then the restore in one
// transaction. The preview is used up by the apply, whatever its outcome.

const previewTTL = 15 * time.Minute

// backupUploadLimit caps an uploaded backup. A test lowers it.
var backupUploadLimit int64 = 2 << 30

// restorePreview is a backup held for an apply. owned is true for an upload, whose file the server must remove.
type restorePreview struct {
	path       string
	owned      bool
	createdAt  string
	appVersion string
	expires    time.Time
	contents   store.Contents
	warnings   []string
}

// restoreBook holds the previews by id. Its zero value is ready to use.
type restoreBook struct {
	mu    sync.Mutex
	items map[string]restorePreview
}

// add holds a preview and returns its id, after removing the expired previews (their owned files are returned to remove).
func (b *restoreBook) add(p restorePreview, now time.Time) (string, []string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	id := hex.EncodeToString(raw)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.items == nil {
		b.items = map[string]restorePreview{}
	}
	gone := b.sweepLocked(now)
	p.expires = now.Add(previewTTL)
	b.items[id] = p
	return id, gone, nil
}

// take removes a preview and returns it, if it is held and not expired. An expired one is removed too.
func (b *restoreBook) take(id string, now time.Time) (restorePreview, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.items[id]
	if !ok {
		return restorePreview{}, false
	}
	delete(b.items, id)
	if now.After(p.expires) {
		return restorePreview{}, false
	}
	return p, true
}

// sweep removes the expired previews and returns the owned files they held.
func (b *restoreBook) sweep(now time.Time) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sweepLocked(now)
}

func (b *restoreBook) sweepLocked(now time.Time) []string {
	var gone []string
	for id, p := range b.items {
		if now.After(p.expires) {
			delete(b.items, id)
			if p.owned {
				gone = append(gone, p.path)
			}
		}
	}
	return gone
}

// previewJSON is the preview the UI shows before a restore is confirmed.
type previewJSON struct {
	PreviewID  string         `json:"previewId"`
	CreatedAt  string         `json:"createdAt"`
	AppVersion string         `json:"appVersion"`
	Contents   store.Contents `json:"contents"`
	Warnings   []string       `json:"warnings"`
}

// postBackupPreview is POST /api/v2/backups/preview: a backup file uploaded as "file" (multipart), or {backupId} for a
// kept backup. It answers what the backup holds, and holds it for an apply.
func (s *Server) postBackupPreview(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if !requireBackupSession(w, r, rs) {
		return
	}
	ctx := r.Context()
	now := time.Now()
	live, err := rs.DB.CurrentSchema(ctx)
	if err != nil {
		internalError(w)
		return
	}
	removeFiles(s.restores.sweep(now))

	var p restorePreview
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		var ok bool
		p, ok = s.previewUpload(w, r, rs, live, now)
		if !ok {
			return
		}
	} else {
		var ok bool
		p, ok = s.previewKept(w, r, rs, live)
		if !ok {
			return
		}
	}
	id, gone, err := s.restores.add(p, now)
	removeFiles(gone)
	if err != nil {
		removeOwned(p)
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, previewJSON{
		PreviewID: id, CreatedAt: p.createdAt, AppVersion: p.appVersion, Contents: p.contents, Warnings: p.warnings,
	})
}

// previewUpload stores the uploaded file in the backups folder and validates it before anything else.
func (s *Server) previewUpload(w http.ResponseWriter, r *http.Request, rs *Deps, live int, now time.Time) (restorePreview, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, backupUploadLimit)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That file is too large to be a backup.", "", 0)
			return restorePreview{}, false
		}
		writeError(w, http.StatusBadRequest, "not_a_backup", "Choose a backup file to upload.", "", 0)
		return restorePreview{}, false
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "not_a_backup", "Choose a backup file to upload.", "", 0)
		return restorePreview{}, false
	}
	defer file.Close()
	if err := os.MkdirAll(rs.DB.BackupFolder(), 0o755); err != nil {
		internalError(w)
		return restorePreview{}, false
	}
	tmp := filepath.Join(rs.DB.BackupFolder(), "upload-"+strconv.FormatInt(now.UnixNano(), 10)+".db")
	out, err := os.Create(tmp)
	if err != nil {
		internalError(w)
		return restorePreview{}, false
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		os.Remove(tmp)
		writeError(w, http.StatusRequestEntityTooLarge, "too_large", "That file is too large to be a backup.", "", 0)
		return restorePreview{}, false
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		internalError(w)
		return restorePreview{}, false
	}
	contents, schema, err := store.ValidateBackup(r.Context(), tmp, live)
	if !validateOK(w, err, tmp) {
		return restorePreview{}, false
	}
	p := restorePreview{path: tmp, owned: true, createdAt: now.UTC().Format(time.RFC3339), contents: contents}
	if schema < live {
		p.warnings = append(p.warnings, fmt.Sprintf("This backup is from an older schema (%d). The set-up it holds is restored; anything it does not have keeps its value.", schema))
	}
	return p, true
}

// previewKept previews a backup the server keeps.
func (s *Server) previewKept(w http.ResponseWriter, r *http.Request, rs *Deps, live int) (restorePreview, bool) {
	var body struct {
		BackupID string `json:"backupId"`
	}
	if !decode(w, r, &body) {
		return restorePreview{}, false
	}
	path, err := rs.DB.BackupPath(body.BackupID)
	if errors.Is(err, store.ErrBackupNotFound) {
		writeError(w, http.StatusNotFound, "backup_not_found", "That backup doesn't exist.", "", 0)
		return restorePreview{}, false
	}
	if err != nil {
		internalError(w)
		return restorePreview{}, false
	}
	contents, schema, err := store.ValidateBackup(r.Context(), path, live)
	if !validateOK(w, err, "") {
		return restorePreview{}, false
	}
	p := restorePreview{path: path, contents: contents}
	if list, err := rs.DB.ListBackups(); err == nil {
		for _, b := range list {
			if b.ID == body.BackupID {
				p.createdAt = b.CreatedAt.Format(time.RFC3339)
				p.appVersion = b.AppVersion
			}
		}
	}
	if schema < live {
		p.warnings = append(p.warnings, fmt.Sprintf("This backup is from an older schema (%d). The set-up it holds is restored; anything it does not have keeps its value.", schema))
	}
	return p, true
}

// validateOK answers the refusal for a backup that cannot be restored, and reports whether the backup is fine. tmp, when
// set, is an upload's file, removed on refusal.
func validateOK(w http.ResponseWriter, err error, tmp string) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, store.ErrNotABackup):
		removeFile(tmp)
		writeError(w, http.StatusBadRequest, "not_a_backup", "That file is not a backup of this app.", "", 0)
	case errors.Is(err, store.ErrBackupTooNew):
		removeFile(tmp)
		writeError(w, http.StatusConflict, "backup_too_new", "That backup is from a newer version of the app. Update the app first.", "", 0)
	default:
		removeFile(tmp)
		internalError(w)
	}
	return false
}

// postBackupApply is POST /api/v2/backups/apply {previewId}: a safety backup, then the restore of the set-up in one
// transaction. Jobs, history, the trash and files are not touched, and neither are the sessions.
func (s *Server) postBackupApply(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if !requireBackupSession(w, r, rs) {
		return
	}
	var body struct {
		PreviewID string `json:"previewId"`
	}
	if !decode(w, r, &body) {
		return
	}
	ctx := r.Context()
	now := time.Now()
	p, ok := s.restores.take(body.PreviewID, now)
	if !ok {
		writeError(w, http.StatusNotFound, "preview_expired", "That preview has expired. Preview the backup again.", "", 0)
		return
	}
	if p.owned {
		defer removeFile(p.path)
	}
	tally, err := rs.Jobs.Tally(ctx, 0)
	if err != nil {
		internalError(w)
		return
	}
	if tally.Running > 0 {
		writeError(w, http.StatusConflict, "jobs_running", "Wait for the running jobs to finish, then restore.", "", 0)
		return
	}
	safety, err := rs.DB.MakeBackup("manual", rs.AppVersion, now)
	if err != nil {
		internalError(w)
		return
	}
	report, err := rs.DB.RestoreSetUp(ctx, p.path, nil)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "restore_conflict",
				"A library in the backup uses a folder that another library already has. Nothing was restored.", "", 0)
			return
		}
		writeError(w, http.StatusInternalServerError, "restore_failed",
			"The restore failed and nothing was changed. The backup made just before it is "+safety.ID+".", "", 0)
		return
	}
	var warnings []string
	keep, err := s.keepCount(r, rs)
	if err != nil {
		internalError(w)
		return
	}
	if err := rs.DB.PruneBackups(keep, safety.ID); err != nil {
		warnings = append(warnings, "The old backups were not all removed: "+err.Error())
	}
	if warnings == nil {
		warnings = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"restored":        report.Restored,
		"safetyBackup":    backupItemOf(safety),
		"keptNotInBackup": report.KeptNotInBackup,
		"warnings":        warnings,
	})
}

func removeFile(path string) {
	if path != "" {
		os.Remove(path)
	}
}

func removeFiles(paths []string) {
	for _, p := range paths {
		removeFile(p)
	}
}

func removeOwned(p restorePreview) {
	if p.owned {
		removeFile(p.path)
	}
}
