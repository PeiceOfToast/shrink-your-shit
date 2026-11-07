// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// The Done and Kept originals lists (UI-API-CONTRACT.md "Activity", slice 17). Restore and purge are
// slice 18.
func activityRoutes(api *http.ServeMux, store *jobs.Store, ss *settingsStore, now func() time.Time) {
	page := func(r *http.Request) (limit, offset int) {
		limit, offset = 50, 0
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
			limit = min(n, 500)
		}
		if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
			offset = n
		}
		return
	}
	// daysLeft is how many days until deleteOn, rounded up (0 once it is due).
	daysLeft := func(deleteOn string) *int {
		t, err := time.Parse(time.RFC3339, deleteOn)
		if err != nil {
			return nil
		}
		d := max(0, int(math.Ceil(t.Sub(now()).Hours()/24)))
		return &d
	}

	api.HandleFunc("GET /api/activity/done", func(w http.ResponseWriter, r *http.Request) {
		limit, offset := page(r)
		items, total, err := store.DoneItems(limit, offset)
		if err != nil {
			serverError(w, err)
			return
		}
		out := []map[string]any{}
		for _, d := range items {
			// beside: the original was never touched, so it is still there ("kept", with no trash entry)
			state, keptID, left := "deleted", any(nil), (*int)(nil)
			if d.KeptID != 0 {
				state, keptID, left = "kept", strconv.FormatInt(d.KeptID, 10), daysLeft(d.DeleteOn)
			} else if d.Beside {
				state = "kept"
			}
			out = append(out, map[string]any{"jobId": strconv.FormatInt(d.JobID, 10), "library": d.Library, "path": d.Path,
				"beforeBytes": d.BeforeBytes, "afterBytes": d.AfterBytes, "originalState": state, "keptDaysLeft": left, "keptId": keptID})
		}
		writeJSON(w, map[string]any{"items": out, "total": total})
	})

	api.HandleFunc("GET /api/activity/kept", func(w http.ResponseWriter, r *http.Request) {
		items, err := store.KeptItems()
		if err != nil {
			serverError(w, err)
			return
		}
		s, err := ss.load()
		if err != nil {
			serverError(w, err)
			return
		}
		var bytes int64
		out := []map[string]any{}
		for _, k := range items {
			bytes += k.Bytes
			out = append(out, map[string]any{"keptId": strconv.FormatInt(k.ID, 10), "library": k.Library, "path": k.Path,
				"originalBytes": k.Bytes, "nowBytes": k.NewBytes, "deleteOn": k.DeleteOn})
		}
		writeJSON(w, map[string]any{"keptBytes": bytes, "count": len(items), "days": s.KeepOriginalsDays, "items": out})
	})

	api.HandleFunc("POST /api/kept/{keptId}/restore", func(w http.ResponseWriter, r *http.Request) {
		apiError(w, http.StatusNotImplemented, "not_available", "Restoring an original comes in the next update. It is kept until then.")
	})
}
