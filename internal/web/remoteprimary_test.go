// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strings"
	"testing"
	"time"
)

// A remote worker's main encoder is chosen against its own report: a passed encoder is saved and shown on its
// card; one that didn't pass there is refused (409). Without a saved choice the card shows the default.
func TestRemoteMainEncoderIsChosenAgainstItsOwnReport(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	caps := `[{"key":"encoder:hevc_qsv","kind":"encoder","slot":"gpu","test":"passed"},` +
		`{"key":"encoder:hevc_nvenc","kind":"encoder","slot":"gpu","test":"failed"}]`
	code, _, _ := st.NewPairing(time.Now())
	rw, _, err := st.Pair(code, "Desktop", "1", "linux", "amd64", 1, caps, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v2/workers/" + rw.ID

	if c, b, _ := do(t, nil, srv, call{method: "GET", path: path}); c != 200 || !strings.Contains(b, `"primaryEncoder":"hevc_qsv"`) {
		t.Errorf("no saved choice: %d %s, want the default hevc_qsv", c, b)
	}
	if c, b, _ := do(t, nil, srv, call{method: "PATCH", path: path, ctype: "application/json", body: `{"primaryEncoder":"hevc_nvenc"}`}); c != 409 || errCode(b) != "encoder_not_working" {
		t.Errorf("PATCH a GPU that didn't pass on this worker: %d %s, want 409 encoder_not_working", c, b)
	}
	if c, b, _ := do(t, nil, srv, call{method: "PATCH", path: path, ctype: "application/json", body: `{"primaryEncoder":"hevc_qsv"}`}); c != 200 || !strings.Contains(b, `"primaryEncoder":"hevc_qsv"`) {
		t.Errorf("PATCH a GPU that passed: %d %s", c, b)
	}
	if c, b, _ := do(t, nil, srv, call{method: "GET", path: path}); c != 200 || !strings.Contains(b, `"primaryEncoder":"hevc_qsv"`) {
		t.Errorf("the saved choice isn't shown: %d %s", c, b)
	}
	if c, b, _ := do(t, nil, srv, call{method: "PATCH", path: path, ctype: "application/json", body: `{"primaryEncoder":"cpu"}`}); c != 200 || !strings.Contains(b, `"primaryEncoder":"cpu"`) {
		t.Errorf("PATCH cpu: %d %s", c, b)
	}
	if c, b, _ := do(t, nil, srv, call{method: "PATCH", path: path, ctype: "application/json", body: `{"primaryEncoder":"hevc_qsv2"}`}); c != 400 || errCode(b) != "invalid_encoder" {
		t.Errorf("PATCH a name that isn't an encoder: %d %s", c, b)
	}
}
