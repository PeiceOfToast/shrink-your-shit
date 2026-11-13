// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// D4b: a share whose folder stops answering pauses its libraries (no job taken, the page says
// paused_share with pausedBy); when it answers again they start by themselves. The person's own pause
// stays "disabled" through both.
func TestShareOutagePausesItsLibrariesUntilItAnswers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("app mode (the mount helper, share mount points) is Linux only")
	}
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	root := shareRoot
	shareRoot = t.TempDir()
	shareAnswers = func(p string) bool { _, err := os.Stat(p); return err == nil } // the fake mount: a folder that exists
	t.Cleanup(func() {
		shareRoot = root
		shareAnswers = func(p string) bool { return mountedAt(p) && statWithin(p, shareStatTimeout) }
	})

	sh, err := st.SaveShare(jobs.Share{Name: "Movies NAS", Type: "smb", Address: "//nas/movies"}, "")
	if err != nil {
		t.Fatal(err)
	}
	mp := mountPointFor(sh)
	if err := os.MkdirAll(filepath.Join(mp, "Films"), 0o755); err != nil {
		t.Fatal(err)
	}
	films, _ := st.AddLibrary("Films", filepath.Join(mp, "Films"), rules.SliderTemplate(3), true)
	kids, _ := st.AddLibrary("Kids", filepath.Join(mp, "Kids"), rules.SliderTemplate(3), true)
	if err := st.StopLibrary(kids.ID); err != nil { // the person's own pause
		t.Fatal(err)
	}
	job, _ := st.Enqueue(jobs.Job{Library: films.Path, LibraryID: films.ID, Path: filepath.Join(films.Path, "a.mkv")})

	checkShares(st)
	if l, _ := st.GetLibrary(films.ID); l.SharePausedBy != 0 {
		t.Fatalf("paused while the share answers: %+v", l)
	}

	if err := os.RemoveAll(mp); err != nil { // the NAS goes away
		t.Fatal(err)
	}
	checkShares(st)
	if l, _ := st.GetLibrary(films.ID); l.SharePausedBy != sh.ID || !l.Started {
		t.Fatalf("outage: want share-paused by %d and still started, got %+v", sh.ID, l)
	}
	if _, err := st.Claim("w"); err != jobs.ErrNoJob {
		t.Errorf("a share-paused library's job was taken: %v (job %d)", err, job)
	}
	mounts, _ := st.ShareMountsByShare(sh.ID)
	if len(mounts) != 1 || mounts[0].Status != "not_connected" || !strings.Contains(mounts[0].Error, `"host_unreachable"`) {
		t.Errorf("share status during outage: %+v", mounts)
	}

	id := strconv.FormatInt(films.ID, 10)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/libraries/" + id})
	var v struct {
		Status   string `json:"status"`
		PausedBy struct {
			ShareID   string `json:"shareId"`
			ShareName string `json:"shareName"`
			Use       string `json:"use"`
		} `json:"pausedBy"`
	}
	json.Unmarshal([]byte(body), &v)
	if v.Status != "paused_share" || v.PausedBy.ShareID != strconv.FormatInt(sh.ID, 10) || v.PausedBy.ShareName != "Movies NAS" || v.PausedBy.Use != "folder" {
		t.Errorf("during outage: %s", body)
	}
	_, kidsBody, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/libraries/" + strconv.FormatInt(kids.ID, 10)})
	if !strings.Contains(kidsBody, `"status":"disabled"`) || !strings.Contains(kidsBody, `"pausedBy":null`) {
		t.Errorf("the person's pause during an outage: %s", kidsBody)
	}

	if err := os.MkdirAll(filepath.Join(mp, "Films"), 0o755); err != nil { // the share answers again
		t.Fatal(err)
	}
	checkShares(st)
	if l, _ := st.GetLibrary(films.ID); l.SharePausedBy != 0 || !l.Started {
		t.Errorf("after recovery: %+v", l)
	}
	if j, err := st.Claim("w"); err != nil || j.ID != job {
		t.Errorf("after recovery claimed %d %v, want %d", j.ID, err, job)
	}
	mounts, _ = st.ShareMountsByShare(sh.ID)
	if len(mounts) != 1 || mounts[0].Status != "connected" {
		t.Errorf("share status after recovery: %+v", mounts)
	}
	if l, _ := st.GetLibrary(kids.ID); l.Started || l.SharePausedBy != 0 {
		t.Errorf("the person's pause changed by the outage: %+v", l)
	}
	_, kidsBody, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/libraries/" + strconv.FormatInt(kids.ID, 10)})
	if !strings.Contains(kidsBody, `"status":"disabled"`) {
		t.Errorf("the person's pause after recovery: %s", kidsBody)
	}
}

