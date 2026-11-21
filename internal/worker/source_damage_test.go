// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
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
	s.assertKept("the original file has errors", "source_damaged")
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
	s.assertKept("the original file has errors", "source_damaged")
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
		{"ffmpeg's file ended prematurely is kept", "[h264 @ 0x1] error while decoding MB 1 2\n[matroska,webm @ 0x1] File ended prematurely", false},
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
	s.assertKept("the original file has errors", "source_damaged")
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

// Frames that never come out of the decoder count as damaged pictures, against the same 1% limit. -1 means the
// conversion did not say how many it decoded (a remote worker): nothing is added then.
func TestWithLostFrames(t *testing.T) {
	cases := []struct {
		name            string
		rep             sourceDamageReport
		frames, decoded int64
		wantPictures    int
		wantTolerated   bool
	}{
		{"30 of 500 lost with no message: kept", sourceDamageReport{}, 500, 470, 30, false},
		{"2 of 500 lost: converts", sourceDamageReport{}, 500, 498, 2, true},
		{"all decoded: unchanged", sourceDamageReport{Pictures: 1}, 500, 500, 1, true},
		{"count not reported: unchanged", sourceDamageReport{Pictures: 1}, 500, -1, 1, true},
		{"frames not known: unchanged", sourceDamageReport{Pictures: 1}, 0, 470, 1, false},
		{"the named picture and the 2 lost frames are the same: 2 of 500", sourceDamageReport{Pictures: 1}, 500, 498, 2, true},
		{"CI real ffmpeg 8: 4 named lines, 2 lost, same pictures: 4 of 500 converts", sourceDamageReport{Pictures: 4}, 500, 498, 4, true},
		{"edit-list mp4: 240 presented, 240 decoded: nothing lost", sourceDamageReport{}, 240, 240, 0, true},
	}
	for _, c := range cases {
		got := withLostFrames(c.rep, c.frames, c.decoded)
		if got.Pictures != c.wantPictures {
			t.Errorf("%s: pictures %d, want %d", c.name, got.Pictures, c.wantPictures)
		}
		if tol := damageTolerated(got, c.frames); tol != c.wantTolerated {
			t.Errorf("%s: tolerated %v, want %v", c.name, tol, c.wantTolerated)
		}
	}
}

// The frames the lost-frame count is measured against come from the video stream: what it states, else its own
// duration. Only the container's duration is a guess (audio can run past the video), so it is not exact.
func TestSourceFrameCount(t *testing.T) {
	video := func(s media.Stream, container float64) media.Info {
		s.Type, s.FrameRate = "video", 25
		return media.Info{DurationSec: container, Streams: []media.Stream{{Type: "audio"}, s}}
	}
	cases := []struct {
		name      string
		src       media.Info
		want      int64
		wantExact bool
	}{
		{"mp4 states the frame count", video(media.Stream{Frames: 250}, 11), 250, true},
		{"mkv states the stream duration", video(media.Stream{Duration: 10}, 11), 250, true},
		// An mp4 edit list trims 10 frames: the count stays 250, but ffmpeg outputs the 9.6 s (240) presented.
		{"edit list: the presented duration, not the trimmed count", video(media.Stream{Frames: 250, Duration: 9.6}, 10.6), 240, true},
		{"neither: the container's 11 s is a guess", video(media.Stream{}, 11), 275, false},
	}
	for _, c := range cases {
		got, exact := sourceFrameCount(c.src)
		if got != c.want || exact != c.wantExact {
			t.Errorf("%s: frames %d exact %v, want %d exact %v", c.name, got, exact, c.want, c.wantExact)
		}
	}
	if sourceFrames(video(media.Stream{Frames: 250}, 11)) != 250 {
		t.Error("sourceFrames must report the stream's count")
	}
}

// The converted file must match the frames ffmpeg presents. An edit-list mp4 counts 250 packets for a clip that
// presents 240 (9.6 s at 25 fps), so 240 is expected; a clip with nothing trimmed, or without a stated duration,
// keeps the packet count.
func TestExpectedPackets(t *testing.T) {
	video := func(s media.Stream, container float64) media.Info {
		s.Type, s.FrameRate = "video", 25
		return media.Info{DurationSec: container, Streams: []media.Stream{{Type: "audio"}, s}}
	}
	cases := []struct {
		name    string
		src     media.Info
		counted int64
		want    int64
	}{
		{"edit list: 250 packets, 240 presented", video(media.Stream{Frames: 250, Duration: 9.6}, 10.6), 250, 240},
		{"nothing trimmed: the count stands", video(media.Stream{Frames: 250, Duration: 10}, 11), 250, 250},
		{"no stated duration: the packet count", video(media.Stream{Frames: 250}, 11), 250, 250},
		{"container guess only: the packet count", video(media.Stream{}, 11), 250, 250},
	}
	for _, c := range cases {
		if got := expectedPackets(c.src, c.counted); got != c.want {
			t.Errorf("%s: expected %d, want %d", c.name, got, c.want)
		}
	}
}
