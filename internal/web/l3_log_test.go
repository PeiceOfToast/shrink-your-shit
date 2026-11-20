// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// R14: the replace step carries its file actions as moves[], in the order the worker took them, each with what
// it did, from and to, whether it worked, and the system's words when it did not.
func TestLogReplaceStepMoves(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	id, _ := st.Enqueue(jobs.Job{Library: "/m", LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Claim("t")
	st.RecordRunStep(id, jobs.RunStep{Index: 0, Name: "encode", Outcome: "ok"})
	st.RecordRunStep(id, jobs.RunStep{Index: 1, Name: "replace", Outcome: "ok"})
	st.RecordJobMove(id, jobs.Move{Action: "chmod", From: "/m/x", To: "/m/x", OK: true})
	st.RecordJobMove(id, jobs.Move{Action: "chown", From: "/m/x", To: "/m/x", OK: false, Error: "chown /m/x: operation not permitted"})
	st.RecordJobMove(id, jobs.Move{Action: "move", From: "/m/.a.1.syp-tmp/new.mkv", To: "/m/a.mkv", OK: true})
	st.Complete(id, "/m/a.mkv", 400)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/jobs/" + strconv.FormatInt(id, 10) + "/log"})
	var r struct {
		Steps []struct {
			Name  string
			Moves []map[string]any
		}
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Steps) != 2 {
		t.Fatalf("steps %s", body)
	}
	if r.Steps[0].Moves != nil {
		t.Errorf("the encode step has moves: %v", r.Steps[0].Moves)
	}
	moves := r.Steps[1].Moves
	if len(moves) != 3 {
		t.Fatalf("moves %v", moves)
	}
	if moves[0]["action"] != "chmod" || moves[0]["ok"] != true || moves[0]["from"] != "/m/x" {
		t.Errorf("chmod: %v", moves[0])
	}
	if moves[1]["action"] != "chown" || moves[1]["ok"] != false || moves[1]["error"] != "chown /m/x: operation not permitted" {
		t.Errorf("chown: %v", moves[1])
	}
	if moves[2]["action"] != "move" || moves[2]["from"] != "/m/.a.1.syp-tmp/new.mkv" || moves[2]["to"] != "/m/a.mkv" || moves[2]["ok"] != true {
		t.Errorf("move: %v", moves[2])
	}
}

// R14: summary.tempRemoved is true only when nothing was left behind; a failed remove makes it false, and a
// job that recorded no file actions has no answer (null).
func TestSummaryTempRemoved(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j, lines := sampleJob()
	if s := summaryJSON(st, j, lines); s["tempRemoved"] != nil {
		t.Fatalf("a job with no moves: %v, want null", s["tempRemoved"])
	}
	st.RecordJobMove(j.ID, jobs.Move{Action: "move", From: "/m/new", To: "/m/a.mkv", OK: true})
	st.RecordJobMove(j.ID, jobs.Move{Action: "remove", From: "/m/.a.1.syp-tmp", OK: true})
	if s := summaryJSON(st, j, lines); s["tempRemoved"] != true {
		t.Fatalf("every removal worked: %v, want true", s["tempRemoved"])
	}
	st.RecordJobMove(j.ID, jobs.Move{Action: "remove", From: "/m/.a.1.syp-tmp", OK: false, Error: "permission denied"})
	if s := summaryJSON(st, j, lines); s["tempRemoved"] != false {
		t.Fatalf("a removal failed: %v, want false", s["tempRemoved"])
	}
}

// R4 + R5: a graphics encoder whose test passed is still refused when the new file is 10-bit (an HDR plan,
// or a 10-bit SDR source: the check keeps the source's depth) unless its own 10-bit test passed, and for an
// HDR10 file with mastering data unless its family is proven to keep that data (a plain HDR/HLG file without
// mastering data is not gated).
func TestCandidateLinesNameTheHDRGate(t *testing.T) {
	hdr10 := &rules.HDR{Transfer: "smpte2084", Primaries: "bt2020", Matrix: "bt2020nc", MasterDisplay: "G(13250,34500)B(7500,3000)R(34000,16000)WP(15635,16450)L(10000000,1)", CLL: "1000,400"}
	hlg := &rules.HDR{Transfer: "arib-std-b67", Primaries: "bt2020", Matrix: "bt2020nc"}
	for _, c := range []struct {
		name           string
		need10, tenBit bool
		hdr            *rules.HDR
		want           string // "" when the encoder can do the file
	}{
		{name: "hevc_qsv", need10: true, tenBit: true, hdr: hdr10},   // proven to keep HDR10's data
		{name: "hevc_vaapi", need10: true, tenBit: true, hdr: hdr10}, // proven too
		{name: "hevc_nvenc", need10: true, tenBit: true, hdr: hdr10, want: "not proven to keep HDR10's mastering data"},
		{name: "hevc_nvenc", need10: true, tenBit: true, hdr: hlg}, // no mastering data to keep
		{name: "hevc_nvenc", need10: true, tenBit: false, hdr: hdr10, want: "8-bit only"},
		{name: "hevc_nvenc", need10: true, tenBit: false, want: "8-bit only"}, // a 10-bit SDR source: the check keeps its depth
		{name: "hevc_nvenc", tenBit: false},                                   // an 8-bit SDR file needs no 10-bit test
	} {
		got := candidateReject(c.name, c.need10, c.tenBit, c.hdr)
		if c.want == "" {
			if got != "" {
				t.Errorf("%s (10-bit %v): refused, want usable: %q", c.name, c.tenBit, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s (need10 %v, 10-bit %v): %q, want it to say %q", c.name, c.need10, c.tenBit, got, c.want)
		}
	}
}
