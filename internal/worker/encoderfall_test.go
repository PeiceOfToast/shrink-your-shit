// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// fall records a graphics-encoder failure for the scenario's job on its worker, as a fall-back does.
func fall(t *testing.T, s *scenario, w *Worker) {
	t.Helper()
	if _, err := s.store.EncoderFell(s.id, w.ID, "hevc_nvenc", "Unknown encoder 'hevc_nvenc'"); err != nil {
		t.Fatal(err)
	}
}

// A worker whose graphics encoder is paused does not run it: the file goes to the processor first time.
func TestPausedGraphicsEncoderIsNotUsed(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	for i := 0; i < 3; i++ {
		fall(t, s, w)
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	enc := encodeCalls(s)
	if len(enc) != 1 || strings.Contains(enc[0], "hevc_nvenc") || !strings.Contains(enc[0], "libx265") {
		t.Fatalf("encodes, want the processor only:\n%s", strings.Join(enc, "\n"))
	}
	if n := logCount(t, s, "is paused until"); n != 1 {
		t.Errorf("paused line: %d, want 1", n)
	}
}

// A graphics success ends a run of failures: two falls, a success, then two more falls do not pause the encoder.
func TestGraphicsSuccessEndsARunOfFalls(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	fall(t, s, w)
	fall(t, s, w)
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if enc := encodeCalls(s); len(enc) != 1 || !strings.Contains(enc[0], "hevc_nvenc") {
		t.Fatalf("encodes, want the graphics encoder once:\n%s", strings.Join(enc, "\n"))
	}
	fall(t, s, w)
	fall(t, s, w)
	if _, paused, err := s.store.EncoderPaused(w.ID); err != nil || paused {
		t.Errorf("paused %v, %v after a success and two falls: want not paused", paused, err)
	}
}

// When the processor fails too, the encoder was not the fault: no fall-back is recorded, nothing counts toward a
// pause, and the job fails as it did before fall-backs.
func TestProcessorFailureIsNotAFallBack(t *testing.T) {
	s := newScenario(t)
	s.failAllEncodes = true
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_nvenc", "" }
	if _, err := w.RunOne(context.Background()); err == nil {
		t.Fatal("RunOne: want the failed conversion's error")
	}
	if j := s.job(); j.Status == jobs.Done {
		t.Fatalf("status %s: the job should fail when the processor fails too", j.Status)
	}
	if enc := encodeCalls(s); len(enc) != 2 || !strings.Contains(enc[1], "libx265") {
		t.Fatalf("encodes, want the graphics encoder then the processor:\n%s", strings.Join(enc, "\n"))
	}
	falls, err := s.store.EncoderFallbacksSince(time.Unix(0, 0))
	if err != nil || len(falls) != 0 {
		t.Errorf("fall-backs %+v, %v: want none", falls, err)
	}
	if enc, _, err := s.store.EncoderLastFell(w.ID); err != nil || enc != "" {
		t.Errorf("last fall %q, %v: want none", enc, err)
	}
	if n := logCount(t, s, "converted on the processor"); n != 0 {
		t.Errorf("converted-on-processor line: %d, want 0", n)
	}
}

// A 10-bit SDR source is kept at its depth, so the worker asks its encoder with need10 set even though the
// file is not HDR: an 8-bit-only graphics encoder would be refused by the check after the whole encode.
func TestTenBitSDRSourceAsksForATenBitEncoder(t *testing.T) {
	s := newScenario(t)
	s.srcSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("h264", 640, 360, "yuv420p10le"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
	s.outSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("hevc", 640, 360, "yuv420p10le"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
	w := s.worker(Replace)
	var codec string
	var need10 bool
	var hdr *rules.HDR
	w.Encoder = func(c string, n bool, h *rules.HDR) (string, string) { codec, need10, hdr = c, n, h; return "", "" }
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne: %v", err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if codec != "libx265" || !need10 || hdr != nil {
		t.Fatalf("encoder asked with codec %q need10 %v hdr %v, want libx265, true, nil", codec, need10, hdr)
	}
}

// The rule reads the source's depth, not the target's: a 10-bit SDR source made into H.264 still asks the
// encoder with need10 set, because the card must not be the one dropping the depth (its H.264 has no 10-bit).
// A card whose H.264 encoder is 8-bit only is refused, and the processor's libx264 converts the file at its
// 10 bits, keeping the source's depth whatever the target; the file is replaced and the log says 10-bit.
// Reading the target instead (EncodeTenBit, false for H.264) would pass every other test and hand the file
// to that encoder, silently making it 8-bit on the card — the #498 downgrade.
func TestTenBitSDRSourceToH264UsesTheProcessor(t *testing.T) {
	rules.SetAvailableFormats(map[string]bool{"libx264": true}) // an ffmpeg with libx264; HEVC is always offered
	defer rules.SetAvailableFormats(nil)
	s := newScenario(t)
	s.srcSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("hevc", 640, 360, "yuv420p10le"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
	s.outSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{ // what libx264 writes from a 10-bit source
		vid("h264", 640, 360, "yuv420p10le"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
	w := s.worker(Replace)
	sel := rules.DefaultSelectors(3)
	sel.Format = "h264"
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	p := tmpl.Compile(must(media.ParseProbe([]byte(s.srcSpec.json()))))
	if p.Skip != "" || !p.Video.TenBit || p.EncodeTenBit() || !rules.NeedsTenBit(p, nil) {
		t.Fatalf("the case does not take the path it is about: skip %q, tenBit %v, encodeTenBit %v, needsTenBit %v",
			p.Skip, p.Video.TenBit, p.EncodeTenBit(), rules.NeedsTenBit(p, nil))
	}
	var asked bool
	var need10 bool
	w.Encoder = func(c string, n bool, h *rules.HDR) (string, string) {
		if c != "libx264" {
			return "", ""
		}
		asked, need10 = true, n
		if n { // this card's H.264 encoder takes 8-bit frames only
			return "", "h264_nvenc can't do a 10-bit file"
		}
		return "h264_nvenc", ""
	}
	w.RunOne(context.Background())
	if !asked || !need10 {
		t.Fatalf("encoder asked %v with need10 %v, want libx264 with need10 true", asked, need10)
	}
	if enc := encodeCalls(s); len(enc) != 1 || !strings.Contains(enc[0], "libx264") || strings.Contains(enc[0], "h264_nvenc") {
		t.Fatalf("encodes, want the processor's libx264 only:\n%s", strings.Join(enc, "\n"))
	}
	if b, _ := os.ReadFile(s.src); string(b) != "new" { // libx264 keeps the 10 bits, so the file is replaced
		t.Errorf("the original should be replaced by the converted file, has %q", b)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Errorf("status %s (error %q), want done", j.Status, j.Error)
	}
	var converted string
	if steps, err := s.store.Steps(s.id); err != nil {
		t.Fatal(err)
	} else {
		for _, st := range steps {
			if st.Step == "converted" {
				converted = st.Words
			}
		}
	}
	// The depth is the one the encoder that ran made: the processor's libx264 kept 10 bits, so the line says so.
	if !strings.Contains(converted, "· 10-bit ·") {
		t.Errorf("the converted step says %q, want it to name 10-bit", converted)
	}
}
