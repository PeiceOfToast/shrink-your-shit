// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

// installCommand re-runs the installer, which upgrades a script install to the newest release.
const installCommand = "curl -fsSL https://raw.githubusercontent.com/PeiceOfToast/shrink-your-shit/main/install.sh | sh"

// updateHelper is syp-update-helper (update.Helper); tests use a fake.
type updateHelper interface {
	Call(update.HelperRequest) (update.HelperAnswer, error)
}

// updater serves the update notice and installs an update through the helper, on the person's click.
type updater struct {
	ck          *update.Checker
	ss          *settingsStore
	dataDir     string
	installType func() string
	goos        string
	helper      updateHelper
	poll        time.Duration // how often a waiting install looks at the running jobs and the helper
	now         func() time.Time

	mu    sync.Mutex
	state string // "" | "waiting" (for running jobs to end) | "failed" (before the helper took over)
	err   string
}

func newUpdater(ss *settingsStore, eng Engine) *updater {
	return &updater{ck: &update.Checker{Store: ss.store, Current: eng.Version}, ss: ss, dataDir: eng.DataDir,
		installType: func() string { return update.InstallType(eng.Version, runtime.GOOS, update.DetectEnv()) },
		goos:        runtime.GOOS, helper: update.Helper{}, poll: 5 * time.Second, now: time.Now}
}

// v2Update is the update notice and the update itself. GET /api/v2/update is the last check, kept (it asks
// GitHub for nothing); POST /api/v2/update/check asks now, at most one real request a minute;
// POST /api/v2/update/install and /rollback ask the update helper to do it (Linux, opt-in).
func v2Update(api *http.ServeMux, u *updater) {
	api.HandleFunc("GET /api/v2/update", func(w http.ResponseWriter, r *http.Request) {
		on, err := updatesOn(u.ss)
		if err != nil {
			serverError(w, err)
			return
		}
		st, err := update.Load(u.ss.store, u.ck.Current)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, u.body(st, on))
	})
	api.HandleFunc("POST /api/v2/update/check", func(w http.ResponseWriter, r *http.Request) {
		on, err := updatesOn(u.ss)
		if err != nil {
			serverError(w, err)
			return
		}
		if !on {
			apiError(w, http.StatusConflict, "updates_off", "update checks are off")
			return
		}
		st, err := u.ck.Check(time.Minute)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, u.body(st, true))
	})
	api.HandleFunc("POST /api/v2/update/install", u.install)
	api.HandleFunc("POST /api/v2/update/rollback", u.rollback)
}

// method is how this install updates: "helper" (the button), "commands" (shown for the person to run)
// or "none". helper is the helper's status when it answers.
func (u *updater) method() (string, *update.HelperAnswer) {
	switch u.installType() {
	case "docker":
		return "commands", nil
	case "script":
		if u.goos == "linux" {
			if a, err := u.helper.Call(update.HelperRequest{Cmd: "status"}); err == nil {
				return "helper", &a
			}
		}
		return "commands", nil
	}
	return "none", nil
}

func (u *updater) commands() []string {
	switch u.installType() {
	case "docker": // the app never touches the Docker socket: the person runs these where the compose file is
		return []string{"docker compose pull", "docker compose up -d"}
	case "script":
		return []string{installCommand}
	}
	return nil
}

