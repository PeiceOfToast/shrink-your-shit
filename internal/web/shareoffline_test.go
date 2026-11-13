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

	if _, ok := ShareDownFor(st, filepath.Join(mp, "Films")); ok {
		t.Fatal("a share that answers reads as down")
	}
	if err := os.RemoveAll(mp); err != nil {
		t.Fatal(err)
	}
	if d, ok := ShareDownFor(st, filepath.Join(mp, "Films")); !ok || d.ShareID != sh.ID || d.MountPath != mp {
		t.Fatalf("down share not found: %+v %v", d, ok)
	}
	if _, ok := ShareDownFor(st, t.TempDir()); ok {
		t.Error("a folder on no share reads as down")
	}
	checkShares(st)

	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs"})
	if !strings.Contains(body, `"code":"share_offline"`) || !strings.Contains(body, "Movies NAS isn't connected.") {
		t.Errorf("waiting file does not say why: %s", body)
	}
}
