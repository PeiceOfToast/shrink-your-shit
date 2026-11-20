// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A new file whose audio starts 0.5 s late is kept, and the reason names the audio track. Both new files are
// HEVC, the codec the rule asks for, so no format problem is mixed in. A clean re-encode passes.
func TestRealShiftedAudioIsKept(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "aac", src)

	// -itsoffset goes before the audio input, so only the audio starts 0.5 s late.
	shifted := filepath.Join(dir, "shifted.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-itsoffset", "0.5", "-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000",
		"-c:v", "libx265", "-preset", "ultrafast", "-crf", "28", "-c:a", "aac", shifted)

	clean := filepath.Join(dir, "clean.mkv")
	runFFmpeg(t, "-i", src, "-c:v", "libx265", "-preset", "ultrafast", "-crf", "28", "-c:a", "aac", clean)

	st, _ := realJob(t, src, 10)
	w := realWorker(st, media.OSExec{})
	srcInfo := probeFile(t, src)
	exp := rules.DefaultExpect(srcInfo, 0)
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}

	err = w.verify(context.Background(), srcInfo, exp, src, shifted, fi.Size())
	var k *KeptError
	if !errors.As(err, &k) {
		t.Fatalf("a track 0.5 s late: %v, want the original kept", err)
	}
	// The shift also lengthens the file to 11 s, which the existing container-length check reports too; that
	// line is allowed here. The audio track must be named as out of step.
	if !strings.Contains(k.Reason, "audio #1") || !strings.Contains(k.Reason, "out of step") {
		t.Fatalf("reason %q does not name the audio track as out of step", k.Reason)
	}
	assertPlain(t, k.Reason)

	if err := w.verify(context.Background(), srcInfo, exp, src, clean, fi.Size()); err != nil {
		t.Fatalf("a clean re-encode was not kept: %v", err)
	}
}

// A 10 s picture whose audio is cut to 6 s in the new file is kept, and the reason names the audio track.
func TestRealCutAudioIsKept(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "aac", src)

	cut := filepath.Join(dir, "cut.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=6:sample_rate=48000",
		"-c:v", "libx265", "-preset", "ultrafast", "-crf", "28", "-c:a", "aac", cut)

	st, _ := realJob(t, src, 10)
	w := realWorker(st, media.OSExec{})
	srcInfo := probeFile(t, src)
	exp := rules.DefaultExpect(srcInfo, 0)
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	err = w.verify(context.Background(), srcInfo, exp, src, cut, fi.Size())
	var k *KeptError
	if !errors.As(err, &k) {
		t.Fatalf("audio cut to 6 s under a 10 s picture: %v, want the original kept", err)
	}
	if !strings.Contains(k.Reason, "audio #1") || !strings.Contains(k.Reason, "long") {
		t.Fatalf("reason %q does not name the audio track as too short", k.Reason)
	}
	assertPlain(t, k.Reason)
}

// A source whose audio is genuinely 2 s shorter than its picture, converted faithfully, passes: the track is
// judged against its own original length, not against the picture.
func TestRealShorterSourceAudioPasses(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=8:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "aac", src)

	clean := filepath.Join(dir, "clean.mkv")
	runFFmpeg(t, "-i", src, "-c:v", "libx265", "-preset", "ultrafast", "-crf", "28", "-c:a", "aac", clean)

	st, _ := realJob(t, src, 10)
	w := realWorker(st, media.OSExec{})
	srcInfo := probeFile(t, src)
	exp := rules.DefaultExpect(srcInfo, 0)
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.verify(context.Background(), srcInfo, exp, src, clean, fi.Size()); err != nil {
		t.Fatalf("a faithful conversion of an 8 s track under a 10 s picture was not kept: %v", err)
	}
}
