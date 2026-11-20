// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// OutputRatio is what the Dashboard projects a converting file's size from when the encode can't say:
// the bytes of the last n done conversions of a library against their originals, then the same over
// every library, and nothing at all before anything has been converted.
func TestOutputRatio(t *testing.T) {
	s := open(t)
	a, _ := s.AddLibrary("A", "/a", rules.SliderTemplate(3), true)
	b, _ := s.AddLibrary("B", "/b", rules.SliderTemplate(3), true)
	convert := func(libraryID int64, before, after int64) {
		id, err := s.Enqueue(Job{LibraryID: libraryID, Path: "x", SizeBefore: before})
		if err != nil {
			t.Fatal(err)
		}
		s.Claim("w")
		if err := s.Complete(id, "x", after); err != nil {
			t.Fatal(err)
		}
	}
	convert(a.ID, 1000, 400)
	convert(a.ID, 3000, 600)
	convert(b.ID, 1000, 500)

	// a library's own: (400+600)/(1000+3000)
	if r, ok, err := s.OutputRatio(a.ID, 20); err != nil || !ok || r != 0.25 {
		t.Errorf("library A: %v %v %v, want 0.25", r, ok, err)
	}
	// its last one only: 600/3000
	if r, ok, _ := s.OutputRatio(a.ID, 1); !ok || r != 0.2 {
		t.Errorf("library A, last 1: %v %v, want 0.2", r, ok)
	}
	// every library: (400+600+500)/(1000+3000+1000)
	if r, ok, _ := s.OutputRatio(0, 20); !ok || r != 0.3 {
		t.Errorf("every library: %v %v, want 0.3", r, ok)
	}
	// nothing converted yet
	empty, _ := s.AddLibrary("Empty", "/e", rules.SliderTemplate(3), true)
	if _, ok, err := s.OutputRatio(empty.ID, 20); err != nil || ok {
		t.Errorf("no history: %v %v, want none", ok, err)
	}
}
