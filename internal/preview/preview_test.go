// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package preview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/scanner"
)

// probes answers ffprobe by file name: the codec, bitrate and size it describes. It counts calls and is
// safe for the Preview's parallel probes.
type probes struct {
	mu    sync.Mutex
	calls int
	gate  chan struct{} // if set, every probe waits for it
}

func (p *probes) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	path := args[len(args)-1]
	base := filepath.Base(path)
	codec, kbps := "h264", 8000 // 1080p24 at 8 Mbps: 0.16 bits per pixel, worth converting at every position
	subs := ""
	switch {
	case strings.HasPrefix(base, "subs"): // a text, a picture and a TTML subtitle
		subs = `,{"index":3,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}},
 {"index":4,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle","tags":{"language":"ger"}},
 {"index":5,"codec_type":"subtitle","codec_name":"ttml","tags":{"language":"fre"}}`
	case strings.HasPrefix(base, "textsubs"): // a text subtitle only
		subs = `,{"index":3,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}`
	case strings.HasPrefix(base, "hevc"):
		codec = "hevc"
	case strings.HasPrefix(base, "bad"):
		return nil, errors.New("invalid data found when processing input")
	case strings.HasPrefix(base, "lean"):
		kbps = 1500 // 0.03 bits per pixel: already efficient
	}
	st, _ := os.Stat(path)
	dur := float64(st.Size()) * 8 / float64(kbps*1000)
	return fmt.Appendf(nil, `{"streams":[
 {"index":0,"codec_type":"video","codec_name":%q,"width":1920,"height":1080,"avg_frame_rate":"24/1","r_frame_rate":"24/1","bit_rate":"%d"},
 {"index":1,"codec_type":"audio","codec_name":"aac"},{"index":2,"codec_type":"audio","codec_name":"ac3"}%s],
 "format":{"format_name":"matroska,webm","duration":"%f","size":"%d","bit_rate":"%d"}}`, codec, kbps*1000, subs, dur, st.Size(), kbps*1000), nil
}

func (p *probes) Stream(context.Context, func(string), string, ...string) (string, error) {
	return "", errors.New("the Preview must never run ffmpeg")
}

func (p *probes) Capture(context.Context, string, ...string) ([]byte, []byte, error) {
	return nil, nil, errors.New("the Preview must never run ffmpeg")
}

func (p *probes) n() int { p.mu.Lock(); defer p.mu.Unlock(); return p.calls }

func store(t *testing.T) *jobs.Store {
	s, err := jobs.Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetPaused(false) // the test's worker is started (S2: a new install starts stopped)
	t.Cleanup(func() { s.Close() })
	return s
}

