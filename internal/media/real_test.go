// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package media

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// requireFFmpeg is L-INST-7: a real-media test skips only when ffmpeg is missing and
// SYP_REQUIRE_FFMPEG is unset. The CI step sets it and also fails on any "--- SKIP: TestReal",
// so the two together cannot be defeated by a missing tool going unnoticed.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("SYP_REQUIRE_FFMPEG") != "" {
				t.Fatalf("%s is not on PATH and SYP_REQUIRE_FFMPEG is set: the real-media tests must not skip", tool)
			}
			t.Skipf("%s is not on PATH (set SYP_REQUIRE_FFMPEG=1 to fail instead)", tool)
		}
	}
}

func TestRealProbeGeneratedClips(t *testing.T) {
	requireFFmpeg(t)
	clips := generateClips(t)
	ctx := context.Background()

	probe := func(t *testing.T, name string) Info {
		t.Helper()
		info, err := Probe(ctx, clips[name])
		if err != nil {
			t.Fatalf("probe %s: %v", name, err)
		}
		return info
	}

	t.Run("h264", func(t *testing.T) {
		info := probe(t, "h264-aac.mp4")
		if info.Format.Names[0] != "mov" || info.Format.Duration < 1900*time.Millisecond {
			t.Errorf("format = %v %v, want mov, about 2s", info.Format.Names, info.Format.Duration)
		}
		if len(info.Video) != 1 || info.Video[0].Codec != "h264" {
			t.Fatalf("video = %+v, want one h264 track", info.Video)
		}
		if v := info.Video[0]; v.Width != 320 || v.Height != 240 || v.FrameRate.Float() != 30 {
			t.Errorf("video = %dx%d at %v, want 320x240 at 30", v.Width, v.Height, v.FrameRate.Float())
		}
		if len(info.Audio) != 1 || info.Audio[0].Codec != "aac" || info.Audio[0].Channels != 2 {
			t.Fatalf("audio = %+v, want one stereo aac", info.Audio)
		}
	})

	t.Run("hevc10", func(t *testing.T) {
		info := probe(t, "hevc10.mkv")
		if len(info.Video) != 1 {
			t.Fatalf("video = %d tracks, want 1", len(info.Video))
		}
		if v := info.Video[0]; v.Codec != "hevc" || v.BitDepth != 10 || v.PixelFormat != "yuv420p10le" {
			t.Errorf("video = %s %s %d-bit, want hevc yuv420p10le 10-bit", v.Codec, v.PixelFormat, v.BitDepth)
		}
	})

	t.Run("surround", func(t *testing.T) {
		info := probe(t, "surround.mp4")
		if len(info.Audio) != 1 {
			t.Fatalf("audio = %d tracks, want 1", len(info.Audio))
		}
		if a := info.Audio[0]; a.Channels != 6 || a.ChannelLayout != "5.1" {
			t.Errorf("audio = %d channels (%s), want 6 (5.1)", a.Channels, a.ChannelLayout)
		}
	})

	t.Run("multilingual", func(t *testing.T) {
		info := probe(t, "multilingual.mkv")
		if len(info.Audio) != 2 {
			t.Fatalf("audio = %d tracks, want 2", len(info.Audio))
		}
		if a := info.Audio[0]; a.Language != "eng" || a.Title != "English" {
			t.Errorf("first audio = %s/%q, want eng/English", a.Language, a.Title)
		}
		if a := info.Audio[1]; a.Language != "ger" || a.Title != "Deutsch" {
			t.Errorf("second audio = %s/%q, want ger/Deutsch", a.Language, a.Title)
		}
		if len(info.Subtitles) != 1 {
			t.Fatalf("subtitles = %d, want 1", len(info.Subtitles))
		}
		if s := info.Subtitles[0]; s.Codec != "subrip" || s.Kind() != SubtitleText || s.Language != "eng" {
			t.Errorf("subtitle = %+v, want text subrip eng", s)
		}
		if len(info.Chapters) != 2 {
			t.Fatalf("chapters = %d, want 2", len(info.Chapters))
		}
		if c := info.Chapters[0]; c.Title != "Intro" || c.Start != 0 || c.End != 500*time.Millisecond {
			t.Errorf("first chapter = %+v, want Intro 0-500ms", c)
		}
	})

	t.Run("hdr10", func(t *testing.T) {
		info := probe(t, "hdr10.mkv")
		if len(info.Video) != 1 {
			t.Fatalf("video = %d tracks, want 1", len(info.Video))
		}
		v := info.Video[0]
		if v.ColorPrimaries != "bt2020" || v.ColorTransfer != "smpte2084" {
			t.Errorf("colour = %s/%s, want bt2020/smpte2084", v.ColorPrimaries, v.ColorTransfer)
		}
		if v.HDRKind() != "HDR10" {
			t.Errorf("HDR kind = %q, want HDR10", v.HDRKind())
		}
	})

	t.Run("cover", func(t *testing.T) {
		info := probe(t, "cover.mp4")
		if len(info.Video) != 2 {
			t.Fatalf("video = %d tracks, want 2 (the video and its cover)", len(info.Video))
		}
		if info.Video[0].AttachedPicture || !info.Video[1].AttachedPicture {
			t.Errorf("attached picture flags = %v/%v, want false/true",
				info.Video[0].AttachedPicture, info.Video[1].AttachedPicture)
		}
	})
}

