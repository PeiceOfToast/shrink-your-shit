// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The default answers keep new files next to the library: the sample's original line names that folder
// the way the library's own folder is named ("<library> (shrunk)").
func TestGuidePreviewSampleOriginalLineNamesTheLibraryFolder(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dune.mkv"), make([]byte, 2_000_000), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	want, prob := jobs.NextToLibrary(l.Name, l.Path)
	if prob != nil || !strings.HasSuffix(want, " (shrunk)") {
		t.Fatalf("library folder %q, problem %v", want, prob)
	}
	dune := filepath.Join(l.Path, "Dune.mkv")
	if real, err := jobs.RealPath(dune); err == nil {
		dune = real
	}
	body := fmt.Sprintf(`{"answers":{"goal":"smaller"},"libraryId":%d,"sampleFiles":[%q]}`, l.ID, dune)

	code, b := post(t, srv, "POST", "/api/v2/workflows/guide/preview", body)
	if code != 200 || !strings.Contains(b, `"text":"Original: stays as it is; the new file goes to `+want+`"`) {
		t.Errorf("%d %s; want the original line to go to %s", code, b, want)
	}
}
