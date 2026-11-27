// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
)

// guidedBuildable is the answers the engine can build today: copy the audio, every language, keep
// every subtitle, next to the original, at the quality given. Page 06's defaults use stereo AAC,
// which the item-7 gates still refuse, so the routes that must validate use these.
func guidedBuildable(quality int) map[string]any {
	return map[string]any{
		"format": "hevc", "quality": quality, "useGpu": false, "ifGpuFails": "cpu", "resolution": "keep",
		"slower": false, "audio": "as_is", "allLanguages": true, "languages": []string{},
		"languagesEverywhere": false, "subtitles": "keep_all", "subtitleLanguages": []string{},
		"fileType": "mkv", "includeDolbyVision": false, "original": "next_to_original", "folder": "",
	}
}

// answersJSON is an answers object as comparable JSON.
func answersJSON(t *testing.T, a any) string {
	t.Helper()
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal the answers: %v", err)
	}
	return string(raw)
}

type guideAnswers struct {
	Answers    map[string]any `json:"answers"`
	FitsGuided bool           `json:"fitsGuided"`
	Rev        int            `json:"rev"`
}

// conflictBody is the 409 changed_since envelope: the error and the draft's current revision.
type conflictBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Rev int `json:"rev"`
}

func changedSince(t *testing.T, rec *httptest.ResponseRecorder) conflictBody {
	t.Helper()
	var got conflictBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("conflict body: %v; %s", err, rec.Body)
	}
	return got
}

