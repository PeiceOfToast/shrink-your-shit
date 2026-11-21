// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"path/filepath"
	"testing"
)

// A probe is shown only for the file it was taken of: its size has to match (slice 7). A replace in place keeps
// the path, so the path alone can't tell the original from the new file.
func TestProbeOfSizeMatchesTheFileItWasTakenOf(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := "/m/a.mkv"
	if err := s.PutProbe(p, 100, 1, `A`); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.ProbeOfSize(p, 100); !ok || got != `A` {
		t.Errorf("ProbeOfSize(100) = %q, %v; want A", got, ok)
	}
	if _, ok := s.ProbeOfSize(p, 200); ok {
		t.Error("ProbeOfSize(200) returned the probe of another size")
	}
	if _, ok := s.ProbeOfSize(p, 0); ok {
		t.Error("ProbeOfSize(0) returned a probe; an unknown size shows none")
	}
}
