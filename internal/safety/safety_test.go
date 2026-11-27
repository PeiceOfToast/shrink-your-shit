// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
)

// stubProber answers probes from a fixed Info, counts calls, and can hang until its context ends.
type stubProber struct {
	info  media.Info
	err   error
	hang  bool
	calls int
}

func (p *stubProber) Probe(ctx context.Context, path string) (media.Info, error) {
	p.calls++
	if p.hang {
		<-ctx.Done()
		return media.Info{}, ctx.Err()
	}
	return p.info, p.err
}

// goodInfo is an output with one video, two audio tracks (eng, jpn) and one subtitle (eng).
func goodInfo(d time.Duration) media.Info {
	return media.Info{
		Format:    media.Format{Duration: d},
		Video:     []media.VideoTrack{{Codec: "h264"}},
		Audio:     []media.AudioTrack{{Language: "eng"}, {Language: "jpn"}},
		Subtitles: []media.SubtitleTrack{{Language: "eng"}},
	}
}

var wantAll = Want{AudioLanguages: []string{"eng", "jpn"}, SubtitleLanguages: []string{"eng"}}

// stagedWith stages a file in a temp dir and writes data into it.
func stagedWith(t *testing.T, data string) (Staged, string) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "My Film. 2024.mkv")
	st, err := Stage(dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return st, dst
}

func TestStageIsPrivateAndInvisibleAtTheFinalName(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "a b.c.mkv")
	st, err := Stage(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(st.Folder), ".a b.c.") || !strings.HasSuffix(st.Folder, ".syp-tmp") {
		t.Fatalf("folder name %q is not .<stem>.<random>.syp-tmp", filepath.Base(st.Folder))
	}
	fi, err := os.Stat(st.Folder)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("folder mode = %v, %v; want a 0700 directory", fi.Mode(), err)
	}
	ff, err := os.Stat(st.Path)
	if err != nil || ff.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v; want 0600", ff.Mode(), err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("nothing may appear at the final name before Place: %v", err)
	}
	st2, err := Stage(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Folder == st.Folder {
		t.Fatal("two stagings got the same folder: the names are not unpredictable")
	}
}

func TestVerifyTooShortStopsBeforeStreams(t *testing.T) {
	st, _ := stagedWith(t, "x")
	src := goodInfo(100 * time.Second)
	p := &stubProber{info: goodInfo(90 * time.Second)}
	v, err := Verify(context.Background(), p, Recorder{}, st, src, wantAll, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if v.OK || v.Code != CodeTooShort || v.NextAction != NextKeepOriginal {
		t.Fatalf("verdict = %+v; want too_short with keep_original", v)
	}
	if p.calls != 1 {
		t.Fatalf("probe calls = %d; want 1 (the later checks must not run)", p.calls)
	}
}

func TestVerifyTooLong(t *testing.T) {
	st, _ := stagedWith(t, "x")
	v, err := Verify(context.Background(), &stubProber{info: goodInfo(105 * time.Second)}, Recorder{}, st, goodInfo(100*time.Second), wantAll, time.Minute)
	if err != nil || v.Code != CodeTooLong {
		t.Fatalf("verdict = %+v, %v; want too_long", v, err)
	}
}

func TestVerifyDurationToleranceIsTheLargerOfOneSecondAndHalfAPercent(t *testing.T) {
	st, _ := stagedWith(t, "x")
	// 0.5 % of 4000 s is 20 s, so a 15 s gap passes on a long film, while 1.5 s fails on a 100 s one.
	v, _ := Verify(context.Background(), &stubProber{info: goodInfo(4015 * time.Second)}, Recorder{}, st, goodInfo(4000*time.Second), wantAll, time.Minute)
	if !v.OK {
		t.Fatalf("15 s off a 4000 s film: %+v; want OK", v)
	}
	v, _ = Verify(context.Background(), &stubProber{info: goodInfo(101500 * time.Millisecond)}, Recorder{}, st, goodInfo(100*time.Second), wantAll, time.Minute)
	if v.Code != CodeTooLong {
		t.Fatalf("1.5 s off a 100 s film: %+v; want too_long (floor is 1 s, 0.5 %% is 0.5 s)", v)
	}
}

func TestVerifyStreamChecks(t *testing.T) {
	cases := map[string]struct {
		mut  func(*media.Info)
		code string
	}{
		"video missing":    {func(i *media.Info) { i.Video = nil }, CodeVideoMissing},
		"audio missing":    {func(i *media.Info) { i.Audio = i.Audio[:1] }, CodeAudioMissing},
		"wrong language":   {func(i *media.Info) { i.Audio[1].Language = "fra" }, CodeAudioLanguage},
		"subtitle missing": {func(i *media.Info) { i.Subtitles = nil }, CodeSubtitleMissing},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			st, _ := stagedWith(t, "x")
			info := goodInfo(100 * time.Second)
			c.mut(&info)
			v, err := Verify(context.Background(), &stubProber{info: info}, Recorder{}, st, goodInfo(100*time.Second), wantAll, time.Minute)
			if err != nil || v.Code != c.code || v.OK {
				t.Fatalf("verdict = %+v, %v; want %s", v, err, c.code)
			}
		})
	}
}