// The guided page's reads and its save: "new" opens on the defaults, an existing workflow on its
// own answers, and a save writes the answers' flow into the draft.
func TestGuidedAnswersRoutes(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	var fresh guideAnswers
	get(t, e, cookie, "/api/v2/workflows/new/answers", &fresh)
	if !fresh.FitsGuided || fresh.Rev != 0 || answersJSON(t, fresh.Answers) != answersJSON(t, flow.DefaultAnswers()) {
		t.Fatalf("new answers = %+v; want the guide's defaults at rev 0", fresh)
	}
	if fresh.Answers["original"] != "next_to_original" {
		t.Errorf("new original = %v, want next_to_original", fresh.Answers["original"])
	}

	// Setup's own workflow was made from the guided starter, so it opens on that starter's answers.
	var saved guideAnswers
	get(t, e, cookie, "/api/v2/workflows/1/answers", &saved)
	starter, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	own, fits := flow.ToAnswers(starter.Flow)
	if !fits || !saved.FitsGuided || saved.Rev != 0 || answersJSON(t, saved.Answers) != answersJSON(t, own) {
		t.Fatalf("the setup workflow's answers = %+v; want the starter's %+v at rev 0", saved, own)
	}

	// The guided save writes the answers' flow as the draft and answers with it.
	rec := e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": guidedBuildable(5)}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("put answers = %d %s; want 200", rec.Code, rec.Body)
	}
	var savedFlow struct {
		Flow flow.Flow `json:"flow"`
		Rev  int       `json:"rev"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &savedFlow); err != nil {
		t.Fatalf("put answers body: %v; %s", err, rec.Body)
	}
	if savedFlow.Rev != 1 || apiQualityOf(t, savedFlow.Flow) != 5 {
		t.Fatalf("put answers = rev %d quality %d; want rev 1 at quality 5", savedFlow.Rev, apiQualityOf(t, savedFlow.Flow))
	}
	// The read serves the revision the page sends back as baseRev.
	var reread guideAnswers
	get(t, e, cookie, "/api/v2/workflows/1/answers", &reread)
	if reread.Rev != 1 {
		t.Errorf("answers read after the save = rev %d, want 1", reread.Rev)
	}
	var got struct {
		Flow       flow.Flow `json:"flow"`
		Rev        int       `json:"rev"`
		FitsGuided bool      `json:"fitsGuided"`
	}
	get(t, e, cookie, "/api/v2/workflows/1", &got)
	if got.FitsGuided != true || apiQualityOf(t, got.Flow) != 5 {
		t.Fatalf("flow after the guided save = quality %d fitsGuided %v; want quality 5 and fitting",
			apiQualityOf(t, got.Flow), got.FitsGuided)
	}
	if rows := listWorkflowRows(t, e, cookie); !rowNamed(t, rows, "1").FitsGuided {
		t.Error("the list says the guided-saved workflow does not fit")
	}

	// An answer the guide does not offer is page 06's refusal on its field.
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": map[string]any{"quality": 9}}), write(withSession(cookie)), false)
	if code, message, env := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_answer" ||
		env["field"] != "quality" || message != "Quality is 1 to 5." {
		t.Fatalf("bad answers save = %d %q %v; want 400 invalid_answer on quality", rec.Code, code, env["field"])
	}
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/answers", "{}", write(withSession(cookie)), false)
	if code, _, env := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_request" || env["field"] != "answers" {
		t.Fatalf("answers save with no answers = %d %q %v", rec.Code, code, env["field"])
	}

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		path := "/api/v2/workflows/99/answers"
		body := ""
		if method == http.MethodPut {
			body = mustJSON(t, map[string]any{"answers": guidedBuildable(3)})
		}
		rec := e.do(method, path, body, write(withSession(cookie)), false)
		if code, _, _ := errorBody(t, rec); rec.Code != http.StatusNotFound || code != "workflow_not_found" {
			t.Fatalf("%s %s = %d %q; want 404 workflow_not_found", method, path, rec.Code, code)
		}
	}
}

// A guided save never overwrites an editor save silently when the page sends the revision it read:
// a stale baseRev is refused with the draft's current rev. With the current revision the guide's
// flow becomes the draft again, and its answers come back, even after an editor saved a flow the
// guide cannot say (lead, 11-27 20:31Z: refusing that save blocked every stage 1 install).
func TestAGuidedSaveOverAnEditorDraft(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	// What the guide opened on: the setup workflow's own answers at rev 0.
	var opened guideAnswers
	get(t, e, cookie, "/api/v2/workflows/1/answers", &opened)
	if !opened.FitsGuided || opened.Rev != 0 {
		t.Fatalf("opened = rev %d fits %v; want the setup workflow's answers at rev 0", opened.Rev, opened.FitsGuided)
	}

	// A guided save the page really did gets rev 1.
	rec := e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": guidedBuildable(4), "baseRev": opened.Rev}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("guided save = %d %s", rec.Code, rec.Body)
	}
	var saved struct {
		Rev int `json:"rev"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &saved); err != nil || saved.Rev != 1 {
		t.Fatalf("guided save rev = %d, %v; want 1", saved.Rev, err)
	}

	// The same page saves again with the revision it read before, which has moved on.
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": guidedBuildable(4), "baseRev": opened.Rev}),
		write(withSession(cookie)), false)
	if got := changedSince(t, rec); rec.Code != http.StatusConflict || got.Error.Code != "changed_since" ||
		got.Rev != 1 || !strings.Contains(got.Error.Message, "saved somewhere else") {
		t.Fatalf("stale baseRev save = %d %+v; want 409 changed_since at rev 1", rec.Code, got)
	}

	// With the revision it now holds, the save goes through.
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": guidedBuildable(4), "baseRev": 1}),
		write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("save with the current baseRev = %d %s; want 200", rec.Code, rec.Body)
	}

	// Someone edits the workflow in the editor into a flow the guide cannot say: Audio's Advanced
	// codec Copy. That save clears the answers.
	edited := apiFlow(t, "Edited", 3)
	audio, err := json.Marshal(map[string]any{"format": "copy", "codec": "copy"})
	if err != nil {
		t.Fatal(err)
	}
	edited.Steps[1].Settings = audio
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/draft",
		mustJSON(t, map[string]any{"flow": edited, "baseRev": 2}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("editor save = %d %s; want 200", rec.Code, rec.Body)
	}

	// The guided page's save with the revision it read before the editor saved is refused, and the
	// 409 carries the draft's current revision.
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": guidedBuildable(5), "baseRev": 1}), write(withSession(cookie)), false)
	if got := changedSince(t, rec); rec.Code != http.StatusConflict || got.Error.Code != "changed_since" ||
		got.Rev != 3 {
		t.Fatalf("guided save over the editor draft = %d %+v; want 409 changed_since at rev 3", rec.Code, got)
	}

	// With the current revision the save is allowed: the person chose the guide's flow, so its flow
	// and answers replace the editor's draft.
	rec = e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": guidedBuildable(5), "baseRev": 3}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("guided save at the current rev = %d %s; want 200", rec.Code, rec.Body)
	}
	var after struct {
		Flow       flow.Flow `json:"flow"`
		FitsGuided bool      `json:"fitsGuided"`
	}
	get(t, e, cookie, "/api/v2/workflows/1", &after)
	if !after.FitsGuided || apiQualityOf(t, after.Flow) != 5 {
		t.Fatalf("workflow after the guided save = quality %d fits %v; want the guide's flow, fitting",
			apiQualityOf(t, after.Flow), after.FitsGuided)
	}
}