func write(t *testing.T, path string, size int) {
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// library: two convertible files, one HEVC, one already efficient, one unreadable, one shared with
// another folder, plus things the scanner never looks at.
func library(t *testing.T) string {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.mkv"), 1_000_000)
	write(t, filepath.Join(dir, "sub", "b.mp4"), 2_000_000)
	write(t, filepath.Join(dir, "hevc.mkv"), 500_000)
	write(t, filepath.Join(dir, "lean.mkv"), 300_000)
	write(t, filepath.Join(dir, "bad.mkv"), 100_000)
	write(t, filepath.Join(dir, "notes.txt"), 10)
	write(t, filepath.Join(dir, ".hidden", "x.mkv"), 10)
	other := t.TempDir()
	write(t, filepath.Join(other, "shared.mkv"), 4_000_000)
	if err := os.Link(filepath.Join(other, "shared.mkv"), filepath.Join(dir, "shared.mkv")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPreviewCountsAndEstimates(t *testing.T) {
	x := &probes{}
	p := &Previewer{Store: store(t), Exec: x, FFprobe: "ffprobe"}
	dir := library(t)
	r := p.Library(1, dir, rules.SliderTemplate(3), 10*time.Second)
	if r.Status != "ready" || r.Files != 6 || r.TotalBytes != 7_900_000 || r.CanShrink != 4 { // lean.mkv too (always standardize), hevc.mkv repackaged (S4)
		t.Fatalf("%+v", r)
	}
	// the shared file is skipped (slice 23's file guards), as the scanner skips it
	if fmt.Sprint(r.Skipped) != "map[shared_file:1 unreadable:1]" {
		t.Errorf("skipped %v", r.Skipped)
	}
	if r.SharedNoSaving != 0 {
		t.Errorf("shared %d: always 0 since the file guards", r.SharedNoSaving)
	}
	// a.mkv and b.mp4 are 3 MB of video at 0.16 bits per pixel; Balanced saves 10% to 55% of it. lean.mkv
	// (300 kB, near the old threshold) adds 0 to 55%: 165 000 at the high end.
	if r.EstimateBytesLow != 300_000 || r.EstimateBytesHigh != 1_815_000 {
		t.Errorf("estimate %d - %d", r.EstimateBytesLow, r.EstimateBytesHigh)
	}
	if r.EstimateBytesLow >= r.EstimateBytesHigh {
		t.Error("a range, never one figure")
	}
}

// Every position can be previewed; a second look at the same setting and files is the cached answer,
// and another setting reuses the probes.
func TestPreviewIsCached(t *testing.T) {
	x := &probes{}
	p := &Previewer{Store: store(t), Exec: x, FFprobe: "ffprobe"}
	dir := library(t)
	first := p.Library(1, dir, rules.SliderTemplate(3), 10*time.Second)
	n := x.n()
	if n != 6 { // 5 files, plus hevc.mkv's first frame (S3: HDR side data)
		t.Fatalf("%d probes for 6 videos (the shared one is never read)", n)
	}
	if again := p.Library(1, dir, rules.SliderTemplate(3), 0); fmt.Sprint(again) != fmt.Sprint(first) {
		t.Errorf("second preview %+v, first %+v", again, first)
	}
	var prev Result
	for q := 1; q <= 5; q++ {
		r := p.Library(1, dir, rules.SliderTemplate(q), 10*time.Second)
		if r.Status != "ready" {
			t.Fatalf("position %d: %+v", q, r)
		}
		if q > 1 && r.EstimateBytesHigh > prev.EstimateBytesHigh {
			t.Errorf("position %d saves more than %d: %d > %d", q, q-1, r.EstimateBytesHigh, prev.EstimateBytesHigh)
		}
		prev = r
	}
	if x.n() != n+4 { // only the unreadable file is tried again (once per new setting): a failed probe is not cached
		t.Errorf("%d more probes for other settings", x.n()-n)
	}
}

// A long preview answers "computing", then the result; when files change, the old one is shown as stale
// until the new one is ready.
func TestPreviewComputingAndStale(t *testing.T) {
	x := &probes{gate: make(chan struct{})}
	p := &Previewer{Store: store(t), Exec: x, FFprobe: "ffprobe"}
	dir := library(t)
	if r := p.Library(1, dir, rules.SliderTemplate(3), 50*time.Millisecond); r.Status != "computing" || r.Stale || r.Progress == nil || r.Progress.Total != 6 || r.Progress.Done > 1 { // the shared file needs no probe
		t.Fatalf("%+v %+v", r, r.Progress)
	}
	close(x.gate)
	var r Result
	for i := 0; i < 100 && r.Status != "ready"; i++ {
		r = p.Library(1, dir, rules.SliderTemplate(3), 100*time.Millisecond)
	}
	if r.Status != "ready" || r.Files != 6 || r.Progress != nil {
		t.Fatalf("%+v", r)
	}
	x.gate = make(chan struct{})
	write(t, filepath.Join(dir, "new.mkv"), 1_000_000)
	if s := p.Library(1, dir, rules.SliderTemplate(3), 50*time.Millisecond); !s.Stale || s.Status != "computing" || s.Files != 6 {
		t.Errorf("after a change: %+v", s)
	}
	close(x.gate)
	for i := 0; i < 100 && (r.Files != 7 || r.Status != "ready"); i++ {
		r = p.Library(1, dir, rules.SliderTemplate(3), 100*time.Millisecond)
	}
	if r.Stale || r.Files != 7 {
		t.Errorf("%+v", r)
	}
}

func TestPreviewOfAMissingFolderFails(t *testing.T) {
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	if r := p.Library(1, filepath.Join(t.TempDir(), "gone"), rules.SliderTemplate(3), time.Second); r.Status != "failed" {
		t.Errorf("%+v", r)
	}
}

// The Preview reads only: no file in the library changes, and nothing is added.
func TestPreviewWritesNothing(t *testing.T) {
	dir := library(t)
	before := snapshot(t, dir)
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	p.Library(1, dir, rules.SliderTemplate(1), 10*time.Second)
	a, _ := os.Stat(filepath.Join(dir, "a.mkv"))
	p.Sample(context.Background(), dir, filepath.Join(dir, "a.mkv"), a, rules.SliderTemplate(5))
	if after := snapshot(t, dir); after != before {
		t.Errorf("the library changed:\n%s\n%s", before, after)
	}
}

func snapshot(t *testing.T, dir string) string {
	var b strings.Builder
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			fmt.Fprintf(&b, "%s %d %d %v\n", p, fi.Size(), fi.ModTime().UnixNano(), fi.Mode())
		}
		return nil
	})
	return b.String()
}