// The real check: a folder that is not a mount is not "answering" (an empty folder would look fine),
// and an existing folder is not "mounted".
func TestShareAnswersNeedsAMountAndAFolder(t *testing.T) {
	dir := t.TempDir()
	if mountedAt(dir) {
		t.Errorf("a plain folder reads as mounted")
	}
	if statWithin(filepath.Join(dir, "gone"), shareStatTimeout) {
		t.Errorf("a missing folder answered")
	}
	if !statWithin(dir, shareStatTimeout) {
		t.Errorf("an existing folder did not answer")
	}
}

// D4c: a file waiting in a share-paused library says why (share_offline) on the jobs list; the share's
// folder being down is what ShareDownFor reports for a failed convert, and only then.
func TestWaitingFileSaysItsShareIsOffline(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("app mode (the mount helper, share mount points) is Linux only")
	}
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	root := shareRoot
	shareRoot = t.TempDir()
	shareAnswers = func(p string) bool { _, err := os.Stat(p); return err == nil }
	t.Cleanup(func() {
		shareRoot = root
		shareAnswers = func(p string) bool { return mountedAt(p) && statWithin(p, shareStatTimeout) }
	})

	sh, _ := st.SaveShare(jobs.Share{Name: "Movies NAS", Type: "smb", Address: "//nas/movies"}, "")
	mp := mountPointFor(sh)
	if err := os.MkdirAll(filepath.Join(mp, "Films"), 0o755); err != nil {
		t.Fatal(err)
	}
	films, _ := st.AddLibrary("Films", filepath.Join(mp, "Films"), rules.SliderTemplate(3), true)
	st.Enqueue(jobs.Job{Library: films.Path, LibraryID: films.ID, Path: filepath.Join(films.Path, "a.mkv")})
	st.SetShareMountStatus(sh.ID, "linux", "connected", "") // it was set up and connected once (D3a: only then can it drop)

	if _, ok := ShareDownFor(st, filepath.Join(mp, "Films"), 0); ok {
		t.Fatal("a share that answers reads as down")
	}
	if err := os.RemoveAll(mp); err != nil {
		t.Fatal(err)
	}
	if d, ok := ShareDownFor(st, filepath.Join(mp, "Films"), 0); !ok || d.ShareID != sh.ID || d.MountPath != mp {
		t.Fatalf("down share not found: %+v %v", d, ok)
	}
	if _, ok := ShareDownFor(st, t.TempDir(), 0); ok {
		t.Error("a folder on no share reads as down")
	}
	checkShares(st)

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs"})
	if !strings.Contains(body, `"code":"share_offline"`) || !strings.Contains(body, "Movies NAS isn't connected.") {
		t.Errorf("waiting file does not say why: %s", body)
	}
}

// D4c fix: a share that drops and comes back while a convert runs is down for that convert, so its failure is
// put back; a share with no change since the job started is not.
func TestShareChangedDuringConvertCountsAsDown(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("app mode (the mount helper, share mount points) is Linux only")
	}
	_, st, _, _ := libServer(t, false)
	root := shareRoot
	shareRoot = t.TempDir()
	shareAnswers = func(p string) bool { _, err := os.Stat(p); return err == nil }
	t.Cleanup(func() {
		shareRoot = root
		shareAnswers = func(p string) bool { return mountedAt(p) && statWithin(p, shareStatTimeout) }
	})

	sh, _ := st.SaveShare(jobs.Share{Name: "Movies NAS", Type: "smb", Address: "//nas/movies"}, "")
	mp := mountPointFor(sh)
	if err := os.MkdirAll(filepath.Join(mp, "Films"), 0o755); err != nil {
		t.Fatal(err)
	}
	films, _ := st.AddLibrary("Films", filepath.Join(mp, "Films"), rules.SliderTemplate(3), true)
	st.Enqueue(jobs.Job{Library: films.Path, LibraryID: films.ID, Path: filepath.Join(films.Path, "a.mkv")})
	st.SetShareMountStatus(sh.ID, "linux", "connected", "")
	time.Sleep(1100 * time.Millisecond) // RFC3339 is to the second: the next stamps must differ
	job, err := st.Claim("w")
	if err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(mp, "Films")
	if _, ok := ShareDownFor(st, folder, job.ID); ok {
		t.Fatal("a share connected since before the job started reads as changed")
	}
	time.Sleep(1100 * time.Millisecond)
	st.SetShareMountStatus(sh.ID, "linux", "not_connected", "gone")
	st.SetShareMountStatus(sh.ID, "linux", "connected", "")
	d, ok := ShareDownFor(st, folder, job.ID)
	if !ok || !d.Answering || d.ShareID != sh.ID || d.MountPath != mp {
		t.Fatalf("an outage during the convert is not down for it: %+v %v", d, ok)
	}
}

