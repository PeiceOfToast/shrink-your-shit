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
	// G2's key is not there yet, so the per-workflow list stays out of the draft and reads back empty
	if len(a.Languages) != 0 || strings.Contains(body, `"languages":["eng"`) {
		t.Errorf("the languages key must wait for G2: %s", body)
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
	// the Advanced flow (G1b): the draft carries the lists; an Advanced save writes them; a Basic save over
	// a workflow Basic can't show is refused, an Advanced one over it is not
	code, body = send("PUT", "/api/v2/workflows/"+id+"/answers", `{"answers":{"flow":"advanced","goal":"both","before":[{"type":"check.name","config":{"pattern":"sample","match":"contains"},"then":{"side":"yes","end":"leave"}}],"afterCards":[],"convertFails":{"end":"stop"}}}`, rev.Etag)
	json.Unmarshal([]byte(body), &rev)
	if code != 200 || rev.Guide.Coverage != "none" || rev.Guide.Advanced != "full" || len(rev.Guide.Answers.Before) != 1 || rev.Guide.Answers.Before[0].Then.Side != "yes" || rev.Guide.Answers.ConvertFails.End != "stop" {
		t.Fatalf("advanced save: %d %s", code, body)
	}
	if code, b := send("PUT", "/api/v2/workflows/"+id+"/answers", `{"answers":{"flow":"basic","goal":"both"}}`, rev.Etag); code != 409 || errCode(b) != "not_guided" || !strings.Contains(b, `"flow":"basic"`) {
		t.Errorf("a Basic save over an Advanced workflow: %d %s", code, b)
	}
	code, body = send("POST", "/api/v2/workflows/guide/preview", `{"answers":{"flow":"advanced","before":[{"type":"check.name","config":{"pattern":"x"},"then":{"end":"leave"}}],"afterCards":[{"type":"action.tag","config":{"add":"done"}}]}}`, "")
	json.Unmarshal([]byte(body), &pv)
	if code != 200 || !strings.HasPrefix(pv.Summary[0], "Check the file name") || pv.Steps[len(pv.Steps)-1] != "Tag the file" {
		t.Fatalf("advanced preview: %d %s", code, body)
	}
	// a workflow the questions can't read: the draft says so, and answers are refused with not_guided
	f, err := st.AddWorkflow("Hand made", workflow.FromTemplate(rules.Template{Kind: rules.KindLegacy}, nil))
	if err != nil {
		t.Fatal(err)
	}
	_, body = send("GET", "/api/v2/workflows/"+strconv.FormatInt(f.ID, 10)+"/draft", "", "")
	json.Unmarshal([]byte(body), &rev)
	if rev.Guide.Coverage != "none" || rev.Guide.Advanced != "none" || rev.Guide.Answers != nil || !strings.Contains(rev.Guide.Why, "earlier install") {
		t.Fatalf("legacy guide: %s", body)
	}
	if code, b := send("PUT", "/api/v2/workflows/"+strconv.FormatInt(f.ID, 10)+"/answers", `{"answers":{}}`, rev.Etag); code != 409 || errCode(b) != "not_guided" {
		t.Errorf("not guided: %d %s", code, b)
	}
}

