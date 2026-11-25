// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// U-B: Empty trash sends what it could not reach as a list, [] when there is nothing, never null.
func TestEmptyTrashSendsUnreachableAsAList(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, body := post(t, srv, "POST", "/api/v2/trash/purge", `{}`)
	if code != 200 {
		t.Fatalf("empty trash: status %d: %s", code, body)
	}
	if !strings.Contains(body, `"unreachable":[]`) {
		t.Errorf("want \"unreachable\":[] beside deletedFiles: %s", body)
	}
}

// U-B: forgetting an id that is not a kept original is a 404, and nothing is changed.
func TestForgetAnUnknownOriginalIsNotFound(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, body := post(t, srv, "POST", "/api/v2/originals/999999/forget", "")
	if code != 404 {
		t.Errorf("forget unknown id: status %d, want 404: %s", code, body)
	}
}

// U-B P2: a kept original under a share that is not connected is not known to be gone: forget is refused with
// not_mounted, and the row stays kept.
func TestForgetRefusesAKeptOriginalOnAnUnpluggedShare(t *testing.T) {
	if runtime.GOOS != "linux" { // shares are watched on Linux and Windows; the mount path here is a Linux one
		t.Skip("shares are watched on Linux")
	}
	srv, st, _, _ := libServer(t, false)
	oldAnswers := shareAnswers
	shareAnswers = func(string) bool { return false }
	t.Cleanup(func() { shareAnswers = oldAnswers })

	mnt := filepath.Join(t.TempDir(), "mnt")
	if err := os.MkdirAll(filepath.Join(mnt, "movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh, err := st.SaveShare(jobs.Share{Type: "smb", Address: "//nas/movies", Name: "Movies"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareMountPoint(sh.ID, mnt); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(mnt, "movies", "film.mkv")
	jobID, err := st.Enqueue(jobs.Job{Library: filepath.Join(mnt, "movies"), Path: src})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	row, err := st.BeginSwap(jobs.Original{JobID: jobID, Path: src, Trash: filepath.Join(mnt, "movies", ".syp-trash", "film.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishSwap(row, time.Now().Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if o, _ := st.OriginalByID(row); o.State != jobs.OriginalKept {
		t.Fatalf("setup: row %s, want kept", o.State)
	}

	code, body := post(t, srv, "POST", fmt.Sprintf("/api/v2/originals/%d/forget", row), "")
	if code != 409 || !strings.Contains(body, `"not_mounted"`) {
		t.Errorf("forget on an unplugged share: status %d, want 409 not_mounted: %s", code, body)
	}
	if o, _ := st.OriginalByID(row); o.State != jobs.OriginalKept {
		t.Errorf("row %s after the refusal, want kept", o.State)
	}
}
