// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

const h264Big = `{"streams":[{"codec_type":"video","codec_name":"h264","bit_rate":"8000000"}],"format":{"duration":"60","size":"5"}}`
const hevcBig = `{"streams":[{"codec_type":"video","codec_name":"hevc","bit_rate":"8000000"}],"format":{"duration":"60","size":"5"}}`

func setup(t *testing.T, probe string) (*Scanner, *jobs.Store, string, *media.FakeExec) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lib := t.TempDir()
	os.WriteFile(filepath.Join(lib, "a.mkv"), []byte("12345"), 0o644)
	os.WriteFile(filepath.Join(lib, "notes.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(lib, "b.syp-tmp.mkv"), []byte("x"), 0o644)
	f := &media.FakeExec{Out: []byte(probe)}
	return &Scanner{Store: st, Exec: f, FFprobe: "ffprobe", Rule: rules.Default(), Libraries: []string{lib}}, st, lib, f
}

func TestIsVideo(t *testing.T) {
	for p, want := range map[string]bool{"a.mkv": true, "A.MP4": true, "a.txt": false, "a.syp-tmp.mkv": false, "a.hevc.mkv": false, "Show.S01E01.hevc.x265.mkv": true} {
		if IsVideo(p) != want {
			t.Errorf("%s: want %v", p, want)
		}
	}
}

func TestQueuesMatchingFileOnce(t *testing.T) {
	s, st, lib, f := setup(t, h264Big)
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Fatalf("queued %d", n)
	}
	if len(f.Calls) != 1 {
		t.Errorf("only a.mkv should be probed, got %d calls", len(f.Calls))
	}
	l, _ := st.List(10)
	if len(l) != 1 || l[0].Path != filepath.Join(lib, "a.mkv") || l[0].Status != jobs.Queued || l[0].DurationSec != 60 {
		t.Errorf("%+v", l)
	}
	// Second scan: unchanged file is neither probed nor queued again.
	if n := s.ScanOnce(context.Background()); n != 0 || len(f.Calls) != 1 {
		t.Errorf("rescan queued %d, calls %d", n, len(f.Calls))
	}
}

func TestSkipsNonMatching(t *testing.T) {
	s, st, _, _ := setup(t, hevcBig)
	if n := s.ScanOnce(context.Background()); n != 0 {
		t.Errorf("queued %d", n)
	}
	if l, _ := st.List(10); len(l) != 0 {
		t.Errorf("%+v", l)
	}
}

func TestChangedFileIsRescanned(t *testing.T) {
	s, _, lib, f := setup(t, hevcBig)
	s.ScanOnce(context.Background())
	p := filepath.Join(lib, "a.mkv")
	os.WriteFile(p, []byte("123456789"), 0o644)
	f.Out = []byte(h264Big)
	if n := s.ScanOnce(context.Background()); n != 1 {
		t.Errorf("changed file should be rescanned and queued, got %d", n)
	}
}

func TestMinAgeSkipsFreshFiles(t *testing.T) {
	s, _, _, f := setup(t, h264Big)
	s.MinAge = time.Hour
	if n := s.ScanOnce(context.Background()); n != 0 || len(f.Calls) != 0 {
		t.Errorf("fresh file should be left alone: %d %d", n, len(f.Calls))
	}
}

func TestNoSecondJobWhileOpen(t *testing.T) {
	s, st, lib, _ := setup(t, h264Big)
	s.ScanOnce(context.Background())
	os.WriteFile(filepath.Join(lib, "a.mkv"), []byte("changed-before-worker-ran"), 0o644)
	if n := s.ScanOnce(context.Background()); n != 0 {
		t.Errorf("queued a duplicate: %d", n)
	}
	if l, _ := st.List(10); len(l) != 1 {
		t.Errorf("%+v", l)
	}
}
