// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A real WebM job with a text subtitle (user bug 11-20: it failed at ffmpeg's header): the subtitle is
// converted to webvtt and the job is done, not a damaged-file failure.
func TestRealWebMKeepsTextSubtitles(t *testing.T) {
	needTools(t)
	rules.SetAvailableFormats(map[string]bool{"libx265": true, "libvpx-vp9": true})
	defer rules.SetAvailableFormats(nil)
	dir := t.TempDir()
	srt := filepath.Join(dir, "s.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,000\nHello there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ass := filepath.Join(dir, "s.ass")
	if err := os.WriteFile(ass, []byte("[Script Info]\nScriptType: v4.00+\n[V4+ Styles]\nFormat: Name, Fontname, Fontsize\nStyle: Default,Arial,20\n[Events]\nFormat: Layer, Start, End, Style, Text\nDialogue: 0,0:00:00.00,0:00:01.00,Default,Styled\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "m.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=320x180:rate=25", "-i", srt, "-i", ass,
		"-map", "0:v", "-map", "0:a:0?", "-map", "1", "-map", "2",
		"-c:v", "mpeg2video", "-b:v", "2000k", "-c:s", "srt", "-metadata:s:s:0", "language=eng",
		"-c:s:1", "ass", "-metadata:s:s:1", "language=eng", src)
	s := rules.DefaultSelectors(1)
	// faster: the smallest VP9 work that still makes a file
	s.Speed, s.FileType, s.Format, s.Audio, s.AfterConverting = "faster", "webm", "vp9", "stereo_opus", "beside"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := s.Template()
	st, id := realJob(t, src, 3)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	w.run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	out := probe(t, j.OutputPath)
	subs := out.Subtitles()
	if filepath.Ext(j.OutputPath) != ".webm" || len(subs) != 2 || subs[0].Codec != "webvtt" || subs[1].Codec != "webvtt" || subs[0].Language != "eng" {
		t.Errorf("new file %s: %+v", j.OutputPath, out.Streams)
	}
	if text := logTextOf(st, id); strings.Count(text, rules.StyledSubsWebMNote) != 1 || strings.Count(text, rules.StyledSubsNote) != 0 {
		t.Errorf("the ASS note in WebM's words %d times (MP4's %d):\n%s", strings.Count(text, rules.StyledSubsWebMNote), strings.Count(text, rules.StyledSubsNote), text)
	}
}

// An MP4's subtitles are mov_text, and that is the common shape of the user's bug: a hand-written list
// of subtitle codecs missed it, so it was copied into the WebM and the job died at ffmpeg's header.
// Now it is converted like any other text subtitle.
func TestRealWebMMovTextBecomesWebVTT(t *testing.T) {
	needTools(t)
	rules.SetAvailableFormats(map[string]bool{"libx265": true, "libvpx-vp9": true})
	defer rules.SetAvailableFormats(nil)
	dir := t.TempDir()
	srt := filepath.Join(dir, "s.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:00,500 --> 00:00:02,000\nHello there\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "m.mp4")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=3:size=320x180:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=3", "-i", srt,
		"-map", "0:v", "-map", "1:a", "-map", "2",
		"-c:v", "mpeg4", "-c:a", "aac", "-c:s", "mov_text", "-metadata:s:s:0", "language=eng", src)
	if got := probe(t, src).Subtitles(); len(got) != 1 || got[0].Codec != "mov_text" || got[0].SubKind != "text" {
		t.Fatalf("the source's subtitles: %+v", got)
	}
	s := rules.DefaultSelectors(1)
	s.Speed, s.FileType, s.Format, s.Audio, s.AfterConverting = "faster", "webm", "vp9", "stereo_opus", "beside"
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	tmpl := s.Template()
	st, id := realJob(t, src, 3)
	w := realWorker(st, media.OSExec{})
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return tmpl, 0, nil }
	w.run(t)
	j, _ := st.Get(id)
	if j.Status != jobs.Done {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	out := probe(t, j.OutputPath)
	subs := out.Subtitles()
	if filepath.Ext(j.OutputPath) != ".webm" || len(subs) != 1 || subs[0].Codec != "webvtt" || subs[0].Language != "eng" {
		t.Errorf("new file %s: %+v", j.OutputPath, out.Streams)
	}
}

// A job whose ffmpeg stops at the new file's header says what the new type can't hold, never that the
// file may be damaged (user bug 11-20).
func TestHeaderRefusalIsNotDamage(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Template = func(jobs.Job) (rules.Template, int64, error) { return rules.SliderTemplate(3), 0, nil }
	s.encodeErr = errors.New("exit status 234")
	s.encodeStderr = "[webm @ 0x1] Only VP8 or VP9 or AV1 video and Vorbis or Opus audio and WebVTT subtitles are supported for WebM.\n" +
		"[out#0/webm @ 0x2] Could not write header (incorrect codec parameters ?): Invalid argument"
	w.RunOne(context.Background())
	j, _ := s.store.Get(s.id)
	if j.Status != jobs.Failed {
		t.Fatalf("%s %q", j.Status, j.Error)
	}
	if !strings.Contains(j.Error, "Only VP8 or VP9 or AV1 video") || strings.Contains(j.Error, "damaged") {
		t.Errorf("the job's message: %q", j.Error)
	}
}

// Slice 15: a file-type refusal is the workflow's settings, not the graphics card's machine. The job
// fails once, with the header sentence; the card is not tried again on the processor and does not count
// as failing. Before the settings_rejected code existed the job fell back and counted.
func TestHeaderRefusalIsNotAnEncoderFailure(t *testing.T) {
	s := newScenario(t)
	w := s.worker(Replace)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	s.failEncoder = "hevc_qsv"
	s.fullGPUStderr = "[webm @ 0x1] Only VP8 or VP9 or AV1 video and Vorbis or Opus audio and WebVTT subtitles are supported for WebM.\n" +
		"[out#0/webm @ 0x2] Could not write header (incorrect codec parameters ?): Invalid argument"
	if _, err := w.RunOne(context.Background()); err == nil {
		t.Errorf("RunOne: want the refusal to fail the job")
	}
	j, _ := s.store.Get(s.id)
	if j.Status != jobs.Failed {
		t.Errorf("status %s, error %q", j.Status, j.Error)
	}
	if enc := encodeCalls(s); len(enc) != 1 || !strings.Contains(enc[0], "hevc_qsv") {
		t.Errorf("encodes, want the graphics encoder once and no processor retry:\n%s", strings.Join(enc, "\n"))
	}
	if falls, err := s.store.EncoderFallbacksSince(time.Unix(0, 0)); err != nil || len(falls) != 0 {
		t.Errorf("fall-backs %+v, %v: want none", falls, err)
	}
	if enc, _, err := s.store.EncoderLastFell(w.ID); err != nil || enc != "" {
		t.Errorf("last fall %q, %v: want none", enc, err)
	}
	if !strings.Contains(j.Error, "Only VP8 or VP9 or AV1 video") || !strings.HasSuffix(j.Error, "Your original was not touched.") {
		t.Errorf("the job's message: %q", j.Error)
	}
	ev, _ := s.store.JobEvents(s.id)
	if len(ev) == 0 || ev[len(ev)-1].Reason != "settings_rejected" {
		t.Errorf("events %+v, want the last to be settings_rejected", ev)
	}
}

// Slice 15, review #580: on the full GPU pipeline any failure steps down, so a file-type refusal must be
// named as one there too: one encode, no step-down, no encoder-health change, and the header sentence.
func TestHeaderRefusalOnTheFullPipelineDoesNotStepDown(t *testing.T) {
	s := newScenario(t)
	s.failFullGPU = true
	s.fullGPUStderr = "[webm @ 0x1] Only VP8 or VP9 or AV1 video and Vorbis or Opus audio and WebVTT subtitles are supported for WebM.\n" +
		"[out#0/webm @ 0x2] Could not write header (incorrect codec parameters ?): Invalid argument"
	w := s.worker(Replace)
	var asked string
	w.GraphicsDecodes = decodeHook(true, &asked)
	w.Encoder = func(string, bool, *rules.HDR) (string, string) { return "hevc_qsv", "" }
	if _, err := w.RunOne(context.Background()); err == nil {
		t.Errorf("RunOne: want the refusal to fail the job")
	}
	j, _ := s.store.Get(s.id)
	if j.Status != jobs.Failed {
		t.Errorf("status %s, error %q", j.Status, j.Error)
	}
	if enc := encodeCalls(s); len(enc) != 1 || !strings.Contains(enc[0], "-hwaccel") {
		t.Errorf("encodes, want the full pipeline once and no step-down:\n%s", strings.Join(enc, "\n"))
	}
	if falls, err := s.store.EncoderFallbacksSince(time.Unix(0, 0)); err != nil || len(falls) != 0 {
		t.Errorf("fall-backs %+v, %v: want none", falls, err)
	}
	if enc, _, err := s.store.EncoderLastFell(w.ID); err != nil || enc != "" {
		t.Errorf("last fall %q, %v: want none", enc, err)
	}
	if n := logCount(t, s, "full GPU pipeline failed"); n != 0 {
		t.Errorf("step-down line: %d, want none", n)
	}
	if !strings.Contains(j.Error, "supported for WebM") || !strings.HasSuffix(j.Error, "Your original was not touched.") {
		t.Errorf("the job's message: %q", j.Error)
	}
}

// ffmpeg stopping before the new file's header is written: the message says what the new type can't
// hold, never that the file may be damaged (user bug 11-20).
func TestHeaderWords(t *testing.T) {
	webmErr := "[webm @ 0x55d0] Only VP8 or VP9 or AV1 video and Vorbis or Opus audio and WebVTT subtitles are supported for WebM.\n" +
		"Could not write header for output file #0 (incorrect codec parameters ?): Invalid argument\nError initializing output stream 0:1 --"
	if got := headerWords(webmErr); !strings.Contains(got, "Only VP8 or VP9 or AV1 video") || !strings.HasSuffix(got, "Your original was not touched.") || strings.Contains(got, "damaged") {
		t.Errorf("got %q", got)
	}
	plain := "Could not write header for output file #0 (incorrect codec parameters ?): Invalid argument"
	if got := headerWords(plain); got != "ffmpeg couldn't write the new file. Your original was not touched." {
		t.Errorf("got %q", got)
	}
	if got := headerWords("Invalid data found when processing input"); got != "" {
		t.Errorf("a read error must not be called a header refusal: %q", got)
	}
}
