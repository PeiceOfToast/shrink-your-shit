//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

// releaseServer serves release v<version> for this machine, with prog as its program.
func releaseServer(t *testing.T, version, prog string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "shrink-your-shit_linux_" + runtime.GOARCH + "/shrink-your-shit", Mode: 0o755, Size: int64(len(prog)), Typeflag: tar.TypeReg})
	tw.Write([]byte(prog))
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	name := update.ArchiveName("linux", runtime.GOARCH)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v" + version + "/" + name:
			w.Write(archive)
		case "/v" + version + "/checksums.txt":
			w.Write([]byte(hex.EncodeToString(sum[:]) + "  " + name + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// testHelper is a helper for an install of v0.1.0 at a temporary path; healthy answers the health check.
func testHelper(t *testing.T, base string, healthy func() bool) (*helper, *int32) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "shrink-your-shit")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	var restarts int32
	return &helper{bin: bin, base: base, client: http.DefaultClient, wait: 50 * time.Millisecond, poll: 5 * time.Millisecond,
		restart: func() error { atomic.AddInt32(&restarts, 1); return nil },
		healthy: healthy,
		version: func(string) (string, error) { return "0.1.0", nil }}, &restarts
}

func finish(t *testing.T, h *helper) update.HelperAnswer {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the operation did not end")
	}
	return h.handle(update.HelperRequest{Cmd: "status"})
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRefusesAnythingButANewerPlainVersion(t *testing.T) {
	h, _ := testHelper(t, "http://127.0.0.1:1", func() bool { return true })
	for v, code := range map[string]string{
		"":          "bad_request",
		"v0.2.0":    "bad_request",
		"0.2":       "bad_request",
		"0.2.0-rc1": "bad_request",
		"../0.2.0":  "bad_request",
		"0.2.0/x":   "bad_request",
		"0.1.0":     "not_newer",
		"0.0.9":     "not_newer",
	} {
		if a := h.handle(update.HelperRequest{Cmd: "install", Version: v}); a.OK || a.Code != code {
			t.Errorf("install %q: %+v, want %s", v, a, code)
		}
	}
	if a := h.handle(update.HelperRequest{Cmd: "rollback"}); a.OK || a.Code != "no_previous" {
		t.Errorf("rollback with nothing kept: %+v", a)
	}
	if a := h.handle(update.HelperRequest{Cmd: "rm -rf /"}); a.OK || a.Code != "bad_request" {
		t.Errorf("unknown command: %+v", a)
	}
}

func TestInstallSwapsInTheNewProgramAndKeepsThePrevious(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "new")
	h, restarts := testHelper(t, srv.URL, func() bool { return true })
	if a := h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"}); !a.OK || a.State != "downloading" {
		t.Fatalf("install: %+v", a)
	}
	if a := h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"}); a.OK || a.Code != "busy" {
		t.Errorf("a second install while one runs: %+v", a)
	}
	a := finish(t, h)
	if a.State != "idle" || a.Error != "" || !a.Previous {
		t.Fatalf("after: %+v", a)
	}
	if read(t, h.bin) != "new" || read(t, h.bin+".previous") != "old" || *restarts != 1 {
		t.Errorf("bin %q, previous %q, restarts %d", read(t, h.bin), read(t, h.bin+".previous"), *restarts)
	}
	if _, err := os.Stat(h.bin + ".new"); !os.IsNotExist(err) {
		t.Errorf("the download is left: %v", err)
	}
}

// A new program that never answers "ok" is replaced by the previous one, which is restarted.
func TestInstallRollsBackWhenTheNewProgramDoesNotAnswer(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "broken")
	h, restarts := testHelper(t, srv.URL, nil)
	h.healthy = func() bool { return read(t, h.bin) == "old" }
	h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"})
	a := finish(t, h)
	if a.State != "failed" || !strings.Contains(a.Error, "did not answer") || strings.Contains(a.Error, "does not answer either") {
		t.Fatalf("after: %+v", a)
	}
	if read(t, h.bin) != "old" || *restarts != 2 {
		t.Errorf("bin %q, restarts %d: want the previous program back, restarted twice", read(t, h.bin), *restarts)
	}
}

func TestAFailedDownloadLeavesTheProgram(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "new")
	h, restarts := testHelper(t, srv.URL+"/nothing-here", func() bool { return true })
	h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"})
	if a := finish(t, h); a.State != "failed" || read(t, h.bin) != "old" || a.Previous || *restarts != 0 {
		t.Errorf("after: %+v, bin %q, restarts %d", a, read(t, h.bin), *restarts)
	}
}

func TestRollbackSwapsBackAndUndoesWhenThePreviousDoesNotAnswer(t *testing.T) {
	h, _ := testHelper(t, "", func() bool { return true })
	os.WriteFile(h.bin+".previous", []byte("prev"), 0o755)
	h.handle(update.HelperRequest{Cmd: "rollback"})
	if a := finish(t, h); a.State != "idle" || read(t, h.bin) != "prev" || read(t, h.bin+".previous") != "old" {
		t.Fatalf("rollback: %+v, bin %q", a, read(t, h.bin))
	}
	h.healthy = func() bool { return read(t, h.bin) == "prev" } // "old" (now the previous) does not answer
	h.handle(update.HelperRequest{Cmd: "rollback"})
	if a := finish(t, h); a.State != "failed" || read(t, h.bin) != "prev" || read(t, h.bin+".previous") != "old" {
		t.Errorf("failed rollback: %+v, bin %q", a, read(t, h.bin))
	}
}
