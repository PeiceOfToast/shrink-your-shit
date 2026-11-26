// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// originalLine is the original line of the first sample in a preview response (its samples, each with lines). The paths in it are compared decoded, so the test holds on Windows too.
func originalLine(t *testing.T, body string) string {
	t.Helper()
	var resp struct {
		Samples []struct {
			File  string `json:"file"`
			Lines []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"lines"`
		} `json:"samples"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil || len(resp.Samples) == 0 {
		t.Fatalf("preview response: %v %s", err, body)
	}
	for _, l := range resp.Samples[0].Lines {
		if l.Kind == "original" {
			return l.Text
		}
	}
	t.Fatalf("no original line in %s", body)
	return ""
}

// The default answers keep new files next to the library: the sample's original line names that folder
// the way the library's own folder is named ("<library> (shrunk)"), and a file in a subfolder of the
// library goes to the same subfolder there, as the worker puts it.
func TestGuidePreviewSampleOriginalLineNamesTheLibraryFolder(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dune.mkv"), make([]byte, 2_000_000), 0o644)
	os.MkdirAll(filepath.Join(dir, "Anime"), 0o755)
	os.WriteFile(filepath.Join(dir, "Anime", "x.mkv"), make([]byte, 2_000_000), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	want, prob := jobs.NextToLibrary(l.Name, l.Path)
	if prob != nil || !strings.HasSuffix(want, " (shrunk)") {
		t.Fatalf("library folder %q, problem %v", want, prob)
	}
	real := func(p string) string {
		if r, err := jobs.RealPath(p); err == nil {
			return r
		}
		return p
	}
	dune, anime := real(filepath.Join(l.Path, "Dune.mkv")), real(filepath.Join(l.Path, "Anime", "x.mkv"))

	code, b := post(t, srv, "POST", "/api/v2/workflows/guide/preview", mustBody(t, map[string]any{
		"answers": map[string]any{"goal": "smaller"}, "libraryId": l.ID, "sampleFiles": []string{dune}}))
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if got := originalLine(t, b); got != "Original: stays as it is; the new file goes to "+want {
		t.Errorf("top-level file: got %q, want the library folder %q", got, want)
	}

	code, b = post(t, srv, "POST", "/api/v2/workflows/guide/preview", mustBody(t, map[string]any{
		"answers": map[string]any{"goal": "smaller"}, "libraryId": l.ID, "sampleFiles": []string{anime}}))
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if got := originalLine(t, b); got != "Original: stays as it is; the new file goes to "+filepath.Join(want, "Anime") {
		t.Errorf("subfolder file: got %q, want %q", got, filepath.Join(want, "Anime"))
	}
}

// A folder with {{library}} in it is filled with the library's name for each file, and the sample's line
// names that filled folder.
func TestGuidePreviewSampleFillsALibraryFolderPattern(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dune.mkv"), make([]byte, 2_000_000), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	dune := filepath.Join(l.Path, "Dune.mkv")
	if real, err := jobs.RealPath(dune); err == nil {
		dune = real
	}
	outRoot := t.TempDir() // outside the library: a folder inside it is refused
	folder := filepath.Join(outRoot, "{{library}}", "out")
	code, b := post(t, srv, "POST", "/api/v2/workflows/guide/preview", mustBody(t, map[string]any{
		"answers":     map[string]any{"goal": "smaller", "original": "one_folder", "folder": folder},
		"libraryId":   l.ID,
		"sampleFiles": []string{dune},
	}))
	if code != 200 {
		t.Fatalf("%d %s", code, b)
	}
	if got, want := originalLine(t, b), "Original: stays as it is; the new file goes to "+filepath.Join(outRoot, "Movies", "out"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// mustBody is a JSON body built by encoding, so a Windows path's backslashes are escaped correctly.
func mustBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
