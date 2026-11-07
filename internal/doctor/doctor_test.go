// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package doctor

import (
	"context"
	"errors"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
)

// fakeFFmpeg answers like a working ffmpeg with libx265 and ssim; hw lists hardware encoders, and
// working says which of them encode.
func fakeFFmpeg(x265, ssim bool, hw []string, working map[string]bool) *media.FakeExec {
	return &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		a := strings.Join(args, " ")
		switch {
		case strings.Contains(a, "-version"):
			return name + " version 7.1.5-0+deb13u1 Copyright", "", nil
		case strings.Contains(a, "-encoders"):
			out := " V....D libx264              H.264\n"
			if x265 {
				out += " V....D libx265              H.265 / HEVC\n"
			}
			for _, h := range hw {
				out += " V....D " + h + "           hardware HEVC\n"
			}
			return out, "", nil
		case strings.Contains(a, "-filters"):
			if ssim {
				return " ... ssim              VV->V      Calculate the SSIM.\n", "", nil
			}
			return " ... psnr              VV->V      Calculate the PSNR.\n", "", nil
		case strings.Contains(a, "-c:v"):
			for h, ok := range working {
				if strings.Contains(a, "-c:v "+h) && ok {
					return "", "", nil
				}
			}
			return "", "", errors.New("no device")
		}
		return "", "", errors.New("unexpected " + a)
	}}
}

func checker(free uint64) *Checker {
	return &Checker{Exec: fakeFFmpeg(true, true, nil, nil), FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe",
		FreeSpace: func(string) (uint64, error) { return free, nil }}
}

func codes(is []Issue) []string {
	var c []string
	for _, i := range is {
		c = append(c, i.Code)
	}
	return c
}

func videosIn(t *testing.T, sizes ...int) string {
	dir := t.TempDir()
	for i, n := range sizes {
		os.WriteFile(filepath.Join(dir, "movie"+string(rune('a'+i))+".mkv"), make([]byte, n), 0o644)
	}
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a video"), 0o644)
	os.MkdirAll(filepath.Join(dir, "@eaDir"), 0o755)
	os.WriteFile(filepath.Join(dir, "@eaDir", "thumb.mkv"), []byte("skipped like the scanner does"), 0o644)
	return dir
}

func TestAGoodFolder(t *testing.T) {
	dir := videosIn(t, 1000, 3000)
	r := checker(1<<40).Folder(context.Background(), dir)
	if !r.OK || len(r.Blockers) != 0 || r.VideoCount != 2 || r.TotalBytes != 4000 {
		t.Fatalf("%+v", r)
	}
	if strings.Join(codes(r.Notes), ",") != "no_hw_encoder" || r.Notes[0].Message != "Not found. That is fine." {
		t.Errorf("notes %+v", r.Notes)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".syp-write-check-*")); len(left) != 0 {
		t.Errorf("the write test left %v", left)
	}
}

func TestMissingFolderIsUnreadable(t *testing.T) {
	r := checker(1<<40).Folder(context.Background(), filepath.Join(t.TempDir(), "nope"))
	if r.OK || strings.Join(codes(r.Blockers), ",") != "unreadable" || !strings.Contains(r.Blockers[0].Message, "We can't open this folder") {
		t.Errorf("%+v", r)
	}
}

func TestReadOnlyFolderSaysHowToFixIt(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot write (POSIX, not root)")
	}
	dir := videosIn(t, 1000)
	os.Chmod(dir, 0o555)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	r := checker(1<<40).Folder(context.Background(), dir)
	if r.OK || !strings.Contains(strings.Join(codes(r.Blockers), ","), "read_only") {
		t.Fatalf("%+v", r)
	}
	msg := r.Blockers[0].Message
	if runtime.GOOS == "linux" && !inContainer() && (!strings.Contains(msg, "sudo chown -R") || !strings.Contains(msg, dir)) {
		t.Errorf("Linux needs the exact fix: %q", msg)
	}
	if !strings.HasPrefix(msg, "This folder is read-only. Make it writable, then press Check again. ") {
		t.Errorf("%q", msg)
	}
}

func TestReadOnlyMessageForTheService(t *testing.T) {
	// the exact words the brief asks for, for the Linux service user
	m := ReadOnlyMessage("/srv/videos")
	if runtime.GOOS == "linux" && !inContainer() {
		if !strings.Contains(m, "sudo chown -R") || !strings.Contains(m, "/srv/videos") {
			t.Errorf("%q", m)
		}
	}
}

