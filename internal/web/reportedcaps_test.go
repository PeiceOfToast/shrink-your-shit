package web

import (
	"encoding/json"
	"strings"
	"testing"
)

// G1: what a remote worker may report about its encoder test: the list, or the empty object an older worker
// sends. Anything else is refused, so nothing unreadable is stored.
func TestReportedCaps(t *testing.T) {
	for _, in := range []string{"", "{}", "[]", `[{"key":"encoder:cpu","kind":"encoder","test":"passed"}]`} {
		if _, ok := reportedCaps(json.RawMessage(in)); !ok {
			t.Errorf("%q refused", in)
		}
	}
	if got, _ := reportedCaps(json.RawMessage("[]")); got != "[]" {
		t.Errorf("an empty list stored as %q", got)
	}
	if got, _ := reportedCaps(nil); got != "{}" {
		t.Errorf("no report stored as %q", got)
	}
	for _, in := range []string{`{"a":1}`, `"x"`, `[1]`, `null`, `[`, `[{"key":"x"}`} {
		if _, ok := reportedCaps(json.RawMessage(in)); ok {
			t.Errorf("%q accepted", in)
		}
	}
	big := "[" + strings.Repeat(`{"key":"encoder:x"},`, 500) + `{"key":"encoder:x"}]` // over 8 KiB
	if _, ok := reportedCaps(json.RawMessage(big)); ok {
		t.Error("a list over 8 KiB accepted")
	}
}

// A join from a worker that reports its encoder list is asked (202); a report that is neither is refused.
func TestJoinAcceptsTheEncoderList(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, b := joinFrom(srv, "POST", "/api/worker/v1/join",
		`{"name":"List PC","os":"linux","arch":"amd64","capabilities":[{"key":"encoder:cpu","kind":"encoder","test":"passed","slot":"cpu"}]}`,
		"", "192.168.1.21", false)
	if code != 202 {
		t.Fatalf("join with the encoder list: %d %s", code, b)
	}
	if code, b := joinFrom(srv, "POST", "/api/worker/v1/join", `{"name":"Bad PC","capabilities":"x"}`, "", "192.168.1.22", false); code != 400 {
		t.Errorf("join with a string report: %d %s", code, b)
	}
}
