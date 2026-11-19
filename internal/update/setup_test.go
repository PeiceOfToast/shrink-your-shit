// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupServer serves each version's installer and its checksums.txt (which also lists another file).
// badSum writes a wrong checksum for the installer.
func setupServer(t *testing.T, setups map[string][]byte, badSum bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for ver, body := range setups {
			switch r.URL.Path {
			case "/v" + ver + "/checksums.txt":
				sum := sha256.Sum256(body)
				got := hex.EncodeToString(sum[:])
				if badSum {
					got = strings.Repeat("0", 64)
				}
				w.Write([]byte(strings.Repeat("1", 64) + "  shrink-your-shit_linux_amd64.tar.gz\n" + got + "  " + SetupName + "\n"))
				return
			case "/v" + ver + "/" + SetupName:
				w.Write(body)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchSetupAcceptsAMatch(t *testing.T) {
	body := []byte("installer 0.2.0")
	srv := setupServer(t, map[string][]byte{"0.2.0": body}, false)
	dest := filepath.Join(t.TempDir(), "setup.exe")
	if err := FetchSetup(context.Background(), srv.Client(), srv.URL, "0.2.0", dest); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(body) {
		t.Errorf("installer %q", got)
	}
}

// A setup.exe whose checksum does not match is refused: nothing is left at dest or under a temp name.
func TestFetchSetupRefusesAChecksumMismatch(t *testing.T) {
	srv := setupServer(t, map[string][]byte{"0.2.0": []byte("tampered")}, true)
	dir := t.TempDir()
	dest := filepath.Join(dir, "setup.exe")
	err := FetchSetup(context.Background(), srv.Client(), srv.URL, "0.2.0", dest)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err %v, want a checksum mismatch", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestFetchSetupRefusesAVersionThatIsNotPlain(t *testing.T) {
	if err := FetchSetup(context.Background(), http.DefaultClient, "http://unused", "v0.2.0", filepath.Join(t.TempDir(), "x")); err == nil {
		t.Error("a version with a v prefix was accepted")
	}
}
