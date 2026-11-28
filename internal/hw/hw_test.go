// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media"
	"github.com/PeiceOfToast/shrink-your-shit/internal/media/fake"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// outFor is where Prove writes its test encode for c in dir.
func outFor(dir string, c Candidate) string { return filepath.Join(dir, c.Encoder+".test.mkv") }

// An encoder counts only when the test output's ENCODER tag names it.
func TestEncoderCountsOnlyWithItsTag(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc"}
	cases := []struct {
		name, tag string
		want      bool
	}{
		{"the right tag", "hevc_qsv", true},
		{"the tag as real encoders write it", "Lavc61.3.100 hevc_qsv", true},
		{"a longer encoder name is not a match", "Lavc61.3.100 hevc_qsv_extra", false},
		{"a Lavc tag naming another encoder", "Lavc61.3.100 h264_qsv", false},
		{"another encoder's tag", "h264_qsv", false},
		{"no tag", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &fake.Media{Probes: map[string]media.Info{
				outFor(dir, c): {Video: []media.VideoTrack{{Codec: "hevc", Encoder: tc.tag, Width: 256, Height: 256}}},
			}}
			v := Prove(context.Background(), m, dir, c)
			if v.Available != tc.want {
				t.Fatalf("available = %v (reason %q), want %v", v.Available, v.Reason, tc.want)
			}
			if !tc.want && !strings.Contains(v.Reason, "written by") {
				t.Errorf("reason = %q, want it to say which encoder wrote the output", v.Reason)
			}
		})
	}
}

// A test encode that hangs times out and reads as not available, with the reason in plain words.
func TestHungTestEncodeTimesOut(t *testing.T) {
	defer func(d time.Duration) { TestTimeout = d }(TestTimeout)
	TestTimeout = 50 * time.Millisecond
	dir := t.TempDir()
	c := Candidate{Encoder: "h264_nvenc", Family: encode.FamilyNVENC, Codec: "h264"}
	m := &fake.Media{Runs: []fake.RunScript{{Hang: true}}}
	v := Prove(context.Background(), m, dir, c)
	if v.Available || !strings.Contains(v.Reason, "did not finish") {
		t.Fatalf("verdict = %+v; want not available, did not finish", v)
	}
}

// A card whose driver refuses the encode reads as not available, with the plain reason.
func TestRefusedQSVReadsInPlainWords(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "av1_qsv", Family: encode.FamilyQSV, Codec: "av1"}
	m := &fake.Media{Runs: []fake.RunScript{{Err: errString("Error creating a MFX session: -9")}}}
	v := Prove(context.Background(), m, dir, c)
	if v.Available || v.Reason != "no QSV runtime for this chip on this system" {
		t.Fatalf("verdict = %+v; want the QSV runtime reason", v)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// VAAPI never uses NVIDIA; Intel or AMD first; otherwise the first node; otherwise renderD128.
func TestVAAPIRenderNodeChoice(t *testing.T) {
	cases := []struct {
		name  string
		nodes []RenderNode
		want  string
	}{
		{"skips NVIDIA", []RenderNode{{"/dev/dri/renderD128", vendorNVIDIA}, {"/dev/dri/renderD129", vendorIntel}}, "/dev/dri/renderD129"},
		{"AMD", []RenderNode{{"/dev/dri/renderD128", vendorAMD}}, "/dev/dri/renderD128"},
		{"only NVIDIA", []RenderNode{{"/dev/dri/renderD128", vendorNVIDIA}}, fallbackNode},
		{"no nodes", nil, fallbackNode},
		{"unknown vendor takes the first", []RenderNode{{"/dev/dri/renderD130", "0x1234"}}, "/dev/dri/renderD130"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ChooseRenderNode(tc.nodes); got != tc.want {
				t.Errorf("ChooseRenderNode = %q, want %q", got, tc.want)
			}
		})
	}
}