func TestSaving(t *testing.T) {
	// 100 s of 1080p24: video at kbps, plus 1 MB of audio and container that is never counted
	info := func(kbps int) media.Info {
		return media.Info{DurationSec: 100, VideoCodec: "h264", BitrateKbps: kbps, Streams: []media.Stream{
			{Type: "video", Codec: "h264", Width: 1920, Height: 1080, FrameRate: 24, BitrateKbps: kbps}}}
	}
	size := func(kbps int) int64 { return int64(kbps)*1000/8*100 + 1_000_000 }
	save := func(t rules.Template, kbps int) (int64, int64) {
		i := info(kbps)
		return Saving(t, t.Compile(i), i, size(kbps))
	}
	video := int64(12000) * 1000 / 8 * 100 // 0.24 bits per pixel: above 1.5x every threshold
	for _, c := range []struct{ q, lo, hi int }{{1, 15, 60}, {3, 10, 55}, {5, 5, 25}} {
		lo, hi := save(rules.SliderTemplate(c.q), 12000)
		if lo != video*int64(c.lo)/100 || hi != video*int64(c.hi)/100 {
			t.Errorf("position %d: %d - %d, want %d%% - %d%% of the video", c.q, lo, hi, c.lo, c.hi)
		}
	}
	// within 1.5x of Balanced's 0.08 threshold (0.10 bits per pixel): counts from 0
	if lo, hi := save(rules.SliderTemplate(3), 5000); lo != 0 || hi != int64(5000)*1000/8*100*55/100 {
		t.Errorf("near the threshold: %d - %d", lo, hi)
	}
	// a skipped file saves nothing
	if lo, _ := save(rules.SliderTemplate(5), 8000); lo != 0 { // 0.16: within 1.5x of Best's 0.12
		t.Errorf("Best near its threshold: %d", lo)
	}
	if lo, hi := Saving(rules.SliderTemplate(3), rules.Plan{Skip: "x"}, info(8000), size(8000)); lo != 0 || hi != 0 {
		t.Error("a skipped file saves nothing")
	}
	// a legacy flow uses the nearest position's band (CRF 26: Smaller) and its bitrate threshold
	legacy := rules.LegacyTemplate(rules.Default(), "replace") // h264 from 4000 kbps, CRF 26
	if lo, hi := save(legacy, 12000); lo != video*12/100 || hi != video*57/100 {
		t.Errorf("legacy: %d - %d", lo, hi)
	}
	if lo, _ := save(legacy, 5000); lo != 0 {
		t.Errorf("legacy near its 4000 kbps threshold counts from 0: %d", lo)
	}
	// every position's low end is at most its minimum saving, and the high end shrinks toward Best
	for i, p := range rules.Slider {
		if p.SaveLowPct >= p.SaveHighPct || i > 0 && p.SaveHighPct > rules.Slider[i-1].SaveHighPct {
			t.Errorf("band %+v", p)
		}
	}
}

