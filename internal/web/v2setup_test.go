// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func TestV2SetupFreshInstallIsUnset(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/setup"})
	if code != 200 || !strings.Contains(b, `"role":"unset"`) || !strings.Contains(b, `"join":null`) {
		t.Fatalf("fresh: %d %s", code, b)
	}
}

func TestV2SetupExistingInstallIsAServer(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	if _, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true); err != nil {
		t.Fatal(err)
	}
	code, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/setup"})
	if code != 200 || !strings.Contains(b, `"role":"server"`) {
		t.Fatalf("existing install: %d %s", code, b)
	}
}

func TestV2SetupRole(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	send := func(method, body string) (int, string) {
		t.Helper()
		path := "/api/v2/setup"
		if method == "POST" {
			path += "/role"
		}
		code, b, _ := do(t, nil, srv, call{method: method, path: path, body: body, ctype: "application/json"})
		return code, b
	}

	if code, b := send("POST", `{"role":"worker"}`); code != 400 || errCode(b) != "not_available" {
		t.Errorf("worker: %d %s", code, b)
	}
	if code, b := send("POST", `{"role":"boss"}`); code != 400 || errCode(b) != "invalid_role" {
		t.Errorf("unknown role: %d %s", code, b)
	}
	if code, b := send("POST", `{"role":"server"}`); code != 200 || !strings.Contains(b, `"role":"server"`) {
		t.Fatalf("server: %d %s", code, b)
	}
	if code, b := send("GET", ""); code != 200 || !strings.Contains(b, `"role":"server"`) {
		t.Errorf("server then get: %d %s", code, b)
	}
	if code, b := send("POST", `{"role":"unset"}`); code != 200 || !strings.Contains(b, `"role":"unset"`) {
		t.Errorf("unset with nothing running: %d %s", code, b)
	}

	if code, b := send("POST", `{"role":"server"}`); code != 200 {
		t.Fatalf("server again: %d %s", code, b)
	}
	st.Enqueue(jobs.Job{Library: "/m", Path: "/m/a.mkv"})
	st.Claim("w")
	if code, b := send("POST", `{"role":"unset"}`); code != 409 || errCode(b) != "role_change_busy" || !strings.Contains(b, `"runningJobs":1`) {
		t.Errorf("unset with a job running: %d %s", code, b)
	}
	if code, b := send("GET", ""); !strings.Contains(b, `"role":"server"`) {
		t.Errorf("a refused change must leave the role: %d %s", code, b)
	}

	events, err := st.EventsAfter(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, ev := range events {
		if ev.Kind == "setup.changed" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("no setup.changed event after a role change: %+v", events)
	}
}
