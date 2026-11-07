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

type previewOut struct {
	Status                              string
	Files, CanShrink, SharedNoSaving    int
	TotalBytes                          int64
	EstimateBytesLow, EstimateBytesHigh int64
	Skipped                             map[string]int
	Sample                              *struct {
		Lines                               []string
		ExpectedBytesLow, ExpectedBytesHigh int64
	}
}

func TestPreviewEndpoint(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	for _, n := range []string{"a.mkv", "b.mkv"} {
		os.WriteFile(filepath.Join(dir, n), make([]byte, 1_000_000), 0o644)
	}
	outside := filepath.Join(t.TempDir(), "x.mkv")
	os.WriteFile(outside, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644)
	// not started: the Preview is for before Start (SAF-UI-6)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(1), false)
	path := fmt.Sprintf("/api/libraries/%d/preview", l.ID)
	pv := func(body string) (int, previewOut, string) {
		code, b := post(t, srv, "POST", path, body)
		var out previewOut
		json.Unmarshal([]byte(b), &out)
		return code, out, b
	}

	// the saved setting (empty or absent), and an unsaved one
	for _, body := range []string{`{}`, `{"setting":{}}`, `{"setting":null,"sampleFile":null}`, `{"setting":{"kind":"slider","quality":5}}`} {
		code, out, b := pv(body)
		if code != 200 || out.Status != "ready" || out.Files != 2 || out.CanShrink != 2 || out.TotalBytes != 2_000_000 ||
			out.EstimateBytesLow < 0 || out.EstimateBytesHigh <= out.EstimateBytesLow || out.Sample != nil || !strings.Contains(b, `"skipped":{}`) {
			t.Errorf("%s: %d %s", body, code, b)
		}
	}
	// Standard's selectors left at Balanced's own values are Balanced (addendum 6)
	std := `{"setting":{"quality":3,"speed":"normal","format":"hevc","resolution":"keep","audio":"keep_all","subtitles":"keep_all",
		"fileType":"keep","afterConverting":"replace","order":"newest","maxBitrate":"none","ignore":[],"skipHdr":true,"skipSmall":true,"minSavingPct":10}}`
	_, bal, _ := pv(`{"setting":{"quality":3}}`)
	if code, out, b := pv(std); code != 200 || out.EstimateBytesHigh != bal.EstimateBytesHigh || out.CanShrink != 2 {
		t.Errorf("selectors at Balanced: %d %s", code, b)
	}
	_, saved, _ := pv(`{}`)
	_, best, _ := pv(`{"setting":{"quality":5}}`)
	if best.EstimateBytesHigh >= saved.EstimateBytesHigh {
		t.Errorf("Best quality (unsaved) must estimate less than the saved Smallest file: %d >= %d", best.EstimateBytesHigh, saved.EstimateBytesHigh)
	}
	if f, _ := st.GetFlow(l.FlowID); f.Template != rules.SliderTemplate(1) {
		t.Error("previewing an unsaved setting must not save it")
	}

	code, out, b := pv(fmt.Sprintf(`{"setting":{"quality":3},"sampleFile":%q}`, filepath.Join(dir, "a.mkv")))
	if code != 200 || out.Sample == nil || out.Sample.Lines[0] != "Video: converted to the smaller format, same picture size and length" ||
		out.Sample.ExpectedBytesLow <= 0 || out.Sample.ExpectedBytesHigh >= 1_000_000 {
		t.Errorf("sample: %d %s", code, b)
	}

	// warnings for the previewed setting (unsaved too): always present, [] when none
	for body, want := range map[string]int{`{"setting":{"quality":3,"skipHdr":false}}`: 1, `{"setting":{"quality":3,"skipHdr":true}}`: 0, `{}`: 0} {
		code, b := post(t, srv, "POST", path, body)
		var v struct{ Warnings []string }
		json.Unmarshal([]byte(b), &v)
		if code != 200 || !strings.Contains(b, `"warnings":[`) || len(v.Warnings) != want || (want == 1 && v.Warnings[0] != `HDR and Dolby Vision files will be converted without their HDR information, so they can look washed out. Turn "Skip HDR and Dolby Vision files" back on to leave them as they are.`) {
			t.Errorf("%s: %d %s", body, code, b)
		}
	}

	// the Standard selectors are previewed as they would run (slice 25a)
	code, out, b = pv(fmt.Sprintf(`{"setting":{"quality":3,"resolution":"720","maxBitrate":"10","fileType":"mkv","afterConverting":"beside"},"sampleFile":%q}`, filepath.Join(dir, "a.mkv")))
	if code != 200 || out.Sample == nil || out.Sample.Lines[0] != "Video: converted to the smaller format and made 720 lines high (from 1080), same length, at most 10 Mbps" ||
		!strings.Contains(strings.Join(out.Sample.Lines, "|"), "The new MKV file is written next to the original") {
		t.Errorf("selectors sample: %d %s", code, b)
	}

	for body, want := range map[string]string{
		`{"setting":{"quality":0}}`:                                       "400 invalid_setting",
		`{"setting":{"quality":3,"format":"av1"}}`:                        "400 not_available",
		`{"setting":{"quality":3,"audio":"my_languages"}}`:                "400 languages_needed",
		`{"setting":{"quality":3,"audioBitrate":"128"}}`:                  "400 invalid_setting",
		`{"setting":{"quality":3,"speed":"fast"}}`:                        "400 invalid_setting",
		fmt.Sprintf(`{"sampleFile":%q}`, outside):                         "400 outside_library",
		fmt.Sprintf(`{"sampleFile":%q}`, dir):                             "400 outside_library",
		fmt.Sprintf(`{"sampleFile":%q}`, filepath.Join(dir, "../x.mkv")):  "400 outside_library",
		`{"sampleFile":"a.mkv"}`:                                          "400 outside_library",
		fmt.Sprintf(`{"sampleFile":%q}`, filepath.Join(dir, "notes.txt")): "400 not_a_video",
		fmt.Sprintf(`{"sampleFile":%q}`, filepath.Join(dir, "gone.mkv")):  "404 not_found",
	} {
		code, b := post(t, srv, "POST", path, body)
		var e struct{ Error struct{ Code string } }
		json.Unmarshal([]byte(b), &e)
		if got := fmt.Sprintf("%d %s", code, e.Error.Code); got != want {
			t.Errorf("%s: %s, want %s (%s)", body, got, want, b)
		}
	}
	if code, _ := post(t, srv, "POST", "/api/libraries/999/preview", `{}`); code != 404 {
		t.Errorf("unknown library: %d", code)
	}
	if j, _ := st.List(100); len(j) != 0 {
		t.Errorf("a Preview makes no job: %+v", j)
	}
}

