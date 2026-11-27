// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package hw

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/encode"
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
	for name, vendor := range map[string]string{"renderD128": "0x8086\n", "renderD129": "0x10de\n"} {
		dev := filepath.Join(sys, name, "device")
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dev, "vendor"), []byte(vendor), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	nodes := ReadRenderNodes(sys)
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
		inv, ok := d.Current()
		if ok {
			if len(inv.Verdicts) != 0 {
				t.Errorf("a cancelled pass kept verdicts: %+v", inv.Verdicts)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the pass did not finish after its context ended")
		}
		time.Sleep(10 * time.Millisecond)
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
