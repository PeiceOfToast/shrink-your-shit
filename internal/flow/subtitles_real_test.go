// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package flow_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// writeSup writes a one-frame PGS stream. The program reads a subtitle's kind from its codec, so the
// track needs no picture anyone looks at: just a display set ffmpeg can mux, so ffprobe reports
// hdmv_pgs_subtitle. Segments are "PG", PTS, DTS, type, a two-byte length, then the payload.
func writeSup(t *testing.T, path string) {
	t.Helper()
	seg := func(typ byte, payload []byte) []byte {
		b := []byte("PG")
		b = binary.BigEndian.AppendUint32(b, 90000) // PTS
		b = binary.BigEndian.AppendUint32(b, 90000) // DTS
		b = append(b, typ)
		b = binary.BigEndian.AppendUint16(b, uint16(len(payload)))
		return append(b, payload...)
	}
	u16 := func(b []byte, v uint16) []byte { return binary.BigEndian.AppendUint16(b, v) }

	// PCS: 1920x1080, one 100x100 window with one object in it.
	var pcs []byte
	pcs = u16(pcs, 1920)
	pcs = u16(pcs, 1080)
	pcs = append(pcs, 0x10, 0, 0, 0x80, 0, 0, 1)
	pcs = u16(pcs, 0)    // object id
	pcs = append(pcs, 0) // window id
	pcs = append(pcs, 0) // flags
	pcs = u16(pcs, 0)
	pcs = u16(pcs, 0)
	// WDS: the one window.
	wds := []byte{1, 0}
	wds = u16(wds, 0)
	wds = u16(wds, 0)
	wds = u16(wds, 100)
	wds = u16(wds, 100)
	// PDS: palette 0, transparent black and opaque white.
	pds := []byte{0, 0, 0, 16, 128, 128, 255, 1, 235, 128, 128, 255}
	// ODS: object 0, first and last in its sequence, one byte of RLE.
	ods := []byte{0, 0, 0xC0, 0, 0, 1, 0}

	var out []byte
	out = append(out, seg(0x16, pcs)...)
	out = append(out, seg(0x17, wds)...)
	out = append(out, seg(0x14, pds)...)
	out = append(out, seg(0x15, ods)...)
	out = append(out, seg(0x80, nil)...)
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// makeSubtitleClip writes a real MKV: a fat H.264 picture, one AAC track, one text subtitle per
// language in textLanguages, then one PGS track in the first language.
func makeSubtitleClip(t *testing.T, src string, textLanguages ...string) {
	t.Helper()
	if len(textLanguages) == 0 {
		t.Fatal("makeSubtitleClip needs at least one text subtitle language")
	}
	dir := t.TempDir()
	args := []string{
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
	}
	for i, lang := range textLanguages {
		path := filepath.Join(dir, fmt.Sprintf("text-%d.srt", i))
		if err := os.WriteFile(path, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello from "+lang+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		args = append(args, "-f", "srt", "-i", path)
	}
	sup := filepath.Join(dir, "picture.sup")
	writeSup(t, sup)
	args = append(args, "-f", "sup", "-i", sup, "-map", "0:v", "-map", "1:a")
	for i := range textLanguages {
		args = append(args, "-map", fmt.Sprintf("%d:s", 2+i))
	}
	args = append(args, "-map", fmt.Sprintf("%d:s", 2+len(textLanguages)),
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "6M", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-c:s", "copy")
	for i, lang := range textLanguages {
		args = append(args, fmt.Sprintf("-metadata:s:s:%d", i), "language="+lang)
	}
	args = append(args, fmt.Sprintf("-metadata:s:s:%d", len(textLanguages)), "language="+textLanguages[0])
	makeRealClip(t, src, args...)
}

// requireSubtitleEncoders is every encoder these tests need to make and re-encode their clip.
func requireSubtitleEncoders(t *testing.T) {
	t.Helper()
	requireRealTools(t)
	for _, encoder := range []string{"libx264", "libx265", "aac"} {
		if !hasEncoder(t, encoder) {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("this ffmpeg build has no %s and SYP_REQUIRE_FFMPEG is set", encoder)
			}
			t.Skipf("this ffmpeg build has no %s", encoder)
		}
	}
}

// TestRealRunTextOnlyKeepsTheTextSubtitles is L-AUD-21's real path: a file with two text subtitles
// and a PGS track comes out with the two text tracks, read back with ffprobe, and the picture
// subtitle is gone.
func TestRealRunTextOnlyKeepsTheTextSubtitles(t *testing.T) {
	requireSubtitleEncoders(t)

	root := t.TempDir()
	src := filepath.Join(root, "subtitled.mkv")
	makeSubtitleClip(t, src, "eng", "ger")
	before, err := media.Probe(context.Background(), src)
	if err != nil {
		t.Fatalf("probe the source: %v", err)
	}
	if len(before.Subtitles) != 3 || before.Subtitles[2].Codec != "hdmv_pgs_subtitle" {
		t.Fatalf("source subtitles = %+v, want two text tracks and one PGS", before.Subtitles)
	}

	res := runRealFlow(t, src, `{"format":"copy","subtitles":"text_only"}`)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	got, err := media.Probe(context.Background(), res.OutputPath)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	if len(got.Subtitles) != 2 {
		t.Fatalf("output subtitles = %+v, want the two text tracks", got.Subtitles)
	}
	for _, s := range got.Subtitles {
		if s.Codec != "subrip" {
			t.Errorf("output subtitle = %+v, want only text tracks", s)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(root, "*.srt")); len(matches) != 0 {
		t.Errorf("text only wrote sidecar files: %v", matches)
	}
}

// TestRealRunSrtWritesTheNamedFiles is L-AUD-22 and L-AUD-25's real path: the two chosen text
// subtitles are written as .srt files named for the new file, non-empty, and they are the only
// subtitles that leave it; the picture subtitle stays inside.
func TestRealRunSrtWritesTheNamedFiles(t *testing.T) {
	requireSubtitleEncoders(t)

	root := t.TempDir()
	src := filepath.Join(root, "subtitled.mkv")
	makeSubtitleClip(t, src, "eng", "eng")

	res := runRealFlow(t, src, `{"format":"copy","subtitles":"srt","subtitleLanguages":["eng"]}`)
	if res.Outcome != flow.OutcomeSmaller {
		t.Fatalf("result = %+v, want smaller", res)
	}
	out := filepath.Join(root, "subtitled-shrunk.mkv")
	if res.OutputPath != out {
		t.Fatalf("output = %q, want %q", res.OutputPath, out)
	}
	for _, name := range []string{"subtitled-shrunk.eng.srt", "subtitled-shrunk.eng.2.srt"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Errorf("%s: %v, want the .srt file beside the new one", name, err)
			continue
		}
		if len(b) == 0 || !bytes.Contains(b, []byte("-->")) {
			t.Errorf("%s = %q, want real subtitle text", name, b)
		}
	}

	got, err := media.Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("probe the output: %v", err)
	}
	if len(got.Subtitles) != 1 || got.Subtitles[0].Codec != "hdmv_pgs_subtitle" {
		t.Errorf("output subtitles = %+v, want only the picture subtitle left inside", got.Subtitles)
	}

	// The original file is untouched and nothing staged is left behind.
	if _, err := os.Stat(src); err != nil {
		t.Errorf("the original is gone: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".syp-tmp") {
			t.Errorf("staging folder %s left behind", e.Name())
		}
	}
}
