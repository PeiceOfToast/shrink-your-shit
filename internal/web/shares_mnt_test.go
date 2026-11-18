// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// setMountRoots points the new and the older share folders at temp folders, so nothing here touches /mnt.
func setMountRoots(t *testing.T) (newRoot, legacyRoot string) {
	t.Helper()
	newRoot, legacyRoot = t.TempDir(), t.TempDir()
	oldNew, oldLegacy := newShareRoot, shareRoot
	newShareRoot, shareRoot = newRoot, legacyRoot
	t.Cleanup(func() { newShareRoot, shareRoot = oldNew, oldLegacy })
	return newRoot, legacyRoot
}

func setShareHome(t *testing.T) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", home) // the share key folder on Windows
}

// An older share (no stored mount point) keeps its folder under shareRoot; a stored one is used as it is.
func TestMountPointForOlderAndNewShares(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows connects shares itself: no mount folder")
	}
	_, legacy := setMountRoots(t)
	older := jobs.Share{ID: 4, Name: "Old NAS"}
	if got, want := mountPointFor(older), legacy+"/old-nas"; got != want {
		t.Errorf("older share: %q, want %q", got, want)
	}
	newer := jobs.Share{ID: 5, Name: "Media", MountPoint: "/mnt/media"}
	if got := mountPointFor(newer); got != "/mnt/media" {
		t.Errorf("stored mount point: %q, want /mnt/media", got)
	}
}

// A share made now is mounted at newShareRoot/<name>.
func TestNewShareMountsUnderMnt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows connects shares itself: no mount folder")
	}
	setShareHome(t)
	newRoot, _ := setMountRoots(t)
	srv, _, st := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\media","name":"Media Library","username":"alice"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	shares, err := st.LoadShares()
	if err != nil {
		t.Fatal(err)
	}
	if len(shares) != 1 {
		t.Fatalf("want 1 share, got %d", len(shares))
	}
	if got, want := shares[0].MountPoint, newRoot+"/media-library"; got != want {
		t.Errorf("new share mount point %q, want %q", got, want)
	}
}

// A rename keeps each share in its own root: a new share's folder follows its name under newShareRoot,
// and an older share keeps its folder under shareRoot, with no stored mount point.
func TestRenameKeepsEachShareInItsRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows connects shares itself: no mount folder")
	}
	setShareHome(t)
	newRoot, legacy := setMountRoots(t)
	srv, _, st := server(t, false)

	newer, err := st.SaveShare(jobs.Share{Name: "Old Name", Type: "smb", Address: `//nas/x`, Username: "alice"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareMountPoint(newer.ID, newRoot+"/old-name"); err != nil {
		t.Fatal(err)
	}
	older, err := st.SaveShare(jobs.Share{Name: "Legacy", Type: "smb", Address: `//nas/y`, Username: "alice"}, "")
	if err != nil {
		t.Fatal(err)
	}

	code, resp, _ := do(t, nil, srv, call{method: "PATCH", path: fmt.Sprintf("/api/v2/shares/%d", newer.ID),
		body:  `{"address":"\\\\nas\\x","name":"New Name","username":"alice"}`,
		ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("PATCH new share: %d %s", code, resp)
	}
	code, resp, _ = do(t, nil, srv, call{method: "PATCH", path: fmt.Sprintf("/api/v2/shares/%d", older.ID),
		body:  `{"address":"\\\\nas\\y","name":"Renamed Legacy","username":"alice"}`,
		ctype: "application/json"})
	if code != http.StatusOK {
		t.Fatalf("PATCH older share: %d %s", code, resp)
	}

	gotNew, err := st.ShareByID(newer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := newRoot + "/new-name"; gotNew.MountPoint != want {
		t.Errorf("renamed new share mount point %q, want %q", gotNew.MountPoint, want)
	}
	gotOld, err := st.ShareByID(older.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotOld.MountPoint != "" {
		t.Errorf("older share got a mount point %q on rename; it should stay on the legacy path", gotOld.MountPoint)
	}
	if want := legacy + "/renamed-legacy"; mountPointFor(*gotOld) != want {
		t.Errorf("older share mounts at %q, want %q", mountPointFor(*gotOld), want)
	}
}
