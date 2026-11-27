// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package library

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fixture is a mount table as the kernel writes it, from the lines given.
func fixture(lines ...string) []Mount {
	m, _ := ParseMountInfo(strings.NewReader(strings.Join(lines, "\n") + "\n"))
	return m
}

// relation is how two real folders stand on their shares: "same", "inside" (a under b), "contains" (b under a), or
// "none". A folder not on a network share is "none".
func relation(mounts []Mount, a, b string) string {
	sa, okA := ShareOf(mounts, a)
	sb, okB := ShareOf(mounts, b)
	switch {
	case !okA || !okB:
		return "none"
	case sa.same(sb):
		return "same"
	case sa.inside(sb):
		return "inside"
	case sb.inside(sa):
		return "contains"
	}
	return "none"
}

// nfsOneExport is one export mounted twice: /mnt/a and /mnt/b are both server:/export.
var nfsOneExport = []string{
	"36 25 0:32 / /mnt/a rw,relatime shared:1 - nfs4 server:/export rw,vers=4.2",
	"37 25 0:33 / /mnt/b rw,relatime shared:2 - nfs4 server:/export rw,vers=4.2",
}

// TestShareComparesMountSourceAndPath is the S2-10a table: each mount table and the relation the two folders have.
func TestShareComparesMountSourceAndPath(t *testing.T) {
	cases := []struct {
		name  string
		table []string
		a, b  string
		want  string
	}{
		{
			name:  "two mounts of one NFS export are one folder",
			table: nfsOneExport, a: "/mnt/a", b: "/mnt/b", want: "same",
		},
		{
			name: "a subfolder of an export reached through a second mount is the same folder",
			table: []string{
				"36 25 0:32 / /mnt/a rw - nfs4 server:/export rw",
				"37 25 0:33 / /mnt/b rw - nfs4 server:/export/sub rw",
			},
			a: "/mnt/a/sub", b: "/mnt/b", want: "same",
		},
		{
			name: "a subfolder of one export inside another mount is nested",
			table: []string{
				"36 25 0:32 / /mnt/a rw - nfs4 server:/export rw",
				"37 25 0:33 / /mnt/b rw - nfs4 server:/export/sub rw",
			},
			a: "/mnt/a/sub/x", b: "/mnt/b", want: "inside",
		},
		{
			name: "different exports on one server are different folders",
			table: []string{
				"36 25 0:32 / /mnt/a rw - nfs4 server:/export1 rw",
				"37 25 0:33 / /mnt/b rw - nfs4 server:/export2 rw",
			},
			a: "/mnt/a", b: "/mnt/b", want: "none",
		},
		{
			name: "SMB host and share names are case-insensitive",
			table: []string{
				"36 25 0:32 / /mnt/a rw - cifs //SERVER/share rw",
				"37 25 0:33 / /mnt/b rw - cifs //server/SHARE rw",
			},
			a: "/mnt/a", b: "/mnt/b", want: "same",
		},
		{
			name: "NFS export paths stay case-sensitive",
			table: []string{
				"36 25 0:32 / /mnt/a rw - nfs4 SERVER:/Export rw",
				"37 25 0:33 / /mnt/b rw - nfs4 server:/export rw",
			},
			a: "/mnt/a", b: "/mnt/b", want: "none",
		},
		{
			name: "trailing slashes on the export are not part of it",
			table: []string{
				"36 25 0:32 / /mnt/a rw - nfs4 server:/export/ rw",
				"37 25 0:33 / /mnt/b rw - nfs4 server:/export rw",
			},
			a: "/mnt/a", b: "/mnt/b", want: "same",
		},
		{
			name: "a folder under a share mounted inside another share sits on the inner share (longest mount point wins)",
			table: []string{
				"36 25 0:32 / /mnt/outer rw - nfs4 server:/outer rw",
				"37 25 0:33 / /mnt/outer/inner rw - nfs4 server:/inner rw",
				"38 25 0:34 / /mnt/b rw - nfs4 server:/inner rw",
			},
			a: "/mnt/outer/inner/x", b: "/mnt/b/x", want: "same",
		},
		{
			name: "the same folder through the outer share is a different share from the inner one",
			table: []string{
				"36 25 0:32 / /mnt/outer rw - nfs4 server:/outer rw",
				"37 25 0:33 / /mnt/outer/inner rw - nfs4 server:/inner rw",
				"38 25 0:34 / /mnt/b rw - nfs4 server:/inner rw",
			},
			a: "/mnt/outer/x", b: "/mnt/b/x", want: "none",
		},
		{
			name: "a local disk mounted under a share is not on the share (review #856)",
			table: []string{
				"36 25 0:32 / /mnt/nfs rw - nfs4 server:/export rw",
				"37 25 8:17 / /mnt/nfs/local rw - ext4 /dev/sdb1 rw",
				"38 25 0:33 / /mnt/b rw - nfs4 server:/export rw",
			},
			a: "/mnt/nfs/local/x", b: "/mnt/b/local/x", want: "none",
		},
		{
			name: "the same export path on two hosts is two shares (NFS)",
			table: []string{
				"36 25 0:32 / /mnt/a rw - nfs4 alpha:/media rw",
				"37 25 0:33 / /mnt/b rw - nfs4 beta:/media rw",
			},
			a: "/mnt/a", b: "/mnt/b", want: "none",
		},
		{
			name: "the same SMB share on two hosts is two shares",
			table: []string{
				"36 25 0:32 / /mnt/a rw - cifs //alpha/share rw",
				"37 25 0:33 / /mnt/b rw - cifs //beta/share rw",
			},
			a: "/mnt/a", b: "/mnt/b", want: "none",
		},
		{
			name: "local disks are unaffected: two mounts of one disk are not compared by source",
			table: []string{
				"36 25 8:17 / /mnt/a rw - ext4 /dev/sdb1 rw",
				"37 25 8:17 / /mnt/b rw - ext4 /dev/sdb1 rw",
			},
			a: "/mnt/a", b: "/mnt/b", want: "none",
		},
		{
			name: "a mount point with a space is read with its octal escape undone",
			table: []string{
				`36 25 0:32 / /mnt/my\040share rw - nfs4 server:/export rw`,
				"37 25 0:33 / /mnt/b rw - nfs4 server:/export rw",
			},
			a: "/mnt/my share", b: "/mnt/b", want: "same",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := relation(fixture(c.table...), c.a, c.b); got != c.want {
				t.Errorf("relation(%s, %s) = %q, want %q", c.a, c.b, got, c.want)
			}
		})
	}
}

