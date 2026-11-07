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
	switch {
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
 {"index":1,"codec_type":"audio","codec_name":"aac"},{"index":2,"codec_type":"audio","codec_name":"ac3"}],
 "format":{"format_name":"matroska,webm","duration":"%f","size":"%d","bit_rate":"%d"}}`, codec, kbps*1000, dur, st.Size(), kbps*1000), nil
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
	if r.Status != "ready" || r.Files != 6 || r.TotalBytes != 7_900_000 || r.CanShrink != 3 {
		t.Fatalf("%+v", r)
	}
	if fmt.Sprint(r.Skipped) != "map[already_efficient:1 already_hevc:1 unreadable:1]" {
		t.Errorf("skipped %v", r.Skipped)
	}
	if r.SharedNoSaving != 1 {
		t.Errorf("shared %d", r.SharedNoSaving)
	}
	// a.mkv and b.mp4 are 3 MB of video at 0.16 bits per pixel; Balanced (CRF 23) makes 0.03 to 0.075
	// of it: 53% to 81% saved. The shared file (4 MB) frees nothing and is left out.
	if r.EstimateBytesLow < 1_500_000 || r.EstimateBytesLow > 1_650_000 || r.EstimateBytesHigh < 2_400_000 || r.EstimateBytesHigh > 2_500_000 {
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
	if n != 6 {
		t.Fatalf("%d probes for 6 videos", n)
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
	if r := p.Library(1, dir, rules.SliderTemplate(3), 50*time.Millisecond); r.Status != "computing" || r.Stale {
		t.Fatalf("%+v", r)
	}
	close(x.gate)
	var r Result
	for i := 0; i < 100 && r.Status != "ready"; i++ {
		r = p.Library(1, dir, rules.SliderTemplate(3), 100*time.Millisecond)
	}
	if r.Status != "ready" || r.Files != 6 {
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
	p.Sample(context.Background(), filepath.Join(dir, "a.mkv"), a, rules.SliderTemplate(5))
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
	info := func(kbps int) media.Info {
		return media.Info{DurationSec: 100, SizeBytes: int64(kbps) * 1000 / 8 * 100, Streams: []media.Stream{
			{Type: "video", Codec: "h264", Width: 1920, Height: 1080, FrameRate: 24, BitrateKbps: kbps}}}
	}
	plan := func(crf, minPct int) rules.Plan {
		return rules.Plan{Video: rules.VideoEncode{CRF: crf}, MinSavingPct: minPct}
	}
	size := int64(100_000_000)
	lo, hi := Saving(plan(23, 10), info(8000), size)
	if lo <= 0 || hi <= lo || hi >= size {
		t.Errorf("8 Mbps at 23: %d - %d", lo, hi)
	}
	// a higher CRF makes a smaller file
	if lo2, hi2 := Saving(plan(27, 10), info(8000), size); lo2 <= lo || hi2 <= hi {
		t.Errorf("CRF 27 %d - %d is not more than CRF 23 %d - %d", lo2, hi2, lo, hi)
	}
	// a lean source would be kept by its minimum saving: nothing freed
	if lo, hi := Saving(plan(19, 20), info(1500), size); lo != 0 || hi != 0 {
		t.Errorf("lean source %d - %d", lo, hi)
	}
	// unknown bits per pixel: the wide fallback band
	if lo, hi := Saving(plan(23, 0), media.Info{}, size); lo != 20_000_000 || hi != 60_000_000 {
		t.Errorf("unknown %d - %d", lo, hi)
	}
	if lo, hi := Saving(rules.Plan{Skip: "x"}, info(8000), size); lo != 0 || hi != 0 {
		t.Error("a skipped file saves nothing")
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
		return p.Sample(context.Background(), filepath.Join(dir, name), st, rules.SliderTemplate(q))
	}
	s := sample("a.mkv", 3)
	if strings.Join(s.Lines, "|") != "Video: converted to the smaller format, same picture size and length|Audio: 2 tracks kept|Kept as it is if the new file is not at least 10% smaller" {
		t.Errorf("%q", s.Lines)
	}
	if s.ExpectedBytesLow <= 0 || s.ExpectedBytesLow >= s.ExpectedBytesHigh || s.ExpectedBytesHigh >= 1_000_000 {
		t.Errorf("expected %d - %d", s.ExpectedBytesLow, s.ExpectedBytesHigh)
	}
	if s := sample("hevc.mkv", 3); s.Lines[0] != "Left as it is: it already uses a modern, efficient format." || s.ExpectedBytesLow != 500_000 {
		t.Errorf("%+v", s)
	}
	if s := sample("bad.mkv", 3); s.Lines[0] != "Left as it is: it can't be read." {
		t.Errorf("%+v", s)
	}
	if s := sample("shared.mkv", 3); !strings.Contains(strings.Join(s.Lines, "|"), "frees no space") {
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