// A sample reached through a link that points outside the library is outside it.
func TestPreviewSampleThroughALinkOutside(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir, other := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(other, "x.mkv"), []byte("x"), 0o644)
	if err := os.Symlink(filepath.Join(other, "x.mkv"), filepath.Join(dir, "x.mkv")); err != nil {
		t.Skip("cannot make links here")
	}
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	code, b := post(t, srv, "POST", fmt.Sprintf("/api/libraries/%d/preview", l.ID), fmt.Sprintf(`{"sampleFile":%q}`, filepath.Join(dir, "x.mkv")))
	if code != 400 || !strings.Contains(b, "outside_library") {
		t.Errorf("%d %s", code, b)
	}
}

func TestLibraryFilesEndpoint(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Films"), 0o755)
	os.WriteFile(filepath.Join(dir, "Films", "Heat (1995).mkv"), []byte("xx"), 0o644)
	os.WriteFile(filepath.Join(dir, "clip.mp4"), []byte("x"), 0o644)
	l, _ := st.AddLibrary("Movies", dir, rules.SliderTemplate(3), false)
	dir, _ = jobs.RealPath(dir) // the library is stored resolved (/private/var on macOS), and so are its files
	_, body, _ := do(t, nil, srv, call{method: "GET", path: fmt.Sprintf("/api/libraries/%d/files?q=heat", l.ID)})
	var out struct {
		Items []struct {
			Path, Name string
			Bytes      int64
		}
		Total int
	}
	json.Unmarshal([]byte(body), &out)
	if out.Total != 1 || len(out.Items) != 1 || out.Items[0].Name != "Films/Heat (1995).mkv" || out.Items[0].Bytes != 2 || out.Items[0].Path != filepath.Join(dir, "Films", "Heat (1995).mkv") {
		t.Errorf("%s", body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: fmt.Sprintf("/api/libraries/%d/files?limit=1&offset=1", l.ID)})
	out.Items = nil
	json.Unmarshal([]byte(body), &out)
	if out.Total != 2 || len(out.Items) != 1 || out.Items[0].Name != "clip.mp4" { // sorted: "Films/..." < "clip.mp4"
		t.Errorf("paged: %s", body)
	}
}

// The macOS /var -> /private/var case on any system: the library was added through a link, so it is
// stored resolved, and the page sends the sample through the link. Inside is inside; a text file is
// not a video; a missing file is not found.
func TestPreviewSampleThroughALinkedLibraryPath(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	real := t.TempDir()
	os.WriteFile(filepath.Join(real, "a.mkv"), make([]byte, 1_000_000), 0o644)
	os.WriteFile(filepath.Join(real, "notes.txt"), []byte("x"), 0o644)
	link := filepath.Join(t.TempDir(), "media")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("cannot make links here")
	}
	l, _ := st.AddLibrary("Movies", link, rules.SliderTemplate(3), false)
	path := fmt.Sprintf("/api/libraries/%d/preview", l.ID)
	for name, want := range map[string]int{"a.mkv": 200, "notes.txt": 400, "gone.mkv": 404} {
		code, b := post(t, srv, "POST", path, fmt.Sprintf(`{"sampleFile":%q}`, filepath.Join(link, name)))
		if code != want || strings.Contains(b, "outside_library") {
			t.Errorf("%s: %d %s", name, code, b)
		}
	}
}
