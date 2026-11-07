// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// filesRoutes is S5's Files view (UI-API-CONTRACT.md, "Workshop addition 21:00Z"): one list of files by
// card, and each job's run log. The old queue/skipped/failed/done/kept routes stay for one release.
func filesRoutes(api *http.ServeMux, store *jobs.Store, eng Engine) {
	api.HandleFunc("GET /api/activity/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		limit, offset := pageOf(r)
		f := jobs.FileFilter{Show: q.Get("show"), Q: q.Get("q"), Kept: q.Get("kept") == "1", Limit: limit, Offset: offset}
		for _, c := range strings.Split(q.Get("reason"), ",") { // skipped: reason=a,b (contract 22:30Z)
			if c = strings.TrimSpace(c); c != "" {
				f.Reasons = append(f.Reasons, c)
			}
		}
		if f.Show == "" {
			f.Show = jobs.CardUnprocessed
		}
		if lib := q.Get("library"); lib != "" {
			id, err := strconv.ParseInt(lib, 10, 64)
			if err != nil || id <= 0 {
				apiError(w, http.StatusBadRequest, "invalid", "library must be a library's id.")
				return
			}
			f.LibraryID = id
		}
		rows, total, counts, err := store.FileList(f)
		if err != nil {
			if strings.HasPrefix(err.Error(), "show must be") {
				apiError(w, http.StatusBadRequest, "invalid", "show must be "+strings.Join(jobs.Cards, ", ")+".")
				return
			}
			serverError(w, err)
			return
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		keepDays := 7 // kept originals' days left follow the setting now, as the purge does
		if s, err := LoadSettings(store); err == nil {
			keepDays = s.KeepOriginalsDays
		}
		items := []map[string]any{}
		for _, row := range rows {
			items = append(items, fileItem(store, libs, row, f.Show, keepDays))
		}
		writeJSON(w, map[string]any{"items": items, "total": total, "counts": counts})
	})

	api.HandleFunc("GET /api/activity/files/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			apiError(w, http.StatusNotFound, "not_found", "There is no such file.")
			return
		}
		j, err := store.Get(id)
		if err != nil {
			apiError(w, http.StatusNotFound, "not_found", "There is no such file.")
			return
		}
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		text, next, err := eng.RunLogs.Read(id, from)
		if err != nil {
			serverError(w, err)
			return
		}
		steps, err := store.Steps(id)
		if err != nil {
			serverError(w, err)
			return
		}
		if len(steps) == 0 { // a job from before S5: what its record says
			steps = append(steps, jobs.Step{Step: "found", At: j.Created, OK: true, Words: "Found in " + j.Library + "."})
			switch j.Status {
			case jobs.Done:
				if store.KeptBeside(id) {
					steps = append(steps, jobs.Step{Step: "kept", At: j.Updated, OK: true, Words: "The new file was written next to the original, which stays as it is."})
				} else {
					steps = append(steps, jobs.Step{Step: "replaced", At: j.Updated, OK: true, Words: "Converted."})
				}
			case jobs.Failed, jobs.Kept:
				steps = append(steps, jobs.Step{Step: "converted", At: j.Updated, OK: false, Words: j.Error})
			}
		}
		writeJSON(w, map[string]any{"summary": steps, "text": text, "next": next, "live": j.Status == jobs.Running})
	})
}

// pageOf reads ?limit= (default 50, at most 500) and ?offset=.
func pageOf(r *http.Request) (limit, offset int) {
	limit, offset = 50, 0
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 500)
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && n > 0 {
		offset = n
	}
	return
}

