// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

func TestPrintHealthExitCode(t *testing.T) {
	var out bytes.Buffer
	ok := doctor.Health{Checks: []doctor.Check{chk("ffmpeg", "Converter (ffmpeg)", "ok", "Version 7.1"), chk("libraries", "Libraries", "warn", "None yet.")}}
	if rc := printHealth(&out, ok); rc != 0 || !strings.Contains(out.String(), "[ok  ] Converter (ffmpeg): Version 7.1") || !strings.Contains(out.String(), "[WARN]") {
		t.Errorf("rc %d\n%s", rc, out.String())
	}
	out.Reset()
	bad := doctor.Health{Checks: []doctor.Check{chk("library:1", "Library: Movies", "bad", "read-only")}}
	if rc := printHealth(&out, bad); rc != 1 || !strings.Contains(out.String(), "[BAD ] Library: Movies: read-only") {
		t.Errorf("rc %d\n%s", rc, out.String())
	}
}

func TestLocalURL(t *testing.T) {
	for in, want := range map[string]string{"0.0.0.0:8484": "http://127.0.0.1:8484", "127.0.0.1:9000": "http://127.0.0.1:9000", "[::]:8484": "http://127.0.0.1:8484", "192.0.2.4:8484": "http://192.0.2.4:8484"} {
		if got := localURL(in); got != want {
			t.Errorf("%s -> %s, want %s", in, got, want)
		}
	}
}

// The command asks the running service (so its folder checks are the service user's), with the token.
func TestDoctorCommandAsksTheService(t *testing.T) {
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, web.TokenFile), []byte("tok\n"), 0o600)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/system/health" || r.Header.Get("Authorization") != "Bearer tok" {
			http.Error(w, "no", 401)
			return
		}
		json.NewEncoder(w).Encode(doctor.Health{Version: "9.9", Checks: []doctor.Check{chk("data", "Data folder", "bad", "Cannot write")}})
	}))
	defer srv.Close()
	t.Setenv("SYP_DATA", data)
	t.Setenv("SYP_ADDR", strings.TrimPrefix(srv.URL, "http://"))
	var out, errOut bytes.Buffer
	if rc := doctorCommand(&out, &errOut); rc != 1 || !strings.Contains(out.String(), "as the running service sees it") || !strings.Contains(out.String(), "[BAD ] Data folder: Cannot write") {
		t.Errorf("rc %d\nout %s\nerr %s", rc, out.String(), errOut.String())
	}
}

// When nothing answers, it checks from the terminal and says so.
func TestDoctorCommandWithoutTheService(t *testing.T) {
	t.Setenv("SYP_DATA", t.TempDir())
	t.Setenv("SYP_ADDR", "127.0.0.1:1") // nothing listens there
	t.Setenv("SYP_FFMPEG", "/nonexistent/ffmpeg")
	var out, errOut bytes.Buffer
	rc := doctorCommand(&out, &errOut)
	if rc != 1 || !strings.Contains(errOut.String(), "The service did not answer") || !strings.Contains(out.String(), "[BAD ] Converter (ffmpeg)") {
		t.Errorf("rc %d\nout %s\nerr %s", rc, out.String(), errOut.String())
	}
}

func chk(id, label, status, detail string) doctor.Check {
	return doctor.Check{ID: id, Label: label, Status: status, Detail: detail}
}
