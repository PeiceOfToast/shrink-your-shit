// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// recordExec is a converter that records each ffmpeg call's arguments; fail says which call fails.
type recordExec struct {
	calls [][]string
	fail  func(args []string) bool
}

func (x *recordExec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return nil, nil
}

func (x *recordExec) Capture(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	return nil, nil, nil
}

func (x *recordExec) Stream(ctx context.Context, onLine func(string), name string, args ...string) (string, error) {
	x.calls = append(x.calls, args)
	if x.fail != nil && x.fail(args) {
		return "Device creation failed", errors.New("exit status 1")
	}
	return "", nil
}

// recorder is the server a remote worker talks to in these tests: its job-log notes and the encoder each
// converting report names.
type recorder struct {
	mu       sync.Mutex
	notes    []string
	encoders []string
}

func recordingServer(t *testing.T) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		rec.mu.Lock()
		defer rec.mu.Unlock()
		switch {
		case strings.HasSuffix(r.URL.Path, "/note"):
			text, _ := in["text"].(string)
			rec.notes = append(rec.notes, text)
		case strings.HasSuffix(r.URL.Path, "/progress") && in["stage"] == "converting":
			enc, _ := in["encoder"].(string)
			rec.encoders = append(rec.encoders, enc)
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func encoderCap(name, test string) map[string]any {
	return map[string]any{"key": "encoder:" + name, "kind": "encoder", "slot": "gpu", "test": test}
}

// convertH264 converts an H.264 plan on a worker whose report is caps, with the server's main encoder primary
// (nil: an older server that sends none), and returns the job-log notes, the encoder each converting report
// named, and the error.
func convertH264(t *testing.T, primary *string, caps []map[string]any, x *recordExec, copyVideo bool) (notes, encs []string, err error) {
	t.Helper()
	srv, rec := recordingServer(t)
	c := &Client{Server: srv.URL, Version: "test", Caps: caps}
	l := &Lease{ID: "L1", Primary: primary}
	cv := Converter{Exec: x, FFmpeg: "ffmpeg", FFprobe: "ffprobe", Work: t.TempDir()}
	plan := rules.Plan{Video: rules.VideoEncode{Codec: "libx264", CRF: 20, Preset: "medium", Copy: copyVideo}}
	out := filepath.Join(t.TempDir(), "out.mkv")
	if err := os.WriteFile(out, nil, 0o600); err != nil { // the staged file the encode writes into
		t.Fatal(err)
	}
	_, err = c.convert(context.Background(), cv, l, rules.Snapshot{Plan: plan}, "/in.mkv", out)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.notes, rec.encoders, err
}

func ptr(s string) *string { return &s }

// A remote worker whose main encoder is hevc_qsv, with h264_qsv passed in its own test, converts an H.264 plan
// with h264_qsv, and the progress says so.
func TestRemoteWorkerUsesItsMainEncoderForTheFormat(t *testing.T) {
	x := &recordExec{}
	notes, encs, err := convertH264(t, ptr("hevc_qsv"), []map[string]any{encoderCap("hevc_qsv", "passed"), encoderCap("h264_qsv", "passed")}, x, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "h264_qsv") || slices.Contains(x.calls[0], "libx264") {
		t.Errorf("ffmpeg args %v: want h264_qsv", x.calls)
	}
	if len(notes) != 0 || !slices.Equal(encs, []string{"h264_qsv"}) {
		t.Errorf("notes %q, encoders %q: want no note and h264_qsv reported", notes, encs)
	}
}

// Main encoder's format encoder didn't pass on this worker: the processor, with the job-log note.
func TestRemoteWorkerNotPassedFallsBackToTheProcessor(t *testing.T) {
	x := &recordExec{}
	notes, encs, err := convertH264(t, ptr("hevc_qsv"), []map[string]any{encoderCap("hevc_qsv", "passed"), encoderCap("h264_qsv", "failed")}, x, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "libx264") || slices.Contains(x.calls[0], "h264_qsv") {
		t.Errorf("ffmpeg args %v: want the processor", x.calls)
	}
	want := "encoder h264_qsv didn't pass its test here; using the processor"
	if !slices.Equal(notes, []string{want}) || !slices.Equal(encs, []string{""}) {
		t.Errorf("notes %q, encoders %q: want %q and the processor reported", notes, encs, want)
	}
}

// A main encoder of cpu is the processor, with no note.
func TestRemoteWorkerCPUMainIsTheProcessorWithNoNote(t *testing.T) {
	x := &recordExec{}
	notes, _, err := convertH264(t, ptr("cpu"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "libx264") || len(notes) != 0 {
		t.Errorf("cpu: args %v, notes %q", x.calls, notes)
	}
}

// An older server that sends no main encoder: the processor, with no note.
func TestRemoteWorkerWithoutAMainEncoderIsTheProcessor(t *testing.T) {
	x := &recordExec{}
	notes, _, err := convertH264(t, nil, []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "libx264") || len(notes) != 0 {
		t.Errorf("no main: args %v, notes %q", x.calls, notes)
	}
}

// A graphics encode that fails converts again on the processor, as the local worker does; the progress from
// then on names the processor.
func TestRemoteWorkerGPUFailureConvertsOnTheProcessor(t *testing.T) {
	x := &recordExec{fail: func(args []string) bool { return slices.Contains(args, "h264_qsv") }}
	notes, encs, err := convertH264(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err != nil {
		t.Fatalf("the processor should have converted it: %v", err)
	}
	if len(x.calls) != 2 || !slices.Contains(x.calls[1], "libx264") {
		t.Fatalf("ffmpeg calls %v: want h264_qsv then the processor", x.calls)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "h264_qsv failed (Device creation failed); converted on the processor") {
		t.Errorf("notes %q", notes)
	}
	if len(encs) < 1 || encs[len(encs)-1] != "" {
		t.Errorf("encoders %q: want the processor last", encs)
	}
}

// A copied video (repackaged) is never encoded: no graphics encoder, no note.
func TestRemoteWorkerCopiedVideoIsNotEncoded(t *testing.T) {
	x := &recordExec{}
	notes, _, err := convertH264(t, ptr("hevc_qsv"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || slices.Contains(x.calls[0], "h264_qsv") || len(notes) != 0 {
		t.Errorf("copy: args %v, notes %q", x.calls, notes)
	}
}

// The lease carries the server's main encoder; a lease without one (an older server) leaves it nil.
func TestLeaseCarriesTheMainEncoder(t *testing.T) {
	for body, want := range map[string]*string{
		`{"lease":{"id":"L1","primaryEncoder":"hevc_qsv","job":{"id":"7"}}}`: ptr("hevc_qsv"),
		`{"lease":{"id":"L1","primaryEncoder":"cpu","job":{"id":"7"}}}`:      ptr("cpu"),
		`{"lease":{"id":"L1","job":{"id":"7"}}}`:                             nil,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		}))
		l, err := (&Client{Server: srv.URL, Version: "test"}).Lease(context.Background())
		srv.Close()
		if err != nil || l == nil {
			t.Fatalf("%s: lease %v %v", body, l, err)
		}
		switch {
		case want == nil && l.Primary != nil, want != nil && (l.Primary == nil || *l.Primary != *want):
			t.Errorf("%s: primary %v, want %v", body, l.Primary, want)
		}
	}
}
