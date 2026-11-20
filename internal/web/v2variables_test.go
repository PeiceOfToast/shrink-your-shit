// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W4a, design-joblog-vars section 3: the names a workflow's {{name}} fields take, with the sample's
// values; the page builds the example and checks names itself.

type varItem struct {
	Name, Description, Group string
	Value                    *string
	Fields                   []string
}

type varList struct {
	Sample struct {
		Path    string
		BuiltIn bool
	}
	Variables []varItem
}

func (v varList) find(t *testing.T, name string) varItem {
	t.Helper()
	for _, it := range v.Variables {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("no %q in the list: %+v", name, v.Variables)
	return varItem{}
}

func (v varItem) str(t *testing.T) string {
	t.Helper()
	if v.Value == nil {
		t.Fatalf("%s has no value", v.Name)
	}
	return *v.Value
}

func getVars(t *testing.T, srv *httptest.Server, workflowID int64) (int, varList) {
	t.Helper()
	code, body := post(t, srv, "GET", "/api/v2/workflow/variables?workflow="+strconv.FormatInt(workflowID, 10), "")
	var got varList
	if code == http.StatusOK {
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("%v: %s", err, body)
		}
	}
	return code, got
}

// With nothing converted, the list is the built-ins on the server's own sample (builtIn true), and
// the values the server knows from the path and the workflow are filled.
func TestVariablesBuiltInSample(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	if err != nil {
		t.Fatal(err)
	}
	code, got := getVars(t, srv, lib.FlowID)
	if code != http.StatusOK {
		t.Fatalf("GET variables: %d", code)
	}
	sample := filepath.FromSlash(builtInSamplePath)
	if !got.Sample.BuiltIn || got.Sample.Path != sample {
		t.Fatalf("sample: %+v, want the built-in %s", got.Sample, sample)
	}
	for name, want := range map[string]string{
		"title": "Movie (2019)", "file_name": "Movie (2019).mkv", "ext": "mkv", "folder": filepath.Dir(sample),
		"path": sample, "library": "Movies", "library_root": lib.Path, "workflow": "Movies",
	} {
		if v := got.find(t, name); v.str(t) != want {
			t.Errorf("%s = %q, want %q", name, v.str(t), want)
		}
	}
	if v := got.find(t, "size_bytes"); v.Value != nil {
		t.Errorf("size_bytes = %q, want null: the built-in sample's size is not known", *v.Value)
	}
	if v := got.find(t, "video_codec"); v.Value != nil {
		t.Errorf("video_codec = %q, want null: the built-in sample was not probed", *v.Value)
	}
	if v := got.find(t, "title"); v.Group != "File" || v.Description != "The file name without its extension" || v.Fields != nil {
		t.Errorf("title: %+v", v)
	}
	for _, name := range []string{"sub_lang", "sub_kind"} { // the .srt name's own two: offered with the setting they belong to
		if v := got.find(t, name); v.Group != "Subtitles" || len(v.Fields) != 1 || v.Fields[0] != "srtName" || v.Value != nil {
			t.Errorf("%s: %+v; want the Subtitles group, fields [srtName], and no value for the built-in sample", name, v)
		}
	}

	if code, _ := getVars(t, srv, 999999); code != http.StatusNotFound {
		t.Errorf("unknown workflow: %d, want 404", code)
	}
	if code, _ := post(t, srv, "GET", "/api/v2/workflow/variables", ""); code != http.StatusNotFound {
		t.Errorf("no workflow: %d, want 404", code)
	}
}

