// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// The worker pairs (sending its version and API level), saves its token readable only by its owner,
// heartbeats, and stops with the server's words once it is removed.
func TestPairSaveAndHeartbeat(t *testing.T) {
	var beats atomic.Int32
	removed := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-SYP-Worker") != "1.2.3; api=1" || r.Header.Get("Content-Type") != "application/json" {
			w.WriteHeader(426)
			w.Write([]byte(`{"error":{"code":"update_worker","message":"Update this worker."}}`))
			return
		}
		switch r.URL.Path {
		case "/api/worker/v1/pair":
			var in struct{ Code, Name string }
			json.NewDecoder(r.Body).Decode(&in)
			if in.Code != "ABCD-EFGH" || in.Name != "Desktop" {
				w.WriteHeader(403)
				return
			}
			w.Write([]byte(`{"workerId":"w-1","name":"Desktop","token":"secret"}`))
		case "/api/worker/v1/heartbeat":
			if r.Header.Get("Authorization") != "Bearer secret" || removed.Load() {
				w.WriteHeader(403)
				w.Write([]byte(`{"error":{"code":"worker_token","message":"This worker isn't paired with this server."}}`))
				return
			}
			if beats.Add(1) == 2 {
				removed.Store(true)
			}
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := &Client{Server: srv.URL, Version: "1.2.3"}
	st, err := c.Pair(context.Background(), "ABCD-EFGH", "Desktop")
	if err != nil || st.Token != "secret" || st.WorkerID != "w-1" || st.Server != srv.URL {
		t.Fatalf("%+v %v", st, err)
	}
	dir := filepath.Join(t.TempDir(), "worker")
	if err := st.Save(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(dir, StateFile)); fi == nil || runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode %v", fi)
	}
	if back, err := Load(dir); err != nil || back != st {
		t.Errorf("loaded %+v %v", back, err)
	}
	old := HeartbeatEvery
	HeartbeatEvery = 10 * time.Millisecond
	defer func() { HeartbeatEvery = old }()
	c.Token = st.Token
	err = c.Run(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Code != "worker_token" || beats.Load() != 2 {
		t.Errorf("run: %v after %d beats", err, beats.Load())
	}
}
