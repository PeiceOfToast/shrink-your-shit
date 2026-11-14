// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// fakeAnnouncer is an announcer whose announce counts its calls and never reaches the network.
func fakeAnnouncer(on bool, calls *atomic.Int32) *announcer {
	return &announcer{on: on, port: 8484, version: "test", announce: func(string, int, string) (func(), error) {
		calls.Add(1)
		return func() {}, nil
	}}
}

func openAnnounceStore(t *testing.T) *jobs.Store {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// A legacy install (libraries, no saved role) announces as a server: its worker setup page lists it.
func TestLegacyInstallWithLibrariesAnnouncesAsAServer(t *testing.T) {
	st := openAnnounceStore(t)
	if _, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true); err != nil {
		t.Fatal(err)
	}
	if got := announceRole(st); got != "server" {
		t.Fatalf("announceRole with libraries and no saved role = %q, want %q", got, "server")
	}
	var calls atomic.Int32
	fakeAnnouncer(true, &calls).SetRole(announceRole(st))
	if calls.Load() != 1 {
		t.Errorf("legacy install with libraries announced %d times, want 1", calls.Load())
	}
}

// An install with no libraries and no saved role is not a server yet, so it does not announce.
func TestFreshInstallWithoutLibrariesDoesNotAnnounce(t *testing.T) {
	st := openAnnounceStore(t)
	if got := announceRole(st); got != "" {
		t.Fatalf("announceRole with no libraries and no saved role = %q, want empty", got)
	}
	var calls atomic.Int32
	fakeAnnouncer(true, &calls).SetRole(announceRole(st))
	if calls.Load() != 0 {
		t.Errorf("fresh install announced %d times, want 0", calls.Load())
	}
}

// A saved role wins over the library rule: a saved worker never announces, even with libraries.
func TestSavedRoleWinsOverTheLegacyRule(t *testing.T) {
	st := openAnnounceStore(t)
	if _, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMeta("setupRole", "worker"); err != nil {
		t.Fatal(err)
	}
	if got := announceRole(st); got != "worker" {
		t.Fatalf("announceRole with a saved worker role = %q, want %q", got, "worker")
	}
	var calls atomic.Int32
	fakeAnnouncer(true, &calls).SetRole(announceRole(st))
	if calls.Load() != 0 {
		t.Errorf("saved worker announced %d times, want 0", calls.Load())
	}
}

// A server that listens on this computer alone never announces, legacy or not.
func TestLoopbackOnlyServerDoesNotAnnounce(t *testing.T) {
	var calls atomic.Int32
	fakeAnnouncer(false, &calls).SetRole("server")
	if calls.Load() != 0 {
		t.Errorf("loopback-only server announced %d times, want 0", calls.Load())
	}
}
