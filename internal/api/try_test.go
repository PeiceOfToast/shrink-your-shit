// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
)

// tryEnv is newEnv whose try double answers with the given lines, recording what it was asked.
func tryEnv(t *testing.T, lines []flow.TryLine) (*env, *tryCall) {
	t.Helper()
	call := &tryCall{}
	e := newEnv(t, false)
	e.try = func(ctx context.Context, f flow.Flow, file string) (flow.TryResult, error) {
		call.flow, call.file, call.ctx = &f, file, ctx
		return flow.TryResult{Lines: lines}, nil
	}
	e.ready()
	return e, call
}

// tryLibrary makes a library the api knows about, with one video in it, and answers the file's path.
// A try reads library files only, so every accepted request needs one.
func tryLibrary(t *testing.T, e *env) string {
	t.Helper()
	root := t.TempDir()
	file := filepath.Join(root, "holiday.mkv")
	if err := os.WriteFile(file, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.catalog.AddLibrary(context.Background(), library.NewLibrary{Path: root}); err != nil {
		t.Fatalf("add the library: %v", err)
	}
	return file
}

// tryCall is what the try double was asked.
type tryCall struct {
	flow *flow.Flow
	file string
	ctx  context.Context
}

// TestTryAnswersTheLines pins the route's shape: the page sends its flow or its answers and the
// file's path, and gets back a line per step, in the order it was given them.
func TestTryAnswersTheLines(t *testing.T) {
	lines := []flow.TryLine{
		{StepID: "video", Text: "Video: about 3 s."},
		{StepID: "audio", Text: "Audio: about 1 s."},
		{StepID: "save", Text: "Save: about 1 s."},
		{StepID: "save", Text: "Size: 1.9 GB (now 4.6 GB)."},
	}
	e, call := tryEnv(t, lines)
	cookie := e.setup()
	file := tryLibrary(t, e)

	rec := e.do(http.MethodPost, "/api/v2/workflows/try",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Try", 3), "file": file}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("try = %d %s; want 200", rec.Code, rec.Body)
	}
	var got struct {
		Lines []flow.TryLine `json:"lines"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("try body: %v; %s", err, rec.Body)
	}
	if len(got.Lines) != len(lines) {
		t.Fatalf("lines = %+v, want %+v", got.Lines, lines)
	}
	for i, want := range lines {
		if got.Lines[i] != want {
			t.Errorf("line %d = %+v, want %+v", i, got.Lines[i], want)
		}
	}
	if call.file != file || call.flow == nil || apiQualityOf(t, *call.flow) != 3 {
		t.Errorf("the try got file %q and flow %+v, want %q and quality 3", call.file, call.flow, file)
	}

	// The guided page sends answers instead of a flow; the route builds the same flow from them.
	rec = e.do(http.MethodPost, "/api/v2/workflows/try",
		mustJSON(t, map[string]any{"answers": flow.DefaultAnswers(), "file": file}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("try from answers = %d %s; want 200", rec.Code, rec.Body)
	}
	if call.flow == nil || apiQualityOf(t, *call.flow) != 3 {
		t.Errorf("the try from answers got %+v, want the guide's quality 3", call.flow)
	}
}

// TestTryRefusesWhatThePageCannotSend: each malformed request gets the sentence the page shows,
// with the field that needs fixing.
func TestTryRefusesWhatThePageCannotSend(t *testing.T) {
	e, call := tryEnv(t, nil)
	cookie := e.setup()
	file := filepath.Join(t.TempDir(), "holiday.mkv")

	cases := []struct {
		name  string
		body  map[string]any
		code  int
		err   string
		field string
	}{
		{"no file", map[string]any{"flow": apiFlow(t, "Try", 3)}, http.StatusBadRequest, "file_required", "file"},
		{"blank file", map[string]any{"flow": apiFlow(t, "Try", 3), "file": "  "}, http.StatusBadRequest, "file_required", "file"},
		{"a relative file", map[string]any{"flow": apiFlow(t, "Try", 3), "file": "holiday.mkv"}, http.StatusBadRequest, "invalid_file", "file"},
		{"flow and answers", map[string]any{"flow": apiFlow(t, "Try", 3), "answers": flow.DefaultAnswers(), "file": file}, http.StatusBadRequest, "invalid_request", "flow"},
		{"neither", map[string]any{"file": file}, http.StatusBadRequest, "invalid_request", "flow"},
		{"a flow the check refuses", map[string]any{"flow": apiFlow(t, "Try", 9), "file": file}, http.StatusConflict, "has_problems", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			call.flow = nil
			rec := e.do(http.MethodPost, "/api/v2/workflows/try", mustJSON(t, c.body), write(withSession(cookie)), false)
			code, _, env := errorBody(t, rec)
			wantField := any(c.field)
			if c.field == "" {
				wantField = nil // no field in the error envelope
			}
			if rec.Code != c.code || code != c.err || env["field"] != wantField {
				t.Fatalf("= %d %q field %v; want %d %q field %q (%s)", rec.Code, code, env["field"], c.code, c.err, c.field, rec.Body)
			}
			if call.flow != nil {
				t.Errorf("a refused request still ran the try on %q", call.file)
			}
		})
	}
}

// TestTryAnswersItsOwnRefusal: a try the engine could not run keeps its code and sentence.
func TestTryAnswersItsOwnRefusal(t *testing.T) {
	lines := []flow.TryLine{{StepID: "video", Text: "should not be sent"}}
	e, _ := tryEnv(t, lines)
	e.try = func(ctx context.Context, f flow.Flow, file string) (flow.TryResult, error) {
		return flow.TryResult{}, &flow.TryError{Code: "invalid_file", Message: "The file could not be read as media."}
	}
	e.ready()
	cookie := e.setup()
	file := tryLibrary(t, e)

	rec := e.do(http.MethodPost, "/api/v2/workflows/try",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Try", 3), "file": file}),
		write(withSession(cookie)), false)
	code, msg, _ := errorBody(t, rec)
	if rec.Code != http.StatusBadRequest || code != "invalid_file" || msg != "The file could not be read as media." {
		t.Fatalf("= %d %q %q; want 400 invalid_file with the try's own sentence", rec.Code, code, msg)
	}
}

// TestAStoppedTryAnswersNothing: the page's Stop reaches the try through the request's context,
// and a request nobody is waiting for any more gets no answer written.
func TestAStoppedTryAnswersNothing(t *testing.T) {
	e := newEnv(t, false)
	started := make(chan struct{})
	cancelled := make(chan struct{})
	e.try = func(ctx context.Context, f flow.Flow, file string) (flow.TryResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return flow.TryResult{}, ctx.Err()
	}
	e.ready()
	cookie := e.setup()
	file := tryLibrary(t, e)

	ctx, cancel := context.WithCancel(context.Background())
	body := mustJSON(t, map[string]any{"flow": apiFlow(t, "Try", 3), "file": file})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/workflows/try", strings.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range write(withSession(cookie)) {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		e.srv.ServeHTTP(rec, req)
	}()
	<-started
	cancel()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the request's cancel did not reach the try")
	}
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("the handler kept running after the try stopped")
	}
	if rec.Body.Len() != 0 {
		t.Errorf("a stopped try answered %q, want nothing", rec.Body)
	}
}

// TestTryReadsOnlyFilesInsideALibrary: a try reads a file a library owns, judged by its real path.
// A file no library holds is refused, and so is a link inside a library that points outside it.
func TestTryReadsOnlyFilesInsideALibrary(t *testing.T) {
	e, call := tryEnv(t, nil)
	cookie := e.setup()
	libraryFile := tryLibrary(t, e)

	outside := filepath.Join(t.TempDir(), "elsewhere.mkv")
	if err := os.WriteFile(outside, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := []string{outside}
	link := filepath.Join(filepath.Dir(libraryFile), "link.mkv")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("this Windows account cannot make a symlink: %v", err)
		}
		t.Fatalf("make the link: %v", err)
	}
	out = append(out, link)

	for _, file := range out {
		call.flow = nil
		rec := e.do(http.MethodPost, "/api/v2/workflows/try",
			mustJSON(t, map[string]any{"flow": apiFlow(t, "Try", 3), "file": file}),
			write(withSession(cookie)), false)
		code, msg, env := errorBody(t, rec)
		if rec.Code != http.StatusBadRequest || code != "not_in_library" || env["field"] != "file" {
			t.Fatalf("%s = %d %q field %v; want 400 not_in_library on file", file, rec.Code, code, env["field"])
		}
		if msg != "Pick a file inside one of your libraries." {
			t.Errorf("message = %q, want ui-shop's sentence", msg)
		}
		if call.flow != nil {
			t.Errorf("%s was tried anyway", file)
		}
	}

	// The library's own file passes the check; the double then answers.
	rec := e.do(http.MethodPost, "/api/v2/workflows/try",
		mustJSON(t, map[string]any{"flow": apiFlow(t, "Try", 3), "file": libraryFile}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("a library file = %d %s; want 200", rec.Code, rec.Body)
	}
}