// TestMountTableReadsWhatTheKernelWrites: a line that does not parse is skipped, not fatal to the table.
func TestMountTableReadsWhatTheKernelWrites(t *testing.T) {
	table := fixture(
		"this line has no separator",
		"36 25 0:32 / /mnt/a rw - nfs4 server:/export rw",
		"37 25 0:33 / /mnt/b rw - nfs4 server:/export rw",
	)
	if len(table) != 2 {
		t.Fatalf("mounts = %d, want the two that parse", len(table))
	}
	if table[0].Point != "/mnt/a" || table[0].FSType != "nfs4" || table[0].Source != "server:/export" {
		t.Errorf("first mount = %+v", table[0])
	}
}

// TestUnreadableMountTableSkipsTheShareCheck: a mount table that cannot be read is not an error, so the share check
// is skipped and the identity check alone decides.
func TestUnreadableMountTableSkipsTheShareCheck(t *testing.T) {
	saved := mountInfoSource
	defer func() { mountInfoSource = saved }()
	mountInfoSource = func() (io.ReadCloser, error) { return nil, errors.New("no table") }
	if got := readMounts(); got != nil {
		t.Errorf("readMounts = %v, want nil when the table cannot be read", got)
	}
}

// TestSecondMountOfOneNFSExportIsRefused is the integration: two real folders on one export are refused by the
// share compare, which the device identity cannot see. It runs with a fixture mount table on real folders, so no
// mount is needed; the mount table is the only thing faked.
func TestSecondMountOfOneNFSExportIsRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the mount table is read on Linux only")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	for _, d := range []string{a, b, filepath.Join(b, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	table := strings.Join([]string{
		"36 25 0:32 / " + a + " rw - nfs4 server:/export rw",
		"37 25 0:33 / " + b + " rw - nfs4 server:/export rw",
	}, "\n") + "\n"
	saved := mountInfoSource
	defer func() { mountInfoSource = saved }()
	mountInfoSource = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(table)), nil }

	c, _ := newTestCatalog(t)
	addLibrary(t, c, a)
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: b}); !errors.Is(err, ErrPathAlreadyWatched) {
		t.Fatalf("the second mount of the export was not refused as the same folder: %v", err)
	}
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: filepath.Join(b, "sub")}); !errors.Is(err, ErrPathNested) {
		t.Fatalf("a folder below the second mount was not refused as nested: %v", err)
	}
}

