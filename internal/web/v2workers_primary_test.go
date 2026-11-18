package web

import (
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
)

// Without a saved choice the main encoder is the first graphics encoder that passed, else the processor.
func TestPrimaryEncoderDefaultIsFirstPassedGraphics(t *testing.T) {
	both := capabilities(doctor.Tools{Hardware: []string{"hevc_qsv", "hevc_nvenc"}}, true)
	if got := primaryEncoder(nil, both); got != "hevc_qsv" {
		t.Errorf("two passed: default = %q, want hevc_qsv", got)
	}
	none := capabilities(doctor.Tools{Software: []string{"libx265"}, HardwareListed: []string{"hevc_qsv"}}, true)
	if got := primaryEncoder(nil, none); got != "cpu" {
		t.Errorf("none passed: default = %q, want cpu", got)
	}
}

// A saved graphics encoder is used only while its test passes; cpu is always allowed.
func TestPrimaryEncoderSavedWinsOnlyWhilePassing(t *testing.T) {
	caps := capabilities(doctor.Tools{Hardware: []string{"hevc_qsv", "hevc_nvenc"}, HardwareListed: []string{"hevc_vaapi"}}, true)
	pick := func(s string) *string { return &s }
	if got := primaryEncoder(pick("hevc_nvenc"), caps); got != "hevc_nvenc" {
		t.Errorf("saved passing encoder: got %q, want hevc_nvenc", got)
	}
	if got := primaryEncoder(pick("hevc_vaapi"), caps); got != "hevc_qsv" {
		t.Errorf("saved failing encoder: got %q, want the default hevc_qsv", got)
	}
	if got := primaryEncoder(pick("hevc_amf"), caps); got != "hevc_qsv" {
		t.Errorf("saved unknown encoder: got %q, want the default hevc_qsv", got)
	}
	if got := primaryEncoder(pick("cpu"), caps); got != "cpu" {
		t.Errorf("saved cpu: got %q, want cpu", got)
	}
}

// PATCH takes cpu or a passing graphics encoder. A graphics encoder that failed is refused with 409
// encoder_not_working; any other name (a processor encoder, an unknown one) with 400 invalid_encoder.
// Either refusal changes nothing.
func TestPatchPrimaryEncoderRefusesFailedOrUnknown(t *testing.T) {
	srv, _ := workersServer(t)
	if code, b, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/workers/local/capabilities/test", body: "{}", ctype: "application/json"}); code != 200 {
		t.Fatalf("capability test %d %s", code, b)
	}
	primary := func() string {
		_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workers/local"})
		return decode[struct {
			PrimaryEncoder string `json:"primaryEncoder"`
		}](t, b).PrimaryEncoder
	}
	patch := func(enc string) (int, string) {
		code, b, _ := do(t, nil, srv, call{method: "PATCH", path: "/api/v2/workers/local", ctype: "application/json", body: `{"primaryEncoder":"` + enc + `"}`})
		return code, b
	}

	if got := primary(); got != "hevc_nvenc" {
		t.Fatalf("default primary = %q, want hevc_nvenc (the only passing graphics encoder)", got)
	}
	for _, enc := range []string{"hevc_vaapi", "hevc_amf"} {
		code, b := patch(enc)
		want := encoderLabels[enc]
		if want == "" {
			want = enc
		}
		if code != 409 || errCode(b) != "encoder_not_working" {
			t.Errorf("PATCH %s: %d %s, want 409 encoder_not_working", enc, code, b)
		}
		if msg := want + " isn't working on this worker, so it can't be the main encoder."; !strings.Contains(b, msg) {
			t.Errorf("PATCH %s message: %s, want %q", enc, b, msg)
		}
	}
	for _, enc := range []string{"libx265", "not_an_encoder", ""} {
		code, b := patch(enc)
		if code != 400 || errCode(b) != "invalid_encoder" {
			t.Errorf("PATCH %q: %d %s, want 400 invalid_encoder", enc, code, b)
		}
		if msg := "The main encoder is cpu or a graphics encoder that passed its test. Nothing was changed."; !strings.Contains(b, msg) {
			t.Errorf("PATCH %q message: %s", enc, b)
		}
	}
	if got := primary(); got != "hevc_nvenc" {
		t.Errorf("after refusals primary = %q, want it unchanged", got)
	}
	if code, b := patch("cpu"); code != 200 {
		t.Fatalf("PATCH cpu: %d %s", code, b)
	}
	if got := primary(); got != "cpu" {
		t.Errorf("after cpu: primary = %q", got)
	}
	if code, b := patch("hevc_nvenc"); code != 200 {
		t.Fatalf("PATCH hevc_nvenc: %d %s", code, b)
	}
	if got := primary(); got != "hevc_nvenc" {
		t.Errorf("after nvenc: primary = %q", got)
	}
}