func TestVerifyUnreadableFileIsANoAndAHangIsAnError(t *testing.T) {
	st, _ := stagedWith(t, "x")
	v, err := Verify(context.Background(), &stubProber{err: media.ErrNotMedia}, Recorder{}, st, goodInfo(time.Second), wantAll, time.Minute)
	if err != nil || v.Code != CodeIncomplete {
		t.Fatalf("not-media file: %+v, %v; want a check.incomplete no", v, err)
	}
	_, err = Verify(context.Background(), &stubProber{hang: true}, Recorder{}, st, goodInfo(time.Second), wantAll, 20*time.Millisecond)
	if err == nil {
		t.Fatal("a probe that hangs past the timeout must be an error, not a verdict")
	}
	_, err = Verify(context.Background(), &stubProber{err: media.ErrFFprobeMissing}, Recorder{}, st, goodInfo(time.Second), wantAll, time.Minute)
	if err == nil {
		t.Fatal("ffprobe missing is a check that could not run: an error")
	}
}

func TestPlaceNeverOverwritesAndNamesTheCopy(t *testing.T) {
	st, dst := stagedWith(t, "new")
	if err := os.WriteFile(dst, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	used, err := Place(context.Background(), st, dst)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(used) != "My Film. 2024 (2).mkv" {
		t.Fatalf("used %q; want the (2) name", used)
	}
	if got, _ := os.ReadFile(dst); string(got) != "theirs" {
		t.Fatalf("existing file was overwritten: %q", got)
	}
	if got, _ := os.ReadFile(used); string(got) != "new" {
		t.Fatalf("placed file = %q", got)
	}
	if _, err := os.Stat(st.Folder); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging folder should be gone after Place: %v", err)
	}
}

func TestPlaceCancelStopsTheFreeNameLoop(t *testing.T) {
	st, dst := stagedWith(t, "new")
	if err := os.WriteFile(dst, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Place(ctx, st, dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
}

func TestPlaceRefusesAChangedStagedFile(t *testing.T) {
	st, dst := stagedWith(t, "new")
	// Move the original aside first: it stays alive, so the replacement cannot reuse its inode.
	if err := os.Rename(st.Path, st.Path+".aside"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.Path, []byte("swapped"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Place(context.Background(), st, dst); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("err = %v; want ErrIdentityChanged", err)
	}
	if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a refused placement must not create the final name")
	}
}

func TestPlaceIgnoresAnFsyncThatSaysEINVAL(t *testing.T) {
	old := syncFile
	syncFile = func(*os.File) error { return syscall.EINVAL }
	t.Cleanup(func() { syncFile = old })
	st, dst := stagedWith(t, "new")
	if _, err := Place(context.Background(), st, dst); err != nil {
		t.Fatalf("EINVAL from fsync must be ignored: %v", err)
	}
}

func TestPlaceHandlesPathsWithSpacesAndDots(t *testing.T) {
	st, dst := stagedWith(t, "ok")
	used, err := Place(context.Background(), st, dst)
	if err != nil || used != dst {
		t.Fatalf("used %q, %v", used, err)
	}
}

func TestSweepRemovesOnlyOurFolders(t *testing.T) {
	dir := t.TempDir()
	ours := filepath.Join(dir, ".film.abc.syp-tmp")
	mustMkdir(t, ours)
	mustWrite(t, filepath.Join(ours, "out.mkv"))
	mustWrite(t, filepath.Join(ours, "sub.1.srt"))

	stranger := filepath.Join(dir, ".other.def.syp-tmp")
	mustMkdir(t, stranger)
	mustWrite(t, filepath.Join(stranger, "out.mkv"))
	mustWrite(t, filepath.Join(stranger, "notes.txt"))

	rep, err := Sweep(context.Background(), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ours); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("our folder should be removed: %v", err)
	}
	if _, err := os.Stat(stranger); err != nil {
		t.Fatalf("a folder with a stranger's file must be left: %v", err)
	}
	if len(rep.Removed) != 1 || len(rep.Left) != 1 {
		t.Fatalf("report = %+v", rep)
	}
}

func TestSweepDoesNotFollowASymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "keep.mkv")
	mustWrite(t, target)
	link := filepath.Join(dir, ".film.x.syp-tmp")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Sweep(context.Background(), []string{dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the link's target must survive: %v", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the link itself should be removed")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}
