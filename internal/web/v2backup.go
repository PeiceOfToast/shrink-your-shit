// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// previewLife is how long a preview waits for its apply.
const previewLife = 15 * time.Minute

// stagedPreview is a backup that was previewed. Its file is always a copy made for the preview, so pruning
// the stored backups cannot remove it before the apply.
type stagedPreview struct {
	path    string
	expires time.Time
}

// previewBook holds the previews of this process. A restart forgets them: the UI asks again.
type previewBook struct {
	mu sync.Mutex
	m  map[string]stagedPreview
}

func (b *previewBook) add(path string) (string, error) {
	var rnd [16]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(rnd[:])
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweepLocked(time.Now())
	b.m[id] = stagedPreview{path: path, expires: time.Now().Add(previewLife)}
	return id, nil
}

// get returns a preview that has not expired.
func (b *previewBook) get(id string) (stagedPreview, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sweepLocked(time.Now())
	p, ok := b.m[id]
	return p, ok
}

// drop forgets a preview and removes its file.
func (b *previewBook) drop(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if p, ok := b.m[id]; ok {
		os.Remove(p.path)
		delete(b.m, id)
	}
}

func (b *previewBook) sweepLocked(now time.Time) {
	for id, p := range b.m {
		if now.After(p.expires) {
			os.Remove(p.path)
			delete(b.m, id)
		}
	}
}

// v2Backups is item 5 (UI-API-CONTRACT-v2.md "Backup"): list, make, download, delete, and the two-step import
// (preview, then apply).
func v2Backups(api *http.ServeMux, store *jobs.Store, ss *settingsStore) {
	previews := &previewBook{m: map[string]stagedPreview{}}
	api.HandleFunc("GET /api/v2/backups", func(w http.ResponseWriter, r *http.Request) {
		list, err := store.ListBackups()
		if err != nil {
			serverError(w, err)
			return
		}
		ss.mu.Lock()
		s, err := ss.load()
		ss.mu.Unlock()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"backups": list, "keep": s.BackupKeep})
	})
	api.HandleFunc("POST /api/v2/backups", func(w http.ResponseWriter, r *http.Request) {
		b, err := store.WriteBackup(jobs.ReasonManual)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSONStatus(w, http.StatusCreated, b)
	})
	api.HandleFunc("GET /api/v2/backups/{id}/download", func(w http.ResponseWriter, r *http.Request) {
		path, b, err := store.BackupFile(r.PathValue("id"))
		if errors.Is(err, jobs.ErrNoBackup) {
			apiError(w, http.StatusNotFound, "not_found", "There is no backup with that id.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		f, err := os.Open(path)
		if err != nil {
			serverError(w, err)
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+b.Name+`"`)
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		io.Copy(w, f)
	})
	api.HandleFunc("DELETE /api/v2/backups/{id}", func(w http.ResponseWriter, r *http.Request) {
		err := store.DeleteBackup(r.PathValue("id"))
		if errors.Is(err, jobs.ErrNoBackup) {
			apiError(w, http.StatusNotFound, "not_found", "There is no backup with that id.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("POST /api/v2/backups/preview", func(w http.ResponseWriter, r *http.Request) {
		staged, err := stageForPreview(store, w, r)
		if err != nil {
			switch {
			case errors.Is(err, jobs.ErrTooLarge):
				apiError(w, http.StatusRequestEntityTooLarge, "too_large", "This file is larger than 200 MB, too large to be a backup.")
			case errors.Is(err, jobs.ErrNoBackup):
				apiError(w, http.StatusNotFound, "not_found", "There is no backup with that id.")
			case errors.Is(err, errNoBackupFile):
				apiError(w, http.StatusBadRequest, "not_a_backup", "This file isn't a Shrink Your Shit backup.")
			default:
				serverError(w, err)
			}
			return
		}
		p, err := store.PreviewBackup(staged)
		if err != nil {
			os.Remove(staged)
			var tooNew *jobs.TooNewError
			switch {
			case errors.Is(err, jobs.ErrNotABackup):
				apiError(w, http.StatusBadRequest, "not_a_backup", "This file isn't a Shrink Your Shit backup.")
			case errors.As(err, &tooNew):
				apiError(w, http.StatusConflict, "backup_too_new", "This backup is from version "+tooNew.BackupVersion+", newer than this app ("+tooNew.ThisVersion+"). Update the app, then restore it.")
			default:
				serverError(w, err)
			}
			return
		}
		id, err := previews.add(staged)
		if err != nil {
			os.Remove(staged)
			serverError(w, err)
			return
		}
		writeJSON(w, backupPreviewOut{PreviewID: id, BackupPreview: p})
	})
	api.HandleFunc("POST /api/v2/backups/apply", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			PreviewID string `json:"previewId"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
			apiError(w, http.StatusBadRequest, "bad_request", "The request isn't valid JSON.")
			return
		}
		p, ok := previews.get(in.PreviewID)
		if !ok {
			apiError(w, http.StatusNotFound, "preview_expired", "This preview has expired. Choose the file again.")
			return
		}
		// Refused before anything changes: a preview kept for a retry after the queue is paused.
		if n, err := store.CountRunning(); err != nil {
			serverError(w, err)
			return
		} else if n > 0 {
			jobsRunning(w)
			return
		}
		safety, err := store.WriteBackup(jobs.ReasonBeforeImport)
		if err != nil {
			serverError(w, err)
			return
		}
		contents, err := store.RestoreBackup(p.path)
		if errors.Is(err, jobs.ErrJobsRunning) {
			jobsRunning(w)
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		previews.drop(in.PreviewID)
		writeJSON(w, map[string]any{"restored": contents, "safetyBackup": safety})
	})
}

// errNoBackupFile means a preview request had neither a file nor a backup id.
var errNoBackupFile = errors.New("no backup file")

// backupPreviewOut is the contract's BackupPreview: the preview's id with what the file holds.
type backupPreviewOut struct {
	PreviewID string `json:"previewId"`
	jobs.BackupPreview
}

// stageForPreview copies the backup a preview request names into a file this preview owns: an uploaded
// multipart "file", or the stored backup named by JSON {backupId}.
func stageForPreview(store *jobs.Store, w http.ResponseWriter, r *http.Request) (string, error) {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt == "multipart/form-data" {
		mr, err := r.MultipartReader()
		if err != nil {
			return "", errNoBackupFile
		}
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				return "", errNoBackupFile
			}
			if err != nil {
				return "", errNoBackupFile
			}
			if part.FormName() == "file" {
				defer part.Close()
				return store.StageBackup(part)
			}
		}
	}
	var in struct {
		BackupID string `json:"backupId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil || strings.TrimSpace(in.BackupID) == "" {
		return "", errNoBackupFile
	}
	path, _, err := store.BackupFile(in.BackupID)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return store.StageBackup(f)
}

func jobsRunning(w http.ResponseWriter) {
	apiError(w, http.StatusConflict, "jobs_running", "Files are converting. Pause the queue and wait for them to finish, then restore. Nothing was changed.")
}