// A stage 1 install wrote workflows without answers. The read computes them from the flow when the
// guide can say it, so the workflow opens in the guide, and nothing is written back; a guided save
// without baseRev then goes through.
func TestAWorkflowWithoutStoredAnswersOpensInTheGuide(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	// What a stage 1 install left behind on the setup workflow: the flow, no answers.
	err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE workflows SET answers = NULL WHERE id = 1`)
		return err
	})
	if err != nil {
		t.Fatalf("clear the answers: %v", err)
	}

	var got guideAnswers
	get(t, e, cookie, "/api/v2/workflows/1/answers", &got)
	starter, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	want, fits := flow.ToAnswers(starter.Flow)
	if !fits || !got.FitsGuided || answersJSON(t, got.Answers) != answersJSON(t, want) {
		t.Fatalf("answers = %+v; want the flow's own %+v, fitting", got, want)
	}
	if row := rowNamed(t, listWorkflowRows(t, e, cookie), "1"); !row.FitsGuided {
		t.Error("the list says a workflow whose flow fits the guide does not fit it")
	}
	var stored sql.NullString
	if err := e.db.Read().QueryRowContext(t.Context(),
		`SELECT answers FROM workflows WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatalf("read the answers column: %v", err)
	}
	if stored.Valid {
		t.Error("the answers read wrote them back; a read stays a read")
	}

	rec := e.do(http.MethodPut, "/api/v2/workflows/1/answers",
		mustJSON(t, map[string]any{"answers": guidedBuildable(5)}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("guided save without baseRev = %d %s; want 200", rec.Code, rec.Body)
	}
}

