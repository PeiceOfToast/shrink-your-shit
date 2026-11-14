//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMountPoint(t *testing.T) {
	ok := []string{"/var/lib/syp/shares/media", "/var/lib/syp/shares/nas-media_2", "/var/lib/syp/shares/a.b"}
	bad := []string{
		"", "/var/lib/syp/shares", "/var/lib/syp/shares/", "/var/lib/syp/shares/media/",
		"/var/lib/syp/shares/../etc", "/var/lib/syp/shares/./media", "/var/lib/syp/shares//media",
		"/var/lib/syp/shares/a/b", "/etc", "/home", "/", "var/lib/syp/shares/media",
		"/var/lib/syp/shares/-x", "/var/lib/syp/shares/.hidden", "/var/lib/syp/shares/Media",
		"/var/lib/syp/shares/a b", "/var/lib/syp/shares/" + strings.Repeat("a", 65),
		"/var/lib/syp/sharesX/media", "/var/lib/syp/shares/a\nb",
	}
	for _, mp := range ok {
		if err := validateMountPoint(mp); err != nil {
			t.Errorf("%q refused: %v", mp, err)
		}
	}
	for _, mp := range bad {
		if validateMountPoint(mp) == nil {
			t.Errorf("%q accepted", mp)
		}
	}
}

func TestValidateAddress(t *testing.T) {
	ok := [][2]string{
		{"cifs", "//nas/media"}, {"cifs", "//192.168.1.5/Movies and TV"}, {"cifs", "//nas.local/share/sub folder"},
		{"cifs", "//[fe80::1]/media"}, {"nfs", "nas:/volume1/media"}, {"nfs", "10.0.0.2:/export"},
		{"nfs", "[fd00::2]:/srv/nfs"},
	}
	bad := [][2]string{
		{"cifs", ""}, {"cifs", "nas/media"}, {"cifs", "//nas"}, {"cifs", "//nas/"}, {"cifs", "//nas//x"},
		{"cifs", "-o/remount"}, {"cifs", "//-nas/media"}, {"cifs", `//nas/me\dia`}, {"cifs", "//nas/me\ndia"},
		{"cifs", "//user@nas/media"}, {"cifs", "nas:/volume1"},
		{"nfs", ""}, {"nfs", "nas"}, {"nfs", "nas:volume1"}, {"nfs", "-nas:/x"}, {"nfs", "//nas/media"},
		{"nfs", "nas:/x\x00y"},
		{"ext4", "/dev/sda1"}, {"tmpfs", "tmpfs"}, {"", "//nas/media"}, {"fuse", "//nas/media"},
	}
	for _, c := range ok {
		if err := validateAddress(c[0], c[1]); err != nil {
			t.Errorf("%s %q refused: %v", c[0], c[1], err)
		}
	}
	for _, c := range bad {
		if validateAddress(c[0], c[1]) == nil {
			t.Errorf("%s %q accepted", c[0], c[1])
		}
	}
}

func TestValidateOptions(t *testing.T) {
	ok := []map[string]string{
		nil, {}, {"vers": "3.1.1"}, {"vers": "4"}, {"soft": ""}, {"hard": ""}, {"timeo": "600"},
		{"sec": "ntlmssp"}, {"domain": "WORKGROUP"}, {"domain": "CORP_1"}, {"port": "445", "rsize": "1048576"},
	}
	bad := []map[string]string{
		{"vers": "3.0,suid"},    // a comma would add another option
		{"vers": "3=1"},         // and so would '='
		{"domain": "CORP,exec"}, // the same through another option
		{"domain": "CORP X"},    // no spaces in a domain name
		{"sec": "krb5,dev"},     //
		{"soft": "1"},           // a flag takes no value
		{"timeo": "-1"}, {"timeo": "1e9"}, {"port": "445,suid"},
		{"suid": ""}, {"dev": ""}, {"exec": ""}, {"bind": ""}, {"user": ""}, {"users": ""},
		{"uid": "0"}, {"gid": "0"}, {"credentials": "/etc/shadow"}, {"file_mode": "4777"},
		{"remount": ""}, {"rw": ""}, {"": ""}, {"vers": ""},
	}
	for _, o := range ok {
		if err := validateOptions(o); err != nil {
			t.Errorf("%v refused: %v", o, err)
		}
	}
	for _, o := range bad {
		if validateOptions(o) == nil {
			t.Errorf("%v accepted", o)
		}
	}
}

