// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// The guided builder through the API (slice 12, G1): the defaults and choices, a preview, a workflow
// made from answers that reads back as them, answers saved into a draft, and the refusals the screen
// acts on (a wrong answer named by its question, a stale draft, a workflow the questions can't read).
func TestGuidedBuilderAPI(t *testing.T) {
	rules.SetAvailableFormats(map[string]bool{"libx265": true, "libx264": true, "aac": true})
	defer rules.SetAvailableFormats(nil)
	srv, st, _, _ := libServer(t, false)
	send := func(method, path, body, ifMatch string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	// the defaults never replace the original or tell anyone; the choices are this computer's
	code, body := send("GET", "/api/v2/workflows/guide", "", "")
	var guide struct {
		Defaults  map[string]workflow.Answers
		Available struct {
			Formats, Audio, Subtitles []string
			After                     struct {
				MediaServer, Downloader bool
				NotifyTargets           []map[string]string
			}
		}
		Languages []string
	}
	json.Unmarshal([]byte(body), &guide)
	if code != 200 || len(guide.Defaults) != 3 || guide.Defaults["every_device"].Format != "h264" || guide.Defaults["smaller"].FileType != "mkv" {
		t.Fatalf("%d %s", code, body)
	}
	for goal, a := range guide.Defaults {
		if a.Original == workflow.OriginalReplace || a.After != (workflow.After{}) {
			t.Errorf("%s: %+v", goal, a)
		}
	}
	if strings.Join(guide.Available.Formats, ",") != "hevc,h264" || !strings.Contains(strings.Join(guide.Available.Audio, ","), "stereo_aac") ||
		strings.Join(guide.Available.Subtitles, ",") != "keep_all,my_languages,text_only" || guide.Available.After.MediaServer || len(guide.Available.After.NotifyTargets) != 0 || len(guide.Languages) != 0 {
		t.Errorf("available: %s", body)
	}
	// a preview says what the answers do and builds no workflow
	code, body = send("POST", "/api/v2/workflows/guide/preview", `{"answers":{"goal":"every_device","skip":{"minSizeGB":0.5}}}`, "")
	var pv struct {
		Summary, Steps []string
		Validation     []workflow.Issue
	}
	json.Unmarshal([]byte(body), &pv)
	if code != 200 || !strings.HasPrefix(pv.Summary[0], "Shrink videos to H.264") || pv.Steps[1] != "Check the file" || len(pv.Validation) != 0 {
		t.Fatalf("%d %s", code, body)
	}
	if _, b := send("GET", "/api/v2/workflows", "", ""); strings.Contains(b, `"name"`) {
		t.Fatalf("a preview made a workflow: %s", b)
	}
	// a wrong answer names its question
	code, body = send("POST", "/api/v2/workflows/guide/preview", `{"answers":{"original":"one_folder"}}`, "")
	if code != 400 || errCode(body) != "invalid_answer" || !strings.Contains(body, `"field":"folder"`) {
		t.Fatalf("%d %s", code, body)
	}
	// a workflow from answers: its draft reads back as those answers, and the languages went everywhere
	code, body = send("POST", "/api/v2/workflows", `{"name":"Guided","answers":{"goal":"both","languages":["eng","ger"],"languagesEverywhere":true,"after":{"downloader":true}}}`, "")
	if code != 201 {
		t.Fatalf("%d %s", code, body)
	}
	id := decode[map[string]any](t, body)["id"].(string)
	_, body = send("GET", "/api/v2/workflows/"+id+"/draft", "", "")
	var rev struct {
		Guide workflow.Guide
		Etag  string
	}
	json.Unmarshal([]byte(body), &rev)
	a := rev.Guide.Answers
	if rev.Guide.Coverage != "full" || a == nil || a.Goal != "both" || a.FileType != "mp4" || a.Subtitles != "text_only" || !a.After.Downloader || a.After.MediaServer {
		t.Fatalf("guide: %s", body)
	}
	if set, _ := LoadSettings(st); strings.Join(set.Languages, ",") != "eng,ger" {
		t.Errorf("languages everywhere: %v", set.Languages)
	}
	// G2: the audio step carries the languages, and the guide reads them back from it
	if strings.Join(a.Languages, ",") != "eng,ger" {
		t.Errorf("the audio step's languages must read back from the draft: %v", a.Languages)
	}
	// answers saved into the draft: only the owned steps change; a stale version is refused
	code, body = send("PUT", "/api/v2/workflows/"+id+"/answers", `{"answers":{"goal":"both","quality":2,"after":{}}}`, rev.Etag)
	json.Unmarshal([]byte(body), &rev)
	if code != 200 || rev.Guide.Answers.Quality != 2 || rev.Guide.Answers.After.Downloader {
		t.Fatalf("%d %s", code, body)
	}
	if code, b := send("PUT", "/api/v2/workflows/"+id+"/answers", `{"answers":{"goal":"both"}}`, "stale"); code != 409 || errCode(b) != "stale_draft" {
		t.Errorf("stale: %d %s", code, b)
	}
	if code, b := send("PUT", "/api/v2/workflows/"+id+"/answers", `{"answers":{"goal":"both"}}`, ""); code != 428 {
		t.Errorf("no version: %d %s", code, b)
	}
	// a workflow the questions can't read: the draft says so, and answers are refused with not_guided
	f, err := st.AddWorkflow("Hand made", workflow.FromTemplate(rules.Template{Kind: rules.KindLegacy}, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, body = send("GET", "/api/v2/workflows/"+strconv.FormatInt(f.ID, 10)+"/draft", "", "")
	json.Unmarshal([]byte(body), &rev)
	if rev.Guide.Coverage != "none" || rev.Guide.Answers != nil || !strings.Contains(rev.Guide.Why, "earlier install") {
		t.Fatalf("legacy guide: %s", body)
	}
	if code, b := send("PUT", "/api/v2/workflows/"+strconv.FormatInt(f.ID, 10)+"/answers", `{"answers":{}}`, rev.Etag); code != 409 || errCode(b) != "not_guided" {
		t.Errorf("not guided: %d %s", code, b)
	}
}
