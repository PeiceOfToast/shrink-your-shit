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
)

// A 24 fps file with AAC audio (48 kHz) plays back with no errors. Without -copyts, AAC's priming shifts the
// timestamps and the playback check reported "non monotonically increasing dts" on every such file.
func TestDecodeRun24fpsAACNoFalseAlarm(t *testing.T) {
	needTools(t)
	p := filepath.Join(t.TempDir(), "t.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "aac", p)
	st, _ := realJob(t, p, 10)
	w := realWorker(st, media.OSExec{})
	if err := w.decodeRun(context.Background(), p, probeFile(t, p), 0, 0, 10, 24); err != nil {
		t.Fatalf("playback check false alarm: %v", err)
	}
	// The sampled path (a 5 s sample from 2 s) has the same false alarm without -copyts.
	if err := w.decodeRun(context.Background(), p, probeFile(t, p), 2, 5, 5, 24); err != nil {
		t.Fatalf("playback check false alarm on the sampled path: %v", err)
	}
}

// A sample that starts after its own length (5 s in, 3 s long) must play its 3 s. With -t after -i and -copyts,
// -t was an absolute end time and the sample played 0 frames, which passed silently.
func TestDecodeRunSampleStartingAfterItsLengthPlays(t *testing.T) {
	needTools(t)
	p := filepath.Join(t.TempDir(), "t.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "aac", p)
	st, _ := realJob(t, p, 10)
	rec := &recordExec{Exec: media.OSExec{}}
	w := realWorker(st, rec)
	if err := w.decodeRun(context.Background(), p, probeFile(t, p), 5, 3, 10, 24); err != nil {
		t.Fatalf("sample 5 s + 3 s: %v", err)
	}
	frame, ok := progressFrame(rec.progress)
	if !ok || frame < 60 {
		t.Fatalf("sample 5 s + 3 s played frame %d (reported %v), want about 72", frame, ok)
	}
	t.Logf("sample 5 s + 3 s played frame %d", frame)
}

// recordExec keeps the progress output of the last capture, so a test can see how many frames ffmpeg played.
type recordExec struct {
	media.Exec
	progress []byte
}

func (r *recordExec) Capture(ctx context.Context, name string, args ...string) ([]byte, []byte, error) {
	out, errOut, err := r.Exec.Capture(ctx, name, args...)
	r.progress = out
	return out, errOut, err
}

// probeFile is the probe of a real file, as the worker has it when it plays the file.
func probeFile(t *testing.T, p string) media.Info {
	t.Helper()
	info, err := media.Probe(context.Background(), media.OSExec{}, "ffprobe", p)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// damageAudioFrames overwrites the payload of 40 ADTS audio frames in the middle of an AAC file, leaving their
// headers, so the file's audio is damaged and nothing else is.
func damageAudioFrames(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var frames [][2]int // offset and length of each ADTS frame
	for i := 0; i+7 <= len(b); {
		n := int(b[i+3]&0x03)<<11 | int(b[i+4])<<3 | int(b[i+5])>>5
		if n < 7 || i+n > len(b) {
			break
		}
		frames = append(frames, [2]int{i, n})
		i += n
	}
	mid := len(frames) / 2
	for _, f := range frames[mid-20 : mid+20] {
		for k := f[0] + 7; k < f[0]+f[1]; k++ {
			b[k] = 0xFF
		}
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Damage in the audio, and only there, keeps the original and names the audio track. The same video with clean
// audio plays to the end, so the damage is the audio's.
func TestRealDamagedAudioIsKept(t *testing.T) {
	needTools(t)
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.mkv")
	runFFmpeg(t, "-f", "lavfi", "-i", "testsrc=rate=24:size=320x240:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000",
		"-c:v", "libx264", "-c:a", "aac", clean)
	aac := filepath.Join(dir, "audio.aac")
	runFFmpeg(t, "-f", "lavfi", "-i", "sine=duration=10:sample_rate=48000", "-c:a", "aac", "-b:a", "128k", "-f", "adts", aac)
	damageAudioFrames(t, aac)
	damaged := filepath.Join(dir, "damaged.mkv")
	runFFmpeg(t, "-i", clean, "-i", aac, "-map", "0:v", "-map", "1:a", "-c", "copy", damaged)

	st, _ := realJob(t, clean, 10)
	w := realWorker(st, media.OSExec{})
	if err := w.decodeRun(context.Background(), clean, probeFile(t, clean), 0, 0, 10, 24); err != nil {
		t.Fatalf("the undamaged file was kept: %v", err)
	}
	err := w.decodeRun(context.Background(), damaged, probeFile(t, damaged), 0, 0, 10, 24)
	var k *KeptError
	if !errors.As(err, &k) {
		t.Fatalf("damaged audio: %v, want the original kept", err)
	}
	// The reason names the audio track in plain words. Here it also says other lines were reported: the null
	// muxer's timestamp messages, which ffmpeg prints with the damage and which name no decoder.
	if !strings.Contains(k.Reason, "audio track 1") {
		t.Fatalf("reason %q does not name the audio track", k.Reason)
	}
	assertPlain(t, k.Reason)
}