// TestRealProbeUnreadableFile keeps a text file with a media name out of the library with a typed
// error, not a crash. A file ffprobe can open but not parse is ErrNotMedia; one it cannot open is
// the file's own error, so a scanner does not quietly skip a permission problem.
func TestRealProbeUnreadableFile(t *testing.T) {
	requireFFmpeg(t)
	path := filepath.Join(t.TempDir(), "not-media.mp4")
	if err := os.WriteFile(path, []byte("this is not a media file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Probe(context.Background(), path); !errors.Is(err, ErrNotMedia) {
		t.Fatalf("error = %v, want ErrNotMedia", err)
	}

	if runtime.GOOS == "windows" {
		t.Skip("a chmod of 0 does not make a file unreadable to this user on Windows")
	}
	locked := filepath.Join(t.TempDir(), "locked.mp4")
	if err := os.WriteFile(locked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o600) })
	_, err := Probe(context.Background(), locked)
	if err == nil {
		t.Fatal("an unreadable file returned no error")
	}
	if errors.Is(err, ErrNotMedia) {
		t.Errorf("error = %v, want the permission error, not ErrNotMedia", err)
	}
}

// TestRealProbeAPathBeginningWithADash is the argument guard: a file named "-clip.mp4" must not be
// read as an option.
func TestRealProbeAPathBeginningWithADash(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	clip := filepath.Join(dir, "clip.mp4")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i",
		"testsrc2=size=160x120:rate=30", "-t", "1", clip).CombinedOutput(); err != nil {
		t.Fatalf("make the clip: %v\n%s", err, out)
	}
	dashed := filepath.Join(dir, "-clip.mp4")
	if err := os.Rename(clip, dashed); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	info, err := Probe(context.Background(), "-clip.mp4")
	if err != nil {
		t.Fatalf("probe a leading-dash path: %v", err)
	}
	if len(info.Video) != 1 || info.Video[0].Width != 160 {
		t.Errorf("video = %+v, want the 160-wide clip", info.Video)
	}
}

// TestRealRunSeesProgress runs one short encode through Run and checks that progress arrives with
// a media position and an output size on it (what the stall watch later reads: L-Q-6, L-SAFE-14).
func TestRealRunSeesProgress(t *testing.T) {
	requireFFmpeg(t)
	clips := generateClips(t)
	out := filepath.Join(t.TempDir(), "encoded.mkv")

	var mu sync.Mutex
	var blocks []Progress
	result, err := Run(context.Background(), []string{
		"ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", clips["h264-aac.mp4"],
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", out,
	}, func(p Progress) {
		mu.Lock()
		blocks = append(blocks, p)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("Run: %v (key lines: %q)", err, result.KeyLines)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", result.ExitCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(blocks) == 0 {
		t.Fatal("no progress was seen")
	}
	var sawPosition, sawSize bool
	for _, p := range blocks {
		if p.OutTime > 0 {
			sawPosition = true
		}
		if p.TotalSize > 0 {
			sawSize = true
		}
	}
	if !sawPosition || !sawSize {
		t.Errorf("progress = %+v, want at least one block with an out_time and one with a total_size", blocks)
	}
}

// TestRealCancelStopsALongEncode cancels a long encode mid-way: Run returns at once and the
// process is gone (L-Q-7). The process-tree proof for children is the Unix shell test; this one
// proves the real ffmpeg path.
func TestRealCancelStopsALongEncode(t *testing.T) {
	requireFFmpeg(t)
	out := filepath.Join(t.TempDir(), "long.mkv")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var once sync.Once
	started := time.Now()
	result, err := Run(ctx, []string{
		"ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30:duration=60",
		"-c:v", "libx264", "-preset", "veryslow", "-pix_fmt", "yuv420p", out,
	}, func(p Progress) {
		if p.OutTime > 0 {
			once.Do(cancel)
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if took := time.Since(started); took > 20*time.Second {
		t.Errorf("the cancel took %v, want a quick stop", took)
	}
	if result.PID == 0 {
		t.Fatal("no pid was reported")
	}
	if isProcessRunning(result.PID) {
		t.Errorf("process %d is still running after the cancel", result.PID)
	}
	if size1, ok := fileSize(out); ok {
		time.Sleep(300 * time.Millisecond)
		if size2, _ := fileSize(out); size2 != size1 {
			t.Errorf("the output grew after the cancel: %d -> %d bytes", size1, size2)
		}
	}
}

func fileSize(path string) (int64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return fi.Size(), true
}