func (u *updater) install(w http.ResponseWriter, r *http.Request) {
	var in struct {
		When string `json:"when"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || (in.When != "now" && in.When != "after-job") {
		apiError(w, http.StatusBadRequest, "bad_request", `when must be "now" or "after-job"`)
		return
	}
	on, err := updatesOn(u.ss)
	if err != nil {
		serverError(w, err)
		return
	}
	if !on {
		apiError(w, http.StatusConflict, "updates_off", "update checks are off")
		return
	}
	method, h := u.method()
	if method != "helper" {
		apiError(w, http.StatusConflict, "install_unsupported", "this install can't update itself; see the commands")
		return
	}
	st, err := update.Load(u.ss.store, u.ck.Current)
	if err != nil {
		serverError(w, err)
		return
	}
	if !st.Available {
		apiError(w, http.StatusConflict, "not_available", "there is no newer release to install")
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state == "waiting" || h.State == "downloading" || h.State == "installing" {
		apiError(w, http.StatusConflict, "update_in_progress", "an update is already in progress")
		return
	}
	running, err := u.running()
	if err != nil {
		serverError(w, err)
		return
	}
	if running > 0 && in.When == "now" {
		apiError(w, http.StatusConflict, "job_running", "a conversion is running; update after it, or stop it first")
		return
	}
	// No new job starts from here on (a restart would cut it off); the next start turns workers on again.
	resume := !u.ss.store.Paused()
	if err := u.ss.store.SetPaused(true); err != nil {
		serverError(w, err)
		return
	}
	u.state, u.err = "waiting", ""
	go u.whenIdle(st.Latest, resume)
	u.mu.Unlock()
	if in.When == "now" { // nothing runs: start at once, so the answer already says so
		u.waitStarted()
	}
	u.mu.Lock()
	writeJSONStatus(w, http.StatusAccepted, u.bodyLocked(st, true))
}

// waitStarted waits briefly until an install that need not wait has asked the helper.
func (u *updater) waitStarted() {
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		u.mu.Lock()
		s := u.state
		u.mu.Unlock()
		if s != "waiting" {
			return
		}
	}
}

func (u *updater) running() (int, error) {
	a, err := u.ss.store.ActivitySummary()
	return a.WorkingFiles, err
}

// whenIdle waits for the running jobs to end, then backs up the database, writes the marker and asks
// the helper to install version. If the helper fails before the program is swapped (this program is
// then still running), it undoes the marker and the pause.
func (u *updater) whenIdle(version string, resume bool) {
	for {
		n, err := u.running()
		if err == nil && n == 0 {
			break
		}
		time.Sleep(u.poll)
	}
	fail := func(err error) {
		log.Printf("update: %v", err)
		update.RemoveMarker(u.dataDir)
		if resume {
			u.ss.store.SetPaused(false)
		}
		u.mu.Lock()
		u.state, u.err = "failed", err.Error()
		u.mu.Unlock()
	}
	backup, err := u.ss.store.Backup()
	if err != nil {
		fail(errors.New("could not back up the database: " + err.Error()))
		return
	}
	m := update.Marker{Kind: "update", From: u.ck.Current, To: version, Backup: backup, Resume: resume, At: u.now().UTC()}
	if err := update.WriteMarker(u.dataDir, m); err != nil {
		fail(err)
		return
	}
	if _, err := u.helper.Call(update.HelperRequest{Cmd: "install", Version: version}); err != nil {
		fail(err)
		return
	}
	u.mu.Lock()
	u.state = ""
	u.mu.Unlock()
	// The helper restarts this program once the new one is in place. Until then, a failure (a download
	// or checksum error) leaves this program running, and it cleans up after itself.
	for end := time.Now().Add(30 * time.Minute); time.Now().Before(end); time.Sleep(u.poll) {
		a, err := u.helper.Call(update.HelperRequest{Cmd: "status"})
		if err != nil || a.State == "downloading" || a.State == "installing" {
			continue
		}
		if a.State == "failed" {
			fail(errors.New(a.Error))
		}
		return
	}
}

func (u *updater) rollback(w http.ResponseWriter, r *http.Request) {
	method, h := u.method()
	if method != "helper" {
		apiError(w, http.StatusConflict, "install_unsupported", "this install can't roll back by itself")
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state == "waiting" || h.State == "downloading" || h.State == "installing" {
		apiError(w, http.StatusConflict, "update_in_progress", "an update is in progress")
		return
	}
	if !h.Previous {
		apiError(w, http.StatusConflict, "no_previous", "there is no previous version to roll back to")
		return
	}
	// The database copy from before the last update goes back with the program, if this program made it.
	m := update.Marker{Kind: "rollback", From: u.ck.Current, At: u.now().UTC()}
	if last := update.LastResult(u.dataDir); last != nil && last.OK && last.Kind == "update" && last.To == u.ck.Current {
		m.To, m.Backup = last.From, last.Backup
	}
	if err := update.WriteMarker(u.dataDir, m); err != nil {
		serverError(w, err)
		return
	}
	if _, err := u.helper.Call(update.HelperRequest{Cmd: "rollback"}); err != nil {
		update.RemoveMarker(u.dataDir)
		var he *update.HelperError
		if errors.As(err, &he) && he.Code == "no_previous" {
			apiError(w, http.StatusConflict, "no_previous", he.Message)
			return
		}
		if errors.As(err, &he) && he.Code == "busy" {
			apiError(w, http.StatusConflict, "update_in_progress", he.Message)
			return
		}
		serverError(w, err)
		return
	}
	st, err := update.Load(u.ss.store, u.ck.Current)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, u.bodyLocked(st, true))
}

// updatesOn reads the setting under the settings lock, as the settings handlers do.
func updatesOn(ss *settingsStore) (bool, error) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, err := ss.load()
	if err != nil {
		return false, err
	}
	return s.UpdateChecks(), nil
}

func (u *updater) body(st update.Status, on bool) map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.bodyLocked(st, on)
}

// bodyLocked is the notice as the API shows it. available is never true while checks are off.
func (u *updater) bodyLocked(st update.Status, on bool) map[string]any {
	checked := any(nil)
	if !st.CheckedAt.IsZero() {
		checked = st.CheckedAt.UTC().Format(time.RFC3339)
	}
	method, h := u.method()
	install := map[string]any{"state": "idle", "error": nil}
	switch {
	case u.state != "":
		install["state"], install["error"] = u.state, emptyAsNil(u.err)
	case h != nil:
		install["state"], install["error"] = h.State, emptyAsNil(h.Error)
	}
	var commands any
	if method == "commands" {
		commands = u.commands()
	}
	var last any
	if r := update.LastResult(u.dataDir); r != nil {
		last = map[string]any{"ok": r.OK, "from": r.From, "to": r.To, "message": r.Message, "dataRestored": r.DataRestored}
	}
	return map[string]any{
		"enabled": on, "current": st.Current, "latest": emptyAsNil(st.Latest), "available": on && st.Available,
		"notes": st.Notes, "url": emptyAsNil(st.URL), "publishedAt": emptyAsNil(st.PublishedAt),
		"checkedAt": checked, "error": emptyAsNil(st.Error),
		"installType": u.installType(),
		"method":      method, "commands": commands, "install": install, "lastResult": last,
	}
}

func emptyAsNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}
