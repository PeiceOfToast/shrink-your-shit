// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/safety"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// TrashItem is one original in the trash, as Settings > Trash draws it (page 11). id is the journal row, as text.
type TrashItem struct {
	ID        string `json:"id"`
	File      string `json:"file"`
	Library   string `json:"library"`
	TrashedAt string `json:"trashedAt"`
	DaysLeft  int    `json:"daysLeft"`
}

// trashRefusals are the three refusals of a restore, each with its code and the sentence the page shows.
var trashRefusals = map[error][2]string{
	safety.ErrOriginalGone:     {"original_gone", "The original isn't in the trash any more."},
	safety.ErrConvertedChanged: {"converted_changed", "The converted file was changed after it was made, so it's left alone."},
	safety.ErrNameTaken:        {"name_taken", "Another file now has this name in the folder."},
}

// getTrash is GET /trash: the originals still in the trash, newest first, with the days each has left.
func (s *Server) getTrash(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	ctx := r.Context()
	j := safety.NewJournal(rs.DB)
	entries, err := j.Trash(ctx)
	if err != nil {
		internalError(w)
		return
	}
	keep, err := j.KeepDays(ctx)
	if err != nil {
		internalError(w)
		return
	}
	libs, err := libraryPaths(ctx, rs.DB)
	if err != nil {
		internalError(w)
		return
	}
	now := time.Now()
	out := make([]TrashItem, 0, len(entries))
	for _, e := range entries {
		out = append(out, TrashItem{
			ID:        strconv.FormatInt(e.ID, 10),
			File:      filepath.Base(e.Original),
			Library:   libraryFor(libs, e.Original),
			TrashedAt: e.When.UTC().Format(time.RFC3339),
			DaysLeft:  daysLeft(keep, e.When, now),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// restoreTrash is POST /trash/{id}/restore: the original goes back and the converted file is removed. A refusal is a
// 409 with its code and sentence, and changes nothing.
func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "trash_not_found", "That file isn't in the trash.", "", 0)
		return
	}
	j := safety.NewJournal(rs.DB)
	row, _, err := j.Entry(r.Context(), id)
	if errors.Is(err, safety.ErrNotFound) {
		writeError(w, http.StatusNotFound, "trash_not_found", "That file isn't in the trash.", "", 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	err = safety.Restore(r.Context(), j, id)
	switch {
	case err == nil:
		// The job's History shows the restore (page 03).
		if rs.Events != nil {
			_ = rs.Events.Emit(events.Event{JobID: row.JobID, Level: events.LevelInfo, Kind: events.KindRestored,
				Source: "safety", Code: "replace.restored", Message: safety.RestoredSentence})
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	case errors.Is(err, safety.ErrNotFound):
		writeError(w, http.StatusNotFound, "trash_not_found", "That file isn't in the trash.", "", 0)
		return
	}
	for refusal, cs := range trashRefusals {
		if errors.Is(err, refusal) {
			writeError(w, http.StatusConflict, cs[0], cs[1], "", 0)
			return
		}
	}
	internalError(w)
}

// daysLeft is how many whole days the copy has left: keep minus the days it has been there, never below 0.
func daysLeft(keep int, when, now time.Time) int {
	elapsed := int(now.Sub(when).Hours() / 24)
	left := keep - elapsed
	if left < 0 {
		return 0
	}
	return left
}

type libPath struct{ name, path string }

// libraryPaths reads each library's name and folder, for naming a trash entry by its library.
func libraryPaths(ctx context.Context, db *store.DB) ([]libPath, error) {
	rows, err := db.Read().QueryContext(ctx, `SELECT name, path FROM libraries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []libPath
	for rows.Next() {
		var l libPath
		if err := rows.Scan(&l.name, &l.path); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// libraryFor names the library a file is in: the one whose folder is the longest prefix of the file's path.
func libraryFor(libs []libPath, file string) string {
	best := ""
	name := ""
	for _, l := range libs {
		if strings.HasPrefix(file, l.path+string(filepath.Separator)) && len(l.path) > len(best) {
			best = l.path
			name = l.name
		}
	}
	return name
}