func TestEmptyFolderHasNoVideos(t *testing.T) {
	r := checker(1<<40).Folder(context.Background(), t.TempDir())
	if r.OK || strings.Join(codes(r.Blockers), ",") != "no_videos" || r.Blockers[0].Message != "We found no videos here. Pick the folder that holds them." {
		t.Errorf("%+v", r)
	}
}

// The same rule as the worker's disk check (worker.SpaceNeeded), for the largest video.
func TestLowSpaceSaysHowMuch(t *testing.T) {
	dir := videosIn(t, 1000)
	r := checker(10<<20).Folder(context.Background(), dir) // 10 MB free; needs about 64 MB
	if r.OK || !strings.Contains(strings.Join(codes(r.Blockers), ","), "low_space") {
		t.Fatalf("%+v", r)
	}
	m := r.Blockers[0].Message
	if !strings.HasPrefix(m, "Only 0.0 GB free on this disk. Free up space (about 0.1 GB is enough to start)") {
		t.Errorf("%q", m)
	}
}

func TestHardlinkedFilesAreANote(t *testing.T) {
	dir := videosIn(t, 1000)
	if err := os.Link(filepath.Join(dir, "moviea.mkv"), filepath.Join(dir, "copy.mkv")); err != nil {
		t.Fatal(err)
	}
	r := checker(1<<40).Folder(context.Background(), dir)
	if !r.OK || !strings.Contains(strings.Join(codes(r.Notes), ","), "hardlinks") {
		t.Fatalf("%+v", r)
	}
	want := "2 files are shared with another folder (for example by Radarr). We will leave them alone, because changing one would break the link."
	if n := r.Notes[0]; n.Message != want {
		t.Errorf("%q", n.Message)
	}
	os.Remove(filepath.Join(dir, "copy.mkv"))
	os.Link(filepath.Join(dir, "moviea.mkv"), filepath.Join(t.TempDir(), "elsewhere.mkv")) // shared with another folder
	if n := checker(1<<40).Folder(context.Background(), dir).Notes[0]; !strings.HasPrefix(n.Message, "1 file is shared with another folder") || !strings.Contains(n.Message, "We will leave it alone, because changing it") {
		t.Errorf("singular: %q", n.Message)
	}
}