// fileItem is one row of the Files view: what every card has, plus the card's own fields.
func fileItem(store *jobs.Store, libs []jobs.Library, row jobs.FileRow, card string, keepDays int) map[string]any {
	it := map[string]any{"path": row.Path, "folder": filepath.Dir(row.Path), "name": filepath.Base(row.Path),
		"libraryId": nil, "library": row.Library, "codec": nil, "resolution": nil, "bytes": row.SizeBefore, "durationSec": nil}
	if row.LibraryID > 0 {
		it["libraryId"] = strconv.FormatInt(row.LibraryID, 10)
	} else {
		for _, l := range libs { // a skipped file: the library whose folder holds it
			if rel, err := filepath.Rel(l.Path, row.Path); err == nil && !strings.HasPrefix(rel, "..") {
				it["library"], it["libraryId"] = l.Name, strconv.FormatInt(l.ID, 10)
			}
		}
	}
	probed := row.Path
	if row.NewPath != "" {
		it["newPath"] = row.NewPath
		probed = row.NewPath
	}
	if card == jobs.CardProcessed && row.OutputPath != "" { // the new file's codec and size (Beside: its own name)
		probed = row.OutputPath
	}
	if card == jobs.CardProcessed && row.SizeAfter > 0 {
		it["bytes"] = row.SizeAfter
	}
	if raw, ok := store.LatestProbe(probed); ok {
		var info media.Info
		if json.Unmarshal([]byte(raw), &info) == nil {
			if v := info.Video(); v != nil {
				it["codec"] = v.Codec
				if v.Width > 0 && v.Height > 0 {
					it["resolution"] = fmt.Sprintf("%dx%d", v.Width, v.Height)
				}
			}
			if info.DurationSec > 0 {
				it["durationSec"] = info.DurationSec
			}
		}
	}
	jobID := strconv.FormatInt(row.JobID, 10)
	switch card {
	case jobs.CardSkipped:
		it["id"] = "skip-" + strconv.FormatInt(row.SkipID, 10)
		it["skippedId"] = strconv.FormatInt(row.SkipID, 10)
		detail := rules.SkipWords[row.Reason]
		if detail == "" {
			detail = strings.TrimPrefix(row.Decision, "skip: ")
		}
		it["reason"], it["detail"] = row.Reason, upperFirst(detail)+"."
		return it
	case jobs.CardUnprocessed:
		it["status"], it["retryAt"], it["attempts"], it["detail"] = "waiting", nil, row.Attempts, nil
		if row.RetryAt != "" {
			it["status"], it["retryAt"], it["detail"] = "retrying", row.RetryAt, row.Error
		}
	case jobs.CardProcessing:
		it["jobId"], it["percent"] = jobID, 0.0
		it["etaSeconds"], it["convertingTo"], it["encoder"], it["decoder"], it["bitrateKbps"], it["fps"], it["worker"] = nil, nil, nil, nil, nil, nil, nil
		if l, ok := worker.Live(row.JobID); ok {
			p := l.Progress
			it["percent"], it["convertingTo"], it["encoder"], it["decoder"], it["worker"] = p.Percent, l.ConvertingTo, l.Encoder, l.Decoder, l.Worker
			if p.ETASeconds >= 0 {
				it["etaSeconds"] = int(p.ETASeconds + 0.5)
			}
			if p.BitrateKbps > 0 {
				it["bitrateKbps"] = int(p.BitrateKbps + 0.5)
			}
			if p.FPS > 0 {
				it["fps"] = p.FPS
			}
		}
	case jobs.CardProcessed:
		it["jobId"], it["beforeBytes"], it["afterBytes"], it["finishedAt"] = jobID, row.SizeBefore, row.SizeAfter, row.Updated
		it["flow"] = nil
		if row.FlowID > 0 { // the library's flow now (a job does not record which flow made it)
			it["flow"] = map[string]any{"id": strconv.FormatInt(row.FlowID, 10), "name": row.FlowName}
		}
		it["keptId"], it["keptDaysLeft"] = nil, nil
		switch {
		case row.OriginalState == jobs.OriginalKept:
			it["originalState"], it["keptId"] = "kept", strconv.FormatInt(row.OriginalID, 10)
			if t, err := time.Parse(time.RFC3339, row.OriginalKeptAt); err == nil {
				it["keptDaysLeft"] = max(0, int(time.Until(t.Add(time.Duration(keepDays)*24*time.Hour)).Hours()/24+0.999))
			}
		case row.OriginalState == jobs.OriginalRestored:
			it["originalState"] = "restored"
		case row.OriginalState == jobs.OriginalPurged:
			it["originalState"] = "deleted"
		case row.Beside:
			it["originalState"] = "kept_beside"
		default:
			it["originalState"] = "deleted" // replaced with nothing kept (0 keep days, or before slice 18)
		}
	case jobs.CardFailed:
		it["jobId"], it["detail"], it["attempts"] = jobID, row.Error, max(row.Attempts, 1)
		it["title"] = "Converting did not finish"
		if row.Status == jobs.Kept {
			it["title"] = "A check said no, so the original was kept"
		}
	}
	it["id"] = jobID
	return it
}
