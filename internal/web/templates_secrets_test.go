// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W4b PR B: the {{name}} template checks at publish, the secrets the draft and revisions never send
// back (and keep when the page saves them back without a value), and the field-only names in the
// variable list.
func TestV2TemplatesPublishCheckAndSecrets(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	_, body := post(t, srv, "POST", "/api/v2/workflows", `{"name":"Vars"}`)
	id := decode[map[string]any](t, body)["id"].(string)
	fid, _ := strconv.ParseInt(id, 10, 64)

	getDraft := func() v2Rev {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id + "/draft"})
		if code != 200 {
			t.Fatalf("draft: %d %s", code, body)
		}
		return decode[v2Rev](t, body)
	}
	put := func(d v2Rev) (int, string) {
		t.Helper()
		b, _ := json.Marshal(map[string]any{"workflow": d.Workflow, "layout": d.Layout})
		req, _ := http.NewRequest("PUT", srv.URL+"/api/v2/workflows/"+id+"/draft", strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", d.Etag)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var sb strings.Builder
		buf := make([]byte, 1<<16)
		for {
			n, err := res.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return res.StatusCode, sb.String()
	}
	commitIdx := func(d v2Rev) int {
		t.Helper()
		for i, n := range d.Workflow.Nodes {
			if n.Type == workflow.TypeCommit {
				return i
			}
		}
		t.Fatal("no Save the result step")
		return -1
	}
	withOutputName := func(d v2Rev, name string) v2Rev {
		d.Workflow.Nodes[commitIdx(d)].Config["outputName"] = name
		return d
	}
	publish := func(etag string) (int, string) {
		t.Helper()
		return post(t, srv, "POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+etag+`"}`)
	}

	// 1. a secret saved on first: the draft's response says one is stored, never the value
	d := getDraft()
	d.Workflow.Variables = []workflow.Variable{
		{Name: "api_key", Type: "secret", Value: "s3cret"},
		{Name: "quality", Type: "string", Value: "high"},
	}
	d = withOutputName(d, "{{title}} ({{quality}})")
	code, body := put(d)
	if code != 200 {
		t.Fatalf("put: %d %s", code, body)
	}
	got := decode[v2Rev](t, body)
	if v := got.Workflow.Variables[0]; v.Value != nil || !v.HasValue {
		t.Errorf("the PUT response sends the secret: %+v, want value null and hasValue", v)
	}
	// 2. the draft GET says the same
	d = getDraft()
	if v := d.Workflow.Variables[0]; v.Value != nil || !v.HasValue {
		t.Errorf("the draft GET sends the secret: %+v", v)
	}
	if v := d.Workflow.Variables[1]; v.Value != "high" {
		t.Errorf("a plain variable's value: %+v", v)
	}
	// 3. saved back as received (no value), the secret keeps the stored one
	if code, body := put(d); code != 200 {
		t.Fatalf("put back: %d %s", code, body)
	}
	cur, _, err := st.Draft(fid)
	if err != nil {
		t.Fatal(err)
	}
	if v := cur.Doc.Variables[0]; v.Value != "s3cret" {
		t.Errorf("the stored secret is %v, want the value the page never saw kept", v.Value)
	}
	if v := cur.Doc.Variables[1]; v.Value != "medium" && v.Value != "high" {
		t.Errorf("a plain variable: %+v", v)
	}

	// 4. an unknown template name stops Publish, and the error names the setting, the name and a close one
	d = withOutputName(getDraft(), "{{titel}}")
	if code, body := put(d); code != 200 {
		t.Fatalf("put the typo: %d %s", code, body)
	}
	code, body = publish(getDraft().Etag)
	e := decode[struct {
		Error struct {
			Code, Message, Field, Name, Suggest string
		}
	}](t, body).Error
	if code != 400 || e.Code != "unknown_variable" || e.Field != "outputName" || e.Name != "titel" || e.Suggest != "title" ||
		!strings.Contains(e.Message, "Nothing was changed.") {
		t.Errorf("a typo: %d %+v", code, e)
	}

	// 5. a {{ with no }} stops it as unclosed
	d = withOutputName(getDraft(), "{{title (smaller)")
	if code, body := put(d); code != 200 {
		t.Fatalf("put the unclosed: %d %s", code, body)
	}
	code, body = publish(getDraft().Etag)
	e = decode[struct {
		Error struct {
			Code, Message, Field, Name, Suggest string
		}
	}](t, body).Error
	if code != 400 || e.Code != "unclosed_variable" || e.Field != "outputName" {
		t.Errorf("an unclosed {{: %d %+v", code, e)
	}

	// 6. a template that checks out publishes: an unknown name is what stops it, not variables as such
	d = withOutputName(getDraft(), "{{title}} ({{quality}})")
	if code, body := put(d); code != 200 {
		t.Fatalf("put the good one: %d %s", code, body)
	}
	if code, body := publish(getDraft().Etag); code != 200 {
		t.Fatalf("publish the good one: %d %s", code, body)
	}

	// 7. restoring a revision whose variables don't pass the check is refused (it was saved before the
	// check existed): the restore must run the same check a draft PUT runs
	cur, _, _ = st.Draft(fid)
	bad := cur.Doc
	bad.Variables = []workflow.Variable{{Name: "Bad"}}
	saved, err := st.SaveDraft(fid, bad, cur.Layout, cur.Etag)
	if err != nil {
		t.Fatal(err)
	}
	flow, err := st.GetFlow(fid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Publish(fid, saved.Etag, flow.Template); err != nil {
		t.Fatal(err)
	}
	// find that revision by what it holds (the numbering is the store's business)
	code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id + "/revisions"})
	var revs struct{ Items []v2Rev }
	revs = decode[struct{ Items []v2Rev }](t, body)
	badRev, newest := "", 0 // the revision row ID of the newest published one (the one just made)
	for _, r := range revs.Items {
		if r.Number != nil && *r.Number > newest {
			newest, badRev = *r.Number, r.ID.(string)
		}
	}
	if badRev == "" {
		t.Fatalf("no revisions listed: %s", body)
	}
	code, body = post(t, srv, "POST", "/api/v2/workflows/"+id+"/revisions/"+badRev+"/restore", "")
	if code != 400 || !strings.Contains(body, "invalid_variable_name") || !strings.Contains(body, "Nothing was changed.") {
		t.Errorf("restore of bad variables: %d %s", code, body)
	}

	// 8. the variable list offers the field-only names with the settings they belong to, and never a
	// secret's value
	code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/workflow/variables?workflow=" + id})
	type tv struct {
		Name   string   `json:"name"`
		Group  string   `json:"group"`
		Value  any      `json:"value"`
		Fields []string `json:"fields"`
	}
	list := decode[struct{ Variables []tv }](t, body).Variables
	byName := map[string]tv{}
	for _, v := range list {
		byName[v.Name] = v
	}
	if code != 200 || byName["sub_lang"].Fields[0] != "srtName" || byName["sub_kind"].Fields[0] != "srtName" {
		t.Errorf("the .srt name's own names: %d %+v", code, byName)
	}
	if byName["title"].Fields != nil || byName["api_key"].Value != nil {
		t.Errorf("title's fields %v, api_key's value %v; want no fields, and no value for the secret", byName["title"].Fields, byName["api_key"].Value)
	}
}
