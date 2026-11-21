// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// v2Overview is the Dashboard's executive overview (user review 11-12 item 5; shapes in
// slices/v2-engine/review-items-shapes.md): space (NET) and the trash (GROSS) kept apart, the workers
// with their files' live progress, the active jobs, each library's sizes and counts, the last 30 days,
// and averages. Live: the page fetches it again on /api/v2/events' messages. Nothing is estimated
// except a converting file's saving, which the person asked for (expectedSavingBytes): what isn't
// recorded is null, or a day with recorded false.
func v2Overview(api *http.ServeMux, store *jobs.Store, wv *workerView, libTotals *libraryTotals) {
	overviewStats := &statsCache{} // this server's store's
	if store != nil {              // so the first page view after start is not the slow one (setup tests run with no store)
		overviewStats.warm(store)
		libTotals.warm(store)
	}
	api.HandleFunc("GET /api/v2/overview", func(w http.ResponseWriter, r *http.Request) {
		st, err := overviewStats.get(store)
		if err != nil {
			serverError(w, err)
			return
		}
		a := st.a
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
		// the encode's speed, ETA and fps are the converting step's: during the check they are not shown (12c)
		live := func(id int64, running bool) (map[string]any, worker.LiveJob) {
			j := map[string]any{"jobId": strconv.FormatInt(id, 10), "percent": nil, "fps": nil, "speed": nil, "etaS": nil}
			l, ok := worker.Live(id)
			if ok {
				p := l.Progress
				j["percent"] = p.Percent
				if !running {
					return j, l
				}
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
			return j, l
		}
		active := []map[string]any{}
		byWorker := map[string][]map[string]any{}
		for _, j := range working {
			steps, _ := store.Steps(j.ID) // staged as the jobs list and the live events stage it
			state := v2State(string(jobs.Running), steps, livePercent(j.ID))
			l, lj := live(j.ID, state == "running")
			l["name"] = filepath.Base(j.Path)
			byWorker[j.Worker] = append(byWorker[j.Worker], l)
			saving, estimated, err := expectedSaving(store, j, lj)
			if err != nil {
				serverError(w, err)
				return
			}
			act := map[string]any{"jobId": l["jobId"], "name": l["name"], "percent": l["percent"], "etaS": l["etaS"],
				"speed": l["speed"], "fps": l["fps"], "stage": nil,
				"expectedSavingBytes": saving, "expectedSavingEstimated": estimated, "library": nil}
			act["stage"] = jobStage[state]
			if j.LibraryID != 0 {
				act["library"] = map[string]any{"id": strconv.FormatInt(j.LibraryID, 10), "name": j.LibraryName}
			}
			active = append(active, act)
		}
		all, err := wv.all(r.Context()) // the same list as GET /workers, each with only its own jobs (#177 review)
		if err != nil {
			serverError(w, err)
			return
		}
		paired := map[string]bool{}
		for _, it := range all {
			if it["kind"] == "remote" {
				paired[it["id"].(string)] = true
			}
		}
		workers := []map[string]any{}
		for _, it := range all {
			own := []map[string]any{}
			if it["kind"] == "remote" {
				own = append(own, byWorker[it["id"].(string)]...)
			} else {
				for id, ws := range byWorker { // this program's pool runs under its own names ("builtin", ...)
					if !paired[id] {
						own = append(own, ws...)
					}
				}
			}
			// cpuPercent is the worker's live figure (this computer's meter, or a remote's last report)
			live, _ := it["live"].(map[string]any)
			var gpu any // NVIDIA on this computer only; a remote's GPU isn't read (null)
			if it["kind"] != "remote" {
				if g := gpuPercent(time.Now()); g != nil {
					gpu = *g
				}
			}
			workers = append(workers, map[string]any{"id": it["id"], "name": it["name"], "kind": it["kind"], "status": it["status"],
				"slots": it["slots"], "jobs": own, "cpuPercent": live["cpuPercent"], "gpuPercent": gpu})
		}
		libs, err := store.Libraries()
		if err != nil {
			serverError(w, err)
			return
		}
		totals, err := libTotals.get(store, libs)
		if err != nil {
			serverError(w, err)
			return
		}
		libraries := []map[string]any{}
		for _, l := range libs {
			t := totals[l.ID]
			c := st.counts[l.ID]
			state := "active"
			pausedBy := sharePausedBy(store, l) // D4b
			switch {
			case l.FlowID == 0: // no workflow: it cannot run, so this wins over paused, as the library item's status does
				state = "no_workflow"
			case !l.Started:
				state = "paused"
			case pausedBy != nil:
				state = "paused_share"
			}
			var pct any
			if t.BytesBefore > 0 {
				pct = 100 * float64(c.FreedBytes) / float64(t.BytesBefore)
			}
			libraries = append(libraries, map[string]any{"id": strconv.FormatInt(l.ID, 10), "name": l.Name, "state": state,
				// now: the files a scan sees (an original waiting in .syp-trash isn't one: review ab99630 P3-3)
				"bytesBefore": t.BytesBefore, "bytesNow": jobs.BytesNow(t, c), "saved": c.FreedBytes, "savedPercent": pct,
				"waitingToFree": c.WaitingBytes, "couldFree": c.CouldFreeBytes, "grew": c.GrewBytes,
				"counts": map[string]int{"done": t.Done, "waiting": c.Waiting, "skipped": t.Skipped, "failed": c.Failed}, "pausedBy": pausedBy})
		}
		now := time.Now()
		days := st.days
		daily := []map[string]any{}
		for _, d := range days {
			daily = append(daily, map[string]any{"day": d.Day, "recorded": d.Recorded, "completed": d.Completed, "skipped": d.Skipped,
				"failed": d.Failed, "freedBytes": d.FreedBytes,
				"savedBytes": d.SavedBytes, "waitingBytes": d.WaitingBytes, "couldFreeBytes": d.CouldFreeBytes})
		}
		saving, savingOK, perGB, timeOK := st.saving, st.savingOK, st.perGB, st.timeOK
		// fileShrinkPercent: how much smaller converted files come out, NOT space saved (review ab99630 P2-1)
		avg := map[string]any{"fileShrinkPercent": nil, "secondsPerGB": nil}
		if savingOK {
			avg["fileShrinkPercent"] = saving
		}
		if timeOK {
			avg["secondsPerGB"] = perGB
		}
		// graphics-encoder fall-backs today, per worker: the count, and when the worker's encoder is paused
		fallbacks, err := store.EncoderFallbacksSince(time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local))
		if err != nil {
			serverError(w, err)
			return
		}
		encoderFallbacks := []map[string]any{}
		for _, f := range fallbacks {
			encoder, lastError, err := store.EncoderLastFell(f.Worker)
			if err != nil {
				serverError(w, err)
				return
			}
			row := map[string]any{"workerId": f.Worker, "worker": workerName(store, f.Worker), "encoder": encoder, "lastError": lastError,
				"count": f.Count, "at": f.At, "pausedUntil": nil}
			if until, paused, err := store.EncoderPaused(f.Worker); err == nil && paused {
				row["pausedUntil"] = until.UTC().Format(time.RFC3339)
			}
			encoderFallbacks = append(encoderFallbacks, row)
		}
		converted := st.converted
		zone := zoneName(now)
		writeJSON(w, map[string]any{
			"headline": map[string]any{"freed": a.FreedBytes, "waitingToFree": a.WaitingBytes, "nextFreeAt": trash.NextDeleteAt,
				"couldFree": a.CouldFreeBytes, "grew": a.GrewBytes, "filesConverted": converted, "filesWaiting": a.WaitingFiles}, // NET
			"trash":            trash, // GROSS (the originals' own size): never added to the headline
			"workers":          workers,
			"active":           active,
			"libraries":        libraries,
			"daily":            daily,
			"timeZone":         zone,
			"averages":         avg,
			"encoderFallbacks": encoderFallbacks,
		})
	})
}

