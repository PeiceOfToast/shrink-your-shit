// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// The health report warns about a worker whose graphics encoder is paused, and only then.
func TestHealthWarnsWhenAnEncoderIsPaused(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.SetPaused(false) // a new store starts paused: the worker takes work only when it is on
	if _, err := st.Enqueue(jobs.Job{Path: "/m/a.mkv", SizeBefore: 100}); err != nil {
		t.Fatal(err)
	}
	j, err := st.Claim("builtin") // a fall-back is recorded against the job's worker
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := st.EncoderFell(j.ID, "builtin", "hevc_nvenc", "Unknown encoder"); err != nil {
			t.Fatal(err)
		}
	}
	checks, err := encoderPauseChecks(st, time.Now())
	if err != nil || len(checks) != 0 {
		t.Fatalf("after two falls: %+v, %v; want no warning", checks, err)
	}
	if _, err := st.EncoderFell(j.ID, "builtin", "hevc_nvenc", "Unknown encoder"); err != nil {
		t.Fatal(err)
	}
	checks, err = encoderPauseChecks(st, time.Now())
	if err != nil || len(checks) != 1 {
		t.Fatalf("after three falls: %+v, %v; want one warning", checks, err)
	}
	c := checks[0]
	if c.ID != "encoder:builtin" || c.Status != "warn" || c.Label == "" || c.Detail == "" {
		t.Errorf("check = %+v", c)
	}
}