// TestLocalDisksAreNotRefusedBySource: two mounts of one local disk with no shared identity are not the share check's
// business, so both libraries are allowed.
func TestLocalDisksAreNotRefusedBySource(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the mount table is read on Linux only")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(base, "a")
	b := filepath.Join(base, "b")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	table := "36 25 8:17 / " + a + " rw - ext4 /dev/sdb1 rw\n37 25 8:17 / " + b + " rw - ext4 /dev/sdb1 rw\n"
	saved := mountInfoSource
	defer func() { mountInfoSource = saved }()
	mountInfoSource = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(table)), nil }

	c, _ := newTestCatalog(t)
	addLibrary(t, c, a)
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: b}); err != nil {
		t.Fatalf("a second local mount was refused by the share check: %v", err)
	}
}

// TestLocalDiskUnderAShareIsNotRefused: a library on a local disk mounted inside a share is a different folder from the
// share folder another library holds, so the add is allowed (review #856, the false refusal).
func TestLocalDiskUnderAShareIsNotRefused(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the mount table is read on Linux only")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	nas := filepath.Join(base, "nas")
	local := filepath.Join(nas, "local")
	b := filepath.Join(base, "b")
	for _, d := range []string{nas, local, filepath.Join(b, "local")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	table := strings.Join([]string{
		"36 25 0:32 / " + nas + " rw - nfs4 server:/export rw",
		"37 25 8:17 / " + local + " rw - ext4 /dev/sdb1 rw",
		"38 25 0:33 / " + b + " rw - nfs4 server:/export rw",
	}, "\n") + "\n"
	saved := mountInfoSource
	defer func() { mountInfoSource = saved }()
	mountInfoSource = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(table)), nil }

	c, _ := newTestCatalog(t)
	addLibrary(t, c, filepath.Join(b, "local")) // the share's folder /export/local
	if _, err := c.AddLibrary(context.Background(), NewLibrary{Path: local}); err != nil {
		t.Fatalf("the local disk under the share was refused as the share folder: %v", err)
	}
}

// TestOverlapsReportsTwoMountsOfOneShare: an install that already holds two mounts of one share is reported at start-up
// by the share compare, which the identity cannot see (review #856).
func TestOverlapsReportsTwoMountsOfOneShare(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the mount table is read on Linux only")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(base, "a", "shows")
	b := filepath.Join(base, "b", "shows")
	for _, d := range []string{a, b} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	table := strings.Join([]string{
		"36 25 0:32 / " + filepath.Join(base, "a") + " rw - nfs4 server:/export rw",
		"37 25 0:33 / " + filepath.Join(base, "b") + " rw - nfs4 server:/export rw",
	}, "\n") + "\n"
	saved := mountInfoSource
	defer func() { mountInfoSource = saved }()
	mountInfoSource = func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(table)), nil }

	c, _ := newTestCatalog(t)
	addLibrary(t, c, a)
	if err := c.db.Tx(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(),
			`INSERT INTO libraries(name, path, workflow_id, watch, paused, next_scan_at) VALUES(?, ?, NULL, 'daily', 0, ?)`,
			"Second mount", b, testNow().UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	overlaps, err := c.Overlaps(context.Background())
	if err != nil {
		t.Fatalf("overlaps: %v", err)
	}
	if len(overlaps) != 1 || !overlaps[0].Same {
		t.Fatalf("overlaps = %+v, want one same-folder pair from two mounts of one share", overlaps)
	}
}
