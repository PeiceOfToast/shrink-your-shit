// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/remote"
)

// joinServer answers a join and gives each poll the next of states (the last repeats), like the server's
// Workers page would once the person answers.
func joinServer(t *testing.T, states ...string) *httptest.Server {
	t.Helper()
	var polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/worker/v1/join":
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"j-1","expiresAt":"2026-11-14T03:00:00Z","secret":"s3cret"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/worker/v1/join/j-1" && r.Header.Get("Authorization") == "Bearer s3cret":
			i := min(int(polls.Add(1))-1, len(states)-1)
			if states[i] == "allowed" {
				w.Write([]byte(`{"state":"allowed","workerId":"w-9","name":"Office PC","token":"tok"}`))
				return
			}
			w.Write([]byte(`{"state":"` + states[i] + `"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	old := remote.JoinEvery
	remote.JoinEvery = time.Millisecond
	t.Cleanup(func() { remote.JoinEvery = old })
	return srv
}

// Allowed on the server: the token is saved in the data folder (readable only by its owner) and the command
// carries on. Here the tools are missing on purpose, so it stops there with exit 1 after the save.
func TestJoinAllowedSavesTheTokenThenCarriesOn(t *testing.T) {
	srv := joinServer(t, "waiting", "allowed")
	data := t.TempDir()
	var out, errOut bytes.Buffer
	code := workerCommand([]string{"--server", srv.URL, "--join", "--name", "Office PC", "--data", data,
		"--ffmpeg", filepath.Join(t.TempDir(), "no-ffmpeg"), "--ffprobe", filepath.Join(t.TempDir(), "no-ffprobe")}, &out, &errOut)
	if !strings.Contains(out.String(), "Waiting for "+srv.URL+" to allow this worker. Open its Workers page and click Allow.") {
		t.Errorf("no waiting line: %q", out.String())
	}
	if !strings.Contains(out.String(), `Joined `+srv.URL+` as "Office PC".`) {
		t.Errorf("no joined line: %q", out.String())
	}
	if code != 1 || !strings.Contains(errOut.String(), "needs ffmpeg") {
		t.Fatalf("exit %d after the save, want 1 at the tools: %q", code, errOut.String())
	}
	st, err := remote.Load(data)
	if err != nil {
		t.Fatalf("no saved state: %v", err)
	}
	if st.Token != "tok" || st.WorkerID != "w-9" || st.Server != srv.URL {
		t.Errorf("saved state %+v", st)
	}
	if fi, err := os.Stat(filepath.Join(data, remote.StateFile)); err == nil && fi.Mode().Perm()&0o077 != 0 {
		t.Errorf("token file mode %v, want owner only", fi.Mode().Perm())
	}
}

func TestJoinDeniedSaysNoAndSavesNothing(t *testing.T) {
	srv := joinServer(t, "waiting", "denied")
	data := t.TempDir()
	var out, errOut bytes.Buffer
	if code := workerCommand([]string{"--server", srv.URL, "--join", "--data", data}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "The server said no.") {
		t.Errorf("message %q", errOut.String())
	}
	if _, err := remote.Load(data); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a denied join saved a state: %v", err)
	}
}

func TestJoinExpiredSaysToRunAgain(t *testing.T) {
	srv := joinServer(t, "expired")
	var out, errOut bytes.Buffer
	if code := workerCommand([]string{"--server", srv.URL, "--join", "--data", t.TempDir()}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if want := "Nobody allowed it within 10 minutes. Run the command again."; !strings.Contains(errOut.String(), want) {
		t.Errorf("message %q", errOut.String())
	}
}

// --pair and --join are two ways in; giving both is a usage error, and nothing is asked.
func TestPairAndJoinTogetherIsAUsageError(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := workerCommand([]string{"--server", "http://127.0.0.1:1", "--pair", "ABCD-EFGH", "--join", "--data", t.TempDir()}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "Use --pair or --join, not both.") {
		t.Errorf("message %q", errOut.String())
	}
}
