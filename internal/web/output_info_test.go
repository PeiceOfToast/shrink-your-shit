// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Foundation slice 4: the job detail's metadata.after is the probe the job's own check took of the new
// file. A kept job has one even though its temp file is gone; a job from before the column falls back to
// today's read of the probes table, unchanged.
func TestV2JobDetailAfterIsTheMeasuredProbe(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)

	after := func(id int64) (codec string, depths []int, before bool) {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10)})
		if code != 200 {
			t.Fatalf("GET job %d: %d %s", id, code, body)
		}
		var d struct {
			Metadata struct {
				Before *json.RawMessage `json:"before"`
				After  *struct {
					VideoCodec string `json:"videoCodec"`
					Streams    []struct {
						BitDepth int `json:"bitDepth"`
					} `json:"streams"`
				} `json:"after"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(body), &d); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
		if d.Metadata.After == nil {
			return "", nil, d.Metadata.Before != nil
		}
		for _, s := range d.Metadata.After.Streams {
			depths = append(depths, s.BitDepth)
		}
		return d.Metadata.After.VideoCodec, depths, d.Metadata.Before != nil
	}

	// a kept job: nothing is in the probes table (its temp file never reached the replace), so only the
	// probe the check took is there to show
	kept := filepath.Join(lib.Path, "kept.mkv")
	id, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: kept, SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOutputInfo(id, `{"container":"matroska,webm","videoCodec":"hevc","durationSec":100,`+
		`"streams":[{"index":0,"type":"video","codec":"hevc","width":640,"height":360,"bitDepth":10}]}`); err != nil {
		t.Fatal(err)
	}
	if err := st.Keep(id, "pictures_differ", "the picture check refused the new file"); err != nil {
		t.Fatal(err)
	}
	if codec, depths, before := after(id); codec != "hevc" || len(depths) != 1 || depths[0] != 10 || before {
		t.Errorf("a kept job's metadata: after %q %v, before set %v; want the check's probe, before null", codec, depths, before)
	}

	// a job from before the column: today's read of the probes table, as it always was
	old := filepath.Join(lib.Path, "old.mkv")
	id2, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: old, SizeBefore: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	st.PutProbe(old, 400, 1, `{"videoCodec":"av1"}`)
	if err := st.Complete(id2, old, 400); err != nil {
		t.Fatal(err)
	}
	if codec, _, _ := after(id2); codec != "av1" {
		t.Errorf("an old job's metadata.after %q, want today's fallback (av1)", codec)
	}
}
