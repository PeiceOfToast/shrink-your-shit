// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/worker"
)

// speedReport pairs a worker, leases a job and sends one report body; it returns the report's status and
// the answer the server's run received (the run is stood in for by a reader of its answer, as the kept
// code's test). The fields are read the same for every report; a refusal stands in here so no upload has
// to arrive.
func speedReport(t *testing.T, report string) (int, worker.RemoteResult) {
	t.Helper()
	_, st, a, _ := libServer(t, true)
	answers := make(chan worker.RemoteResult, 1)
	srv := httptest.NewServer(Handler(st, a, Engine{RunRemote: func(_ jobs.Job, _ string, rj *worker.RemoteJob) {
		answers <- <-rj.Answer()
	}}))
	t.Cleanup(srv.Close)

	_, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/pairings", body: `{}`, ctype: "application/json", bearer: a.Token()})
	var p struct{ Code string }
	json.Unmarshal([]byte(body), &p)
	c, b := workerCall(t, srv, "pair", "", `{"code":"`+p.Code+`","name":"Desktop"}`, WorkerAPILevel)
	var w struct{ WorkerID, Token string }
	json.Unmarshal([]byte(b), &w)
	if c != 200 || w.Token == "" {
		t.Fatalf("pair %d %s", c, b)
	}

	lib, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if _, err := st.Enqueue(jobs.Job{Library: lib.Path, LibraryID: lib.ID, Path: filepath.Join(lib.Path, "a.mkv"), SizeBefore: 1000}); err != nil {
		t.Fatal(err)
	}
	c, b = workerCall(t, srv, "lease", w.Token, `{"slots":1}`, WorkerAPILevel)
	var l struct{ Lease struct{ ID string } }
	json.Unmarshal([]byte(b), &l)
	if c != 200 || l.Lease.ID == "" {
		t.Fatalf("lease %d %s", c, b)
	}

	req := httptest.NewRequest("POST", "/api/worker/v1/leases/"+l.Lease.ID+"/report", strings.NewReader(report))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.Token)
	req.Header.Set(workerHeader, "test; api="+strconv.Itoa(WorkerAPILevel))
	rec := httptest.NewRecorder()
	srv.Config.Handler.ServeHTTP(rec, req)
	select {
	case res := <-answers:
		return rec.Code, res
	case <-time.After(10 * time.Second):
		return rec.Code, worker.RemoteResult{}
	}
}

// F7: the speed a remote worker measures reaches the run with its report, so the step records the encoder
// it ran and how fast it went.
func TestReportCarriesItsSpeed(t *testing.T) {
	c, res := speedReport(t, `{"kept":"the picture differs","kept_code":"`+jobs.ReasonCheckDiffers+`","encoder":"hevc_qsv","speed":3.5,"fps":118}`)
	if c != http.StatusAccepted {
		t.Fatalf("report %d, want 202", c)
	}
	if res.Encoder != "hevc_qsv" || res.Speed != 3.5 || res.FPS != 118 {
		t.Errorf("the run got encoder %q speed %v fps %v, want hevc_qsv at 3.5x and 118 fps", res.Encoder, res.Speed, res.FPS)
	}
}

// F7 review #638 §2: the figures are the paired worker's own claim, and its encoder name reaches the workers
// list, so a report the server would not write itself stores no figure (the job still runs: 202, no error).
func TestReportSpeedIsChecked(t *testing.T) {
	cases := []struct {
		name, report string
		enc          string
		speed, fps   float64
	}{
		{"a name this program never runs", `{"encoder":"totally-made-up","speed":3.5,"fps":118}`, "", 0, 0},
		{"a huge encoder string", `{"encoder":"` + strings.Repeat("hevc_qsv ", 40) + `","speed":3.5,"fps":118}`, "", 0, 0},
		{"a speed with no encoder", `{"encoder":"","speed":3.5,"fps":118}`, "", 0, 0},
		{"a speed beyond any encode", `{"encoder":"hevc_qsv","speed":1e9,"fps":118}`, "", 0, 0},
		{"a negative speed", `{"encoder":"hevc_qsv","speed":-3.5,"fps":118}`, "", 0, 0},
		{"a frame rate beyond any encode", `{"encoder":"hevc_qsv","speed":3.5,"fps":1e9}`, "hevc_qsv", 3.5, 0},
		{"a report with no figures", `{"encoder":"","speed":0,"fps":0}`, "", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, res := speedReport(t, `{"kept":"the picture differs","kept_code":"`+jobs.ReasonCheckDiffers+`",`+c.report[1:])
			if code != http.StatusAccepted {
				t.Fatalf("report %d, want 202: a bad figure must not fail the job", code)
			}
			if res.Encoder != c.enc || res.Speed != c.speed || res.FPS != c.fps {
				t.Errorf("the run got encoder %q speed %v fps %v, want %q at %vx and %v fps", res.Encoder, res.Speed, res.FPS, c.enc, c.speed, c.fps)
			}
		})
	}
}

// F7 review #638 §2: a report also carries its own run steps, and the server stores them as they come, so a
// step row's speed figures go through the same check (a known encoder, a possible number) as the report's.
func TestReportStepsAreChecked(t *testing.T) {
	steps := checkedSteps([]jobs.RunStep{
		{Index: 0, Name: "encode", Encoder: "hevc_qsv", Speed: 3.5, FPS: 118},
		{Index: 7, Name: "encode", Encoder: strings.Repeat("x", 300), Speed: 1e9, FPS: 1e9},
		{Index: 8, Name: "encode", Encoder: "libx265", Speed: 50, FPS: 1e9},
	})
	if steps[0].Encoder != "hevc_qsv" || steps[0].Speed != 3.5 || steps[0].FPS != 118 {
		t.Errorf("a possible figure was dropped: %+v", steps[0])
	}
	if steps[1].Encoder != "" || steps[1].Speed != 0 || steps[1].FPS != 0 {
		t.Errorf("an impossible figure was kept: %+v", steps[1])
	}
	if steps[2].Encoder != "libx265" || steps[2].Speed != 50 || steps[2].FPS != 0 {
		t.Errorf("a step's fps outside any encode was kept: %+v", steps[2])
	}
	if steps[0].Name != "encode" || steps[1].Index != 7 {
		t.Errorf("the check changed a step beyond its figures: %+v", steps)
	}
}
