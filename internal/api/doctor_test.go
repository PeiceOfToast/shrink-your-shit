// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
)

// doctorJSON is GET /doctor's answer as the page reads it.
type doctorJSON struct {
	RanAt  string `json:"ranAt"`
	Checks []struct {
		ID     string  `json:"id"`
		Label  string  `json:"label"`
		Status string  `json:"status"`
		Detail string  `json:"detail"`
		Hint   *string `json:"hint"`
		Fix    *struct {
			Label  string `json:"label"`
			Method string `json:"method"`
			Path   string `json:"path"`
		} `json:"fix"`
	} `json:"checks"`
}

// getDoctor reads the health check's answer, failing on anything but 200.
func (e *env) getDoctor(t *testing.T, path string, cookie *http.Cookie) doctorJSON {
	t.Helper()
	rec := e.do(http.MethodGet, path, "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s; want 200", path, rec.Code, rec.Body)
	}
	var got doctorJSON
	decodeInto(t, rec, &got)
	return got
}

// TestDoctorRouteServesTheChecks: page 11's health check answers its checks over the server's own clock, and the
// card's fix is page 10's Test the encoders.
func TestDoctorRouteServesTheChecks(t *testing.T) {
	e := newEnv(t, false)
	e.cards = qsvCards()
	e.ready()
	lib := t.TempDir()
	cookie := cookieNamed(t, e.do(http.MethodPost, "/api/v2/setup",
		setupWith(t, map[string]any{"name": "Films", "path": lib}), write(nil), false), sessionCookie)
	got := e.getDoctor(t, "/api/v2/doctor", cookie)
	if got.RanAt != "2026-11-27T04:00:00Z" {
		t.Errorf("ranAt = %q, want the server clock's 2026-11-27T04:00:00Z", got.RanAt)
	}
	want := map[string]string{"disk": "ok", "tools": "ok", "gpu": "ok", "library-1": "ok"}
	if len(got.Checks) != len(want) {
		t.Fatalf("checks = %d (%+v), want %d", len(got.Checks), got.Checks, len(want))
	}
	for _, c := range got.Checks {
		if want[c.ID] != c.Status {
			t.Errorf("%s = %q (%s), want %q", c.ID, c.Status, c.Detail, want[c.ID])
		}
		if c.ID != "gpu" {
			if c.Fix != nil {
				t.Errorf("%s has fix %+v; only the card has a route", c.ID, c.Fix)
			}
			continue
		}
		if c.Fix == nil || c.Fix.Label != "Test the encoders" || c.Fix.Method != "POST" ||
			c.Fix.Path != "/api/v2/workers/local/capabilities/test" {
			t.Errorf("gpu fix = %+v, want Test the encoders (POST /api/v2/workers/local/capabilities/test)", c.Fix)
		}
	}
}

// TestDoctorServesTheLastRunAndRetests: a plain read is the last run even when the clock moved; ?retest=1 runs the
// checks again.
func TestDoctorServesTheLastRunAndRetests(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	first := e.getDoctor(t, "/api/v2/doctor", cookie)
	e.clock.Advance(time.Minute)
	second := e.getDoctor(t, "/api/v2/doctor", cookie)
	if second.RanAt != first.RanAt {
		t.Errorf("a second read ran again (ranAt %q → %q), want the last run", first.RanAt, second.RanAt)
	}
	again := e.getDoctor(t, "/api/v2/doctor?retest=1", cookie)
	if again.RanAt != "2026-11-27T04:01:00Z" {
		t.Errorf("retest ranAt = %q, want the clock's new 2026-11-27T04:01:00Z", again.RanAt)
	}
}

// TestDoctorStatusChangeWritesOneHealthEvent: a check that changes is one line in the system log with source
// health, and a check that did not change says nothing.
func TestDoctorStatusChangeWritesOneHealthEvent(t *testing.T) {
	e := newEnv(t, false)
	free := uint64(400 << 30)
	e.doctor = system.NewDoctor(system.Sources{
		Now:        e.clock.Now,
		DataDir:    e.dir,
		DiskFree:   func(string) (uint64, error) { return free, nil },
		LowSpaceGB: func() int { return 10 },
		Emit:       e.bus,
	})
	e.ready()
	cookie := e.setup()
	e.getDoctor(t, "/api/v2/doctor", cookie) // all ok: nothing to say
	e.getDoctor(t, "/api/v2/doctor", cookie) // still the last run: nothing to say
	free = 3 << 30
	e.getDoctor(t, "/api/v2/doctor?retest=1", cookie) // the disk failed: one line
	e.getDoctor(t, "/api/v2/doctor?retest=1", cookie) // unchanged: still one
	e.closeBus()
	evs, _, err := e.bus.Query(context.Background(), events.Filter{Source: "health"}, events.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 {
		t.Fatalf("health events = %d, want one for the one change: %+v", len(evs), evs)
	}
	if ev := evs[0]; ev.Code != "health.failed" || ev.Kind != events.KindWarning || ev.Level != events.LevelWarn {
		t.Errorf("event = %+v, want a health.failed warning", ev)
	}
}

// TestDoctorWithoutADoctorIsStarting: a server whose wiring has no health check says so rather than answering an
// empty list that looks like a clean bill of health.
func TestDoctorWithoutADoctorIsStarting(t *testing.T) {
	e := newEnv(t, false)
	e.doctor = nil
	e.ready()
	cookie := e.setup()
	rec := e.do(http.MethodGet, "/api/v2/doctor", "", withSession(cookie), false)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /doctor without a doctor = %d, want 503", rec.Code)
	}
}

// TestDoctorLibraryFolderIsChecked: a library whose folder is gone is a problem on the health page, not a silent
// pass.
func TestDoctorLibraryFolderIsChecked(t *testing.T) {
	e := newEnv(t, false)
	e.ready()
	lib := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	cookie := cookieNamed(t, e.do(http.MethodPost, "/api/v2/setup",
		setupWith(t, map[string]any{"name": "Films", "path": lib}), write(nil), false), sessionCookie)
	if err := os.RemoveAll(lib); err != nil {
		t.Fatal(err)
	}
	got := e.getDoctor(t, "/api/v2/doctor", cookie)
	var found bool
	for _, c := range got.Checks {
		if c.ID == "library-1" {
			found = true
			if c.Status != "bad" {
				t.Errorf("library-1 = %q (%s), want bad after its folder went", c.Status, c.Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no library-1 check in %+v", got.Checks)
	}
}
