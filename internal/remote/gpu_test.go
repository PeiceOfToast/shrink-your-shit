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

	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// recordExec is a converter that records each ffmpeg call's arguments; fail says which call fails.
type recordExec struct {
	calls [][]string
	fail  func(args []string) bool
	msg   string // what a failing call prints (default: a graphics encoder's device error)
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
		if x.msg != "" {
			return x.msg, errors.New("exit status 1")
		}
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
	worked   int // the structured "a graphics encoder converted a file" notes it was sent
	// converting is each converting report as the server received it
	converting []map[string]any
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
			if in["encoderWorked"] == true {
				rec.worked++
			}
			text, _ := in["text"].(string)
			if text != "" { // a note with no text is a structured signal (a graphics encoder's success), not a job-log line
				rec.notes = append(rec.notes, text)
			}
		case strings.HasSuffix(r.URL.Path, "/progress") && in["stage"] == "converting":
			enc, _ := in["encoder"].(string)
			rec.encoders = append(rec.encoders, enc)
			rec.converting = append(rec.converting, in)
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
	rec, err := convertH264Rec(t, primary, caps, x, copyVideo)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return rec.notes, rec.encoders, err
}

// convertH264Rec is convertH264 with the server's record of what the worker sent.
func convertH264Rec(t *testing.T, primary *string, caps []map[string]any, x *recordExec, copyVideo bool) (*recorder, error) {
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
	_, _, _, err := c.convert(context.Background(), cv, l, rules.Snapshot{Plan: plan}, "/in.mkv", out)
	return rec, err
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
	chosen := "Chosen: Intel Quick Sync (graphics), H.264 — h264_qsv passed its test on this computer and can do this file. Settings: Encoder: h264_qsv"
	if len(notes) != 1 || !strings.HasPrefix(notes[0], chosen) || !slices.Equal(encs, []string{"h264_qsv"}) {
		t.Errorf("notes %q, encoders %q: want only the Chosen line and h264_qsv reported", notes, encs)
	}
}

// A server's 10-bit SDR source needs this worker's 10-bit test too, not only an HDR one: an 8-bit-only encoder
// is not used, so the processor with the job-log note (its 8-bit file would be refused after the whole encode).
func TestRemoteWorkerTenBitSDRSourceFallsBackToTheProcessor(t *testing.T) {
	plan := rules.Plan{Video: rules.VideoEncode{Codec: "libx264", CRF: 20, Preset: "medium"}}
	source := &media.Info{Streams: []media.Stream{{Type: "video", Codec: "h264", BitDepth: 10}}} // SDR: no HDR plan
	caps := []map[string]any{encoderCap("h264_qsv", "passed")}
	args, enc, note := encoderArgs(plan, ptr("hevc_qsv"), caps, source, "/in.mkv", "/out.mkv")
	if enc != "" || !slices.Contains(args, "libx264") || slices.Contains(args, "h264_qsv") {
		t.Errorf("args %v, encoder %q: want the processor", args, enc)
	}
	if want := "this file is 10-bit: h264_qsv didn't pass its 10-bit test here; using the processor"; note != want {
		t.Errorf("note %q, want %q", note, want)
	}
	// The same source with a passed 10-bit test uses the graphics encoder.
	caps[0]["tenBit"] = true
	if _, enc, note := encoderArgs(plan, ptr("hevc_qsv"), caps, source, "/in.mkv", "/out.mkv"); enc != "h264_qsv" || note != "" {
		t.Errorf("both tests passed: encoder %q note %q, want h264_qsv", enc, note)
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
	chosen := "Chosen: x264 (processor). Settings: Encoder: libx264" // the note above said why; not repeated
	if len(notes) != 2 || notes[0] != want || !strings.HasPrefix(notes[1], chosen) || !slices.Equal(encs, []string{""}) {
		t.Errorf("notes %q, encoders %q: want %q, then the Chosen line, and the processor reported", notes, encs, want)
	}
}

// A main encoder of cpu is the processor, with no reason note: only the Chosen line.
func TestRemoteWorkerCPUMainIsTheProcessorWithNoNote(t *testing.T) {
	x := &recordExec{}
	notes, _, err := convertH264(t, ptr("cpu"), []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "libx264") || len(notes) != 1 || !strings.HasPrefix(notes[0], "Chosen: x264 (processor). Settings: Encoder: libx264") {
		t.Errorf("cpu: args %v, notes %q", x.calls, notes)
	}
}

// An older server that sends no main encoder: the processor, with no reason note: only the Chosen line.
func TestRemoteWorkerWithoutAMainEncoderIsTheProcessor(t *testing.T) {
	x := &recordExec{}
	notes, _, err := convertH264(t, nil, []map[string]any{encoderCap("h264_qsv", "passed")}, x, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.calls) != 1 || !slices.Contains(x.calls[0], "libx264") || len(notes) != 1 || !strings.HasPrefix(notes[0], "Chosen: x264 (processor). Settings: Encoder: libx264") {
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
	// The job log: the Chosen line before the encode (the graphics encoder), then the step-down line.
	chosen := "Chosen: Intel Quick Sync (graphics), H.264 — h264_qsv passed its test on this computer and can do this file. Settings: Encoder: h264_qsv"
	fell := "h264_qsv failed (Device creation failed); converted on the processor"
	if len(notes) != 2 || !strings.HasPrefix(notes[0], chosen) || notes[1] != fell {
		t.Errorf("notes %q, want the Chosen line then %q", notes, fell)
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