// Render nodes come from a sysfs folder with the vendor of each card; NVIDIA nodes get no VAAPI candidate.
func TestReadRenderNodesAndCandidates(t *testing.T) {
	sys := t.TempDir()
	devDri := t.TempDir()
	for name, vendor := range map[string]string{"renderD128": "0x8086\n", "renderD129": "0x10de\n"} {
		if err := os.WriteFile(filepath.Join(devDri, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		dev := filepath.Join(sys, name, "device")
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dev, "vendor"), []byte(vendor), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes := ReadRenderNodes(sys, devDri)
	if len(nodes) != 2 || nodes[0].Path != "/dev/dri/renderD128" || nodes[0].Vendor != vendorIntel {
		t.Fatalf("nodes = %+v", nodes)
	}
	vaapi := 0
	for _, c := range Candidates(nodes) {
		if c.Family == encode.FamilyVAAPI {
			vaapi++
			if c.Device == "/dev/dri/renderD129" {
				t.Errorf("an NVIDIA node got a VAAPI candidate: %+v", c)
			}
		}
	}
	if vaapi != 3 { // hevc, h264, av1 on the one Intel node
		t.Errorf("VAAPI candidates = %d, want 3", vaapi)
	}
}

// Detection does not block: Start returns at once even when a test encode hangs, and the last answer is served
// until the pass ends.
func TestDetectionDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	c := Candidate{Encoder: "hevc_nvenc", Family: encode.FamilyNVENC, Codec: "hevc"}
	m := &fake.Media{Runs: []fake.RunScript{{Hang: true}}}
	ctx, cancel := context.WithCancel(context.Background())
	d := NewDetector(m, dir, []Candidate{c}, nil)

	began := time.Now()
	d.Start(ctx)
	if time.Since(began) > 50*time.Millisecond {
		t.Fatalf("Start took %s; it must return at once", time.Since(began))
	}
	if _, ok := d.Current(); ok {
		t.Fatal("an answer is served before any pass has finished")
	}
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.mu.Lock()
		running := d.running
		d.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the pass did not finish after its context ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A cancelled pass is incomplete: with no finished pass before it, nothing is served.
	if inv, ok := d.Current(); ok {
		t.Errorf("a cancelled pass was served as an answer: %+v", inv)
	}
}

// Pauses: three failures in a row pause the family for an hour, a success resets the count, and a pause survives a
// restart because it lives in the store.
func TestPauseAfterThreeFailuresResetsOnSuccessAndSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	p := NewPauses(db, clock, nil)
	ctx := context.Background()

	for i := 0; i < FailLimit-1; i++ {
		if started, err := p.Record(ctx, "local", encode.FamilyQSV, false); err != nil || started {
			t.Fatalf("failure %d: started %v, err %v; want no pause yet", i+1, started, err)
		}
	}
	if started, err := p.Record(ctx, "local", encode.FamilyQSV, true); err != nil || started {
		t.Fatalf("success: started %v, err %v", started, err)
	}
	// The count was reset, so two more failures still do not pause.
	for i := 0; i < FailLimit-1; i++ {
		if started, _ := p.Record(ctx, "local", encode.FamilyQSV, false); started {
			t.Fatal("a pause started after a success reset the count")
		}
	}
	started, err := p.Record(ctx, "local", encode.FamilyQSV, false)
	if err != nil || !started {
		t.Fatalf("the third failure in a row: started %v, err %v; want a pause", started, err)
	}
	until, paused, err := p.Paused(ctx, "local", encode.FamilyQSV)
	if err != nil || !paused || !until.Equal(now.Add(PauseFor)) {
		t.Fatalf("paused = %v until %v (%v); want paused for an hour", paused, until, err)
	}
	if _, other, _ := p.Paused(ctx, "other-worker", encode.FamilyQSV); other {
		t.Error("counts are per worker, but another worker is paused")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Restart: a new store over the same folder still knows the pause.
	db2, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if err := db2.Migrate(); err != nil {
		t.Fatal(err)
	}
	p2 := NewPauses(db2, clock, nil)
	if _, paused, err := p2.Paused(ctx, "local", encode.FamilyQSV); err != nil || !paused {
		t.Fatalf("after a restart: paused = %v, err %v; want still paused", paused, err)
	}
	later := now.Add(PauseFor + time.Minute)
	p3 := NewPauses(db2, func() time.Time { return later }, nil)
	if _, paused, _ := p3.Paused(ctx, "local", encode.FamilyQSV); paused {
		t.Error("the pause still holds after its hour")
	}
}

// eventSink collects what a detector emits.
type eventSink struct {
	mu   sync.Mutex
	seen []events.Event
}

func (s *eventSink) Emit(e events.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, e)
	return nil
}

