// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The job page lists the .srt files a job saved (user item 4, 11-20): the swap keeps their names on the
// job and drops their records; a later run that saves none clears the list.
func TestFinishSidecarsKeepsNames(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dir := t.TempDir()
	id, err := s.Enqueue(Job{Path: filepath.Join(dir, "Movie.mkv")})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.SubtitleFiles(id); err != nil || !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("before any run: %q %v, want []", got, err)
	}
	err = s.AddSidecars([]Sidecar{{JobID: id, Path: filepath.Join(dir, "Movie.eng.srt")}, {JobID: id, Path: filepath.Join(dir, "Movie.eng.sdh.srt")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishSidecars(id); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.SubtitleFiles(id); !reflect.DeepEqual(got, []string{"Movie.eng.srt", "Movie.eng.sdh.srt"}) {
		t.Fatalf("after the swap: %q", got)
	}
	if left, _ := s.Sidecars(); len(left) != 0 {
		t.Fatalf("records left after the swap: %v", left)
	}
	if err := s.FinishSidecars(id); err != nil { // Convert again, no subtitles this time
		t.Fatal(err)
	}
	if got, _ := s.SubtitleFiles(id); !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("a run with none: %q, want []", got)
	}
}
