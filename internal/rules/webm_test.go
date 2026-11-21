// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package rules

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// A WebM holds only webvtt subtitles (user bug 11-20: WebM with SRT subtitles failed at ffmpeg's
// header): its text subtitles become webvtt the way MP4's become mov_text, picture ones are left out
// with a count, and neither is a reason to leave the file alone.
func TestWebMSubtitles(t *testing.T) {
	SetAvailableFormats(map[string]bool{"libx265": true, "libvpx-vp9": true})
	defer SetAvailableFormats(nil)
	webm := func() Template {
		return standard(func(s *Selectors) { s.FileType, s.Format, s.Audio = "webm", "vp9", "stereo_opus" })
	}
	info := probe("h264", 1280, 720, 25, 5000,
		media.Stream{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng", SubKind: "text"},
		media.Stream{Index: 3, Type: "subtitle", Codec: "hdmv_pgs_subtitle", SubKind: "bitmap"})
	p := webm().Compile(info)
	if p.Skip != "" {
		t.Fatalf("WebM with text and picture subtitles: %q", p.Skip)
	}
	args := strings.Join(p.FFmpegArgs("a.mkv", "b.webm"), " ")
	if !strings.Contains(args, "-c:s:0 webvtt") {
		t.Errorf("the SRT subtitle is not converted to webvtt: %s", args)
	}
	if !strings.Contains(args, "-map -0:3") || p.BitmapSubsDropped != 1 {
		t.Errorf("the PGS subtitle is not left out (dropped %d): %s", p.BitmapSubsDropped, args)
	}
	var subs []StreamExpect
	for _, x := range p.Expect(info, 0).Streams {
		if x.Type == "subtitle" {
			subs = append(subs, x)
		}
	}
	if len(subs) != 1 || subs[0].Codec != "webvtt" || subs[0].Language != "eng" || !subs[0].CheckDefault {
		t.Errorf("expected the one webvtt subtitle to be checked: %+v", subs)
	}

	t.Run("mov_text is converted too", func(t *testing.T) {
		// an MP4's subtitles are mov_text; copied into a WebM they failed at ffmpeg's header
		info := probe("h264", 1280, 720, 25, 5000,
			media.Stream{Index: 2, Type: "subtitle", Codec: "mov_text", Language: "eng", SubKind: "text"})
		p := webm().Compile(info)
		if p.Skip != "" {
			t.Fatalf("WebM with a mov_text subtitle: %q", p.Skip)
		}
		if args := strings.Join(p.FFmpegArgs("a.mp4", "b.webm"), " "); !strings.Contains(args, "-c:s:0 webvtt") {
			t.Errorf("the mov_text subtitle is not converted to webvtt: %s", args)
		}
		var subs []StreamExpect
		for _, x := range p.Expect(info, 0).Streams {
			if x.Type == "subtitle" {
				subs = append(subs, x)
			}
		}
		if len(subs) != 1 || subs[0].Codec != "webvtt" || !subs[0].CheckDefault {
			t.Errorf("expected the one webvtt subtitle to be checked: %+v", subs)
		}
	})

	t.Run("a webvtt subtitle is copied as it is", func(t *testing.T) {
		info := probe("h264", 1280, 720, 25, 5000,
			media.Stream{Index: 2, Type: "subtitle", Codec: "webvtt", Language: "eng", SubKind: "text"})
		p := webm().Compile(info)
		if args := strings.Join(p.FFmpegArgs("a.mkv", "b.webm"), " "); p.Skip != "" || strings.Contains(args, "-c:s") {
			t.Errorf("skip %q args %s", p.Skip, args)
		}
	})

	t.Run("styled subtitles say so once, in WebM's words", func(t *testing.T) {
		info := probe("h264", 1280, 720, 25, 5000,
			media.Stream{Index: 2, Type: "subtitle", Codec: "ass", Language: "eng", SubKind: "text"})
		p := webm().Compile(info)
		args := strings.Join(p.FFmpegArgs("a.mkv", "b.webm"), " ")
		if p.Skip != "" || !p.StyledSubsPlain || !strings.Contains(args, "-c:s:0 webvtt") {
			t.Errorf("skip %q styled %v args %s", p.Skip, p.StyledSubsPlain, args)
		}
	})

	t.Run("a picture subtitle already dropped is counted once", func(t *testing.T) {
		info := probe("h264", 1280, 720, 25, 5000,
			media.Stream{Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle", SubKind: "bitmap"})
		p := standard(func(s *Selectors) {
			s.FileType, s.Format, s.Audio, s.Subtitles = "webm", "vp9", "stereo_opus", "text_only"
		}).Compile(info)
		if p.Skip != "" || p.BitmapSubsDropped != 0 {
			t.Errorf("skip %q dropped %d", p.Skip, p.BitmapSubsDropped)
		}
	})

	t.Run("the log line names how many were left out", func(t *testing.T) {
		if got := BitmapSubsNote(1); got != "1 picture subtitle was left out: WebM can't hold it." {
			t.Errorf("got %q", got)
		}
		if got := BitmapSubsNote(3); got != "3 picture subtitles were left out: WebM can't hold them." {
			t.Errorf("got %q", got)
		}
	})

	t.Run("each kept subtitle keeps its source disposition", func(t *testing.T) {
		// WebM is not told the source's default flag: the first subtitle is not default, the second is
		// default and forced, so the new file must say so for each subtitle by its position in the output.
		info := probe("h264", 1280, 720, 25, 5000,
			media.Stream{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng", SubKind: "text"},
			media.Stream{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng", SubKind: "text", Default: true, Forced: true})
		p := webm().Compile(info)
		if p.Skip != "" {
			t.Fatalf("WebM with two text subtitles: %q", p.Skip)
		}
		args := " " + strings.Join(p.FFmpegArgs("a.mkv", "b.webm"), " ") + " "
		for _, want := range []string{" -disposition:s:0 0 ", " -disposition:s:1 default+forced "} {
			if !strings.Contains(args, want) {
				t.Errorf("missing %q: %s", want, args)
			}
		}
	})

	t.Run("MP4 and MKV are as they were", func(t *testing.T) {
		srt := media.Stream{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng", SubKind: "text"}
		mp4 := standard(func(s *Selectors) { s.FileType = "mp4" }).Compile(probe("h264", 1280, 720, 25, 5000, srt))
		if args := strings.Join(mp4.FFmpegArgs("a.mkv", "b.mp4"), " "); !strings.Contains(args, "-c:s:0 mov_text") || strings.Contains(args, "webvtt") {
			t.Errorf("MP4: %s", args)
		}
		mkv := standard(func(s *Selectors) { s.FileType = "mkv" }).Compile(probe("h264", 1280, 720, 25, 5000, srt))
		if args := strings.Join(mkv.FFmpegArgs("a.mkv", "b.mkv"), " "); strings.Contains(args, "-c:s") || strings.Contains(args, "-disposition") || mkv.BitmapSubsDropped != 0 {
			t.Errorf("MKV: %s", args)
		}
	})
}