// contract-g1c-tags: the list's guideFlow and summaryTag follow the questions' reading of the published
// revision: basic with the goal's words, advanced, custom; "" while unpublished or for an earlier
// install's settings, whose tag stays the engine's.
func TestWorkflowListTagsFollowTheQuestions(t *testing.T) {
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
	item := func(id string) (flow, tag string) {
		t.Helper()
		_, body := send("GET", "/api/v2/workflows/"+id, "", "")
		var v struct {
			GuideFlow  string
			SummaryTag string
		}
		json.Unmarshal([]byte(body), &v)
		return v.GuideFlow, v.SummaryTag
	}
	publish := func(id string) {
		t.Helper()
		_, body := send("GET", "/api/v2/workflows/"+id+"/draft", "", "")
		etag := decode[map[string]any](t, body)["etag"].(string)
		if code, b := send("POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+etag+`"}`, ""); code != 200 {
			t.Fatalf("publish: %d %s", code, b)
		}
	}
	// a Basic workflow: "" until it is published, then Basic with its goal's words
	_, body := send("POST", "/api/v2/workflows", `{"name":"Phones","answers":{"goal":"every_device"}}`, "")
	id := decode[map[string]any](t, body)["id"].(string)
	if flow, _ := item(id); flow != "" {
		t.Errorf("unpublished: %q", flow)
	}
	publish(id)
	if flow, tag := item(id); flow != "basic" || tag != "Basic · Plays everywhere" {
		t.Errorf("basic: %q %q", flow, tag)
	}
	// the other two goals' words (review-726: the words per goal are pinned, not just one)
	for goal, want := range map[string]string{"smaller": "Basic · Smaller files", "both": "Basic · Small, plays on most"} {
		_, b := send("POST", "/api/v2/workflows", `{"name":"Goal `+goal+`","answers":{"goal":"`+goal+`"}}`, "")
		gid := decode[map[string]any](t, b)["id"].(string)
		publish(gid)
		if flow, tag := item(gid); flow != "basic" || tag != want {
			t.Errorf("%s: %q %q", goal, flow, tag)
		}
	}
	// an Advanced one: a condition in front
	_, body = send("POST", "/api/v2/workflows", `{"name":"Samples out","answers":{"flow":"advanced","before":[{"type":"check.name","config":{"pattern":"sample"},"then":{"side":"yes","end":"leave"}}]}}`, "")
	id2 := decode[map[string]any](t, body)["id"].(string)
	publish(id2)
	if flow, tag := item(id2); flow != "advanced" || tag != "Advanced" {
		t.Errorf("advanced: %q %q", flow, tag)
	}
	// the list carries the same fields as the single GET (review-726)
	_, body = send("GET", "/api/v2/workflows", "", "")
	var list struct {
		Items []struct {
			ID, GuideFlow, SummaryTag string
		}
	}
	json.Unmarshal([]byte(body), &list)
	found := map[string][2]string{}
	for _, it := range list.Items {
		found[it.ID] = [2]string{it.GuideFlow, it.SummaryTag}
	}
	if found[id] != [2]string{"basic", "Basic · Plays everywhere"} || found[id2] != [2]string{"advanced", "Advanced"} {
		t.Errorf("list: %v %v", found[id], found[id2])
	}
	// custom: the published document has a question whose both sides go on
	_, body = send("GET", "/api/v2/workflows/"+id2+"/draft", "", "")
	var rev struct {
		Workflow workflow.Doc
		Layout   workflow.Layout
		Etag     string
	}
	json.Unmarshal([]byte(body), &rev)
	doc := rev.Workflow
	var q, skips string
	for _, n := range doc.Nodes {
		if n.Type == workflow.TypeCheckName {
			q = n.ID
		}
		if n.Type == workflow.TypeSkips {
			skips = n.ID
		}
	}
	for i := range doc.Edges { // the Yes side goes on too
		if doc.Edges[i].From == q && doc.Edges[i].FromPort == "match" {
			doc.Edges[i].To = skips
		}
	}
	put, _ := json.Marshal(map[string]any{"workflow": doc, "layout": rev.Layout})
	if code, b := send("PUT", "/api/v2/workflows/"+id2+"/draft", string(put), rev.Etag); code != 200 {
		t.Fatalf("hand edit: %d %s", code, b)
	}
	if flow, tag := item(id2); flow != "advanced" || tag != "Advanced" {
		t.Errorf("a draft edit must not change the published reading: %q %q", flow, tag)
	}
	publish(id2)
	if flow, tag := item(id2); flow != "custom" || tag != "Custom" {
		t.Errorf("custom: %q %q", flow, tag)
	}
	// an earlier install's settings keep the engine's tag
	f, err := st.AddWorkflow("Old", workflow.FromTemplate(rules.Template{Kind: rules.KindLegacy}, nil))
	if err != nil {
		t.Fatal(err)
	}
	old := strconv.FormatInt(f.ID, 10)
	if d, _, err := st.Draft(f.ID); err != nil { // the API refuses to publish an earlier install's settings; the store can
		t.Fatal(err)
	} else if _, err := st.Publish(f.ID, d.Etag, rules.Template{Kind: rules.KindLegacy}); err != nil {
		t.Fatal(err)
	}
	if flow, tag := item(old); flow != "" || tag != "Earlier install" {
		t.Errorf("earlier install: %q %q", flow, tag)
	}
}
