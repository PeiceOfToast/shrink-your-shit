// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// releaseArchive is a release archive as scripts/build-release.sh makes it, with prog as the program.
func releaseArchive(t *testing.T, goos, goarch string, prog []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	dir := "shrink-your-shit_" + goos + "_" + goarch + "/"
	for name, body := range map[string][]byte{dir + "README.md": []byte("readme"), dir + "shrink-your-shit": prog, dir + "syp-update-helper": []byte("helper")} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(body)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// releaseServer serves /v<version>/<archive> and checksums.txt; sum overrides the archive's checksum.
func releaseServer(t *testing.T, version string, archive []byte, sum string) *httptest.Server {
	t.Helper()
	name := ArchiveName("linux", "amd64")
	if sum == "" {
		h := sha256.Sum256(archive)
		sum = hex.EncodeToString(h[:])
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v" + version + "/" + name:
			w.Write(archive)
		case "/v" + version + "/checksums.txt":
			w.Write([]byte(strings.Repeat("0", 64) + "  other.zip\n" + sum + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

var testNow = time.Date(2026, 11, 19, 3, 0, 0, 0, time.UTC)

func TestFetchVerifiesAndExtractsOnlyTheProgram(t *testing.T) {
	srv := releaseServer(t, "1.2.3", releaseArchive(t, "linux", "amd64", []byte("new program")), "")
	dir := t.TempDir()
	dest := filepath.Join(dir, "shrink-your-shit.new")
	if err := Fetch(context.Background(), srv.Client(), srv.URL+"/", "1.2.3", "linux", "amd64", dest); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != "new program" {
		t.Fatalf("dest: %q %v", b, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestFetchRefusesAChecksumMismatchAndDeletesTheDownload(t *testing.T) {
	srv := releaseServer(t, "1.2.3", releaseArchive(t, "linux", "amd64", []byte("tampered")), strings.Repeat("a", 64))
	dir := t.TempDir()
	dest := filepath.Join(dir, "shrink-your-shit.new")
	err := Fetch(context.Background(), srv.Client(), srv.URL, "1.2.3", "linux", "amd64", dest)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v, want a checksum mismatch", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("a refused download left files: %v", entries)
	}
}

func TestFetchRefusals(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "p")
	noProgram := releaseArchive(t, "linux", "arm64", []byte("x")) // the wrong folder inside
	srv := releaseServer(t, "1.2.3", noProgram, "")
	for _, c := range []struct{ version, want string }{
		{"1.2.3", "the archive has no shrink-your-shit_linux_amd64/shrink-your-shit"},
		{"1.2.4", "HTTP 404"},
		{"../1.2.3", "not a release version"},
	} {
		err := Fetch(context.Background(), srv.Client(), srv.URL, c.version, "linux", "amd64", dest)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", c.version, err, c.want)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestValidVersion(t *testing.T) {
	for _, v := range []string{"0.1.0", "1.2.3", "10.0.99"} {
		if !ValidVersion(v) {
			t.Errorf("%q refused", v)
		}
	}
	for _, v := range []string{"", "v1.2.3", "1.2", "1.2.3.4", "1.2.3-rc1", "01.2.3", "1.2.3 ", "1..3", "a.b.c", "1.2.3/../x"} {
		if ValidVersion(v) {
			t.Errorf("%q accepted", v)
		}
	}
}

// Settle: a program still at From was rolled back; any other version is the new one.
func TestSettleBothWays(t *testing.T) {
	dir := t.TempDir()
	m := Marker{Kind: "update", From: "0.1.0", To: "0.2.0", Backup: filepath.Join(dir, "syp.db.bak-v40")}
	if err := WriteMarker(dir, m); err != nil {
		t.Fatal(err)
	}
	r, err := Settle(dir, ReadMarker(dir), "0.2.0", testNow)
	if err != nil || !r.OK || r.Message != "Updated from v0.1.0 to v0.2.0" || r.Backup != m.Backup {
		t.Fatalf("new version: %+v %v", r, err)
	}
	if ReadMarker(dir) != nil {
		t.Error("the marker is still there")
	}
	WriteMarker(dir, m)
	r, _ = Settle(dir, ReadMarker(dir), "0.1.0", testNow)
	if got := LastResult(dir); r.OK || got == nil || got.Message != "Update to v0.2.0 failed; rolled back to v0.1.0" || got.From != "0.1.0" || got.To != "0.2.0" {
		t.Fatalf("rolled back: %+v", got)
	}
	WriteMarker(dir, Marker{Kind: "rollback", From: "0.2.0"})
	if r, _ := Settle(dir, ReadMarker(dir), "0.1.0", testNow); !r.OK || r.Message != "Rolled back from v0.2.0 to v0.1.0" {
		t.Errorf("manual roll back: %+v", r)
	}
}

func TestRestoreDB(t *testing.T) {
	dir := t.TempDir()
	db, backup := filepath.Join(dir, "syp.db"), filepath.Join(dir, "syp.db.bak-v40")
	os.WriteFile(db, []byte("newer"), 0o600)
	os.WriteFile(db+"-wal", []byte("wal"), 0o600)
	os.WriteFile(backup, []byte("older"), 0o600)
	if err := RestoreDB(backup, db); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(db); string(b) != "older" {
		t.Errorf("db = %q", b)
	}
	if b, _ := os.ReadFile(db + ".rolled-back"); string(b) != "newer" {
		t.Errorf("the newer database was not kept: %q", b)
	}
	if _, err := os.Stat(db + "-wal"); !os.IsNotExist(err) {
		t.Errorf("the newer database's WAL is still there: %v", err)
	}
}
