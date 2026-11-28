// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	encode "github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/hw"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/workers"
)

// fakeCards is the hardware detector's last pass, set before ready(). TestRun answers run, and after (when set) is
// what a finished run leaves behind, so a test can show the page reading the new result.
type fakeCards struct {
	ans    hw.Answer
	ok     bool
	run    string     // TestRun's answer; "" is a pass already going with no id to give
	after  *hw.Answer // what Current serves once the test has run
	tested []string   // the workers TestRun was asked about
}

func (f *fakeCards) Current() (hw.Answer, bool) { return f.ans, f.ok }

func (f *fakeCards) TestRun(workerID string) string {
	f.tested = append(f.tested, workerID)
	if f.after != nil {
		f.ans, f.ok = *f.after, true
	}
	return f.run
}

// qsvCards is one working HEVC encoder and one whose test failed, on an Intel card.
func qsvCards() *fakeCards {
	device := "/dev/dri/renderD128"
	hevc := hw.Candidate{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc", Device: device}
	h264 := hw.Candidate{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264", Device: device}
	return &fakeCards{ok: true, run: "1", ans: hw.Answer{
		Inventory: hw.Inventory{Verdicts: []hw.Verdict{
			{Candidate: hevc, Available: true},
			{Candidate: h264, Available: false, Reason: "the encoder stopped"},
		}},
		Devices: []hw.DeviceRecord{{Candidate: hevc, Decode: true, Scale: true}},
	}}
}

func (e *env) getWorkerJSON(t *testing.T, cookie *http.Cookie, path string, v any) {
	t.Helper()
	rec := e.do(http.MethodGet, path, "", withSession(cookie), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d %s; want 200", path, rec.Code, rec.Body)
	}
	decodeInto(t, rec, v)
}

func (e *env) patchWorker(t *testing.T, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	return e.do(http.MethodPatch, "/api/v2/workers/local", body, write(withSession(cookie)), false)
}

func TestWorkersListShowsThisComputer(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	var list []workerJSON
	e.getWorkerJSON(t, cookie, "/api/v2/workers", &list)
	if len(list) != 1 {
		t.Fatalf("workers = %d rows; want the built-in one", len(list))
	}
	got := list[0]
	if got.ID != "local" || got.Name != "This computer" || !got.Local || got.Address != "" {
		t.Errorf("worker = %+v; want this computer, local, no address", got)
	}
	if got.State != "idle" || got.Now != nil || got.DoneToday != 0 {
		t.Errorf("state = %q now = %+v doneToday = %d; want idle, nothing running, none done", got.State, got.Now, got.DoneToday)
	}
	if got.Slots != 1 || got.GPUSlots != 1 {
		t.Errorf("slots = %d gpuSlots = %d; want 1 and 1", got.Slots, got.GPUSlots)
	}
	if got.GPU != nil {
		t.Errorf("gpu = %+v; want no card before a detection pass", got.GPU)
	}

	var detail workerDetailJSON
	e.getWorkerJSON(t, cookie, "/api/v2/workers/local", &detail)
	if detail.Hours != nil || detail.PrimaryEncoder != nil {
		t.Errorf("hours = %+v encoder = %v; want any time, automatic", detail.Hours, detail.PrimaryEncoder)
	}
	if len(detail.Encoders) != 0 || detail.Libraries == nil || detail.NewLibraries == nil {
		t.Errorf("encoders = %+v libraries = %v newLibraries = %v; want none and empty lists", detail.Encoders, detail.Libraries, detail.NewLibraries)
	}

	rec := e.do(http.MethodGet, "/api/v2/workers/nope", "", withSession(cookie), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "worker_not_found" {
		t.Errorf("unknown worker = %d %s; want 404 worker_not_found", rec.Code, code)
	}
}

func TestWorkerSettingsArePatchedAndStored(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	for { // the page's wake is what is checked below; earlier handlers may have left one
		select {
		case <-e.scans.wakes:
			continue
		default:
		}
		break
	}

	rec := e.patchWorker(t, cookie, `{"slots":3,"gpuSlots":0,"hours":{"from":"22:00","to":"06:00"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH = %d %s; want 200", rec.Code, rec.Body)
	}
	var detail workerDetailJSON
	decodeInto(t, rec, &detail)
	if detail.Slots != 3 || detail.GPUSlots != 0 {
		t.Errorf("slots = %d gpuSlots = %d; want 3 and 0", detail.Slots, detail.GPUSlots)
	}
	if detail.Hours == nil || detail.Hours.From != "22:00" || detail.Hours.To != "06:00" {
		t.Errorf("hours = %+v; want 22:00 to 06:00", detail.Hours)
	}

	// Stored: a second look, through the store rather than the handler's copy, sees the same settings.
	wk, err := workers.Open(e.db).Get(context.Background(), workers.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if wk.Slots != 3 || wk.GPUSlots != 0 || wk.Hours == nil || wk.Hours.From != "22:00" || wk.Hours.To != "06:00" {
		t.Errorf("stored = %+v; want 3 slots, 0 graphics slots, 22:00 to 06:00", wk)
	}
	select {
	case <-e.scans.wakes:
	case <-time.After(2 * time.Second):
		t.Fatal("saving the settings did not wake the worker")
	}

	// null clears the hours: the worker takes jobs at any time again.
	rec = e.patchWorker(t, cookie, `{"hours":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH hours null = %d %s; want 200", rec.Code, rec.Body)
	}
	decodeInto(t, rec, &detail)
	if detail.Hours != nil {
		t.Errorf("hours = %+v after null; want any time", detail.Hours)
	}
}

func TestWorkerPatchRefusesWhatThePageCannotTake(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()
	cases := []struct {
		name, body, code, field string
		status                  int
	}{
		{"no files at once", `{"slots":0}`, "invalid_setting", "slots", http.StatusBadRequest},
		{"negative graphics slots", `{"gpuSlots":-1}`, "invalid_setting", "gpuSlots", http.StatusBadRequest},
		{"equal hours", `{"hours":{"from":"09:00","to":"09:00"}}`, "invalid_setting", "hours", http.StatusBadRequest},
		{"hours missing a digit", `{"hours":{"from":"9:00","to":"06:00"}}`, "invalid_setting", "hours", http.StatusBadRequest},
		{"libraries on this computer", `{"libraries":[]}`, "local_worker", "", http.StatusConflict},
		{"unknown encoder", `{"primaryEncoder":"hevc_qsv"}`, "invalid_encoder", "primaryEncoder", http.StatusBadRequest},
	}
	for _, c := range cases {
		rec := e.patchWorker(t, cookie, c.body)
		code, _, env := errorBody(t, rec)
		// An error on no one field leaves field out of the envelope entirely.
		wantField := any(c.field)
		if c.field == "" {
			wantField = nil
		}
		if rec.Code != c.status || code != c.code || env["field"] != wantField {
			t.Errorf("%s = %d %q field %v; want %d %q field %q\n%s", c.name, rec.Code, code, env["field"], c.status, c.code, c.field, rec.Body)
		}
	}

	// A refused change stores nothing: the earlier "slots":0 did not sneak through.
	wk, err := workers.Open(e.db).Get(context.Background(), workers.LocalID)
	if err != nil {
		t.Fatal(err)
	}
	if wk.Slots != 1 || wk.GPUSlots != 1 || wk.Hours != nil || wk.PrimaryEncoder != "" {
		t.Errorf("stored = %+v after refusals; want the defaults", wk)
	}
}

func TestWorkerShowsTheCardTheDetectorFound(t *testing.T) {
	e := newEnv(t, false)
	e.cards = qsvCards()
	e.ready()
	cookie := e.setup()

	var detail workerDetailJSON
	e.getWorkerJSON(t, cookie, "/api/v2/workers/local", &detail)
	if detail.GPU == nil || detail.GPU.Name != "Intel Quick Sync (hevc_qsv)" || detail.GPU.Family != "qsv" {
		t.Errorf("gpu = %+v; want the Intel card on hevc_qsv", detail.GPU)
	}
	want := []workerEncoderJSON{
		{Name: "hevc_qsv", Label: "HEVC · Intel Quick Sync", TestOk: true},
		{Name: "h264_qsv", Label: "H.264 · Intel Quick Sync", TestOk: false},
	}
	if len(detail.Encoders) != len(want) {
		t.Fatalf("encoders = %+v; want %+v", detail.Encoders, want)
	}
	for i, w := range want {
		if detail.Encoders[i] != w {
			t.Errorf("encoder %d = %+v; want %+v", i, detail.Encoders[i], w)
		}
	}

	rec := e.patchWorker(t, cookie, `{"primaryEncoder":"hevc_qsv"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pick the working encoder = %d %s; want 200", rec.Code, rec.Body)
	}
	decodeInto(t, rec, &detail)
	if detail.PrimaryEncoder == nil || *detail.PrimaryEncoder != "hevc_qsv" || detail.GPU == nil {
		t.Errorf("encoder = %v gpu = %+v; want hevc_qsv on the card", detail.PrimaryEncoder, detail.GPU)
	}

	rec = e.patchWorker(t, cookie, `{"primaryEncoder":"h264_qsv"}`)
	if code, message, _ := errorBody(t, rec); rec.Code != http.StatusConflict || code != "encoder_not_working" ||
		message != "That encoder failed its test. Test the encoders, then pick it again." {
		t.Errorf("failed encoder = %d %q %q; want 409 encoder_not_working", rec.Code, code, message)
	}

	rec = e.patchWorker(t, cookie, `{"primaryEncoder":"hevc_nvenc"}`)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_encoder" {
		t.Errorf("unknown encoder = %d %q; want 400 invalid_encoder", rec.Code, code)
	}

	// The processor is always a choice, and then no card is shown.
	rec = e.patchWorker(t, cookie, `{"primaryEncoder":"cpu"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("pick the processor = %d %s; want 200", rec.Code, rec.Body)
	}
	decodeInto(t, rec, &detail)
	if detail.PrimaryEncoder == nil || *detail.PrimaryEncoder != "cpu" || detail.GPU != nil {
		t.Errorf("encoder = %v gpu = %+v; want the processor and no card", detail.PrimaryEncoder, detail.GPU)
	}
}

// The list tells the page what the worker is doing and how much it finished today, from the queue.
func TestWorkerShowsTheRunningJobAndTodaysWork(t *testing.T) {
	f := newJobsFixture(t)
	ctx := context.Background()

	var list []workerJSON
	f.e.getWorkerJSON(t, f.cookie, "/api/v2/workers", &list)
	if len(list) != 1 || list[0].DoneToday != 1 {
		t.Fatalf("workers = %+v; want one row, one job done today", list)
	}
	if list[0].State != "idle" || list[0].Now != nil {
		t.Errorf("state = %q now = %+v with nothing claimed; want idle", list[0].State, list[0].Now)
	}

	j, err := f.q.Claim(ctx, "local", jobs.ClaimOptions{})
	if err != nil || j == nil {
		t.Fatalf("claim the waiting job: %+v, %v", j, err)
	}
	var d workerDetailJSON
	f.e.getWorkerJSON(t, f.cookie, "/api/v2/workers/local", &d)
	if d.State != "converting" || d.Now == nil {
		t.Fatalf("state = %q now = %+v while converting; want the running job", d.State, d.Now)
	}
	if d.Now.JobID != fmt.Sprint(j.ID) || d.Now.File != "one.mkv" || d.Now.Step != nil {
		t.Errorf("now = %+v; want job %d on one.mkv with no step started yet", d.Now, j.ID)
	}

	// The last step the job started is the one the page names, with the workflow's step count.
	if err := f.e.bus.Emit(events.Event{JobID: j.ID, Attempt: j.Attempt, Kind: events.KindStepStart, Level: events.LevelInfo,
		StepIndex: 1, StepName: "Video", Message: "1. Video"}); err != nil {
		t.Fatal(err)
	}
	step := latestStepOf(ctx, f.e.bus, f.e.flows, *j)
	deadline := time.Now().Add(3 * time.Second)
	for step == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		step = latestStepOf(ctx, f.e.bus, f.e.flows, *j)
	}
	if step == nil || step.Name != "Video" || step.Index != 1 || step.Count < 2 {
		t.Errorf("step = %+v; want step 1 of the workflow's Video", step)
	}
	rev, err := f.e.flows.Revision(ctx, j.WorkflowID, j.WorkflowRev)
	if err != nil {
		t.Fatal(err)
	}
	if step.Count != len(rev.Flow.Steps) {
		t.Errorf("step count = %d; want the revision's %d steps", step.Count, len(rev.Flow.Steps))
	}

	if err := f.q.Finish(ctx, j.ID, j.Attempt, jobs.Result{Verdict: jobs.VerdictDone, Outcome: "smaller", SizeBefore: 1000, SizeAfter: 400, OutputPath: j.Path + "-shrunk.mkv"}); err != nil {
		t.Fatal(err)
	}
	f.e.getWorkerJSON(t, f.cookie, "/api/v2/workers", &list)
	if list[0].State != "idle" || list[0].Now != nil || list[0].DoneToday != 2 {
		t.Errorf("after finishing = %+v; want idle and two done today", list[0])
	}
}

// The Test button (page 10) starts a pass and answers its run id; the results arrive as probe events.
func TestCapabilitiesTestStartsARun(t *testing.T) {
	e := newEnv(t, false)
	cards := qsvCards()
	cards.run = "7"
	e.cards = cards
	e.ready()
	cookie := e.setup()

	rec := e.do(http.MethodPost, "/api/v2/workers/local/capabilities/test", "", write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("test = %d %s; want 200", rec.Code, rec.Body)
	}
	var got workerTestJSON
	decodeInto(t, rec, &got)
	if got.RunID != "7" {
		t.Errorf("runId = %q, want 7", got.RunID)
	}
	if len(cards.tested) != 1 || cards.tested[0] != workers.LocalID {
		t.Errorf("tested = %v; want one run for the local worker", cards.tested)
	}
}

// After the run, the page reads the run's result: the encoder that now works is offered again.
func TestCapabilitiesTestUpdatesWhatThePageReads(t *testing.T) {
	e := newEnv(t, false)
	cards := qsvCards()
	hevc := hw.Candidate{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc", Device: "/dev/dri/renderD128"}
	h264 := hw.Candidate{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264", Device: "/dev/dri/renderD128"}
	cards.after = &hw.Answer{Inventory: hw.Inventory{Verdicts: []hw.Verdict{
		{Candidate: hevc, Available: true},
		{Candidate: h264, Available: true},
	}}}
	e.cards = cards
	e.ready()
	cookie := e.setup()

	if rec := e.do(http.MethodPost, "/api/v2/workers/local/capabilities/test", "", write(withSession(cookie)), false); rec.Code != http.StatusOK {
		t.Fatalf("test = %d %s; want 200", rec.Code, rec.Body)
	}
	var detail workerDetailJSON
	e.getWorkerJSON(t, cookie, "/api/v2/workers/local", &detail)
	if len(detail.Encoders) != 2 || !detail.Encoders[1].TestOk {
		t.Errorf("encoders after the run = %+v; want h264_qsv working as the run found it", detail.Encoders)
	}
}

// A second click while a run is going is refused with a sentence: the first run's results are still coming.
func TestCapabilitiesTestWhileRunningIsRefused(t *testing.T) {
	e := newEnv(t, false)
	cards := qsvCards()
	cards.run = "" // a pass is going, and it has no run id to hand out
	e.cards = cards
	e.ready()
	cookie := e.setup()

	rec := e.do(http.MethodPost, "/api/v2/workers/local/capabilities/test", "", write(withSession(cookie)), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusConflict || code != "worker_busy" {
		t.Errorf("second click = %d %q; want 409 worker_busy", rec.Code, code)
	}
}

// An unknown worker is page 10's 404, and nothing is tested.
func TestCapabilitiesTestUnknownWorker(t *testing.T) {
	e := newEnv(t, false)
	cards := qsvCards()
	e.cards = cards
	e.ready()
	cookie := e.setup()

	rec := e.do(http.MethodPost, "/api/v2/workers/nope/capabilities/test", "", write(withSession(cookie)), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "worker_not_found" {
		t.Errorf("unknown worker = %d %q; want 404 worker_not_found", rec.Code, code)
	}
	if len(cards.tested) != 0 {
		t.Errorf("tested = %v; want nothing for an unknown worker", cards.tested)
	}
}

// A remote worker's encoders cannot be tested from this computer (they come in stage 4), so it is refused.
func TestCapabilitiesTestRemoteWorkerIsOffline(t *testing.T) {
	e := newEnv(t, false)
	cards := qsvCards()
	e.cards = cards
	e.ready()
	cookie := e.setup()
	e.exec(t, `INSERT INTO workers(id, name, address, local, slots, gpu_slots)
		VALUES('studio', 'Studio', '10.0.0.5', 0, 1, 0)`)

	rec := e.do(http.MethodPost, "/api/v2/workers/studio/capabilities/test", "", write(withSession(cookie)), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusConflict || code != "worker_offline" {
		t.Errorf("remote worker = %d %q; want 409 worker_offline", rec.Code, code)
	}
	if len(cards.tested) != 0 {
		t.Errorf("tested = %v; want nothing for a worker that isn't here", cards.tested)
	}
}

// A machine whose detector could not be built has nothing to run the tests, and says so rather than failing.
func TestCapabilitiesTestWithoutADetector(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	rec := e.do(http.MethodPost, "/api/v2/workers/local/capabilities/test", "", write(withSession(cookie)), false)
	if code, _, _ := errorBody(t, rec); rec.Code != http.StatusConflict || code != "encoder_test_unavailable" {
		t.Errorf("no detector = %d %q; want 409 encoder_test_unavailable", rec.Code, code)
	}
}
