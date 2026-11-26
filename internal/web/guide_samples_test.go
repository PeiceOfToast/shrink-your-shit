// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
		`{` + answers + `,"sampleFiles":` + mustBody(t, []string{dune}) + `}`:                        "invalid_samples",
		`{` + answers + `,"libraryId":` + fmt.Sprint(l.ID) + `}`:                                     "invalid_samples",
		`{` + answers + `,"libraryId":99999,"sampleFiles":` + mustBody(t, []string{dune}) + `}`:      "not_found",
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
	files := mustBody(t, []string{dune, filepath.Join(dir, "notes.txt"), outside, filepath.Join(dir, "gone.mkv")})
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

// GET .../files?sort=newest lists the newest first; without it the list stays A to Z by path.
func TestLibraryFilesSortNewest(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "old.mkv"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.mkv"), []byte("x"), 0o644)
	os.Chtimes(filepath.Join(dir, "old.mkv"), time.Unix(1_000_000, 0), time.Unix(1_000_000, 0))
	os.Chtimes(filepath.Join(dir, "new.mkv"), time.Unix(2_000_000, 0), time.Unix(2_000_000, 0))
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	names := func(q string) string {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: fmt.Sprintf("/api/libraries/%d/files%s", l.ID, q)})
		var out struct{ Items []struct{ Name string } }
		json.Unmarshal([]byte(body), &out)
		var s []string
		for _, i := range out.Items {
			s = append(s, i.Name)
		}
		return strings.Join(s, ",")
	}
	if got := names(""); got != "new.mkv,old.mkv" {
		t.Errorf("default: %s", got)
	}
	if got := names("?sort=newest"); got != "new.mkv,old.mkv" {
		t.Errorf("newest: %s", got)
	}
	// the names sort to the other order, so the modified time is what decides
	os.Chtimes(filepath.Join(dir, "old.mkv"), time.Unix(3_000_000, 0), time.Unix(3_000_000, 0))
	if got := names("?sort=newest"); got != "old.mkv,new.mkv" {
		t.Errorf("newest after touching old: %s", got)
	}
	if got := names(""); got != "new.mkv,old.mkv" {
		t.Errorf("A to Z after touching old: %s", got)
	}
}

// Answers that do not validate compile to no setting: the response keeps the samples key with one entry
// per file, each answers_invalid and no lines, and the validation says why.
func TestGuidePreviewSamplesOnAnswersThatDoNotValidate(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dune.mkv"), make([]byte, 2_000_000), 0o644)
	os.WriteFile(filepath.Join(dir, "Arrival.mkv"), make([]byte, 2_000_000), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	dir, _ = jobs.RealPath(dir)
	files := mustBody(t, []string{filepath.Join(dir, "Dune.mkv"), filepath.Join(dir, "Arrival.mkv")})

	code, body := post(t, srv, "POST", "/api/v2/workflows/guide/preview",
		`{"answers":{"goal":"smaller","format":"h264"},"libraryId":`+fmt.Sprint(l.ID)+`,"sampleFiles":`+files+`}`)
	var out struct {
		Validation []json.RawMessage `json:"validation"`
		Samples    []struct {
			File  string `json:"file"`
			Error *struct {
				Code string `json:"code"`
			} `json:"error"`
			Lines []json.RawMessage `json:"lines"`
		} `json:"samples"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if len(out.Validation) == 0 {
		t.Fatalf("the H.264 answers should not validate on this server: %s", body)
	}
	if len(out.Samples) != 2 {
		t.Fatalf("want one entry per file, got %s", body)
	}
	for i, s := range out.Samples {
		if s.Error == nil || s.Error.Code != "answers_invalid" || len(s.Lines) != 0 {
			t.Errorf("sample %d: %+v; want answers_invalid with no lines", i, s)
		}
	}
	if out.Samples[0].File != filepath.Join(dir, "Dune.mkv") || out.Samples[1].File != filepath.Join(dir, "Arrival.mkv") {
		t.Errorf("order: %s", body)
	}
}
