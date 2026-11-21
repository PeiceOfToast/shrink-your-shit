// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// fileCard is the Files view item named name on the card show.
func fileCard(t *testing.T, srv *httptest.Server, show, name string) map[string]any {
	t.Helper()
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/activity/files?show=" + show})
	var v struct{ Items []map[string]any }
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("%s: %v %s", show, err, body)
	}
	for _, it := range v.Items {
		if it["name"] == name {
			return it
		}
	}
	t.Fatalf("no %s card named %s: %s", show, name, body)
	return nil
}

// Slice 7: the Files view shows a probe only for the size the card shows. Film.mkv was replaced in place (the new
// file is 60 bytes, HEVC, probed); the original, restored, is 100 bytes and its card must not show HEVC.
func TestFilesViewShowsTheProbeOfItsOwnFile(t *testing.T) {
	const newProbe = `{"durationSec":60,"streams":[{"type":"video","codec":"hevc","Width":1920,"Height":1080}]}`
	setup := func(t *testing.T) (*jobs.Store, *httptest.Server, jobs.Library, string) {
		st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		st.SetPaused(false)
		a, _, _ := OpenAccess(t.TempDir(), false)
		srv := httptest.NewServer(Handler(st, a, Engine{}))
		t.Cleanup(srv.Close)
		lib, _ := st.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
		return st, srv, lib, filepath.Join(lib.Path, "Film.mkv")
	}

	t.Run("restored original shows no codec", func(t *testing.T) {
		st, srv, lib, film := setup(t)
		st.PutProbe(film, 60, 2, newProbe)
		st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: film, SizeBefore: 100})
		it := fileCard(t, srv, "unprocessed", "Film.mkv")
		if it["bytes"] != 100.0 {
			t.Fatalf("bytes %v, want the original's 100", it["bytes"])
		}
		if it["codec"] != nil || it["resolution"] != nil {
			t.Errorf("codec %v, resolution %v: the new file's probe is shown for the restored original", it["codec"], it["resolution"])
		}
	})

	t.Run("processed card shows the new file's codec", func(t *testing.T) {
		st, srv, lib, film := setup(t)
		st.PutProbe(film, 60, 2, newProbe)
		id, _ := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: film, SizeBefore: 100})
		st.Claim("w")
		st.Complete(id, film, 60)
		it := fileCard(t, srv, "processed", "Film.mkv")
		if it["bytes"] != 60.0 {
			t.Fatalf("bytes %v, want the new file's 60", it["bytes"])
		}
		if it["codec"] != preview.CodecWord("hevc") || it["resolution"] != "1080p" {
			t.Errorf("codec %v, resolution %v; want the new file's HEVC at 1080p", it["codec"], it["resolution"])
		}
	})
}

const (
	s7HEVC1080Probe = `{"streams":[{"type":"video","codec":"hevc","Width":1920,"Height":1080}]}`
	s7H264Probe     = `{"streams":[{"type":"video","codec":"h264","Width":1280,"Height":720}]}`
)

// sampleAfterJob is the Variables sample of a job without a plan snapshot, from 4000 bytes, completed with 1500 bytes:
// replaced in place (beside false) or written beside the original (true). The path's stored probe is probe, taken
// at probeSize bytes.
func sampleAfterJob(t *testing.T, beside bool, probeSize int64, probe string) varList {
	t.Helper()
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	orig := filepath.Join(lib.Path, "Movie (2019).mkv")
	out := orig
	if beside {
		out = filepath.Join(lib.Path, "Movie (2019).hevc.mkv")
	}
	st.PutProbe(orig, probeSize, 2, probe)
	id, err := st.Enqueue(jobs.Job{LibraryID: lib.ID, Path: orig, SizeBefore: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("local-box"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(id, out, 1500); err != nil {
		t.Fatal(err)
	}
	code, got := getVars(t, srv, lib.FlowID)
	if code != 200 {
		t.Fatalf("GET variables: %d", code)
	}
	return got
}

// Slice 7: replaced in place, the path holds the new file, so the sample takes no probe from it. The new file's probe
// (1500 bytes), or the old file's probe still stored (4000 bytes, the job's own size), gives no example values.
func TestVariablesIgnoreTheNewFileOfAReplacedJob(t *testing.T) {
	for _, c := range []struct {
		name      string
		probeSize int64
		probe     string
	}{
		{"new file probed", 1500, s7HEVC1080Probe},
		{"old file's probe still stored", 4000, s7H264Probe},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := sampleAfterJob(t, false, c.probeSize, c.probe)
			for _, name := range []string{"video_codec", "resolution", "width", "height"} {
				if v := got.find(t, name); v.Value != nil {
					t.Errorf("%s = %q, want none", name, v.str(t))
				}
			}
		})
	}
}

// Slice 7: written beside the original, the original is untouched, so its probe at the job's 4000 bytes is its sample;
// a probe of another size (the file changed since) is not.
func TestVariablesBesideUsesTheProbeOfTheSizeItStartedFrom(t *testing.T) {
	t.Run("probe at the job's size", func(t *testing.T) {
		got := sampleAfterJob(t, true, 4000, s7H264Probe)
		if v := got.find(t, "video_codec"); v.Value == nil || v.str(t) != "h264" {
			t.Errorf("video_codec = %v, want h264 from the original's probe", v.Value)
		}
	})
	t.Run("probe of another size", func(t *testing.T) {
		got := sampleAfterJob(t, true, 1500, s7HEVC1080Probe)
		if v := got.find(t, "video_codec"); v.Value != nil {
			t.Errorf("video_codec = %q from a probe of another size, want none", v.str(t))
		}
	})
}
