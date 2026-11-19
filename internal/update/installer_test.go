// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsInstallerRefusesAMismatchedSetup(t *testing.T) {
	srv := setupServer(t, map[string][]byte{"0.1.0": []byte("old"), "0.2.0": []byte("new")}, true)
	dir := t.TempDir()
	var started bool
	w := &WindowsInstaller{DataDir: dir, Current: "0.1.0", Base: srv.URL, Client: srv.Client(), Health: "http://unused/healthz",
		Self: func() (string, error) { return os.Args[0], nil }, Start: func(string, []string) error { started = true; return nil }, PID: 1}
	if _, err := w.Call(HelperRequest{Cmd: "install", Version: "0.2.0"}); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("install err %v, want a checksum mismatch", err)
	}
	if started {
		t.Error("the watcher started with a setup that did not verify")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, updateDir)); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

// Both installers verify, the watcher is a copy of this program, and it starts with the app's pid and both paths.
func TestWindowsInstallerStartsTheWatcherWithBothSetups(t *testing.T) {
	srv := setupServer(t, map[string][]byte{"0.1.0": []byte("old"), "0.2.0": []byte("new")}, false)
	dir := t.TempDir()
	self := filepath.Join(t.TempDir(), "shrink-your-shit.exe")
	if err := os.WriteFile(self, []byte("program"), 0o755); err != nil {
		t.Fatal(err)
	}
	var exe string
	var args []string
	w := &WindowsInstaller{DataDir: dir, Current: "0.1.0", Base: srv.URL, Client: srv.Client(), Health: "http://127.0.0.1:8484/healthz",
		Self: func() (string, error) { return self, nil },
		Start: func(e string, a []string) error {
			exe, args = e, a
			return nil
		}, PID: 4242}
	ans, err := w.Call(HelperRequest{Cmd: "install", Version: "0.2.0"})
	if err != nil || !ans.OK {
		t.Fatalf("install: %+v, %v", ans, err)
	}
	upd := filepath.Join(dir, updateDir)
	if exe != filepath.Join(upd, "syp-update-watch.exe") {
		t.Errorf("watcher %q", exe)
	}
	if got, _ := os.ReadFile(exe); string(got) != "program" {
		t.Errorf("watcher copy %q", got)
	}
	want := []string{"update-watch", "-pid", "4242", "-from", "0.1.0", "-to", "0.2.0",
		"-new", filepath.Join(upd, "setup-0.2.0.exe"), "-old", filepath.Join(upd, "setup-0.1.0.exe"),
		"-data", dir, "-health", "http://127.0.0.1:8484/healthz"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("args %q, want %q", args, want)
	}
	for _, p := range []string{want[8], want[10]} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("setup %s: %v", p, err)
		}
	}
}

func TestWindowsInstallerRefusesAnOlderVersion(t *testing.T) {
	w := &WindowsInstaller{DataDir: t.TempDir(), Current: "0.2.0"}
	_, err := w.Call(HelperRequest{Cmd: "install", Version: "0.1.9"})
	if he, ok := err.(*HelperError); !ok || he.Code != "not_newer" {
		t.Errorf("err %v", err)
	}
}

func TestWindowsInstallerRollBackIsNotOffered(t *testing.T) {
	w := &WindowsInstaller{DataDir: t.TempDir(), Current: "0.2.0"}
	if _, err := w.Call(HelperRequest{Cmd: "rollback"}); err == nil {
		t.Error("a roll back was accepted on Windows")
	}
}

func TestHealthURL(t *testing.T) {
	for addr, want := range map[string]string{
		"127.0.0.1:8484": "http://127.0.0.1:8484/healthz",
		":8484":          "http://127.0.0.1:8484/healthz",
		"0.0.0.0:9000":   "http://127.0.0.1:9000/healthz",
		"[::]:8484":      "http://127.0.0.1:8484/healthz",
	} {
		if got := healthURL(addr); got != want {
			t.Errorf("healthURL(%q) = %q, want %q", addr, got, want)
		}
	}
}
