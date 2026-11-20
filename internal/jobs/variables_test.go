// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The sample a workflow's variable list shows is the newest file one of its libraries converted. A
// different workflow's conversions, and a done job with no new file, are not it.
func TestLatestConvertedIn(t *testing.T) {
	s := open(t)
	a, err := s.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddLibrary("Shows", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	if a.FlowID == b.FlowID {
		t.Fatal("the two libraries share a workflow; the test needs two")
	}
	if _, ok, err := s.LatestConvertedIn(a.FlowID); ok || err != nil {
		t.Fatalf("nothing converted yet: ok=%v err=%v", ok, err)
	}
	elsewhere := t.TempDir()
	convertTo(t, s, b, filepath.Join(b.Path, "Other.mkv"), filepath.Join(elsewhere, "Other.hevc.mkv"), 1000, 400)
	if _, ok, err := s.LatestConvertedIn(a.FlowID); ok || err != nil {
		t.Fatalf("another workflow's conversion was taken: ok=%v err=%v", ok, err)
	}
	convertTo(t, s, a, filepath.Join(a.Path, "One.mkv"), filepath.Join(elsewhere, "One.hevc.mkv"), 1000, 400)
	newestPath := filepath.Join(a.Path, "Two.mkv")
	newest := convertTo(t, s, a, newestPath, filepath.Join(elsewhere, "Two.hevc.mkv"), 2000, 500)

	// a done job that left no new file is not a sample
	id, err := s.Enqueue(Job{LibraryID: a.ID, Path: filepath.Join(a.Path, "Three.mkv"), SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(id, "", 0); err != nil {
		t.Fatal(err)
	}

	j, ok, err := s.LatestConvertedIn(a.FlowID)
	if err != nil || !ok {
		t.Fatalf("the newest conversion was not found: ok=%v err=%v", ok, err)
	}
	if j.ID != newest || j.Path != newestPath || j.Status != Done {
		t.Fatalf("got job %d (%s, %s), want %d (%s)", j.ID, j.Path, j.Status, newest, newestPath)
	}
}
