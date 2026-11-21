// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// W1 #521 round 2: the page's "replaces originals" warning reads the policy a variable sets, and
// Publish's refusal names the setting for every W1 code, not only the text fields' two.
func TestW1PolicyVariableAndPublishCodes(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	_, body := post(t, srv, "POST", "/api/v2/workflows", `{"name":"Vars"}`)
	id := decode[map[string]any](t, body)["id"].(string)

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
	publish := func(etag string) (int, string) {
		t.Helper()
		return post(t, srv, "POST", "/api/v2/workflows/"+id+"/publish", `{"etag":"`+etag+`"}`)
	}
	nodeIdx := func(d v2Rev, typ string) int {
		t.Helper()
		for i, n := range d.Workflow.Nodes {
			if n.Type == typ {
				return i
			}
		}
		t.Fatalf("no %s step", typ)
		return -1
	}
	workflowView := func() map[string]any {
		t.Helper()
		code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id})
		if code != 200 {
			t.Fatalf("workflow: %d %s", code, body)
		}
		return decode[map[string]any](t, body)
	}

	// 1. a policy that a variable sets to "replace": the warning is on before and after publishing
	d := getDraft()
	d.Workflow.Nodes[nodeIdx(d, workflow.TypeCommit)].Config["policy"] = "{{p}}"
	d.Workflow.Variables = []workflow.Variable{{Name: "p", Type: "string", Value: "replace"}}
	code, body := put(d)
	if code != 200 {
		t.Fatalf("put the variable policy: %d %s", code, body)
	}
	if got := decode[v2Rev](t, body); len(got.Validation) != 0 {
		t.Fatalf("validation %+v, want none", got.Validation)
	}
	if w := workflowView(); w["changesOriginals"] != true {
		t.Errorf("a draft whose {{p}} policy is replace: changesOriginals %v, want true", w["changesOriginals"])
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/v2/workflows/" + id + "/publish-preview"}); !strings.Contains(body, `"changesOriginals":true`) {
		t.Errorf("publish preview %s", body)
	}
	if code, body := publish(getDraft().Etag); code != 200 {
		t.Fatalf("publish: %d %s", code, body)
	}
	if w := workflowView(); w["changesOriginals"] != true {
		t.Errorf("a published {{p}} policy of replace: changesOriginals %v, want true", w["changesOriginals"])
	}

	// 2. the same variable set to "beside": the warning goes off
	d = getDraft()
	d.Workflow.Variables = []workflow.Variable{{Name: "p", Type: "string", Value: "beside"}}
	if code, body := put(d); code != 200 {
		t.Fatalf("put beside: %d %s", code, body)
	}
	if code, body := publish(getDraft().Etag); code != 200 {
		t.Fatalf("publish beside: %d %s", code, body)
	}
	if w := workflowView(); w["changesOriginals"] != false {
		t.Errorf("a published {{p}} policy of beside: changesOriginals %v, want false", w["changesOriginals"])
	}

	// 3. Publish refuses each W1 code with the code, the setting and "Nothing was changed."
	errOf := func(body string) struct {
		Error struct{ Code, Message, Field, Name, Suggest string }
	} {
		t.Helper()
		return decode[struct {
			Error struct{ Code, Message, Field, Name, Suggest string }
		}](t, body)
	}
	for _, c := range []struct {
		name  string
		vars  []workflow.Variable
		raw   string
		code  string
		field string
		says  string
	}{
		{"a value the setting can't take", []workflow.Variable{{Name: "q", Type: "string", Value: "high"}},
			"{{q}}", "variable_wrong_type", "quality", "doesn't fit"},
		{"text around the reference", []workflow.Variable{{Name: "q", Type: "number", Value: float64(2)}},
			"{{q}}5", "variable_ref_not_whole", "quality", "one whole variable"},
		{"a name nobody defines", []workflow.Variable{{Name: "quality", Type: "number", Value: float64(2)}},
			"{{qualit}}", "unknown_variable", "quality", "Did you mean {{quality}}?"},
	} {
		d = getDraft()
		d.Workflow.Variables = c.vars
		d.Workflow.Nodes[nodeIdx(d, workflow.TypeCommit)].Config["policy"] = "beside" // only the quality issue is left
		d.Workflow.Nodes[nodeIdx(d, workflow.TypeConvert)].Config["quality"] = c.raw
		if code, body := put(d); code != 200 {
			t.Fatalf("%s: put: %d %s", c.name, code, body)
		}
		code, body := publish(getDraft().Etag)
		e := errOf(body).Error
		if code != 400 || e.Code != c.code || e.Field != c.field || !strings.Contains(e.Message, c.says) || !strings.Contains(e.Message, "Nothing was changed.") {
			t.Errorf("%s: %d %+v, want 400 %s on %s saying %q", c.name, code, e, c.code, c.field, c.says)
		}
	}
}