func TestNoConverter(t *testing.T) {
	dir := videosIn(t, 1000)
	for name, c := range map[string]*Checker{
		"no ffmpeg":  {Exec: fakeFFmpeg(true, true, nil, nil), FFprobe: "/usr/bin/ffprobe"},
		"no ffprobe": {Exec: fakeFFmpeg(true, true, nil, nil), FFmpeg: "/usr/bin/ffmpeg"},
		"no libx265": {Exec: fakeFFmpeg(false, true, nil, nil), FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe"},
		"no ssim":    {Exec: fakeFFmpeg(true, false, nil, nil), FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe"},
	} {
		c.FreeSpace = func(string) (uint64, error) { return 1 << 40, nil }
		r := c.Folder(context.Background(), dir)
		if r.OK || !strings.Contains(strings.Join(codes(r.Blockers), ","), "no_converter") {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

// Hardware encoders are reported only if they really encode here; listed is not enough.
func TestHardwareIsTestedNotTrusted(t *testing.T) {
	c := checker(1 << 40)
	c.Exec = fakeFFmpeg(true, true, []string{"hevc_nvenc", "hevc_vaapi"}, map[string]bool{"hevc_nvenc": true})
	tools := c.Tools(context.Background())
	if strings.Join(tools.Hardware, ",") != "hevc_nvenc" || strings.Join(tools.HardwareListed, ",") != "hevc_vaapi" {
		t.Errorf("%+v", tools)
	}
	r := c.Folder(context.Background(), videosIn(t, 1000))
	for _, n := range r.Notes {
		if n.Code == "no_hw_encoder" {
			t.Error("a working hardware encoder was found, so no 'not found' note")
		}
	}
	for _, ch := range c.Health(context.Background(), t.TempDir(), nil) {
		if ch.ID == "hardware" && (ch.Status != "ok" || !strings.Contains(ch.Detail, "Not used yet: converting uses the processor")) {
			t.Errorf("never claim hardware use: %+v", ch)
		}
	}
}

func TestHealth(t *testing.T) {
	c := checker(1 << 40)
	good := videosIn(t, 1000)
	checks := c.Health(context.Background(), t.TempDir(), []Library{{1, "Movies", good}, {2, "Gone", filepath.Join(t.TempDir(), "missing")}})
	got := map[string]Check{}
	for _, ch := range checks {
		got[ch.ID] = ch
	}
	for _, id := range []string{"ffmpeg", "ffprobe", "x265", "ssim", "hardware", "data", "library:1"} {
		if got[id].Status != "ok" {
			t.Errorf("%s: %+v", id, got[id])
		}
	}
	if !strings.Contains(got["ffmpeg"].Detail, "7.1.5") || got["library:2"].Status != "bad" || !strings.Contains(got["library:2"].Detail, "can't open") {
		t.Errorf("%+v %+v", got["ffmpeg"], got["library:2"])
	}
	if (Health{Checks: checks}).Bad() != true {
		t.Error("a bad library makes the report bad")
	}
	if len(c.Health(context.Background(), t.TempDir(), nil)) == 0 {
		t.Error("no libraries is a warning, not an empty report")
	}
}

func TestLogTail(t *testing.T) {
	var tail LogTail
	for i := 0; i < 60; i++ {
		tail.Write([]byte("line " + string(rune('A'+i%26)) + "\n"))
	}
	tail.Write([]byte("half"))
	if l := tail.Lines(); len(l) != 50 || l[49] != "line H" {
		t.Errorf("%d lines, last %q", len(l), l[len(l)-1])
	}
	var none *LogTail
	if none.Lines() == nil {
		t.Error("an empty tail is an empty list, not null")
	}
}

// Every message first-run shows is in plain words (DESIGN/ACCEPTANCE.md COPY-1).
func TestFolderMessagesUsePlainWords(t *testing.T) {
	banned := []string{"node", "runner", "plugin", "formula", "preset", "transcode", "ffmpeg", "ffprobe", "encoder", "libx265", "ssim", "hardlink", "CRF"}
	dirs := map[string]string{"good": videosIn(t, 1000), "empty": t.TempDir(), "missing": filepath.Join(t.TempDir(), "nope")}
	links := videosIn(t, 1000)
	os.Link(filepath.Join(links, "moviea.mkv"), filepath.Join(links, "b.mkv"))
	dirs["links"] = links
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := videosIn(t, 1000)
		p := filepath.Join(locked, "private")
		os.Mkdir(p, 0o755)
		os.Chmod(p, 0)
		t.Cleanup(func() { os.Chmod(p, 0o755) })
		dirs["locked"] = locked
	}
	broken := &Checker{Exec: fakeFFmpeg(false, false, nil, nil), FreeSpace: func(string) (uint64, error) { return 1, nil }}
	unknown := checker(0)
	unknown.FreeSpace = func(string) (uint64, error) { return 0, errors.New("statfs failed") }
	for name, d := range dirs {
		for _, c := range []*Checker{checker(1 << 40), checker(1), broken, unknown} {
			r := c.Folder(context.Background(), d)
			for _, i := range append(r.Blockers, r.Notes...) {
				low := strings.ToLower(i.Message)
				for _, w := range banned {
					if strings.Contains(low, strings.ToLower(w)) {
						t.Errorf("%s/%s: %q uses %q", name, i.Code, i.Message, w)
					}
				}
			}
		}
	}
}

// A folder chosen through a link is checked as the folder it points to (WalkDir does not follow a
// link at the top), so its videos are found.
func TestLinkedFolderIsChecked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making links needs extra rights on Windows")
	}
	real := videosIn(t, 1000, 2000)
	link := filepath.Join(t.TempDir(), "videos")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	r := checker(1<<40).Folder(context.Background(), link)
	if !r.OK || r.VideoCount != 2 || r.TotalBytes != 3000 {
		t.Errorf("%+v", r)
	}
}

// Not knowing the free space is said, not passed over.
func TestUnknownFreeSpaceIsANote(t *testing.T) {
	c := checker(0)
	c.FreeSpace = func(string) (uint64, error) { return 0, errors.New("statfs failed") }
	r := c.Folder(context.Background(), videosIn(t, 1000))
	if !strings.Contains(strings.Join(codes(r.Notes), ","), "space_unknown") || !strings.Contains(r.Notes[0].Message, "couldn't check how much space is free") {
		t.Errorf("%+v", r)
	}
}

// Folders inside that cannot be read are counted and said; the rest still counts.
func TestUnreadableFoldersInsideAreANote(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a folder this user cannot read (POSIX, not root)")
	}
	dir := videosIn(t, 1000)
	for _, sub := range []string{"private", "lost+found"} {
		p := filepath.Join(dir, sub)
		os.Mkdir(p, 0o755)
		os.WriteFile(filepath.Join(p, "hidden.mkv"), []byte("x"), 0o644)
		os.Chmod(p, 0)
		t.Cleanup(func() { os.Chmod(p, 0o755) })
	}
	r := checker(1<<40).Folder(context.Background(), dir)
	if !r.OK || r.VideoCount != 1 {
		t.Fatalf("%+v", r)
	}
	if n := r.Notes[0]; n.Code != "unreadable_folders" || n.Message != "2 folders inside couldn't be read; files in them won't be converted." {
		t.Errorf("%+v", r.Notes)
	}
}

// Every library note reaches the health report as its own warning line, also beside a blocker (only
// the hardware note is reported once, as its own check).
func TestHealthCarriesLibraryNotes(t *testing.T) {
	byID := func(checks []Check) map[string]Check {
		m := map[string]Check{}
		for _, ch := range checks {
			m[ch.ID] = ch
		}
		return m
	}
	c := checker(0)
	c.FreeSpace = func(string) (uint64, error) { return 0, errors.New("statfs failed") }
	got := byID(c.Health(context.Background(), t.TempDir(), []Library{{1, "Movies", videosIn(t, 1000)}}))
	if got["library:1"].Status != "warn" || got["library:1:space_unknown"].Status != "warn" ||
		!strings.Contains(got["library:1:space_unknown"].Detail, "couldn't check how much space is free") || got["library:1:no_hw_encoder"].ID != "" {
		t.Errorf("%+v", got)
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return
	}
	// the reviewer's combination: low space (a blocker), a folder inside that cannot be read, a hardlink
	dir := videosIn(t, 1000)
	p := filepath.Join(dir, "private")
	os.Mkdir(p, 0o755)
	os.Chmod(p, 0)
	t.Cleanup(func() { os.Chmod(p, 0o755) })
	os.Link(filepath.Join(dir, "moviea.mkv"), filepath.Join(dir, "linked.mkv"))
	checks := checker(1).Health(context.Background(), t.TempDir(), []Library{{1, "Movies", dir}})
	got = byID(checks)
	if lib := got["library:1"]; lib.Status != "bad" || !strings.Contains(lib.Detail, "free on this disk") {
		t.Errorf("the blocker: %+v", lib)
	}
	if n := got["library:1:unreadable_folders"]; n.Status != "warn" || n.Detail != "1 folder inside couldn't be read; files in it won't be converted." {
		t.Errorf("the unreadable note: %+v", n)
	}
	if n := got["library:1:hardlinks"]; n.Status != "warn" || !strings.Contains(n.Detail, "shared with another folder") {
		t.Errorf("the hardlinks note: %+v", n)
	}
	if !(Health{Checks: checks}).Bad() {
		t.Error("the blocker makes the report bad")
	}
}

// A system folder is a blocker with a plain message (slice 23): the doctor says so before anything else.
func TestFolderRefusesSystemFolders(t *testing.T) {
	r := checker(1<<40).Folder(context.Background(), jobs.SystemDriveRootForTest())
	if r.OK || len(r.Blockers) != 1 || r.Blockers[0].Code != "system_folder" || !strings.Contains(r.Blockers[0].Message, "Pick the folder that holds your videos") {
		t.Errorf("%+v", r)
	}
}

// Originals in .syp-trash that the database does not know (kept by an earlier setup) are a note per
// folder with design's words, count and size; known ones are not; nothing is touched.
func TestOrphanedOriginalsAreANote(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("video"), 0o644)
	trash := filepath.Join(dir, ".syp-trash")
	os.MkdirAll(trash, 0o755)
	os.WriteFile(filepath.Join(trash, "old.1.mkv"), make([]byte, 3<<20), 0o644)
	os.WriteFile(filepath.Join(trash, "old.2.mkv"), make([]byte, 1<<20), 0o644)
	os.WriteFile(filepath.Join(trash, "known.3.mkv"), []byte("k"), 0o644)
	sub := filepath.Join(dir, "Shows", ".syp-trash")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "ep.4.mkv"), make([]byte, 1<<20), 0o644)
	real, _ := filepath.EvalSymlinks(dir)
	c := checker(1 << 40)
	c.KnownOriginals = func() (map[string]bool, error) {
		return map[string]bool{filepath.Join(real, ".syp-trash", "known.3.mkv"): true}, nil
	}
	r := c.Folder(context.Background(), dir)
	var got []string
	for _, n := range r.Notes {
		if n.Code == "orphaned_originals" {
			got = append(got, n.Message)
		}
	}
	want := []string{
		"2 original files (4.0 MB) from an earlier setup are still in the hidden folder " + filepath.Join(real, ".syp-trash") + ". They are not listed under Kept originals and will not be deleted automatically. Move them back or delete them yourself; we won't touch them.",
		"1 original file (1.0 MB) from an earlier setup is still in the hidden folder " + filepath.Join(real, "Shows", ".syp-trash") + ". It is not listed under Kept originals and will not be deleted automatically. Move it back or delete it yourself; we won't touch it.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !r.OK {
		t.Error("a note, not a blocker")
	}
	if _, err := os.Stat(filepath.Join(trash, "old.1.mkv")); err != nil {
		t.Error("nothing may be touched")
	}
	// everything known: no note; no database at hand: no check
	c.KnownOriginals = func() (map[string]bool, error) {
		return map[string]bool{filepath.Join(real, ".syp-trash", "known.3.mkv"): true, filepath.Join(real, ".syp-trash", "old.1.mkv"): true,
			filepath.Join(real, ".syp-trash", "old.2.mkv"): true, filepath.Join(real, "Shows", ".syp-trash", "ep.4.mkv"): true}, nil
	}
	for _, n := range c.Folder(context.Background(), dir).Notes {
		if n.Code == "orphaned_originals" {
			t.Errorf("all known: %s", n.Message)
		}
	}
	c.KnownOriginals = nil
	for _, n := range c.Folder(context.Background(), dir).Notes {
		if n.Code == "orphaned_originals" {
			t.Errorf("no database: %s", n.Message)
		}
	}
}

