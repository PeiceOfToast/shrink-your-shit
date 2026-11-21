// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Foundation slice 4: after the check has read the new file, the job says what it IS (the probe the check
// took), not what the plan predicted: the converted step's words, the job's output_info, and the log.

// tenBitSource is the usual source in 10-bit: HEVC from it is predicted 10-bit.
func tenBitSource() spec {
	s := sourceSpec()
	s.streams[0] = vid("h264", 640, 360, "yuv420p10le")
	return s
}

// outputInfoOf is the probe the worker stored for the job.
func outputInfoOf(t *testing.T, s *scenario) media.Info {
	t.Helper()
	raw, ok := s.store.OutputInfo(s.id)
	if !ok {
		t.Fatal("the job has no output_info")
	}
	var info media.Info
	if err := json.Unmarshal([]byte(raw), &info); err != nil {
		t.Fatalf("output_info %q: %v", raw, err)
	}
	return info
}

// convertedStep is the words of the job's converted step.
func convertedStep(t *testing.T, s *scenario) string {
	t.Helper()
	steps, err := s.store.Steps(s.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Step == "converted" {
			return st.Words
		}
	}
	t.Fatalf("no converted step: %+v", steps)
	return ""
}

// The encoder made 8-bit although the plan predicted 10-bit (HEVC from a 10-bit original), so the check
// refused it: the step must say what the encoder made, not keep claiming the prediction.
func TestTheConvertedStepSaysWhatTheEncoderMade(t *testing.T) {
	s := newScenario(t)
	s.srcSpec = tenBitSource()
	s.outSpec = goodOutput() // HEVC 8-bit: not what the plan predicted for a 10-bit original
	if err := s.run(Replace); err == nil {
		t.Fatal("the check should have refused an 8-bit file where 10-bit was expected")
	}
	if j := s.job(); j.Status != jobs.Kept {
		t.Fatalf("status %s, want kept: %q", j.Status, j.Error)
	}
	if words := convertedStep(t, s); !strings.Contains(words, "8-bit") || strings.Contains(words, "10-bit") {
		t.Errorf("converted step %q: want the measured depth (8-bit), not the prediction (10-bit)", words)
	}
	if info := outputInfoOf(t, s); info.Video() == nil || info.Video().BitDepth != 8 {
		t.Errorf("output_info %+v: want the refused file's own probe (bitDepth 8)", info)
	}
	s.assertOriginalSafe()
}

// A done job stores the check's probe of the new file, says what it measured in the step, and logs the
// same "New file" line a kept job's record has.
func TestADoneJobRecordsWhatTheNewFileIs(t *testing.T) {
	s := newScenario(t)
	s.srcSpec = tenBitSource() // this version converts to HEVC only, so 10-bit stays 10-bit
	s.outSpec = goodOutput()
	s.outSpec.streams[0] = vid("hevc", 640, 360, "yuv420p10le")
	if err := s.run(Replace); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if info := outputInfoOf(t, s); info.Video() == nil || info.Video().Codec != "hevc" || info.Video().BitDepth != 10 {
		t.Errorf("output_info %+v: want the new file's own probe (HEVC, bitDepth 10)", info)
	}
	if words := convertedStep(t, s); !strings.Contains(words, "HEVC · 10-bit") {
		t.Errorf("converted step %q, want the measured HEVC · 10-bit", words)
	}
	if n := logCount(t, s, "New file: hevc "); n != 1 {
		t.Errorf("the log's New file line: %d, want 1", n)
	}
}

// The proof contract's allowed 8-bit case: a 10-bit original onto the card's H.264 encoder, which makes 8
// bits (no 10-bit H.264 on a card), so the check passes. The done job says what the encoder made, and its
// output_info carries that file's own bitDepth.
func TestAnAllowed8BitEncodeIsRecordedAsWhatItMade(t *testing.T) {
	rules.SetAvailableFormats(map[string]bool{"libx264": true}) // an ffmpeg with libx264; HEVC is always offered
	defer rules.SetAvailableFormats(nil)
	s := newScenario(t)
	s.srcSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{
		vid("mpeg2video", 640, 360, "yuv420p10le"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
	s.outSpec = spec{duration: 100, chapters: 2, streams: []map[string]any{ // what the card's H.264 encoder writes
		vid("h264", 640, 360, "yuv420p"), aud("aac", "eng", 2), sub("subrip", "eng", false), att("font.ttf")}}
	w := s.worker(Replace)
	sel := rules.DefaultSelectors(3)
	sel.Format = "h264"
	tmpl := sel.Template()
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
	w.Encoder = func(c string, need10 bool, h *rules.HDR) (string, string) {
		if c != "libx264" {
			return "", ""
		}
		return "h264_nvenc", ""
	}
	if _, err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := s.job(); j.Status != jobs.Done {
		t.Fatalf("status %s, error %q", j.Status, j.Error)
	}
	if info := outputInfoOf(t, s); info.Video() == nil || info.Video().Codec != "h264" || info.Video().BitDepth != 8 {
		t.Errorf("output_info %+v: want the new file's own probe (H.264, bitDepth 8)", info)
	}
	if words := convertedStep(t, s); !strings.Contains(words, "H.264 · 8-bit") {
		t.Errorf("converted step %q, want the measured H.264 · 8-bit", words)
	}
}

// A remote worker's check refused the file and nothing was uploaded: the probe the remote sent is what the
// job's detail has (foundation slice 4, mini-requirement 5).
func TestARemoteKeptJobStoresTheProbeItsWorkerSent(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(3), nil }
	job, err := s.store.Claim("remote-worker")
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.store.LeaseOf(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	rj := NewRemoteJob(lease)
	sent := media.Info{Container: "matroska,webm", DurationSec: 100, Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "hevc", Width: 640, Height: 360, BitDepth: 10}}}
	go func() {
		<-rj.Planned()
		rj.Deliver(RemoteResult{Command: []string{"ffmpeg", "-i", s.src, "-c:v", "libx265", s.src + ".new.mkv"},
			Kept: "the picture check refused", Info: sent})
	}()
	w.RunRemote(context.Background(), job, rj)
	if j := s.job(); j.Status != jobs.Kept {
		t.Fatalf("status %s, want kept: %q", j.Status, j.Error)
	}
	if info := outputInfoOf(t, s); info.Video() == nil || info.Video().Codec != "hevc" || info.Video().BitDepth != 10 {
		t.Errorf("output_info %+v: want the probe the remote worker sent", info)
	}
}
