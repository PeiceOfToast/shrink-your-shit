// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
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
	w.Template = func(jobs.Job) (rules.Template, error) { return tmpl, nil }
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
	w.Template = func(jobs.Job) (rules.Template, error) { return rules.SliderTemplate(3), nil }
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