func TestSampleInPlainWords(t *testing.T) {
	dir := library(t)
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	sample := func(name string, q int) Sample {
		st, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return p.Sample(context.Background(), dir, filepath.Join(dir, name), st, rules.SliderTemplate(q))
	}
	s := sample("a.mkv", 3)
	// Basic (S4): each track as stereo AAC (this fake probe gives no channel count, so the AAC one is
	// converted too)
	if strings.Join(s.Lines, "|") != "Video: converted to the smaller format, same picture size and length|Audio: 2 converted to AAC" {
		t.Errorf("%q", s.Lines)
	}
	if s.ExpectedBytesLow <= 0 || s.ExpectedBytesLow >= s.ExpectedBytesHigh || s.ExpectedBytesHigh >= 1_000_000 {
		t.Errorf("expected %d - %d", s.ExpectedBytesLow, s.ExpectedBytesHigh)
	}
	// HEVC with audio to change: repackaged as MKV, the video kept, no saving counted (S4)
	if s := sample("hevc.mkv", 3); s.Lines[0] != "Video: kept as it is (already in the smaller format); the file is only repackaged as an MKV file" || s.ExpectedBytesLow != 500_000 || s.ExpectedBytesHigh != 500_000 {
		t.Errorf("%+v", s)
	}
	if s := sample("bad.mkv", 3); s.Lines[0] != "Left as it is: it can't be read." {
		t.Errorf("%+v", s)
	}
	if s := sample("shared.mkv", 3); s.Lines[0] != "Left as it is: it is shared with another folder, so converting it would free no space." {
		t.Errorf("%+v", s)
	}
	banned := []string{"crf", "preset", "x265", "hevc", "ffmpeg", "encoder", "transcode", "bits per pixel"}
	for _, name := range []string{"a.mkv", "hevc.mkv", "lean.mkv", "bad.mkv", "shared.mkv"} {
		for q := 1; q <= 5; q++ {
			for _, l := range sample(name, q).Lines {
				for _, w := range banned {
					if strings.Contains(strings.ToLower(l), w) {
						t.Errorf("%s at %d: %q uses %q", name, q, l, w)
					}
				}
			}
		}
	}
}

func TestFiles(t *testing.T) {
	dir := library(t)
	all, err := Files(dir, "")
	if err != nil || len(all) != 6 || all[0].Name != "a.mkv" || all[len(all)-1].Name != "sub/b.mp4" {
		t.Fatalf("%v %+v", err, all)
	}
	if some, _ := Files(dir, "SUB"); len(some) != 1 || some[0].Path != filepath.Join(dir, "sub", "b.mp4") || some[0].Bytes != 2_000_000 {
		t.Errorf("%+v", some)
	}
}

// Start does not probe the library again: the scanner finds the Preview's probes in the cache.
func TestStartReusesThePreviewsProbes(t *testing.T) {
	x := &probes{}
	st := store(t)
	dir := library(t)
	p := &Previewer{Store: st, Exec: x, FFprobe: "ffprobe"}
	if r := p.Library(1, dir, rules.SliderTemplate(3), 10*time.Second); r.Status != "ready" {
		t.Fatalf("%+v", r)
	}
	before := x.n()
	l, err := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	sc := &scanner.Scanner{Store: st, Exec: x, FFprobe: "ffprobe", Targets: func() ([]scanner.Target, error) {
		return []scanner.Target{{LibraryID: l.ID, Path: dir, Template: rules.SliderTemplate(3)}}, nil
	}}
	sc.ScanOnce(context.Background()) // the first look (slice 23: decided on the second)
	if n := sc.ScanOnce(context.Background()); n != 4 {
		t.Errorf("queued %d, want the 4 the Preview said can be shrunk", n)
	}
	if extra := x.n() - before; extra != 1 { // only bad.mkv, whose probe failed and so was not kept
		t.Errorf("the scan probed %d files again", extra)
	}
}

