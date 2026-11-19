// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// modeTemplate is slider quality 3 saved with one output mode: the file replaced, kept beside, or the new file
// put in its own folder.
func modeTemplate(mode, lib string) rules.Template {
	s := rules.DefaultSelectors(3)
	s.AfterConverting = mode
	if mode == "folder" { // an absolute folder beside the library, so its outputs are not scanned
		s.OutputDir = filepath.Join(filepath.Dir(lib), "converted")
	}
	return s.Template()
}

// jobsOf is every job row for path, oldest first. Store.Retry reuses a row, so the count is of rows, not of tries.
func jobsOf(t *testing.T, st *jobs.Store, path string) []jobs.Job {
	t.Helper()
	all, err := st.List(1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []jobs.Job
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Path == path {
			out = append(out, all[i])
		}
	}
	return out
}

// Saving the workflow again (any output mode) must not convert a file that converted fine, and must decide a file
// whose last conversion failed once more. The converted outputs are written outside the scanned folder: the
// fake probe reports every file as h264, so an output inside it would be queued as a new file.
func TestWorkflowChangeDoesNotConvertAgainWhatConverted(t *testing.T) {
	for _, mode := range []string{"replace", "beside", "folder"} {
		t.Run(mode, func(t *testing.T) {
			s, st, lib, _ := setup(t, h264Big)
			os.WriteFile(filepath.Join(lib, "b.mkv"), []byte("12345"), 0o644)
			libs, _ := st.Libraries()
			libID := libs[0].ID
			s.Targets = func() ([]Target, error) {
				return []Target{{LibraryID: libID, Path: lib, Template: modeTemplate(mode, lib)}}, nil
			}
			a, b := filepath.Join(lib, "a.mkv"), filepath.Join(lib, "b.mkv")
			if n := settle(s); n != 2 {
				t.Fatalf("queued %d, want 2", n)
			}
			ids := map[string]int64{}
			for i := 0; i < 2; i++ {
				j, err := st.Claim("w")
				if err != nil {
					t.Fatal(err)
				}
				ids[j.Path] = j.ID
			}
			if err := st.Complete(ids[a], filepath.Join(lib+" (shrunk)", "a.mkv"), 300); err != nil {
				t.Fatal(err)
			}
			if err := st.Fail(ids[b], "convert_failed", "broke"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SaveSlider(libID, 2, ""); err != nil { // forgets the library's file records
				t.Fatal(err)
			}
			settle(s)
			if got := len(jobsOf(t, st, a)); got != 1 {
				t.Errorf("converted file: %d job rows after the workflow change, want 1 (not converted again)", got)
			}
			if got := len(jobsOf(t, st, b)); got != 2 {
				t.Errorf("failed file: %d job rows after the workflow change, want 2 (decided again)", got)
			}
		})
	}
}