// probes is the probe events collected so far.
func (s *eventSink) probes() []events.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []events.Event
	for _, e := range s.seen {
		if e.Kind == events.KindProbe {
			out = append(out, e)
		}
	}
	return out
}

// waitProbes waits until the sink has n probe events, and returns them.
func waitProbes(t *testing.T, s *eventSink, n int) []events.Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := s.probes()
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe events = %d, want %d", len(got), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A run the person asked for says where it stands (page 10): running with its id from the moment it starts, then
// finished with the time. A re-test that finds the same thing as before is still a new, finished run, so a page
// polling for its own run id sees it end.
func TestTestRunReportsRunningThenFinished(t *testing.T) {
	defer func(d time.Duration) { TestTimeout = d }(TestTimeout)
	TestTimeout = 100 * time.Millisecond // every test hangs, so a run's tests end only at their timeout
	dir := t.TempDir()
	m := &fake.Media{Runs: []fake.RunScript{{Hang: true}}}
	d := NewDetector(m, dir, []Candidate{{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"}}, nil)
	if st, ok := d.Test(); ok {
		t.Fatalf("Test before any run = %+v, %v; want nothing yet", st, ok)
	}
	first := d.TestRun("local")
	if st, ok := d.Test(); !ok || st.RunID != first || !st.Running || !st.FinishedAt.IsZero() {
		t.Fatalf("Test while the first run goes = %+v, %v; want run %q running with no finish time", st, ok, first)
	}
	finished := waitTest(t, d)
	if finished.RunID != first || finished.Running || finished.FinishedAt.IsZero() {
		t.Fatalf("Test after the first run = %+v; want run %q finished with a time", finished, first)
	}
	second := d.TestRun("local")
	if second == "" || second == first {
		t.Fatalf("the second run's id = %q; want a new id, not %q", second, first)
	}
	if st, _ := d.Test(); st.RunID != second || !st.Running || !st.FinishedAt.IsZero() {
		t.Fatalf("Test while the re-test goes = %+v; want run %q running", st, second)
	}
	again := waitTest(t, d)
	if again.RunID != second || again.Running || again.FinishedAt.IsZero() || again.FinishedAt.Before(finished.FinishedAt) {
		t.Fatalf("Test after the re-test = %+v; want the new run %q finished, no earlier than the first", again, second)
	}
	// The re-test found the same thing as the first run, and that is still what the page reads.
	if inv := waitInventory(t, d); len(inv.Verdicts) != 1 || inv.Verdicts[0].Available {
		t.Errorf("the re-test's answer = %+v; want the same one failed encoder", inv)
	}
}

// Test the encoders re-runs the same pass for the worker the person asked about (page 10): it answers with a run id
// at once, reports each encoder as one probe event of that run, scoped to the worker, and leaves the new answer as
// what the Workers page reads.
func TestTestRunReportsEachEncoderAsAProbe(t *testing.T) {
	dir := t.TempDir()
	cands := []Candidate{
		{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"},
		{Encoder: "av1_qsv", Family: encode.FamilyQSV, Codec: "av1"},
	}
	m := &fake.Media{
		Runs: []fake.RunScript{{}, {Err: errString("Error creating a MFX session: -9")}},
		Probes: map[string]media.Info{
			outFor(dir, cands[0]): {Video: []media.VideoTrack{{Codec: "h264", Encoder: "Lavc61 h264_qsv"}}},
		},
	}
	sink := &eventSink{}
	d := NewDetector(m, dir, cands, sink)
	id := d.TestRun("local")
	if id == "" {
		t.Fatal("TestRun returned no run id")
	}
	got := waitProbes(t, sink, 2)
	want := []struct {
		encoder string
		ok      bool
	}{{"h264_qsv", true}, {"av1_qsv", false}}
	for i, w := range want {
		e := got[i]
		if e.Source != "hw" || e.WorkerID != "local" || e.Level != events.LevelInfo {
			t.Errorf("probe %d = %+v; want source hw, worker local, level info", i, e)
		}
		if e.Data["runId"] != id || e.Data["encoder"] != w.encoder || e.Data["available"] != w.ok {
			t.Errorf("probe %d data = %v; want runId %q, encoder %q, available %v", i, e.Data, id, w.encoder, w.ok)
		}
	}
	if !strings.Contains(got[0].Message, "passed its test") || !strings.Contains(got[1].Message, "no QSV runtime") {
		t.Errorf("messages = %q, %q; want each encoder's result in words", got[0].Message, got[1].Message)
	}
	// The finished pass is the answer from now on: the worker's capabilities follow it.
	waitInventory(t, d)
	ans, ok := d.Current()
	if !ok || len(ans.Inventory.Verdicts) != 2 || !ans.Inventory.Verdicts[0].Available || ans.Inventory.Verdicts[1].Available {
		t.Fatalf("Current after the run = %+v (ok %v); want the run's two results", ans, ok)
	}
	if next := d.TestRun("local"); next == "" || next == id {
		t.Errorf("the second run's id = %q; want a new id, not %q", next, id)
	}
	waitIdle(t, d)
}

// One run at a time per worker: a second ask while a run is going is that run, so two clicks do not test twice.
func TestTestRunWhileRunningIsTheSameRun(t *testing.T) {
	defer func(d time.Duration) { TestTimeout = d }(TestTimeout)
	TestTimeout = 100 * time.Millisecond
	dir := t.TempDir()
	m := &fake.Media{Runs: []fake.RunScript{{Hang: true}}}
	d := NewDetector(m, dir, []Candidate{{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"}}, nil)
	first := d.TestRun("local")
	second := d.TestRun("local")
	if first == "" || second != first {
		t.Fatalf("run ids = %q then %q; want the one run while it is going", first, second)
	}
	// Let the hung test time out, so no goroutine outlives the test.
	waitIdle(t, d)
}

// A run's successes are the pause rules' own success: the failure count starts again. A failed test is not a job
// attempt, so it does not count toward a pause either (internal/hw/pause.go: FailLimit failures in a row).
func TestTestRunFeedsThePauseRules(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	p := NewPauses(db, func() time.Time { return now }, nil)
	ctx := context.Background()

	// Two failures, then a passing test: the count starts again, so the next failure is only the first in a row. With
	// no reset it would be the third, and pause — this is what makes the check bite.
	for i := 0; i < FailLimit-1; i++ {
		if _, err := p.Record(ctx, "local", encode.FamilyQSV, false); err != nil {
			t.Fatal(err)
		}
	}
	m := &fake.Media{
		Runs: []fake.RunScript{{}},
		Probes: map[string]media.Info{
			outFor(dir, Candidate{Encoder: "h264_qsv"}): {Video: []media.VideoTrack{{Codec: "h264", Encoder: "Lavc61 h264_qsv"}}},
		},
	}
	cands := []Candidate{{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"}}
	d := NewDetector(m, dir, cands, nil).WithPauses(p)
	if d.TestRun("local") == "" {
		t.Fatal("TestRun returned no run id")
	}
	waitInventory(t, d)
	if started, err := p.Record(ctx, "local", encode.FamilyQSV, false); err != nil || started {
		t.Fatalf("after the passing test, the next failure: started %v, err %v; want the count reset", started, err)
	}

	// One failure on another family, then a failing test: the test is no attempt, so the next failure is the second
	// in a row and still no pause.
	if _, err := p.Record(ctx, "local", encode.FamilyNVENC, false); err != nil {
		t.Fatal(err)
	}
	bad := &fake.Media{Runs: []fake.RunScript{{Err: errString("no NVENC")}}}
	dc := []Candidate{{Encoder: "hevc_nvenc", Family: encode.FamilyNVENC, Codec: "hevc"}}
	d2 := NewDetector(bad, dir, dc, nil).WithPauses(p)
	if d2.TestRun("local") == "" {
		t.Fatal("TestRun returned no run id")
	}
	waitInventory(t, d2)
	if started, err := p.Record(ctx, "local", encode.FamilyNVENC, false); err != nil || started {
		t.Fatalf("after the failing test, the next failure: started %v, err %v; want the test not counted", started, err)
	}
}

// A pass cancelled during a test encode drops that encode's answer and keeps no partial pass: the last finished pass
// stays what Current serves.
func TestCancelledPassKeepsTheLastFinishedPass(t *testing.T) {
	dir := t.TempDir()
	cands := []Candidate{
		{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"},
		{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc"},
	}
	m := &fake.Media{
		// Calls 0 and 1 are the first pass's two encodes, which pass; call 2 onward hangs until cancelled.
		Runs: []fake.RunScript{{}, {}, {Hang: true}},
		Probes: map[string]media.Info{
			outFor(dir, cands[0]): {Video: []media.VideoTrack{{Codec: "h264", Encoder: "Lavc61 h264_qsv"}}},
			outFor(dir, cands[1]): {Video: []media.VideoTrack{{Codec: "hevc", Encoder: "Lavc61 hevc_qsv"}}},
		},
	}
	d := NewDetector(m, dir, cands, nil)
	d.Start(context.Background())
	first := waitInventory(t, d)
	if len(first.Verdicts) != 2 || !first.Verdicts[0].Available || !first.Verdicts[1].Available {
		t.Fatalf("first pass = %+v; want two available", first.Verdicts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.Start(ctx)
	time.Sleep(30 * time.Millisecond) // the second pass is now in its hung encode
	cancel()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.mu.Lock()
		running := d.running
		d.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the cancelled pass did not end")
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, ok := d.Current()
	if !ok || !got.Inventory.At.Equal(first.At) || len(got.Inventory.Verdicts) != 2 {
		t.Fatalf("after a cancelled pass Current = %+v (ok %v); want the first finished pass", got, ok)
	}
}

// Detect drops only the test that was in flight when the pass was cancelled; the finished tests before it are kept.
func TestDetectDropsTheEncodeInFlightWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	cands := []Candidate{
		{Encoder: "h264_qsv", Family: encode.FamilyQSV, Codec: "h264"},
		{Encoder: "hevc_qsv", Family: encode.FamilyQSV, Codec: "hevc"},
	}
	m := &fake.Media{
		Runs:   []fake.RunScript{{}, {Hang: true}},
		Probes: map[string]media.Info{outFor(dir, cands[0]): {Video: []media.VideoTrack{{Codec: "h264", Encoder: "Lavc61 h264_qsv"}}}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	inv := Detect(ctx, m, dir, cands)
	if len(inv.Verdicts) != 1 || inv.Verdicts[0].Encoder != "h264_qsv" {
		t.Fatalf("verdicts = %+v; want only the finished h264_qsv test", inv.Verdicts)
	}
}

// A multi-word tag must start with a Lavc version, so another program's words never pass as an encoder name.
func TestMultiWordTagMustStartWithLavc(t *testing.T) {
	if namedBy("x264 hevc_qsv", "hevc_qsv") {
		t.Error("a multi-word tag without a Lavc version was accepted")
	}
	if !namedBy("Lavc61.3.100 hevc_qsv", "hevc_qsv") {
		t.Error("the real Lavc form was refused")
	}
}

// waitIdle waits for the detector's pass to end, so a test that started a run leaves no goroutine reading the package
// knobs (TestTimeout) while the next test writes them.
func waitIdle(t *testing.T, d *Detector) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d.mu.Lock()
		running := d.running
		d.mu.Unlock()
		if !running {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pass did not end")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitTest waits for the latest run the person asked for to finish and returns its state.
func waitTest(t *testing.T, d *Detector) TestState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if st, ok := d.Test(); ok && !st.Running {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatal("the run did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitInventory waits for a finished pass and returns it.
func waitInventory(t *testing.T, d *Detector) Inventory {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if ans, ok := d.Current(); ok {
			return ans.Inventory
		}
		if time.Now().After(deadline) {
			t.Fatal("no pass finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// N1: a render node that is in sysfs but has no device node here is left out: the existence check is what decides.
func TestRenderNodeWithoutADeviceNodeIsLeftOut(t *testing.T) {
	sys := t.TempDir()
	devDri := t.TempDir()
	for _, name := range []string{"renderD128", "renderD130"} {
		dev := filepath.Join(sys, name, "device")
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dev, "vendor"), []byte("0x8086\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(devDri, "renderD128"), nil, 0o644); err != nil { // renderD130 has no device node
		t.Fatal(err)
	}
	nodes := ReadRenderNodes(sys, devDri)
	if len(nodes) != 1 || nodes[0].Path != "/dev/dri/renderD128" {
		t.Fatalf("nodes = %+v; want only renderD128, the one with a device node", nodes)
	}
}
