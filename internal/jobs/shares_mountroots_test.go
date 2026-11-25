// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// MountRoots is the union of the mount point each share was given and the path the share daemon recorded: each
// folder once, and an empty value is dropped.
func TestMountRootsIsTheUnionOfBothRecords(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	save := func(name string) Share {
		sh, err := s.SaveShare(Share{Type: "smb", Address: "//h/" + name, Name: name, Guest: true}, "")
		if err != nil {
			t.Fatal(err)
		}
		return sh
	}
	withPoint := save("point") // mount point a, no daemon record
	if err := s.SetShareMountPoint(withPoint.ID, a); err != nil {
		t.Fatal(err)
	}
	both := save("both") // mount point a, and the daemon recorded a too: listed once
	if err := s.SetShareMountPoint(both.ID, a); err != nil {
		t.Fatal(err)
	}
	daemon := save("daemon") // no mount point, the daemon recorded b
	empty := save("empty")   // recorded with no path: dropped
	for _, sh := range []Share{both, daemon, empty} {
		if err := s.SetShareMountStatus(sh.ID, "linux", "connected", ""); err != nil {
			t.Fatal(err)
		}
	}
	for id, path := range map[int64]string{both.ID: a, daemon.ID: b} {
		if _, err := s.db.Exec(`UPDATE share_mounts SET mount_path=? WHERE share_id=?`, path, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.MountRoots()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{a, b}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MountRoots = %q, want %q", got, want)
	}
}
