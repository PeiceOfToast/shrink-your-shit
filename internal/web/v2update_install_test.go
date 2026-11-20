// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

// fakeUpdateHelper is syp-update-helper: it records requests and answers with its state.
type fakeUpdateHelper struct {
	mu              sync.Mutex
	calls           []update.HelperRequest
	state           string // after an install: what status answers
	errMsg          string
	previous        bool
	previousVersion string
}

func (f *fakeUpdateHelper) Call(r update.HelperRequest) (update.HelperAnswer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r)
	if r.Cmd == "install" && f.state == "" {
		f.state = "downloading"
	}
	st := f.state
	if st == "" {
		st = "idle"
	}
	return update.HelperAnswer{OK: true, State: st, Error: f.errMsg, Previous: f.previous, PreviousVersion: f.previousVersion}, nil
}

func (f *fakeUpdateHelper) installs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var v []string
	for _, c := range f.calls {
		if c.Cmd == "install" || c.Cmd == "rollback" {
			v = append(v, c.Cmd+" "+c.Version)
		}
	}
	return v
}

// installServer is the update API on a script install (Linux) with the helper answering, and a newer
// release 0.2.0 already found.
func installServer(t *testing.T, installType string) (*httptest.Server, *updater, *jobs.Store, *fakeUpdateHelper) {
	t.Helper()
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(newerRelease)) }))
	t.Cleanup(gh.Close)
	data := t.TempDir()
	st, err := jobs.Open(filepath.Join(data, "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ss := &settingsStore{store: st}
	s, _ := ss.load()
	on := true
	s.CheckForUpdates = &on
	if err := ss.save(s); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPaused(false); err != nil { // workers on, as on a running install
		t.Fatal(err)
	}
	fake := &fakeUpdateHelper{}
	u := newUpdater(ss, Engine{Version: "0.1.1", DataDir: data})
	u.ck.URL, u.goos, u.helper, u.poll = gh.URL, "linux", fake, 10*time.Millisecond
	u.installType = func() string { return installType }
	mux := http.NewServeMux()
	v2Update(mux, u)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	postCheck(t, srv.URL+"/api/v2/update/check")
	return srv, u, st, fake
}

func postJSON(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var m map[string]any
	json.Unmarshal(b, &m)
	return resp.StatusCode, m
}

func updErrCode(m map[string]any) any {
	e, _ := m["error"].(map[string]any)
	return e["code"]
}

func runningJob(t *testing.T, st *jobs.Store) int64 {
	t.Helper()
	id, err := st.Enqueue(jobs.Job{Library: "/m", Path: filepath.Join(t.TempDir(), "a.mkv"), SizeBefore: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("t"); err != nil {
		t.Fatal(err)
	}
	return id
}

func updWaitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestUpdateInstallNow(t *testing.T) {
	srv, u, st, fake := installServer(t, "script")
	code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`)
	if code != http.StatusAccepted || body["method"] != "helper" {
		t.Fatalf("install: %d %v", code, body)
	}
	if got := fake.installs(); len(got) != 1 || got[0] != "install 0.2.0" {
		t.Fatalf("helper got %v, want one install of 0.2.0", got)
	}
	if inst := body["install"].(map[string]any); inst["state"] != "downloading" {
		t.Errorf("install state %v, want downloading", inst)
	}
	m := update.ReadMarker(u.dataDir)
	if m == nil || m.Kind != "update" || m.From != "0.1.1" || m.To != "0.2.0" || !m.Resume {
		t.Fatalf("marker %+v", m)
	}
	if _, err := os.Stat(m.Backup); err != nil {
		t.Errorf("the database backup: %v", err)
	}
	if !st.Paused() {
		t.Error("workers still take jobs during the update")
	}
	// a second click while the helper works
	if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`); code != http.StatusConflict || updErrCode(body) != "update_in_progress" {
		t.Errorf("second install: %d %v", code, body)
	}
}

func TestUpdateInstallRefusedWhileAJobRuns(t *testing.T) {
	srv, u, st, fake := installServer(t, "script")
	runningJob(t, st)
	code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`)
	if code != http.StatusConflict || updErrCode(body) != "job_running" {
		t.Fatalf("install with a running job: %d %v", code, body)
	}
	if len(fake.installs()) != 0 || update.ReadMarker(u.dataDir) != nil || st.Paused() {
		t.Errorf("a refused install changed something: helper %v, paused %v", fake.installs(), st.Paused())
	}
}

func TestUpdateInstallAfterJobWaits(t *testing.T) {
	srv, _, st, fake := installServer(t, "script")
	id := runningJob(t, st)
	code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"after-job"}`)
	if code != http.StatusAccepted || body["install"].(map[string]any)["state"] != "waiting" {
		t.Fatalf("after-job: %d %v", code, body)
	}
	if !st.Paused() {
		t.Error("new jobs still start while waiting")
	}
	time.Sleep(50 * time.Millisecond) // several polls
	if got := fake.installs(); len(got) != 0 {
		t.Fatalf("installed while a job runs: %v", got)
	}
	if err := st.Complete(id, "", 5); err != nil {
		t.Fatal(err)
	}
	updWaitFor(t, "the install after the job", func() bool { return len(fake.installs()) == 1 })
}

// A download or checksum failure leaves this program running: the marker goes and workers come back.
func TestUpdateHelperFailureBeforeTheSwapCleansUp(t *testing.T) {
	srv, u, st, fake := installServer(t, "script")
	postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`)
	fake.mu.Lock()
	fake.state, fake.errMsg = "failed", "checksum mismatch"
	fake.mu.Unlock()
	// wait for the failed state (set after the clean-up), then look at the marker once: reading it in a loop holds
	// the file open, and on Windows that can make the clean-up's delete fail
	updWaitFor(t, "the failed state", func() bool { u.mu.Lock(); defer u.mu.Unlock(); return u.state == "failed" })
	if update.ReadMarker(u.dataDir) != nil || st.Paused() {
		t.Errorf("not cleaned up: marker %v, paused %v", update.ReadMarker(u.dataDir), st.Paused())
	}
	_, body := getJSON(t, srv.URL+"/api/v2/update")
	if inst := body["install"].(map[string]any); inst["state"] != "failed" || inst["error"] != "checksum mismatch" {
		t.Errorf("install %v", inst)
	}
}

func TestUpdateInstallRefusals(t *testing.T) {
	srv, _, _, _ := installServer(t, "docker")
	code, body := getJSON(t, srv.URL+"/api/v2/update")
	cmds, _ := body["commands"].([]any)
	if body["method"] != "commands" || len(cmds) != 2 || cmds[0] != "docker compose pull" || cmds[1] != "docker compose up -d" {
		t.Errorf("docker: %d %v", code, body)
	}
	if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`); code != http.StatusConflict || updErrCode(body) != "install_unsupported" {
		t.Errorf("docker install: %d %v", code, body)
	}
	if code, body := postJSON(t, srv.URL+"/api/v2/update/rollback", `{}`); code != http.StatusConflict || updErrCode(body) != "install_unsupported" {
		t.Errorf("docker rollback: %d %v", code, body)
	}

	srv, u, _, _ := installServer(t, "script")
	u.helper = update.Helper{Socket: filepath.Join(t.TempDir(), "none.sock")} // not installed
	_, body = getJSON(t, srv.URL+"/api/v2/update")
	if cmds, _ := body["commands"].([]any); body["method"] != "commands" || len(cmds) != 1 || !strings.Contains(cmds[0].(string), "install.sh") {
		t.Errorf("script without the helper: %v", body)
	}

	srv, u, _, _ = installServer(t, "script")
	u.ck.Current = "0.2.0"
	if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`); code != http.StatusConflict || updErrCode(body) != "not_available" {
		t.Errorf("nothing newer: %d %v", code, body)
	}
}

func TestUpdateRollback(t *testing.T) {
	srv, u, _, fake := installServer(t, "script")
	if code, body := postJSON(t, srv.URL+"/api/v2/update/rollback", `{}`); code != http.StatusConflict || updErrCode(body) != "no_previous" {
		t.Fatalf("rollback without a previous version: %d %v", code, body)
	}
	fake.previous = true
	if code, body := postJSON(t, srv.URL+"/api/v2/update/rollback", `{}`); code != http.StatusAccepted {
		t.Fatalf("rollback: %d %v", code, body)
	}
	if m := update.ReadMarker(u.dataDir); m == nil || m.Kind != "rollback" || m.From != "0.1.1" {
		t.Errorf("marker %+v", m)
	}
	if got := fake.installs(); len(got) != 1 || got[0] != "rollback " {
		t.Errorf("helper got %v", got)
	}
}

// A roll back that put the database back says so in lastResult.
func TestUpdateLastResultSaysTheDatabaseWentBack(t *testing.T) {
	srv, u, _, _ := installServer(t, "script")
	m := &update.Marker{Kind: "rollback", From: "0.2.0", To: "0.1.1", DataRestored: true}
	if _, err := update.Settle(u.dataDir, m, "0.1.1", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, body := getJSON(t, srv.URL+"/api/v2/update")
	last, _ := body["lastResult"].(map[string]any)
	if last["dataRestored"] != true || last["message"] != "Rolled back from 0.2.0 to 0.1.1; changes made since the update were undone: jobs done on the newer version are kept in syp.db.rolled-back" {
		t.Errorf("lastResult %v", body["lastResult"])
	}
}

// After a roll back, the stored result is in GET /api/v2/update.
func TestUpdateLastResultShown(t *testing.T) {
	srv, u, _, _ := installServer(t, "script")
	m := &update.Marker{Kind: "update", From: "0.1.1", To: "0.2.0"}
	if _, err := update.Settle(u.dataDir, m, "0.1.1", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, body := getJSON(t, srv.URL+"/api/v2/update")
	last, _ := body["lastResult"].(map[string]any)
	if last["ok"] != false || last["message"] != "Update to 0.2.0 failed; rolled back to 0.1.1" || last["from"] != "0.1.1" || last["to"] != "0.2.0" || last["dataRestored"] != false {
		t.Errorf("lastResult %v", body["lastResult"])
	}
}

// The rollback block and the os and helperCommand fields: with the helper answering, without it, and off Linux.
func TestUpdateGetSaysWhatRollbackAndInstallNeed(t *testing.T) {
	srv, u, _, fake := installServer(t, "script")
	fake.previous, fake.previousVersion = true, "0.1.0"
	if _, err := update.Settle(u.dataDir, &update.Marker{Kind: "update", From: "0.1.0", To: "0.1.1"}, "0.1.1", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, body := getJSON(t, srv.URL+"/api/v2/update")
	rb, _ := body["rollback"].(map[string]any)
	if body["os"] != "linux" || body["helperCommand"] != nil || body["method"] != "helper" {
		t.Errorf("helper answering: os %v, helperCommand %v, method %v", body["os"], body["helperCommand"], body["method"])
	}
	if rb["available"] != true || rb["version"] != "0.1.0" || rb["dataRestored"] != true {
		t.Errorf("rollback with the helper answering: %v", rb)
	}

	// the helper is not answering on a script install: the line to install it, and no rollback
	u.helper = update.Helper{Socket: filepath.Join(t.TempDir(), "none.sock")}
	_, body = getJSON(t, srv.URL+"/api/v2/update")
	rb, _ = body["rollback"].(map[string]any)
	if cmd, _ := body["helperCommand"].(string); !strings.HasSuffix(cmd, "| sh -s -- --update-helper") || body["method"] != "commands" {
		t.Errorf("helper not answering: helperCommand %v, method %v", body["helperCommand"], body["method"])
	}
	if rb["available"] != false || rb["version"] != nil || rb["dataRestored"] != false {
		t.Errorf("rollback without the helper: %v, want {available false, version null, dataRestored false}", rb)
	}

	// Docker never gets the helper line
	srv, _, _, _ = installServer(t, "docker")
	_, body = getJSON(t, srv.URL+"/api/v2/update")
	rb, _ = body["rollback"].(map[string]any)
	if body["helperCommand"] != nil || rb["available"] != false || rb["version"] != nil || rb["dataRestored"] != false {
		t.Errorf("docker: helperCommand %v, rollback %v", body["helperCommand"], rb)
	}
}

// A failed update that the app rolled back says the rollback worked, in lastResult.rolledBack; a roll back
// that did not get the old version back does not.
func TestUpdateLastResultSaysTheRollbackWorked(t *testing.T) {
	srv, u, _, _ := installServer(t, "script")
	if _, err := update.Settle(u.dataDir, &update.Marker{Kind: "update", From: "0.1.1", To: "0.2.0"}, "0.1.1", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, body := getJSON(t, srv.URL+"/api/v2/update")
	last, _ := body["lastResult"].(map[string]any)
	if last["ok"] != false || last["rolledBack"] != true {
		t.Errorf("after a rollback that worked: lastResult %v", body["lastResult"])
	}
	if _, err := update.Settle(u.dataDir, &update.Marker{Kind: "rollback", From: "0.2.0", To: "0.1.1"}, "0.2.0", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, body = getJSON(t, srv.URL+"/api/v2/update")
	last, _ = body["lastResult"].(map[string]any)
	if last["ok"] != false || last["rolledBack"] != false {
		t.Errorf("after a roll back that failed: lastResult %v", body["lastResult"])
	}
}
