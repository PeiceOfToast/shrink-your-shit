// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"context"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// waitFirst calls Get until the walk and the probes are done.
func waitFirst(t *testing.T, f *FirstLister, root string, ignored func(string) bool) FirstFiles {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		r := f.Get(root, root, ignored)
		if !r.StillLooking {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("still looking: %+v", r)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The first 5 videos A to Z across folders, the total, the probed words, a file that cannot be read;
// hidden folders, .syp-trash, links, other files and the Ignore list left out (slice A).
func TestFirstFiles(t *testing.T) {
	dir := t.TempDir()
	for _, p := range []string{"b.mkv", "A/a1.mkv", "A/a2.mp4", "C/c.mkv", "D/d.avi", "E/e.mkv", "bad.mkv",
		".hidden/h.mkv", ".syp-trash/t.mkv", "notes.txt", "Extras/x.mkv"} {
		write(t, filepath.Join(dir, p), 1000)
	}
	os.Symlink(filepath.Join(dir, "b.mkv"), filepath.Join(dir, "0link.mkv"))
	f := &FirstLister{Exec: &probes{}, FFprobe: "ffprobe"}
	first := f.Get(dir, dir, func(rel string) bool { return rel == "Extras" })
	if !first.StillLooking {
		t.Log("finished before the first answer (fast disk)")
	}
	r := waitFirst(t, f, dir, func(rel string) bool { return rel == "Extras" })
	var names []string
	for _, it := range r.Items {
		names = append(names, it.Folder+"/"+it.Name)
	}
	if strings.Join(names, " ") != "A/a1.mkv A/a2.mp4 C/c.mkv D/d.avi E/e.mkv" || r.Total != 7 {
		t.Fatalf("items %v total %d", names, r.Total) // b.mkv and bad.mkv are 6th and 7th A to Z
	}
	it := r.Items[0]
	if it.State != "ready" || *it.Codec != "H.264" || *it.Resolution != "1080p" || it.DurationSec == nil || it.Bytes != 1000 {
		t.Errorf("a1: %+v", it)
	}
	// fewer than 5, one unreadable, a top-level file's folder is "."
	small := t.TempDir()
	write(t, filepath.Join(small, "bad.mkv"), 1000)
	write(t, filepath.Join(small, "ok.mkv"), 1000)
	r = waitFirst(t, f, small, nil)
	if len(r.Items) != 2 || r.Total != 2 || r.Items[0].State != "unreadable" || r.Items[0].Codec != nil || r.Items[1].Folder != "." {
		t.Errorf("small: %+v", r)
	}
	// none
	if r := waitFirst(t, f, t.TempDir(), nil); len(r.Items) != 0 || r.Total != 0 {
		t.Errorf("empty: %+v", r)
	}
}

func TestWords(t *testing.T) {
	for in, want := range map[string]string{"h264": "H.264", "hevc": "HEVC", "mpeg2video": "MPEG-2", "wmv3": "WMV", "theora": "THEORA"} {
		if got := CodecWord(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
	for _, c := range []struct {
		w, h int
		want string
	}{{1920, 1080, "1080p"}, {1920, 1040, "1080p"}, {3840, 2160, "4K"}, {1280, 720, "720p"}, {720, 576, "576p"}, {640, 360, "640×360"}} {
		if got := ResolutionWord(c.w, c.h); got != c.want {
			t.Errorf("%dx%d: %s", c.w, c.h, got)
		}
	}
}

// A link leading outside the folder is neither listed nor probed: its target is never read (review
// 34c56df). A shared file (more names than one) is listed and probed.
func TestFirstFilesNeverFollowAnOutsideLink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(outside, "elsewhere.mkv"), 1000)
	if err := os.Symlink(filepath.Join(outside, "elsewhere.mkv"), filepath.Join(dir, "a-link.mkv")); err != nil {
		t.Skip(err)
	}
	write(t, filepath.Join(dir, "b.mkv"), 1000)
	if err := os.Link(filepath.Join(dir, "b.mkv"), filepath.Join(outside, "b-other-name.mkv")); err != nil {
		t.Skip(err)
	}
	var probed []string
	x := &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
		probed = append(probed, args[len(args)-1])
		b, err := (&probes{}).Output(context.Background(), name, args...)
		return string(b), "", err
	}}
	r := waitFirst(t, &FirstLister{Exec: x, FFprobe: "ffprobe"}, dir, nil)
	if len(r.Items) != 1 || r.Items[0].Name != "b.mkv" || r.Total != 1 {
		t.Errorf("items %+v", r)
	}
	for _, p := range probed {
		if strings.Contains(p, "link") || strings.Contains(p, "elsewhere") {
			t.Errorf("probed through the link: %s", p)
		}
	}
	if len(probed) != 1 {
		t.Errorf("probed %v, want only b.mkv", probed)
	}
}
