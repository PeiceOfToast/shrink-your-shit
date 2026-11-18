// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// logHas reports whether the job log has a line on the given step and stream containing text.
func (s *scenario) logHas(step int, stream, text string) bool {
	s.t.Helper()
	lines, err := s.store.LogLines(s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, l := range lines {
		if l.Step == step && l.Stream == stream && strings.Contains(l.Text, text) {
			return true
		}
	}
	return false
}

// checkStep returns the job's check run step, or fails the test if there is none.
func (s *scenario) checkStep() jobs.RunStep {
	s.t.Helper()
	steps, err := s.store.RunSteps(s.id)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, st := range steps {
		if st.Name == "check" {
			return st
		}
	}
	s.t.Fatalf("no check step in %+v", steps)
	return jobs.RunStep{}
}

// A few damaged pictures (one bad macroblock in a P-frame, as in a copied file) are tolerated: the file converts
// and the job log says so on the encode step. This is the two-picture case from job 14.
func TestOriginalWithAFewDamagedPicturesConverts(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = "[h264 @ 0x55] error while decoding MB 41 22, bytestream -5\n" +
		"[h264 @ 0x55] concealing 120 DC, 120 AC, 120 MV errors in P frame\n" +
		"[h264 @ 0x55] error while decoding MB 3 7, bytestream -5\n" +
		"[h264 @ 0x55] concealing 8 DC, 8 AC, 8 MV errors in P frame"
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s (error %q), want done", j.Status, j.Error)
	}
	if !s.logHas(0, "app", "(2 damaged pictures, up to 3 are tolerated); converted anyway.") {
		t.Error("want the tolerated line on the encode step")
	}
}

// Five separate damaged pictures are more than the limit: the original is kept, and the check step records the
// refusal with its reason, the count, and the first damage lines.
func TestOriginalWithManyDamagedPicturesIsKept(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = strings.Repeat("[h264 @ 0x55] error while decoding MB 1 2, bytestream -5\n", 5)
	s.run(Replace)
	s.assertKept("the original file has errors")
	st := s.checkStep()
	if st.Outcome != "failed" || !strings.Contains(st.Result, "the original file has errors") {
		t.Errorf("check step %+v, want failed with the kept reason", st)
	}
	for _, want := range []string{"Check: damage in the original, read while converting", "Errors: 5 damaged pictures; up to 3 are tolerated", "error while decoding MB 1 2"} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks %q", want)
		}
	}
}

// Damage a picture cannot be decoded for (a missing reference) counts as a damaged picture too.
func TestMissingReferencesCountAsDamagedPictures(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = strings.Repeat("[h264 @ 0x55] reference picture missing during reorder\n", 4)
	s.run(Replace)
	s.assertKept("the original file has errors")
	if !s.logHas(1, "check", "Errors: 4 damaged pictures; up to 3 are tolerated") {
		t.Error("four missing-reference lines should be four damaged pictures")
	}
}

// A moov atom that is missing is damage to the file as a whole, however few pictures it touches: kept, and the
// check log says the file as a whole is damaged.
func TestMoovAtomMissingIsKeptWhateverElse(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = "[mov,mp4 @ 0x1] moov atom not found"
	s.run(Replace)
	s.assertKept("the original file has errors")
	if st := s.checkStep(); st.Outcome != "failed" {
		t.Errorf("check step %+v, want failed", st)
	}
	if !s.logHas(1, "check", "the file as a whole is damaged") {
		t.Error("the check log should say the file as a whole is damaged")
	}
}

// ffmpeg's "corrupt" lines are about one picture or packet, not the file: one of them is a little damage that is
// tolerated, and enough of them are counted as pictures like any other picture damage.
func TestCorruptLinesCountAsPictures(t *testing.T) {
	for _, tc := range []struct {
		stderr   string
		pictures int
	}{
		{"[h264 @ 0x55] corrupted macroblock 12 30 (total_coeff=-1)", 1},
		{"[h264 @ 0x55] corrupt decoded frame in stream 0", 1},
		{"[mpegts @ 0x55] Packet corrupt (stream = 0, dts = 9000).", 1},
		{strings.Repeat("[NULL @ 0x55] corrupt decoded frame in stream 0\n", 4), 4},
	} {
		rep := sourceDamage(tc.stderr)
		if rep.FileLevel || rep.Pictures != tc.pictures {
			t.Errorf("%q: %+v, want %d damaged pictures and not file-level", tc.stderr, rep, tc.pictures)
		}
	}
	s := newScenario(t)
	s.encodeStderr = "[h264 @ 0x55] corrupted macroblock 12 30 (total_coeff=-1)"
	s.run(Replace)
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("one corrupted macroblock: status %s (error %q), want done", j.Status, j.Error)
	}
}
