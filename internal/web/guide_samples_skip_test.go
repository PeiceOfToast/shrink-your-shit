// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A sample file this computer leaves as it is has no checks to run: "checks" is an empty list, not null.
func TestGuidePreviewSkippedSampleHasEmptyChecks(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	st.SetPaused(false)
	a, _, _ := OpenAccess(t.TempDir(), false)
	a.FailDelay = 0
	// the probe fails on every file, so each one is left as it is ("it can't be read")
	prev := &preview.Previewer{Store: st, Exec: &media.FakeExec{Err: errors.New("ffprobe failed")}, FFprobe: "ffprobe", Probes: 1}
	srv := httptest.NewServer(Handler(st, a, Engine{Preview: prev}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.mkv"), make([]byte, 100_000), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	dir, _ = jobs.RealPath(dir)
	body := fmt.Sprintf(`{"answers":{"goal":"smaller"},"libraryId":%d,"sampleFiles":%s}`, l.ID, mustBody(t, []string{filepath.Join(dir, "bad.mkv")}))

	code, b := post(t, srv, "POST", "/api/v2/workflows/guide/preview", body)
	if code != 200 || !strings.Contains(b, `"skipped":true`) || !strings.Contains(b, `"checks":[]`) {
		t.Errorf("%d %s", code, b)
	}
}
