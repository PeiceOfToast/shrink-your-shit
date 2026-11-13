// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// D4b: a share that stops answering pauses the libraries in its folder by itself; when it answers
// again they start by themselves. The person's own pause is not touched (jobs.PauseLibrariesForShare).

// shareRoot is where each share is mounted (tests point it at a temp folder).
var shareRoot = "/var/lib/syp/shares"

// shareWatchEvery is how often each share is checked (a var, so a test can shorten it).
var shareWatchEvery = time.Minute

// shareStatTimeout is how long a share's folder may take to answer before it counts as not answering.
var shareStatTimeout = 5 * time.Second

// shareAnswers reports whether the share mounted at mountPath answers now. Tests replace it.
var shareAnswers = func(mountPath string) bool {
	return mountedAt(mountPath) && statWithin(mountPath, shareStatTimeout)
}

// mountPointFor is where share sh is mounted.
func mountPointFor(sh jobs.Share) string {
	return shareRoot + "/" + sanitiseName(sh.Name, sh.ID)
}

// WatchShares checks each saved share every shareWatchEvery, in app mode only (the helper mounts them).
func WatchShares(store *jobs.Store) {
	if mode, _ := detectMode(); mode != "app" {
		return
	}
	t := time.NewTicker(shareWatchEvery)
	defer t.Stop()
	for range t.C {
		checkShares(store)
	}
}

// checkShares updates each share's status and the libraries in its folder, once.
func checkShares(store *jobs.Store) {
	shares, err := store.LoadShares()
	if err != nil {
		log.Printf("shares: could not load saved shares: %v", err)
		return
	}
	for _, sh := range shares {
		mp := mountPointFor(sh)
		status := mountStatus(store, sh.ID)
		if shareAnswers(mp) {
			if status != "connected" {
				store.SetShareMountStatus(sh.ID, "linux", "connected", "")
				log.Printf("share %q: answering again", sh.Name)
			}
			if ids, err := store.ResumeSharePause(sh.ID); err != nil {
				log.Printf("share %q: could not resume its libraries: %v", sh.Name, err)
			} else if len(ids) > 0 {
				log.Printf("share %q: %d librar%s back on", sh.Name, len(ids), plural(len(ids)))
			}
			continue
		}
		if status == "connected" { // an outage: say so once, with the reason the page shows
			reason, _ := json.Marshal(map[string]string{"code": "host_unreachable", "message": sh.Name + " stopped answering."})
			store.SetShareMountStatus(sh.ID, "linux", "not_connected", string(reason))
			log.Printf("share %q: stopped answering", sh.Name)
		}
		if ids, err := store.PauseLibrariesForShare(sh.ID, mp); err != nil {
			log.Printf("share %q: could not pause its libraries: %v", sh.Name, err)
		} else if len(ids) > 0 {
			log.Printf("share %q: %d librar%s paused until it answers", sh.Name, len(ids), plural(len(ids)))
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// sharePausedBy is the share that paused library l (the page's pausedBy: shareId, shareName, since, use),
// or nil when no share has, or the person paused the library (their pause shows, as before).
func sharePausedBy(store *jobs.Store, l jobs.Library) map[string]any {
	if l.SharePausedBy == 0 || !l.Started {
		return nil
	}
	sh, err := store.ShareByID(l.SharePausedBy)
	if err != nil || sh == nil {
		return nil
	}
	return map[string]any{"shareId": strconv.FormatInt(sh.ID, 10), "shareName": sh.Name, "since": l.SharePausedSince, "use": "folder"}
}

// ShareDownFor is the saved share whose folder holds folder, when that share does not answer now. The
// worker asks when a convert fails: a failure while the share is down is put back, not failed (D4c).
func ShareDownFor(store *jobs.Store, folder string) (worker.ShareDown, bool) {
	shares, err := store.LoadShares()
	if err != nil {
		return worker.ShareDown{}, false
	}
	for _, sh := range shares {
		mp := mountPointFor(sh)
		if (folder == mp || jobs.Within(folder, mp)) && !shareAnswers(mp) {
			return worker.ShareDown{ShareID: sh.ID, Name: sh.Name, MountPath: mp}, true
		}
	}
	return worker.ShareDown{}, false
}

// mountStatus is share id's status on this computer ("" when it has no mount record yet).
func mountStatus(store *jobs.Store, id int64) string {
	mounts, err := store.ShareMountsByShare(id)
	if err != nil || len(mounts) == 0 {
		return ""
	}
	return mounts[0].Status
}

// mountedAt reports whether a filesystem is mounted at path (Linux: /proc/self/mountinfo).
func mountedAt(path string) bool {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 4 && strings.ReplaceAll(f[4], `\040`, " ") == path {
			return true
		}
	}
	return false
}

// statBusy holds the paths with a stat still running, so a hung mount starts no second one (as freeBytes).
var statBusy = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

// statWithin reports whether path can be stat'ed within d. A stat still hung from before counts as no.
func statWithin(path string, d time.Duration) bool {
	statBusy.Lock()
	if statBusy.m[path] {
		statBusy.Unlock()
		return false
	}
	statBusy.m[path] = true
	statBusy.Unlock()
	done := make(chan bool, 1)
	go func() {
		_, err := os.Stat(path)
		statBusy.Lock()
		delete(statBusy.m, path)
		statBusy.Unlock()
		done <- err == nil
	}()
	select {
	case ok := <-done:
		return ok
	case <-time.After(d):
		return false
	}
}