func TestValidateCredentials(t *testing.T) {
	if err := validateCredentials(`DOMAIN\me`, "p@ss word=,;!"); err != nil {
		t.Errorf("ordinary credentials refused: %v", err)
	}
	if err := validateCredentials("", ""); err != nil {
		t.Errorf("guest refused: %v", err)
	}
	for _, c := range [][2]string{
		{"me", "x\ndomain=evil"}, {"me\npassword=x", "y"}, {"me", "x\ry"}, {"me", "x\x00y"},
		{strings.Repeat("u", 257), "x"}, {"me", strings.Repeat("p", 1025)},
	} {
		if validateCredentials(c[0], c[1]) == nil {
			t.Errorf("%q / %q accepted", c[0], c[1])
		}
	}
}

func TestMountOptionsAlwaysSafe(t *testing.T) {
	got := mountOptions("cifs", map[string]string{"vers": "3.1.1", "soft": ""}, 1001, 1002, "/run/x/share-1")
	want := "soft,vers=3.1.1,nosuid,nodev,noexec,uid=1001,gid=1002,file_mode=0660,dir_mode=0770,credentials=/run/x/share-1"
	if got != want {
		t.Errorf("cifs:\n got %s\nwant %s", got, want)
	}
	if got := mountOptions("cifs", nil, 1001, 1002, ""); !strings.HasSuffix(got, ",guest") || !strings.HasPrefix(got, "nosuid,nodev,noexec,") {
		t.Errorf("cifs guest: %s", got)
	}
	if got := mountOptions("nfs", map[string]string{"vers": "4.2"}, 1001, 1002, ""); got != "vers=4.2,nosuid,nodev,noexec" {
		t.Errorf("nfs: %s", got)
	}
}

// In a container (-nfs-nolock) NFS gets nolock, since there is no rpc.statd; cifs never does.
func TestMountOptionsNFSNoLock(t *testing.T) {
	nfsNoLock = true
	defer func() { nfsNoLock = false }()
	if got := mountOptions("nfs", nil, 1001, 1002, ""); got != "nosuid,nodev,noexec,nolock" {
		t.Errorf("nfs with -nfs-nolock: %s", got)
	}
	if got := mountOptions("cifs", nil, 1001, 1002, ""); strings.Contains(got, "nolock") {
		t.Errorf("cifs must not get nolock: %s", got)
	}
}

// withSharesDir points sharesDir at a temp folder for one test.
func withSharesDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "shares")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := sharesDir
	sharesDir = dir
	t.Cleanup(func() { sharesDir = old })
	return dir
}

func TestPrepareMountPoint(t *testing.T) {
	me := os.Getuid()
	dir := withSharesDir(t)

	mp := filepath.Join(dir, "media")
	if err := prepareMountPoint(mp, me); err != nil {
		t.Fatalf("fresh mount point: %v", err)
	}
	if fi, err := os.Lstat(mp); err != nil || !fi.IsDir() {
		t.Fatalf("not created: %v", err)
	}
	if err := prepareMountPoint(mp, me); err != nil {
		t.Errorf("existing mount point: %v", err)
	}

	// a symlink planted in the shares folder (e.g. "evil -> /etc") is refused, for mount and unmount
	target := t.TempDir()
	evil := filepath.Join(dir, "evil")
	if err := os.Symlink(target, evil); err != nil {
		t.Fatal(err)
	}
	if prepareMountPoint(evil, me) == nil {
		t.Error("symlink mount point accepted for mount")
	}
	if checkExistingMountPoint(evil, me) == nil {
		t.Error("symlink mount point accepted for unmount/status")
	}
	if err := checkExistingMountPoint(filepath.Join(dir, "missing"), me); err != nil {
		t.Errorf("missing mount point (nothing to do) refused: %v", err)
	}

	// a plain file is refused
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if prepareMountPoint(file, me) == nil {
		t.Error("file accepted as mount point")
	}

	// the wrong owner is refused (the folder belongs to me, not to "root" uid me+1)
	if prepareMountPoint(mp, me+1) == nil {
		t.Error("shares folder with the wrong owner accepted")
	}
}

func TestSharesDirMustNotBeWritableByOthers(t *testing.T) {
	me := os.Getuid()
	dir := withSharesDir(t)
	for _, mode := range []os.FileMode{0o775, 0o757, 0o777} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if checkSharesDir(me) == nil {
			t.Errorf("shares folder %o accepted", mode)
		}
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := checkSharesDir(me); err != nil {
		t.Errorf("shares folder 0755 refused: %v", err)
	}

	// a symlinked shares folder is refused
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "shares-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	sharesDir = link
	if checkSharesDir(me) == nil {
		t.Error("symlinked shares folder accepted")
	}
}
