// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// v2Overview is the Dashboard's executive overview (user review 11-12 item 5; shapes in
// slices/v2-engine/review-items-shapes.md): space (NET) and the trash (GROSS) kept apart, the workers
// with their files' live progress, the active jobs, each library's sizes and counts, the last 30 days,
// and averages. Live: the page fetches it again on /api/v2/events' messages. Nothing is estimated:
// what isn't recorded is null, or a day with recorded false.
func v2Overview(api *http.ServeMux, store *jobs.Store, wv *workerView) {
	api.HandleFunc("GET /api/v2/overview", func(w http.ResponseWriter, r *http.Request) {
		a, err := store.ActivitySummary()
		if err != nil {
			serverError(w, err)
			return
		}
		set, err := LoadSettings(store)
		if err != nil {
			serverError(w, err)
			return
		}
		trash, err := store.TrashNow(set.KeepOriginalsDays)
		if err != nil {
			serverError(w, err)
			return
		}
		working, err := store.WorkingJobs()
		if err != nil {
			serverError(w, err)
			return
		}
		live := func(id int64) map[string]any {
			j := map[string]any{"jobId": strconv.FormatInt(id, 10), "percent": nil, "fps": nil, "speed": nil, "etaS": nil}
			if l, ok := worker.Live(id); ok {
				p := l.Progress
				j["percent"] = p.Percent
				if p.FPS > 0 {
					j["fps"] = p.FPS
				}
				if p.Speed > 0 {
					j["speed"] = p.Speed
				}
				if p.ETASeconds >= 0 {
					j["etaS"] = int(p.ETASeconds + 0.5)
				}
			}
			return j
		}
		active := []map[string]any{}
		byWorker := map[string][]map[string]any{}
		for _, j := range working {
			l := live(j.ID)
			l["name"] = filepath.Base(j.Path)
			byWorker[j.Worker] = append(byWorker[j.Worker], l)
			act := map[string]any{"jobId": l["jobId"], "name": l["name"], "percent": l["percent"], "etaS": l["etaS"], "expectedSavingBytes": nil,
				"library": nil}
			if j.LibraryID != 0 {
				act["library"] = map[string]any{"id": strconv.FormatInt(j.LibraryID, 10), "name": j.LibraryName}
			}
			active = append(active, act)
		}
		local, err := wv.local(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		localJobs := []map[string]any{}
		for _, ws := range byWorker { // this program's pool runs under its own names ("builtin", ...)
			localJobs = append(localJobs, ws...)
		}
		workers := []map[string]any{{"id": local["id"], "name": local["name"], "kind": local["kind"], "status": local["status"],
			"slots": local["slots"], "jobs": localJobs, "cpuPercent": nil, "gpuPercent": nil}}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		counts, err := store.Counts()
		if err != nil {
			serverError(w, err)
			return
		}
		libraries := []map[string]any{}
		for _, l := range libs {
			t, err := store.LibraryTotalsOf(l)
			if err != nil {
				serverError(w, err)
				return
			}
			c := counts[l.ID]
			state := "active"
			if !l.Started {
				state = "paused"
			}
			var pct any
			if t.BytesBefore > 0 {
				pct = 100 * float64(c.FreedBytes) / float64(t.BytesBefore)
			}
			libraries = append(libraries, map[string]any{"id": strconv.FormatInt(l.ID, 10), "name": l.Name, "state": state,
				"bytesBefore": t.BytesBefore, "bytesNow": t.BytesBefore - c.FreedBytes, "saved": c.FreedBytes, "savedPercent": pct,
				"waitingToFree": c.WaitingBytes, "couldFree": c.CouldFreeBytes,
				"counts": map[string]int{"done": t.Done, "waiting": c.Waiting, "skipped": t.Skipped, "failed": c.Failed}})
		}
		now := time.Now()
		days, err := store.Days(30, now)
		if err != nil {
			serverError(w, err)
			return
		}
		daily := []map[string]any{}
		for _, d := range days {
			daily = append(daily, map[string]any{"day": d.Day, "recorded": d.Recorded, "completed": d.Completed, "skipped": d.Skipped,
				"failed": d.Failed, "savedBytes": d.SavedBytes})
		}
		saving, savingOK, perGB, timeOK, err := store.Averages()
		if err != nil {
			serverError(w, err)
			return
		}
		avg := map[string]any{"savingPercent": nil, "secondsPerGB": nil}
		if savingOK {
			avg["savingPercent"] = saving
		}
		if timeOK {
			avg["secondsPerGB"] = perGB
		}
		converted, err := store.DoneCount()
		if err != nil {
			serverError(w, err)
			return
		}
		zone, _ := now.Zone()
		if name := time.Local.String(); name != "Local" {
			zone = name
		}
		writeJSON(w, map[string]any{
			"headline": map[string]any{"freed": a.FreedBytes, "waitingToFree": a.WaitingBytes, "nextFreeAt": trash.NextDeleteAt,
				"couldFree": a.CouldFreeBytes, "filesConverted": converted, "filesWaiting": a.WaitingFiles}, // NET
			"trash":     trash, // GROSS (the originals' own size): never added to the headline
			"workers":   workers,
			"active":    active,
			"libraries": libraries,
			"daily":     daily,
			"timeZone":  zone,
			"averages":  avg,
		})
	})
}