// A link made or removed in another folder changes which files are shared: the cached Preview is not
// served as fresh (review 34ab633), and the shared file moves to skipped.shared_file (slice 23).
func TestPreviewSeesLinksMadeElsewhere(t *testing.T) {
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	dir := library(t)
	first := p.Library(1, dir, rules.SliderTemplate(3), 10*time.Second)
	elsewhere := filepath.Join(t.TempDir(), "copy.mkv")
	if err := os.Link(filepath.Join(dir, "a.mkv"), elsewhere); err != nil {
		t.Skip("cannot make links here")
	}
	var r Result
	for i := 0; i < 100 && (r.Status != "ready" || r.Skipped["shared_file"] != first.Skipped["shared_file"]+1); i++ {
		r = p.Library(1, dir, rules.SliderTemplate(3), 100*time.Millisecond)
	}
	if r.Skipped["shared_file"] != first.Skipped["shared_file"]+1 || r.CanShrink != first.CanShrink-1 || r.EstimateBytesHigh >= first.EstimateBytesHigh {
		t.Fatalf("after a link elsewhere: %+v (before %+v)", r, first)
	}
	os.Remove(elsewhere)
	for i := 0; i < 100 && (r.Status != "ready" || r.Skipped["shared_file"] != first.Skipped["shared_file"]); i++ {
		r = p.Library(1, dir, rules.SliderTemplate(3), 100*time.Millisecond)
	}
	if r.CanShrink != first.CanShrink || r.EstimateBytesHigh != first.EstimateBytesHigh {
		t.Errorf("after removing it: %+v", r)
	}
}

// A restored file (slice 18) is counted as skipped "restored", like the scanner skips it.
func TestPreviewCountsRestoredFilesAsSkipped(t *testing.T) {
	st := store(t)
	dir := library(t)
	st.PutFile(jobs.File{Path: filepath.Join(dir, "a.mkv"), Decision: jobs.DecisionRestored})
	r := (&Previewer{Store: st, Exec: &probes{}, FFprobe: "ffprobe"}).Library(1, dir, rules.SliderTemplate(3), 10*time.Second)
	if r.Skipped["restored"] != 1 || r.CanShrink != 3 { // b.mp4, lean.mkv and hevc.mkv (repackaged; the shared file is skipped too)
		t.Errorf("%+v", r)
	}
}

// The sample says "left as it is" for a file the guards leave alone, without reading it (review f4c41fd).
func TestSampleOfASharedFile(t *testing.T) {
	x := &probes{}
	p := &Previewer{Store: store(t), Exec: x, FFprobe: "ffprobe"}
	dir := library(t) // its shared.mkv has a second name in another folder
	path := filepath.Join(dir, "shared.mkv")
	st, _ := os.Stat(path)
	s := p.Sample(context.Background(), dir, path, st, rules.SliderTemplate(3))
	if len(s.Lines) != 1 || s.Lines[0] != "Left as it is: it is shared with another folder, so converting it would free no space." || s.ExpectedBytesLow != st.Size() {
		t.Errorf("%+v", s)
	}
	if x.n() != 0 {
		t.Errorf("the shared file was read (%d probes)", x.n())
	}
}

// "Save as separate .srt files" says when subtitles stay inside (pictures, TTML), in design's words; not
// when every subtitle is saved as a file, and not in other subtitle choices.
func TestSampleSaysWhichSubtitlesStayInside(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "subs.mkv"), 1_000_000)
	write(t, filepath.Join(dir, "textsubs.mkv"), 1_000_000)
	p := &Previewer{Store: store(t), Exec: &probes{}, FFprobe: "ffprobe"}
	sample := func(name, subtitles string) string {
		st, _ := os.Stat(filepath.Join(dir, name))
		sel := rules.DefaultSelectors(3)
		sel.Subtitles = subtitles
		return strings.Join(p.Sample(context.Background(), dir, filepath.Join(dir, name), st, sel.Template()).Lines, "|")
	}
	stay := "Picture subtitles stay inside the file; only text subtitles are saved as .srt files."
	if s := sample("subs.mkv", "srt"); !strings.Contains(s, "Subtitles: 2 subtitles kept, 1 saved as separate .srt files|"+stay) {
		t.Errorf("srt with pictures: %q", s)
	}
	if s := sample("textsubs.mkv", "srt"); strings.Contains(s, stay) || !strings.Contains(s, "Subtitles: 1 saved as separate .srt files") {
		t.Errorf("srt, text only: %q", s)
	}
	if s := sample("subs.mkv", "keep_all"); strings.Contains(s, stay) {
		t.Errorf("keep_all: %q", s)
	}
}
