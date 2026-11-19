//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
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

// serveAs runs one request through serve as a caller with uid (ok false: its identity can't be read).
func serveAs(t *testing.T, h *helper, uid int, ok bool, req string) update.HelperAnswer {
	t.Helper()
	old := callerUID
	callerUID = func(*net.UnixConn) (int, bool) { return uid, ok }
	t.Cleanup(func() { callerUID = old })
	sock := filepath.Join(t.TempDir(), "s")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			serve(c.(*net.UnixConn), 1000, h)
		}
	}()
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte(req + "\n"))
	var a update.HelperAnswer
	if err := json.NewDecoder(c).Decode(&a); err != nil {
		t.Fatal(err)
	}
	return a
}

// Only root and the service user (uid 1000 here) may use the helper; an unreadable caller is refused.
func TestOnlyRootAndTheServiceUserMayCall(t *testing.T) {
	h, _ := testHelper(t, "", func() bool { return true })
	os.WriteFile(h.bin+".previous", []byte("prev"), 0o755)
	for _, c := range []struct {
		uid  int
		ok   bool
		want string
	}{{1001, true, "not_allowed"}, {65534, true, "not_allowed"}, {1000, false, "not_allowed"}, {0, false, "not_allowed"}} {
		if a := serveAs(t, h, c.uid, c.ok, `{"cmd":"rollback"}`); a.OK || a.Code != c.want {
			t.Errorf("uid %d (read %v): %+v, want %s", c.uid, c.ok, a, c.want)
		}
	}
	if read(t, h.bin) != "old" {
		t.Fatal("a refused caller changed the program")
	}
	for _, uid := range []int{1000, 0} {
		if a := serveAs(t, h, uid, true, `{"cmd":"status"}`); !a.OK || a.State != "idle" {
			t.Errorf("uid %d status: %+v", uid, a)
		}
	}
}

// A program that hangs on -version does not hold up a status call.
func TestStatusAnswersWhileTheVersionIsRead(t *testing.T) {
	h, _ := testHelper(t, "", func() bool { return true })
	release := make(chan struct{})
	h.version = func(string) (string, error) { <-release; return "0.1.0", nil }
	defer close(release)
	go h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"})
	time.Sleep(20 * time.Millisecond)
	got := make(chan update.HelperAnswer, 1)
	go func() { got <- h.handle(update.HelperRequest{Cmd: "status"}) }()
	select {
	case a := <-got:
		if !a.OK || a.State != "idle" {
			t.Errorf("status: %+v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("status waited for the version read")
	}
}

// A second install or a roll back while an install runs is refused as busy, and changes nothing.
func TestASecondOperationWhileOneRunsIsRefused(t *testing.T) {
	gate := make(chan struct{})
	rel := releaseServer(t, "0.2.0", "new")
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-gate // the first install's download waits here
		http.Redirect(w, r, rel.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(slow.Close)
	h, _ := testHelper(t, slow.URL, func() bool { return true })
	os.WriteFile(h.bin+".previous", []byte("prev"), 0o755)
	if a := h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"}); !a.OK {
		t.Fatalf("first install: %+v", a)
	}
	for _, req := range []update.HelperRequest{{Cmd: "install", Version: "0.3.0"}, {Cmd: "rollback"}} {
		if a := h.handle(req); a.OK || a.Code != "busy" {
			close(gate)
			t.Fatalf("%s while an install runs: %+v, want busy", req.Cmd, a)
		}
	}
	close(gate)
	if a := finish(t, h); a.State != "idle" || read(t, h.bin) != "new" || read(t, h.bin+".previous") != "old" {
		t.Errorf("after: %+v, bin %q", a, read(t, h.bin))
	}
}

// B reads the installed version (slowly), A installs a newer one meanwhile: B's version is stale and
// B must not install over A's (a downgrade).
func TestAnInstallWhoseVersionReadOverlappedAnotherIsRefused(t *testing.T) {
	srv := releaseServer(t, "0.2.0", "new")
	h, _ := testHelper(t, srv.URL, func() bool { return true })
	inRead, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	h.version = func(bin string) (string, error) {
		v := map[string]string{"old": "0.1.0", "new": "0.2.0"}[read(t, bin)]
		if first.CompareAndSwap(false, true) { // B's read: sees 0.1.0, then hangs
			close(inRead)
			<-release
		}
		return v, nil
	}
	got := make(chan update.HelperAnswer, 1)
	go func() { got <- h.handle(update.HelperRequest{Cmd: "install", Version: "0.1.5"}) }() // B
	<-inRead
	if a := h.handle(update.HelperRequest{Cmd: "install", Version: "0.2.0"}); !a.OK { // A
		t.Fatalf("A: %+v", a)
	}
	finish(t, h)
	close(release)
	if a := <-got; a.OK || a.Code != "busy" {
		t.Errorf("B after A finished: %+v, want busy", a)
	}
	if read(t, h.bin) != "new" {
		t.Errorf("bin %q: B installed over A", read(t, h.bin))
	}
}
