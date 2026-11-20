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
	if !s.logHas(0, "app", "(2 damaged pictures of 2500 frames (0.08%), up to 1% are tolerated); converted anyway.") {
		t.Error("want the tolerated line on the encode step")
	}
}

// Fifty damaged pictures of 2500 frames (2%) are more than the limit: the original is kept, and the check step
// records the refusal with its reason, the count and share, and the first damage lines.
func TestOriginalWithManyDamagedPicturesIsKept(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = strings.Repeat("[h264 @ 0x55] error while decoding MB 1 2, bytestream -5\n", 50)
	s.run(Replace)
	s.assertKept("the original file has errors")
	st := s.checkStep()
	if st.Outcome != "failed" || !strings.Contains(st.Result, "the original file has errors") {
		t.Errorf("check step %+v, want failed with the kept reason", st)
	}
	for _, want := range []string{"Check: damage in the original, read while converting", "Errors: 50 damaged pictures of 2500 frames (2.00%); up to 1% are tolerated", "error while decoding MB 1 2"} {
		if !s.logHas(1, "check", want) {
			t.Errorf("check log lacks %q", want)
		}
	}
}

// Damage a picture cannot be decoded for (a missing reference) counts as a damaged picture too.
func TestMissingReferencesCountAsDamagedPictures(t *testing.T) {
	s := newScenario(t)
	s.encodeStderr = strings.Repeat("[h264 @ 0x55] reference picture missing during reorder\n", 30)
	s.run(Replace)
	s.assertKept("the original file has errors")
	if !s.logHas(1, "check", "Errors: 30 damaged pictures of 2500 frames (1.20%); up to 1% are tolerated") {
		t.Error("thirty missing-reference lines should be thirty damaged pictures")
	}
}

// The share limit is 1% of the frames, inclusive: 0.5% and exactly 1% convert, one picture more than 1% does not,
// and so does damage to the file as a whole at any share. The frame count is 2500 (100 s at 25 fps).
func TestDamageShareLimit(t *testing.T) {
	for _, tc := range []struct {
		name     string
		stderr   string
		tolerant bool
	}{
		{"0.5% converts", strings.Repeat("[h264 @ 0x1] error while decoding MB 1 2\n", 12), true},
		{"exactly 1% converts", strings.Repeat("[h264 @ 0x1] error while decoding MB 1 2\n", 25), true},
		{"just over 1% is kept", strings.Repeat("[h264 @ 0x1] error while decoding MB 1 2\n", 26), false},
		{"2% is kept", strings.Repeat("[h264 @ 0x1] error while decoding MB 1 2\n", 50), false},
		{"stops early after a few damaged pictures is kept", "[h264 @ 0x1] error while decoding MB 1 2\n[matroska,webm @ 0x1] Truncated: unexpected end of file", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newScenario(t)
			s.encodeStderr = tc.stderr
			s.run(Replace)
			if j := s.job(); (j.Status == jobs.Done) != tc.tolerant {
				t.Fatalf("status %s (error %q), tolerant %v", j.Status, j.Error, tc.tolerant)
			}
		})
	}
}

// damageTolerated at its edges, without a run: a file whose frame count is unknown is not tolerated, since there
// is no share to measure.
func TestDamageToleratedEdges(t *testing.T) {
	few := sourceDamageReport{Pictures: 1}
	if damageTolerated(few, 0) {
		t.Error("unknown frame count: must not be tolerated")
	}
	if damageTolerated(sourceDamageReport{Pictures: 1, FileLevel: true}, 2500) {
		t.Error("file-level damage: must not be tolerated, however few pictures")
	}
	if !damageTolerated(sourceDamageReport{}, 2500) {
		t.Error("no damage: tolerated")
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
