// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The overview's lastDoneAt (in headline) and nextScanAt: null until there is a value, then the real one.
func TestOverviewLastDoneAndNextScan(t *testing.T) {
	srv, st, _, _ := libServer(t, false) // NextScan answers 2026-11-07T03:00:00Z
	type overview struct {
		Headline struct {
			LastDoneAt *string `json:"lastDoneAt"`
		} `json:"headline"`
		NextScanAt *string `json:"nextScanAt"`
	}
	get := func(srv *httptest.Server) overview {
		t.Helper()
		status, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/overview"})
		if status != 200 {
			t.Fatalf("overview status %d: %s", status, body)
		}
		var o overview
		if err := json.Unmarshal([]byte(body), &o); err != nil {
			t.Fatal(err)
		}
		return o
	}

	o := get(srv)
	if o.Headline.LastDoneAt != nil {
		t.Errorf("no conversions yet: lastDoneAt %q, want null", *o.Headline.LastDoneAt)
	}
	if o.NextScanAt == nil || *o.NextScanAt != "2026-11-07T03:00:00Z" {
		t.Errorf("nextScanAt %v, want 2026-11-07T03:00:00Z", o.NextScanAt)
	}

	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	p := filepath.Join(lib.Path, "a.mkv")
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p, SizeBefore: 1000})
	st.Claim("w")
	if err := st.Complete(id, p, 400); err != nil {
		t.Fatal(err)
	}
	want, err := st.LastSavedAt()
	if err != nil || want == "" {
		t.Fatalf("LastSavedAt %q (%v) after a conversion", want, err)
	}
	o = get(srv)
	if o.Headline.LastDoneAt == nil || *o.Headline.LastDoneAt != want {
		t.Errorf("lastDoneAt %v, want %q", o.Headline.LastDoneAt, want)
	}

	// No scanner: nextScanAt is null, not a zero time.
	access, _, _ := OpenAccess(t.TempDir(), false)
	bare := httptest.NewServer(Handler(st, access, Engine{}))
	t.Cleanup(bare.Close)
	if o := get(bare); o.NextScanAt != nil {
		t.Errorf("no NextScan: nextScanAt %q, want null", *o.NextScanAt)
	}
}