// estimatedFromJobs is how many of a library's done conversions the fallback ratio averages.
const estimatedFromJobs = 20

// expectedSaving is what converting job j is expected to save: the original's size less the new file's
// projected size, and true (it is always an estimate). The projection is the bytes written so far and
// how far the encode is (worker.Live's ProjectedSize); until the encode has said that, the library's
// own recent conversions say what shape its new files come out (every library's, when that library has
// none). nil when neither can tell, so the page keeps "not known yet" rather than a made-up number.
func expectedSaving(store *jobs.Store, j jobs.Working, l worker.LiveJob) (any, bool, error) {
	if j.SizeBefore <= 0 {
		return nil, false, nil
	}
	out, ok := l.ProjectedSize, l.ProjectedSize > 0
	if !ok {
		libs := []int64{0} // every library
		if j.LibraryID != 0 {
			libs = []int64{j.LibraryID, 0}
		}
		for _, lib := range libs {
			ratio, has, err := store.OutputRatio(lib, estimatedFromJobs)
			if err != nil {
				return nil, false, err
			}
			if has {
				out, ok = int64(float64(j.SizeBefore)*ratio), true
				break
			}
		}
	}
	if !ok {
		return nil, false, nil
	}
	// a new file bigger than its original is a negative saving: the new file is coming out bigger
	return j.SizeBefore - out, true, nil
}

// zoneName is this server's time zone as an IANA name ("Europe/Berlin"): TZ, else where /etc/localtime
// links to, else Go's name, else the abbreviation (review ab99630 P3-1: not "CET").
func zoneName(now time.Time) string {
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); tz != "" {
		return tz
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, name, ok := strings.Cut(target, "zoneinfo/"); ok {
			return name
		}
	}
	if name := time.Local.String(); name != "Local" {
		return name
	}
	z, _ := now.Zone()
	return z
}

// libraryTotals caches each library's totals (its files' records and done jobs), so the Dashboard and
// Libraries pages don't recount on every request (review ab99630 P2-2). Its key: each library's folder and
// last scan, and the newest event (a job finishing or a skip recorded changes the totals at once; the
// events are what the pages refresh on). Shared by the Dashboard and Libraries: one count, one answer.
type libraryTotals struct {
	c swrCache[map[int64]jobs.LibraryTotals]
}

