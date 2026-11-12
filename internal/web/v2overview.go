// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	overviewStats, libTotals := &statsCache{}, &libraryTotals{} // this server's store's
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
			if !l.Started {
				state = "paused"
			}
			var pct any
			if t.BytesBefore > 0 {
				pct = 100 * float64(c.FreedBytes) / float64(t.BytesBefore)
			}
			libraries = append(libraries, map[string]any{"id": strconv.FormatInt(l.ID, 10), "name": l.Name, "state": state,
				// now: the files a scan sees (an original waiting in .syp-trash isn't one: review ab99630 P3-3)
				"bytesBefore": t.BytesBefore, "bytesNow": t.BytesBefore - c.FreedBytes - c.WaitingBytes, "saved": c.FreedBytes, "savedPercent": pct,
				"waitingToFree": c.WaitingBytes, "couldFree": c.CouldFreeBytes,
				"counts": map[string]int{"done": t.Done, "waiting": c.Waiting, "skipped": t.Skipped, "failed": c.Failed}})
		}
		now := time.Now()
		days := st.days
		daily := []map[string]any{}
		for _, d := range days {
			daily = append(daily, map[string]any{"day": d.Day, "recorded": d.Recorded, "completed": d.Completed, "skipped": d.Skipped,
				"failed": d.Failed, "freedBytes": d.FreedBytes})
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
		converted := st.converted
		zone := zoneName(now)
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

// libraryTotals caches each library's totals (its files' records): they change with a scan or a
// library change, not with each job event that makes the page fetch the overview again (review ab99630
// P2-2). Its key: each library's folder and last scan (a library added, removed or moved changes it;
// a skip recorded between scans shows at the next scan).
type libraryTotals struct {
	mu   sync.Mutex
	key  string
	byID map[int64]jobs.LibraryTotals
}

func (c *libraryTotals) get(store *jobs.Store, libs []jobs.Library) (map[int64]jobs.LibraryTotals, error) {
	var b strings.Builder
	for _, l := range libs {
		fmt.Fprintf(&b, "%d|%s|%s|%d;", l.ID, l.Path, l.LastScan, l.LastScanVideos)
	}
	key := b.String()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.byID != nil && c.key == key {
		return c.byID, nil
	}
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
	c.key, c.byID = key, out
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
	mu  sync.Mutex
	key string
	st  *stats
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
	_, newest, err := store.EventBounds()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	// and at least once a minute: a purge or a scan finding an original gone frees bytes without an event
	key := strconv.FormatInt(newest, 10) + "|" + now.Format("2006-01-02") + "|" + strconv.FormatInt(now.Unix()/60, 10)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.st != nil && c.key == key {
		return c.st, nil
	}
	st := &stats{}
	if st.a, err = store.ActivitySummary(); err != nil {
		return nil, err
	}
	if st.counts, err = store.Counts(); err != nil {
		return nil, err
	}
	if st.days, err = store.Days(30, now); err != nil {
		return nil, err
	}
	if st.saving, st.savingOK, st.perGB, st.timeOK, err = store.Averages(); err != nil {
		return nil, err
	}
	if st.converted, err = store.DoneCount(); err != nil {
		return nil, err
	}
	c.key, c.st = key, st
	return st, nil
}