// D3a: in manual and docker mode a share never connected waits for its setup: it is not down, its libraries
// are not paused, and the check (POST .../check) looks at its folder at once and reports it connected.
func TestNeverConnectedShareWaitsForItsSetup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("app mode (the mount helper, share mount points) is Linux only")
	}
	srv, st, _, _ := libServer(t, false)
	st.SetPaused(false)
	root := shareRoot
	shareRoot = t.TempDir()
	shareAnswers = func(p string) bool { _, err := os.Stat(p); return err == nil }
	t.Cleanup(func() {
		shareRoot = root
		shareAnswers = func(p string) bool { return mountedAt(p) && statWithin(p, shareStatTimeout) }
	})

	sh, _ := st.SaveShare(jobs.Share{Name: "Movies NAS", Type: "smb", Address: `\\nas\movies`}, "")
	mp := mountPointFor(sh)
	if err := os.MkdirAll(filepath.Join(mp, "Films"), 0o755); err != nil {
		t.Fatal(err)
	}
	films, _ := st.AddLibrary("Films", filepath.Join(mp, "Films"), rules.SliderTemplate(3), true)
	if err := os.RemoveAll(mp); err != nil { // not set up yet: nothing is mounted there
		t.Fatal(err)
	}
	checkShares(st)
	check := call{method: "POST", path: "/api/v2/shares/" + strconv.FormatInt(sh.ID, 10) + "/check", body: "{}", ctype: "application/json"}
	_, body, _ := do(t, nil, srv, check)
	if !strings.Contains(body, `"status":"waiting_setup"`) {
		t.Errorf("a share never connected is not waiting for its setup: %s", body)
	}
	if l, _ := st.GetLibrary(films.ID); l.SharePausedBy != 0 {
		t.Errorf("a share never connected paused its library: %+v", l)
	}
	if err := os.MkdirAll(filepath.Join(mp, "Films"), 0o755); err != nil { // the person has set it up
		t.Fatal(err)
	}
	_, body, _ = do(t, nil, srv, check)
	if !strings.Contains(body, `"status":"connected"`) {
		t.Errorf("a share set up is not connected at once: %s", body)
	}
}

// D3a: the docker setup text is a compose snippet that works for each kind of share, its volume line uses the
// share's path, and the password never appears in the share's JSON.
func TestDockerSetupTextWorksForEachShare(t *testing.T) {
	t.Setenv("container", "docker")
	_, st, _, _ := libServer(t, false)
	cases := []struct {
		name string
		sh   jobs.Share
		pw   string
		want []string
	}{
		{"smb with password", jobs.Share{Name: "Movies NAS", Type: "smb", Address: `\\nas\movies`, Username: "bob"}, "hunter2-secret",
			[]string{`device: "//nas/movies"`,
				`o: "addr=nas,username=bob,password=${SYP_MOVIES_NAS_PASSWORD},vers=3.1.1,soft,uid=1000,gid=1000,file_mode=0660,dir_mode=0770"`,
				"SYP_MOVIES_NAS_PASSWORD=<the share password> in the .env file", "  shrink-your-shit:"}},
		{"smb guest", jobs.Share{Name: "Movies NAS", Type: "smb", Address: `\\nas\movies`, Guest: true}, "",
			[]string{`device: "//nas/movies"`, `o: "addr=nas,guest,vers=3.1.1,soft,uid=1000,gid=1000,file_mode=0660,dir_mode=0770"`, "  shrink-your-shit:"}},
		{"nfs", jobs.Share{Name: "Movies NAS", Type: "nfs", Address: "nas:/export"}, "",
			[]string{`type: nfs`, `device: ":/export"`, `o: "addr=nas,soft,nfsvers=4"`, "  shrink-your-shit:"}},
	}
	for _, c := range cases {
		sh, err := st.SaveShare(c.sh, c.pw)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		m := shareToJSON(st, sh)
		b, _ := json.Marshal(m)
		if c.pw != "" && strings.Contains(string(b), c.pw) {
			t.Errorf("%s: the password is in the share's JSON: %s", c.name, b)
		}
		setup, _ := m["setup"].(map[string]any)
		text, _ := setup["text"].(string)
		vol := `- "movies-nas:` + m["path"].(string) + `"`
		if setup["kind"] != "compose" || !strings.Contains(text, vol) {
			t.Errorf("%s: the volume line does not use the share's path %q: %s", c.name, m["path"], text)
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%s: setup text lacks %q:\n%s", c.name, w, text)
			}
		}
	}
}