// A known original recorded through a linked folder (as on macOS, where /var is /private/var) is the same
// file the walk finds through the real folder: no note (review 8fd5cd5).
func TestKnownOriginalThroughALinkIsNotOrphaned(t *testing.T) {
	real := t.TempDir()
	trash := filepath.Join(real, ".syp-trash")
	os.MkdirAll(trash, 0o755)
	os.WriteFile(filepath.Join(trash, "movie.1.mkv"), []byte("orig"), 0o644)
	alias := filepath.Join(t.TempDir(), "lib-link")
	if err := os.Symlink(real, alias); err != nil {
		t.Skip(err)
	}
	c := checker(1 << 40)
	c.KnownOriginals = func() (map[string]bool, error) {
		return map[string]bool{filepath.Join(alias, ".syp-trash", "movie.1.mkv"): true}, nil // recorded via the link
	}
	for _, n := range c.Folder(context.Background(), real).Notes {
		if n.Code == "orphaned_originals" {
			t.Errorf("a known original was reported: %s", n.Message)
		}
	}
}

// The Workers page's graphics card (S2): found when a hardware encoder works here, with its vendor when
// the encoder names one.
func TestGPU(t *testing.T) {
	for _, c := range []struct {
		hw     []string
		found  bool
		vendor string
	}{{nil, false, ""}, {[]string{"hevc_nvenc"}, true, "NVIDIA"}, {[]string{"hevc_vaapi", "hevc_qsv"}, true, "Intel"},
		{[]string{"hevc_amf"}, true, "AMD"}, {[]string{"hevc_vaapi"}, true, ""}} {
		if f, v := GPU(Tools{Hardware: c.hw}); f != c.found || v != c.vendor {
			t.Errorf("%v: %v %q", c.hw, f, v)
		}
	}
}

// The hardware test's encodes never hold up the converter check (startup, the health page) or a
// Workers answer: a refresh running in the background with a slow encode leaves Converter and ToolsNow
// answering at once (review 0605262).
func TestHardwareTestDoesNotHoldUpTheConverterCheck(t *testing.T) {
	x := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		for i, a := range args {
			if a == "-c:v" && i+1 < len(args) && args[i+1] == "hevc_nvenc" {
				time.Sleep(time.Second) // the hardware test encode
			}
		}
		return "ffmpeg version 7.1\n V..... libx265  x\n V..... hevc_nvenc  x\n", "", nil
	}}
	c := &Checker{Exec: x, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe"}
	c.Converter(context.Background()) // as startup does first
	c.toolsAt = time.Time{}           // stale, so the next one checks again
	c.ToolsNow()                      // the background refresh: its hardware encode takes a second
	time.Sleep(50 * time.Millisecond) // let it reach the encode
	start := time.Now()
	c.Converter(context.Background())
	c.ToolsNow()
	if took := time.Since(start); took > 300*time.Millisecond {
		t.Errorf("the converter check waited %s for the hardware test", took)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if tl, ok := c.ToolsNow(); ok && len(tl.Hardware) == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the background check never stored its result")
}
