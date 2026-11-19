// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A file that failed, was retried (same job row) and then stopped while running counts once in the last
// 24 hours, as skipped: the same as the day counts, not as a failure and not as nothing.
func TestLast24hCountsAStoppedRunningFileAsSkipped(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	p := filepath.Join(lib.Path, "c.mkv")
	id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: p, SizeBefore: 1000})
	st.Claim("w")
	st.Fail(id, "convert_failed", "boom")
	if err := st.Retry(id); err != nil {
		t.Fatal(err)
	}
	st.Claim("w")
	if err := st.UserStopped(id); err != nil { // the worker's end of a stop on a running job
		t.Fatal(err)
	}
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/summary"})
	var s struct {
		Last24h struct{ Failed, Skipped int } `json:"last24h"`
	}
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatal(err)
	}
	if s.Last24h.Failed != 0 || s.Last24h.Skipped != 1 {
		t.Errorf("last 24 hours after fail, retry, stop while running: failed %d, skipped %d; want 0 and 1",
			s.Last24h.Failed, s.Last24h.Skipped)
	}
}