func (c *libraryTotals) get(store *jobs.Store, libs []jobs.Library) (map[int64]jobs.LibraryTotals, error) {
	key, build := libraryTotalsFuncs(store, libs)
	return c.c.get(key, build)
}

// warm computes the totals once in the background, at start.
func (c *libraryTotals) warm(store *jobs.Store) {
	libs, err := store.Libraries()
	if err != nil {
		log.Printf("numbers: warming the library totals failed: %v", err)
		return
	}
	key, build := libraryTotalsFuncs(store, libs)
	c.c.warm(key, build)
}

// libraryTotalsFuncs is the key and the computation for the libraries' totals.
func libraryTotalsFuncs(store *jobs.Store, libs []jobs.Library) (func() (string, error), func() (map[int64]jobs.LibraryTotals, string, error)) {
	key := func() (string, error) {
		_, newest, err := store.EventBounds()
		if err != nil {
			return "", err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d|%d;", newest, store.SwapChanges()) // a restore writes no event
		for _, l := range libs {
			fmt.Fprintf(&b, "%d|%s|%s|%d;", l.ID, l.Path, l.LastScan, l.LastScanVideos)
		}
		return b.String(), nil
	}
	build := func() (map[int64]jobs.LibraryTotals, string, error) {
		k, err := key()
		if err != nil {
			return nil, "", err
		}
		out, err := libraryTotalsOf(store, libs)
		return out, k, err
	}
	return key, build
}

func libraryTotalsOf(store *jobs.Store, libs []jobs.Library) (map[int64]jobs.LibraryTotals, error) {
	out := map[int64]jobs.LibraryTotals{}
	for _, l := range libs {
		t, err := store.LibraryTotalsOf(l)
		if err != nil {
			return nil, err
		}
		out[l.ID] = t
	}
	// a file in a library inside another is the inner one's only (review ab99630 P3-4): each library
	// gives up what its nearest libraries inside it hold
	for _, outer := range libs {
		for _, inner := range libs {
			if inner.ID == outer.ID || !strictlyInside(inner.Path, outer.Path) {
				continue
			}
			nearest := true
			for _, mid := range libs {
				if mid.ID != inner.ID && mid.ID != outer.ID && strictlyInside(mid.Path, outer.Path) && strictlyInside(inner.Path, mid.Path) {
					nearest = false
				}
			}
			if nearest {
				o, i := out[outer.ID], out[inner.ID]
				o.BytesBefore -= i.BytesBefore
				o.Skipped -= i.Skipped
				o.VideosRecorded -= i.VideosRecorded
				out[outer.ID] = o
			}
		}
	}
	return out, nil
}

// strictlyInside reports whether path p is inside dir (not dir itself).
func strictlyInside(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// statsCache caches the overview's sums over every job (the space, the libraries' counts, the 30
// days, the averages): they change only with an event (a job's change of state, a library's, a pause),
// so a page fetching the overview again on progress gets them from here (review ab99630 P2-2: seconds
// on 150k jobs). Its key: the newest event's seq, today's date (the 30 days move at midnight), and the
// minute.
type statsCache struct {
	c swrCache[*stats]
}

type stats struct {
	a                jobs.Summary
	counts           map[int64]jobs.LibraryCounts
	days             []jobs.Day
	saving, perGB    float64
	savingOK, timeOK bool
	converted        int
}

func (c *statsCache) get(store *jobs.Store) (*stats, error) {
	key, build := statsFuncs(store)
	return c.c.get(key, build)
}

// warm computes the sums once in the background, at start.
func (c *statsCache) warm(store *jobs.Store) {
	key, build := statsFuncs(store)
	c.c.warm(key, build)
}

// statsFuncs is the key and the computation for the overview's sums.
func statsFuncs(store *jobs.Store) (func() (string, error), func() (*stats, string, error)) {
	key := func() (string, error) {
		_, newest, err := store.EventBounds()
		if err != nil {
			return "", err
		}
		now := time.Now()
		// and at least once a minute: a purge or a scan finding an original gone frees bytes without an event
		return strconv.FormatInt(newest, 10) + "|" + strconv.FormatInt(store.SwapChanges(), 10) + "|" + now.Format("2006-01-02") + "|" + strconv.FormatInt(now.Unix()/60, 10), nil
	}
	build := func() (*stats, string, error) {
		k, err := key()
		if err != nil {
			return nil, "", err
		}
		st, err := takeStats(store, time.Now())
		return st, k, err
	}
	return key, build
}

func takeStats(store *jobs.Store, now time.Time) (*stats, error) {
	st := &stats{}
	t, err := store.TotalsOf()
	if err != nil {
		return nil, err
	}
	st.a, st.counts = t.Summary, t.Counts
	if st.days, err = store.Days(30, now, t); err != nil {
		return nil, err
	}
	if st.saving, st.savingOK, st.perGB, st.timeOK, err = store.Averages(); err != nil {
		return nil, err
	}
	if st.converted, err = store.DoneCount(); err != nil {
		return nil, err
	}
	return st, nil
}
