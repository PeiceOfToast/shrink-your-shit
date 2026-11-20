// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"encoding/json"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// P2: the original is not read again just to count its pictures when its container states the count: the
// probe's own count is what the check expects, and no count pass runs. When the container does not state it,
// the count pass still runs (the fallback) and the expected number is the one it gives, as before.
func TestSourceCountIsNotReadAgainWhenTheContainerStatesIt(t *testing.T) {
	cases := []struct {
		name       string
		srcPackets int  // what a count pass on the original answers
		exact      bool // the probe's stream states its duration
		wantCounts int  // count passes: the new file's, plus the original's only without an exact count
	}{
		{"container states the count", 2400, true, 1},
		{"container does not (the count pass)", 2500, false, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newScenario(t)
			s.srcPackets = c.srcPackets
			if c.exact {
				s.srcSpec.streams[0]["duration"] = "100" // 100 s at 25 fps: 2500 pictures
			}
			s.run(Replace)
			if j := s.job(); j.Status != jobs.Done {
				t.Fatalf("%s %q", j.Status, j.Error)
			}
			if got := s.count("-count_packets"); got != c.wantCounts {
				t.Errorf("%d count passes, want %d (the new file's always; the original's only when its container does not state the count)", got, c.wantCounts)
			}
			var snap rules.Snapshot
			if err := json.Unmarshal([]byte(s.job().Plan), &snap); err != nil {
				t.Fatalf("plan %q: %v", s.job().Plan, err)
			}
			// The container's count when it is exact (2500), else the count pass's (2500): the same number as
			// before, never the 2400 packets a count pass would find for the exact file.
			if snap.Expect.VideoPackets != 2500 {
				t.Errorf("expected %d pictures, want 2500", snap.Expect.VideoPackets)
			}
		})
	}
}