// The sample is the newest file a library using the workflow converted, and the values come from how
// it was probed when its job was planned.
func TestVariablesConvertedSample(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	src := media.Info{VideoCodec: "h264", BitrateKbps: 8000, DurationSec: 6312.4, SizeBytes: 4000, Ext: ".mkv",
		Streams: []media.Stream{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "aac", Language: "eng", Channels: 2},
			{Index: 2, Type: "audio", Codec: "ac3", Language: "ger", Channels: 6},
			{Index: 3, Type: "subtitle", Codec: "subrip", Language: "eng"},
			{Index: 4, Type: "subtitle", Codec: "subrip", Language: "eng"},
		}}
	raw, _ := json.Marshal(rules.Snapshot{Plan: rules.Plan{Video: rules.VideoEncode{Codec: "libx265"}}, Source: &src})
	orig := filepath.Join(lib.Path, "Movie (2019).mkv")
	out := filepath.Join(lib.Path, "Movie (2019).hevc.mkv")
	id, err := st.Enqueue(jobs.Job{LibraryID: lib.ID, Path: orig, SizeBefore: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPlan(id, string(raw), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("local-box"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(id, out, 1500); err != nil {
		t.Fatal(err)
	}

	code, got := getVars(t, srv, lib.FlowID)
	if code != http.StatusOK {
		t.Fatalf("GET variables: %d", code)
	}
	if got.Sample.BuiltIn || got.Sample.Path != orig {
		t.Fatalf("sample: %+v, want the converted %s", got.Sample, orig)
	}
	for name, want := range map[string]string{
		"title": "Movie (2019)", "file_name": "Movie (2019).mkv", "ext": "mkv", "path": orig,
		"library": "Movies", "size_bytes": "4000", "width": "1920", "height": "1080",
		"resolution": "1080p", "hdr": "no", "duration": "1:45:12", "bitrate_kbps": "8000",
		"container": "mkv", "audio_languages": "eng, ger", "audio_codecs": "aac, ac3",
		"audio_channels": "2, 6", "subtitle_languages": "eng", "video_codec": "h264",
		"target_codec": "hevc", "worker": "local-box", "output_path": out,
		"new_size_bytes": "1500", "saved_percent": "62.5", "workflow": "Movies",
	} {
		if v := got.find(t, name); v.str(t) != want {
			t.Errorf("%s = %q, want %q", name, v.str(t), want)
		}
	}
}

// Two streams in one language list it once, and an untagged or undetermined language is not shown.
func TestVariablesLanguagesOnce(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	if err != nil {
		t.Fatal(err)
	}
	src := media.Info{VideoCodec: "h264", Ext: ".mkv", Streams: []media.Stream{
		{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720},
		{Index: 1, Type: "audio", Codec: "aac", Language: "und", Channels: 2},
		{Index: 2, Type: "subtitle", Codec: "subrip"},
	}}
	raw, _ := json.Marshal(rules.Snapshot{Plan: rules.Plan{Video: rules.VideoEncode{Codec: "libx264", Format: "h264"}}, Source: &src})
	id, err := st.Enqueue(jobs.Job{LibraryID: lib.ID, Path: filepath.Join(lib.Path, "A.mkv"), SizeBefore: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RecordPlan(id, string(raw), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Claim("w"); err != nil {
		t.Fatal(err)
	}
	if err := st.Complete(id, filepath.Join(lib.Path, "A.c.mkv"), 50); err != nil {
		t.Fatal(err)
	}
	_, got := getVars(t, srv, lib.FlowID)
	if v := got.find(t, "audio_languages"); v.Value != nil {
		t.Errorf("audio_languages = %q, want null for an undetermined language", *v.Value)
	}
	if v := got.find(t, "subtitle_languages"); v.Value != nil {
		t.Errorf("subtitle_languages = %q, want null for an untagged track", *v.Value)
	}
	if v := got.find(t, "target_codec"); v.str(t) != "h264" {
		t.Errorf("target_codec = %q, want h264", v.str(t))
	}
}

// A workflow's own variables come last, under "Workflow", with their values; a secret's value is
// never sent.
func TestVariablesOwnWorkflowVariablesAreListed(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	f, err := st.AddWorkflow("Shrink movies", workflow.Preset())
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := st.Draft(f.ID)
	if err != nil {
		t.Fatal(err)
	}
	d.Doc.Variables = []workflow.Variable{
		{Name: "movie_name", Type: "string", Value: "Night Train", Description: "The name to use"},
		{Name: "api_key", Type: "secret", Value: "s3cret", HasValue: true},
	}
	if _, err := st.SaveDraft(f.ID, d.Doc, d.Layout, d.Etag); err != nil {
		t.Fatal(err)
	}
	code, got := getVars(t, srv, f.ID)
	if code != http.StatusOK {
		t.Fatalf("GET variables: %d", code)
	}
	own := got.find(t, "movie_name")
	if own.Group != "Workflow" || own.str(t) != "Night Train" || own.Description != "The name to use" {
		t.Errorf("movie_name: %+v", own)
	}
	if secret := got.find(t, "api_key"); secret.Group != "Workflow" || secret.Value != nil {
		t.Errorf("api_key: %+v, want its value hidden", secret)
	}
	last := got.Variables[len(got.Variables)-1]
	if last.Name != "api_key" { // the workflow's own come after the built-ins
		t.Errorf("the list ends with %q, want the workflow's own last", last.Name)
	}
	if v := got.find(t, "title"); v.Group != "File" {
		t.Errorf("title: %+v", v)
	}
}

// A draft whose variables break a name rule is refused with a code per problem, and nothing is saved;
// Publish refuses one too (a draft saved by an older build), through Validate.
func TestVariableRulesAreRefused(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	if err != nil {
		t.Fatal(err)
	}
	flow := strconv.FormatInt(lib.FlowID, 10)
	code, body := post(t, srv, "GET", "/api/v2/workflows/"+flow+"/draft", "")
	var draft v2Rev
	if code != http.StatusOK {
		t.Fatalf("GET draft: %d %s", code, body)
	}
	if err := json.Unmarshal([]byte(body), &draft); err != nil {
		t.Fatal(err)
	}
	send := func(method, path, payload, ifMatch string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b := strings.Builder{}
		buf := make([]byte, 4096)
		for {
			n, err := res.Body.Read(buf)
			b.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return res.StatusCode, b.String()
	}
	for _, c := range []struct {
		why  string
		vars []workflow.Variable
		code string
	}{
		{"a capital", []workflow.Variable{{Name: "Title", Type: "string"}}, "invalid_variable_name"},
		{"a built-in", []workflow.Variable{{Name: "title", Type: "string"}}, "builtin_variable_name"},
		{"twice", []workflow.Variable{{Name: "mine", Type: "string"}, {Name: "mine", Type: "string"}}, "duplicate_variable"},
		{"a secret holding an object", []workflow.Variable{{Name: "api_key", Type: "secret", Value: map[string]any{"k": "OBJSECRET"}}}, "invalid_secret_value"},
	} {
		doc := draft.Workflow
		doc.Variables = c.vars
		payload, _ := json.Marshal(map[string]any{"workflow": doc, "layout": draft.Layout})
		code, body := send("PUT", "/api/v2/workflows/"+flow+"/draft", string(payload), draft.Etag)
		if code != http.StatusBadRequest {
			t.Fatalf("%s: PUT draft: %d %s", c.why, code, body)
		}
		e := decode[struct {
			Error struct {
				Code, Message string
				Issues        []workflow.Issue
			}
		}](t, body)
		if e.Error.Code != c.code || !strings.Contains(e.Error.Message, "Nothing was changed.") || len(e.Error.Issues) == 0 {
			t.Errorf("%s: %+v, want %s and nothing changed", c.why, e.Error, c.code)
		}
	}
	// refused means not saved: the draft still has the etag it had and no variables
	code, body = post(t, srv, "GET", "/api/v2/workflows/"+flow+"/draft", "")
	if code != http.StatusOK {
		t.Fatalf("GET draft again: %d %s", code, body)
	}
	after := decode[v2Rev](t, body)
	if after.Etag != draft.Etag || len(after.Workflow.Variables) != 0 {
		t.Fatalf("the refused drafts were saved: etag %s (was %s), variables %+v", after.Etag, draft.Etag, after.Workflow.Variables)
	}

	// a draft saved with a built-in name (an older build, or another way in) can't be published
	bad := draft.Workflow
	bad.Variables = []workflow.Variable{{Name: "title", Type: "string"}}
	saved, err := st.SaveDraft(lib.FlowID, bad, draft.Layout, draft.Etag)
	if err != nil {
		t.Fatal(err)
	}
	code, body = send("POST", "/api/v2/workflows/"+flow+"/publish", fmt.Sprintf(`{"etag":%q}`, saved.Etag), "")
	if code != http.StatusBadRequest {
		t.Fatalf("publish with a built-in variable name: %d %s", code, body)
	}
	e := decode[struct {
		Error struct {
			Code   string
			Issues []workflow.Issue
		}
	}](t, body)
	if e.Error.Code != "has_errors" || !strings.Contains(body, `"builtin_variable_name"`) {
		t.Errorf("publish: %+v %s, want has_errors with builtin_variable_name", e.Error, body)
	}
}

// Names a person may type are saved, and the list shows them.
func TestVariableNameSaves(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	if err != nil {
		t.Fatal(err)
	}
	flow := strconv.FormatInt(lib.FlowID, 10)
	code, body := post(t, srv, "GET", "/api/v2/workflows/"+flow+"/draft", "")
	if code != http.StatusOK {
		t.Fatalf("GET draft: %d %s", code, body)
	}
	draft := decode[v2Rev](t, body)
	draft.Workflow.Variables = []workflow.Variable{{Name: "movie_name", Type: "string", Value: "Night Train"}}
	payload, _ := json.Marshal(map[string]any{"workflow": draft.Workflow, "layout": draft.Layout})
	req, _ := http.NewRequest("PUT", srv.URL+"/api/v2/workflows/"+flow+"/draft", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("If-Match", draft.Etag)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("PUT draft: %d", res.StatusCode)
	}
	_, got := getVars(t, srv, lib.FlowID)
	if v := got.find(t, "movie_name"); v.str(t) != "Night Train" || v.Group != "Workflow" {
		t.Fatalf("movie_name: %+v", v)
	}
}

// FB1d: a name a "Set a variable" step sets is in the list, under "Set by a step", with the step's words, so the
// editor's checks and picker know it; a name that is not one a step may set is left out.
func TestVariablesPublishedByASetStep(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	lib, err := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureRevision(lib.FlowID); err != nil { // the flow's first revision, as the editor's first open makes it
		t.Fatal(err)
	}
	d, _, err := st.Draft(lib.FlowID)
	if err != nil {
		t.Fatal(err)
	}
	doc := d.Doc
	doc.Nodes = append(doc.Nodes,
		workflow.Node{ID: "set1", Type: workflow.TypeSetVariable, TypeVersion: 1, Name: "Pick a profile", Enabled: true, Config: map[string]any{"name": "profile", "value": "remux"}},
		workflow.Node{ID: "set2", Type: workflow.TypeSetVariable, TypeVersion: 1, Enabled: true, Config: map[string]any{"name": "Bad Name"}},
		workflow.Node{ID: "sum", Type: workflow.TypeChecksum, TypeVersion: 1, Enabled: true, Config: map[string]any{}})
	if _, err := st.SaveDraft(lib.FlowID, doc, d.Layout, d.Etag); err != nil {
		t.Fatal(err)
	}
	code, got := getVars(t, srv, lib.FlowID)
	if code != http.StatusOK {
		t.Fatalf("GET variables: %d", code)
	}
	if v := got.find(t, "checksum"); v.Group != "Set by a step" || v.Value != nil || !strings.Contains(v.Description, "Checksum the result") { // FB2a: a card's published name
		t.Errorf("checksum: %+v", v)
	}
	if v := got.find(t, "profile"); v.Group != "Set by a step" || v.Value != nil || !strings.Contains(v.Description, "Pick a profile") || !strings.Contains(v.Description, "remux") {
		t.Errorf("profile: %+v", v)
	}
	for _, v := range got.Variables {
		if v.Name == "Bad Name" {
			t.Errorf("a name a step may not set is listed: %+v", v)
		}
	}
}
