// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A styled-subtitle file: video, a 5.1 FLAC, an SRT subtitle and a font attachment. The plan's ffmpeg arguments
// are run for real. Adding a stereo AAC from the FLAC moves the subtitle, and the font must follow the moved
// streams: before the fix the mux fails with "Received a packet for an attachment stream" (exit 234 on ffmpeg
// 7.1.5, CT 221). This tests the mux, not the worker's damage check.
func TestRealFontAttachmentWithAddedTrack(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	srt := filepath.Join(dir, "s.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:02,000\nhi\n"), 0o644)
	font := filepath.Join(dir, "font.ttf")
	os.WriteFile(font, bytes.Repeat([]byte{0x00, 0x01, 0x00, 0x00}, 1024), 0o644) // ffmpeg copies the bytes as they are
	src := filepath.Join(dir, "src.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc2=duration=5:size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=duration=5:frequency=440", "-i", srt,
		"-map", "0", "-map", "1", "-map", "2",
		"-c:v", "libx264", "-b:v", "1000k", "-c:a", "flac", "-ac", "6", "-c:s", "srt",
		"-metadata:s:a:0", "language=eng", "-metadata:s:s:0", "language=eng",
		"-attach", font, "-metadata:s:t:0", "mimetype=application/x-truetype-font", src)
	info := probe(t, src)

	// mux converts src with the plan for the given audio choice, runs the plan's ffmpeg arguments for real,
	// and returns the probed output. The plan's own arguments are what the worker would run.
	mux := func(t *testing.T, audio string) media.Info {
		t.Helper()
		s := rules.DefaultSelectors(3)
		s.Audio, s.OnlyMyLanguages, s.Subtitles = audio, false, "keep_all"
		tmpl := s.Template()
		tmpl.Languages = ""
		plan := tmpl.Compile(info)
		out := filepath.Join(t.TempDir(), "out.mkv")
		args := plan.FFmpegArgs(src, out)
		if msg, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, msg)
		}
		return probe(t, out)
	}

	t.Run("added stereo AAC keeps the font last", func(t *testing.T) {
		out := mux(t, "stereo_aac_surround")
		var got []string
		for _, a := range out.Audio() {
			got = append(got, a.Codec+strconv.Itoa(a.Channels))
		}
		if strings.Join(got, " ") != "flac6 aac2" {
			t.Errorf("audio %v, want flac6 aac2", got)
		}
		// the added AAC is stream 2, right after its FLAC source; the subtitle follows, the font comes last
		if a := out.Audio(); len(a) != 2 || a[1].Index != 2 {
			t.Errorf("the added AAC is stream %+v, want index 2", a)
		}
		att := out.Attachments()
		if len(att) != 1 || att[0].MimeType != "application/x-truetype-font" {
			t.Fatalf("attachments %+v, want one truetype font", att)
		}
		if subs := out.Subtitles(); len(subs) != 1 || att[0].Index <= subs[0].Index {
			t.Errorf("font at stream %d, want after the subtitle %+v", att[0].Index, subs)
		}
	})

	t.Run("no added track keeps the font", func(t *testing.T) {
		out := mux(t, "keep_all")
		att := out.Attachments()
		if len(att) != 1 || att[0].MimeType != "application/x-truetype-font" {
			t.Errorf("attachments %+v, want one truetype font", att)
		}
	})
}
