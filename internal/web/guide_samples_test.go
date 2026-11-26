// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// The guide's "Try it on one file" (S1): unsaved answers previewed on 1 to 4 files of a library. Each file
// is answered or refused on its own; the request is refused only for its shape.
func TestGuidePreviewSamplesOnUnsavedAnswers(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dune.mkv"), make([]byte, 2_000_000), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	outside := filepath.Join(t.TempDir(), "x.mkv")
	os.WriteFile(outside, make([]byte, 10), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	dir, _ = jobs.RealPath(dir)
	dune := filepath.Join(dir, "Dune.mkv")
	answers := `"answers":{"goal":"smaller"}` // HEVC: the only format this test server can convert to
	guide := "/api/v2/workflows/guide/preview"

	// no samples asked: today's answer, with no samples key
	code, body := post(t, srv, "POST", guide, `{`+answers+`}`)
	if code != 200 || strings.Contains(body, `"samples"`) {
		t.Fatalf("%d %s", code, body)
	}

	// the request shape: no library, no files, too many files, or files without a library
	refused := map[string]string{
		`{` + answers + `,"libraryId":` + fmt.Sprint(l.ID) + `,"sampleFiles":[]}`:                    "invalid_samples",
		`{` + answers + `,"libraryId":` + fmt.Sprint(l.ID) + `,"sampleFiles":["a","b","c","d","e"]}`: "invalid_samples",
		`{` + answers + `,"sampleFiles":["` + dune + `"]}`:                                           "invalid_samples",
		`{` + answers + `,"libraryId":` + fmt.Sprint(l.ID) + `}`:                                     "invalid_samples",
		`{` + answers + `,"libraryId":99999,"sampleFiles":["` + dune + `"]}`:                         "not_found",
	}
	for body, want := range refused {
		code, b := post(t, srv, "POST", guide, body)
		wantCode := 400
		if want == "not_found" {
			wantCode = 404
		}
		if code != wantCode || errCode(b) != want {
			t.Errorf("%s: %d %s; want %d %s", body, code, b, wantCode, want)
		}
	}

	// one good file and three refused ones: each answered on its own, in the order asked
	files := fmt.Sprintf(`["%s","%s","%s","%s"]`, dune, filepath.Join(dir, "notes.txt"), outside, filepath.Join(dir, "gone.mkv"))
	code, body = post(t, srv, "POST", guide, `{`+answers+`,"libraryId":`+fmt.Sprint(l.ID)+`,"sampleFiles":`+files+`}`)
	var out struct {
		Samples []struct {
			File  string `json:"file"`
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
			Lines []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"lines"`
			ExpectedBytesLow  int64    `json:"expectedBytesLow"`
			ExpectedBytesHigh int64    `json:"expectedBytesHigh"`
			Checks            []string `json:"checks"`
			Skipped           bool     `json:"skipped"`
			EstimatedSeconds  *int64   `json:"estimatedSeconds"`
		} `json:"samples"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != 200 || len(out.Samples) != 4 {
		t.Fatalf("%d %s", code, body)
	}
	good := out.Samples[0]
	if good.File != dune || good.Error != nil || good.Skipped || len(good.Lines) < 2 || good.Lines[0].Kind != "video" ||
		good.ExpectedBytesLow <= 0 || good.ExpectedBytesLow >= good.ExpectedBytesHigh || good.ExpectedBytesHigh > 2_000_000 ||
		len(good.Checks) == 0 || good.Checks[0] != "Same length as the original" {
		t.Errorf("good file: %s", body)
	}
	if out.Samples[1].Error == nil || out.Samples[1].Error.Code != "not_a_video" || out.Samples[1].Lines != nil {
		t.Errorf("notes.txt: %s", body)
	}
	if out.Samples[2].Error == nil || out.Samples[2].Error.Code != "outside_library" {
		t.Errorf("outside: %s", body)
	}
	if out.Samples[3].Error == nil || out.Samples[3].Error.Code != "not_found" {
		t.Errorf("gone: %s", body)
	}
	// no speed on this computer for the encoder yet: the estimate is left out
	if good.EstimatedSeconds != nil {
		t.Errorf("estimate without a speed: %s", body)
	}
	if strings.Contains(body, `"error":null`) {
		t.Errorf("an answered file must leave the error out: %s", body)
	}
}