// The preview is page 06's "What this does": the sentences, the flow and the editor's check, with
// nothing saved.
func TestGuidedPreview(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	var preview struct {
		Sentences []string        `json:"sentences"`
		Flow      flow.Flow       `json:"flow"`
		Problems  []flow.Problem  `json:"problems"`
		Disabled  []flow.Disabled `json:"disabled"`
	}
	rec := e.do(http.MethodPost, "/api/v2/workflows/preview",
		mustJSON(t, map[string]any{"answers": flow.DefaultAnswers()}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview = %d %s; want 200", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("preview body: %v; %s", err, rec.Body)
	}
	want := []string{
		"Converts video to HEVC at quality 3, keeping the picture size, on the graphics card when it can.",
		"Audio: AAC, stereo, every language.",
		"Keeps every subtitle.",
		"Saves as MKV.",
		"Puts the new file next to the original; the original stays where it is.",
	}
	if !reflect.DeepEqual(preview.Sentences, want) {
		t.Errorf("sentences = %q, want page 06's %q", preview.Sentences, want)
	}
	if apiQualityOf(t, preview.Flow) != 3 {
		t.Errorf("preview quality = %d, want 3", apiQualityOf(t, preview.Flow))
	}
	// The defaults are what the guide starts a person on, so they must be publishable today.
	if len(preview.Problems) != 0 {
		t.Errorf("preview of the defaults has problems: %+v", preview.Problems)
	}
	if rows := listWorkflowRows(t, e, cookie); len(rows) != 1 {
		t.Errorf("preview made rows: %+v; a preview saves nothing", rows)
	}

	rec = e.do(http.MethodPost, "/api/v2/workflows/preview",
		mustJSON(t, map[string]any{"answers": guidedBuildable(3)}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("buildable preview = %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Problems) != 0 {
		t.Errorf("buildable preview problems = %+v, want none", preview.Problems)
	}

	rec = e.do(http.MethodPost, "/api/v2/workflows/preview",
		mustJSON(t, map[string]any{"answers": map[string]any{"quality": 0}}), write(withSession(cookie)), false)
	if code, _, env := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_answer" || env["field"] != "quality" {
		t.Fatalf("bad preview = %d %q %v; want 400 invalid_answer on quality", rec.Code, code, env["field"])
	}
	rec = e.do(http.MethodPost, "/api/v2/workflows/preview", "{}", write(withSession(cookie)), false)
	if code, _, env := errorBody(t, rec); rec.Code != http.StatusBadRequest || code != "invalid_request" || env["field"] != "answers" {
		t.Fatalf("preview with no answers = %d %q %v", rec.Code, code, env["field"])
	}
}

// A workflow the editor made into something the guide cannot say keeps working, but it does not
// claim to fit: the list, the editor's read and the answers read all say so, and the answers read
// opens on the guide's defaults instead of an empty form.
func TestAFlowTheGuideCannotSayDoesNotFit(t *testing.T) {
	e := newEnv(t, true)
	cookie := e.setup()

	// "Copy" in Audio's Advanced codec is a stored pick the guide has no answer for; it validates,
	// so it is a real workflow, not a broken one.
	f := apiFlow(t, "Copy codec", 3)
	audio := map[string]any{"format": "copy", "codec": "copy"}
	settings, err := json.Marshal(audio)
	if err != nil {
		t.Fatal(err)
	}
	f.Steps[1].Settings = settings
	rec := e.do(http.MethodPost, "/api/v2/workflows",
		mustJSON(t, map[string]any{"name": "Copy codec", "flow": f}), write(withSession(cookie)), false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var made struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &made); err != nil {
		t.Fatal(err)
	}
	var got struct {
		FitsGuided bool `json:"fitsGuided"`
	}
	get(t, e, cookie, "/api/v2/workflows/"+made.ID, &got)
	if got.FitsGuided {
		t.Error("a flow the guide cannot say reports fitsGuided true")
	}
	if row := rowNamed(t, listWorkflowRows(t, e, cookie), made.ID); row.FitsGuided {
		t.Error("the list says a flow the guide cannot say fits")
	}
	var answers guideAnswers
	get(t, e, cookie, "/api/v2/workflows/"+made.ID+"/answers", &answers)
	if answers.FitsGuided || answersJSON(t, answers.Answers) != answersJSON(t, flow.DefaultAnswers()) {
		t.Errorf("answers read = %+v; want the defaults with fitsGuided false", answers)
	}
	// Saving the guide's answers is then the person's choice (the page sent them to the editor, and
	// they came back to the guide): the save with no baseRev replaces the draft with the guide's
	// flow and writes its answers.
	rec = e.do(http.MethodPut, "/api/v2/workflows/"+made.ID+"/answers",
		mustJSON(t, map[string]any{"answers": flow.DefaultAnswers()}), write(withSession(cookie)), false)
	if rec.Code != http.StatusOK {
		t.Fatalf("guided save over a flow the guide cannot say = %d %s; want 200", rec.Code, rec.Body)
	}
	var after struct {
		Flow       flow.Flow `json:"flow"`
		FitsGuided bool      `json:"fitsGuided"`
	}
	get(t, e, cookie, "/api/v2/workflows/"+made.ID, &after)
	if !after.FitsGuided || apiQualityOf(t, after.Flow) != 3 {
		t.Errorf("after the guided save the workflow = quality %d fits %v; want the guide's flow, fitting",
			apiQualityOf(t, after.Flow), after.FitsGuided)
	}
}
