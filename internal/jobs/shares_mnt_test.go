// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
)

// A share saved before the upgrade has no mount point (the older folder), and a stored one round-trips.
func TestShareMountPointRoundTrips(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	sh, err := st.SaveShare(Share{Name: "Older", Type: "smb", Address: "//nas/o"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.ShareByID(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MountPoint != "" {
		t.Errorf("share with no stored mount point: %q, want empty", got.MountPoint)
	}

	if err := st.SetShareMountPoint(sh.ID, "/mnt/older"); err != nil {
		t.Fatal(err)
	}
	got, err = st.ShareByID(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.MountPoint != "/mnt/older" {
		t.Errorf("ShareByID mount point %q, want /mnt/older", got.MountPoint)
	}
	all, err := st.LoadShares()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].MountPoint != "/mnt/older" {
		t.Errorf("LoadShares: %+v", all)
	}
}
