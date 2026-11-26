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

// A hand-made workflow the Advanced flow can't show in one stretch after the save still opens: the draft
// carries its readable parts and names the stretch; a save writes the readable parts (200) and keeps the
// stretch. A front join, which no part of the questions can show, is refused with not_guided.
func TestGuidedBuilderOpensAPartlyUnreadableWorkflow(t *testing.T) {
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
	draft := func(id int64) (workflow.Guide, string) {
		t.Helper()
		_, body := send("GET", "/api/v2/workflows/"+strconv.FormatInt(id, 10)+"/draft", "", "")
		var rev struct {
			Guide workflow.Guide
			Etag  string
		}
		if err := json.Unmarshal([]byte(body), &rev); err != nil {
			t.Fatal(err)
		}
		return rev.Guide, rev.Etag
	}

	// the stretch after the save: a question whose No side joins at a step that has another link into it
	a := workflow.DefaultAnswers(workflow.GoalSmaller)
	a.AfterCards = []workflow.Card{{Type: workflow.TypeCheckResult, Config: map[string]any{"test": "saved_at_least", "percent": 50},
		Yes: []workflow.Card{{Type: workflow.TypeNotify, Config: map[string]any{"kind": "good", "text": "done"}}}}}
	d := workflow.Build(a)
	q := findNode(t, d, workflow.TypeCheckResult)
	n := findNode(t, d, workflow.TypeNotify)
	d.Edges = append(d.Edges, workflow.Edge{From: q.ID, FromPort: "else", To: n.ID})
	f, err := st.AddWorkflow("Partly hand made", d)
	if err != nil {
		t.Fatal(err)
	}
	id := f.ID
	g, etag := draft(id)
	if g.Advanced != "none" || g.Answers == nil || len(g.Unreadable) != 1 || g.Unreadable[0] != "Compare the result" {
		t.Fatalf("draft guide: %+v", g)
	}

	code, body := send("PUT", "/api/v2/workflows/"+strconv.FormatInt(id, 10)+"/answers",
		`{"answers":{"flow":"advanced","goal":"smaller","convertFails":{"end":"stop"},"afterCards":[]}}`, etag)
	if code != 200 {
		t.Fatalf("save: %d %s", code, body)
	}
	cur, _, err := st.Draft(id)
	if err != nil {
		t.Fatal(err)
	}
	if mustJSON(findNodeIn(cur.Doc, q.ID)) != mustJSON(q) || mustJSON(findNodeIn(cur.Doc, n.ID)) != mustJSON(n) {
		t.Errorf("the stretch the questions can't show changed")
	}
	if g, _ := draft(id); g.Answers == nil || g.Answers.ConvertFails == nil || g.Answers.ConvertFails.End != "stop" {
		t.Errorf("the readable part was not saved: %+v", g.Answers)
	}

	// a front join: no readable main path, so the draft has no answers and nothing is named
	front := workflow.Build(workflow.DefaultAnswers(workflow.GoalSmaller))
	skips := findNode(t, front, workflow.TypeSkips)
	chk := workflow.Node{ID: "front-check", Type: workflow.TypeCheckName, Enabled: true, Config: map[string]any{"pattern": "x"}}
	front.Nodes = append(front.Nodes, chk)
	for i := range front.Edges {
		if front.Edges[i].To == skips.ID {
			front.Edges[i].To = chk.ID
		}
	}
	front.Edges = append(front.Edges, workflow.Edge{From: chk.ID, FromPort: "match", To: skips.ID}, workflow.Edge{From: chk.ID, FromPort: "else", To: skips.ID})
	f2, err := st.AddWorkflow("Front join", front)
	if err != nil {
		t.Fatal(err)
	}
	g2, etag2 := draft(f2.ID)
	if g2.Answers != nil || len(g2.Unreadable) != 0 || g2.Advanced != "none" {
		t.Fatalf("front join guide: %+v", g2)
	}
	if code, b := send("PUT", "/api/v2/workflows/"+strconv.FormatInt(f2.ID, 10)+"/answers", `{"answers":{"flow":"advanced","goal":"smaller"}}`, etag2); code != 409 || errCode(b) != "not_guided" {
		t.Errorf("front join save: %d %s", code, b)
	}
}

func findNode(t *testing.T, d workflow.Doc, typ string) workflow.Node {
	t.Helper()
	for _, n := range d.Nodes {
		if n.Type == typ {
			return n
		}
	}
	t.Fatalf("no %s step", typ)
	return workflow.Node{}
}

func findNodeIn(d workflow.Doc, id string) workflow.Node {
	for _, n := range d.Nodes {
		if n.ID == id {
			return n
		}
	}
	return workflow.Node{}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
