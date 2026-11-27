package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	// The real steps, so the step-definitions golden is the registry the binary serves.
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/flow/steps"
)

// writeGolden writes internal/api/testdata/contract/<slug>.json from a real response. The UI's `npm run contract`
// checks each golden's body against the exported type named here, so a golden that drifts from types.ts fails CI.
// Each normalize string is replaced with $DIR in the body, so a temp folder does not rewrite the golden on every run.
func writeGolden(t *testing.T, slug, typeName, method, path string, rec *httptest.ResponseRecorder, normalize ...string) {
	t.Helper()
	var body json.RawMessage
	if rec.Body.Len() > 0 {
		var v any
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("%s %s: body is not JSON: %v", method, path, err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		// Only the body is normalized: the request path is the same on every run.
		for _, n := range normalize {
			raw = bytes.ReplaceAll(raw, []byte(n), []byte("$DIR"))
		}
		body = raw
	} else {
		body = json.RawMessage("null")
	}
	out, err := json.MarshalIndent(map[string]any{
		"type": typeName, "method": method, "path": path, "status": rec.Code, "body": body,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("testdata", "contract")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, slug+".json"), append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// One golden per route, from the live answer, so the UI sees the real shape.
func TestContractGoldens(t *testing.T) {
	e := newEnv(t, true)
	dir := t.TempDir()
	clips := filepath.Join(dir, "clips")
	if err := os.MkdirAll(filepath.Join(clips, "Family"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clips, "one.mkv"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	more := filepath.Join(dir, "more")
	if err := os.MkdirAll(more, 0o755); err != nil {
		t.Fatal(err)
	}

	writeGolden(t, "setup-get", "Setup", http.MethodGet, "/api/v2/setup",
		e.do(http.MethodGet, "/api/v2/setup", "", nil, false))
	rec := e.do(http.MethodPost, "/api/v2/setup",
		setupWith(t, map[string]any{"name": "Clips", "path": clips}),
		write(nil), false)
	cookie := cookieNamed(t, rec, sessionCookie)
	writeGolden(t, "setup-post", "SetupDone", http.MethodPost, "/api/v2/setup", rec)
	writeGolden(t, "health", "Health", http.MethodGet, "/api/v2/health",
		e.do(http.MethodGet, "/api/v2/health", "", nil, false))
	writeGolden(t, "signin", "none", http.MethodPost, "/api/v2/signin",
		e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+testPassword+`"}`, write(nil), false))
	writeGolden(t, "signout", "none", http.MethodPost, "/api/v2/signout",
		e.do(http.MethodPost, "/api/v2/signout", "", write(nil), false))

	// The first-run page's reads (R1-9a).
	writeGolden(t, "languages", "Language[]", http.MethodGet, "/api/v2/languages",
		e.do(http.MethodGet, "/api/v2/languages", "", withSession(cookie), false))
	writeGolden(t, "starters", "Starter[]", http.MethodGet, "/api/v2/workflows/starters",
		e.do(http.MethodGet, "/api/v2/workflows/starters", "", withSession(cookie), false))
	writeGolden(t, "starter", "StarterDetail", http.MethodGet, "/api/v2/workflows/starters/shrink-videos",
		e.do(http.MethodGet, "/api/v2/workflows/starters/shrink-videos", "", withSession(cookie), false))
	writeGolden(t, "guide", "GuideDoc", http.MethodGet, "/api/v2/workflows/guide",
		e.do(http.MethodGet, "/api/v2/workflows/guide", "", withSession(cookie), false))
	writeGolden(t, "folders", "FolderListing", http.MethodGet, "/api/v2/folders?path=$DIR/clips",
		e.do(http.MethodGet, "/api/v2/folders?path="+clips, "", withSession(cookie), false), dir)
	writeGolden(t, "libraries-check", "FolderCheck", http.MethodPost, "/api/v2/libraries/check",
		e.do(http.MethodPost, "/api/v2/libraries/check", mustJSON(t, map[string]any{"path": clips}), write(withSession(cookie)), false), dir)

	// Libraries (R1-9a).
	writeGolden(t, "libraries", "Library[]", http.MethodGet, "/api/v2/libraries",
		e.do(http.MethodGet, "/api/v2/libraries", "", withSession(cookie), false), dir)
	writeGolden(t, "library", "Library", http.MethodGet, "/api/v2/libraries/1",
		e.do(http.MethodGet, "/api/v2/libraries/1", "", withSession(cookie), false), dir)
	writeGolden(t, "library-post", "Library", http.MethodPost, "/api/v2/libraries",
		e.do(http.MethodPost, "/api/v2/libraries", mustJSON(t, map[string]any{"name": "More", "path": more}), write(withSession(cookie)), false), dir)
	writeGolden(t, "library-scan", "Library", http.MethodPost, "/api/v2/libraries/1/scan",
		e.do(http.MethodPost, "/api/v2/libraries/1/scan", "", write(withSession(cookie)), false), dir)
	writeGolden(t, "libraries-scan", "ScanAllDone", http.MethodPost, "/api/v2/libraries/scan",
		e.do(http.MethodPost, "/api/v2/libraries/scan", "", write(withSession(cookie)), false), dir)

	// The steps' cards (S2-1): page 07's form, from the registry.
	writeGolden(t, "step-definitions", "StepDefinition[]", http.MethodGet, "/api/v2/step-definitions",
		e.do(http.MethodGet, "/api/v2/step-definitions", "", withSession(cookie), false))

	// The flow editor's check (S2-2): the starter's flow as page 07 sends it.
	starter, ok := flow.StarterByID("shrink-videos")
	if !ok {
		t.Fatal("no shrink-videos starter")
	}
	// Read the guided starter's answers before the editor's flow below shares and edits its steps.
	starterGuided, fits := flow.ToAnswers(starter.Flow)
	if !fits {
		t.Fatal("the guided starter's answers do not rebuild it")
	}
	writeGolden(t, "workflow-validate", "FlowCheck", http.MethodPost, "/api/v2/workflows/1/validate",
		e.do(http.MethodPost, "/api/v2/workflows/1/validate",
			mustJSON(t, map[string]any{"flow": starter.Flow}), write(withSession(cookie)), false))

	// Workflows (S2-3): the list, the editor's document, create, save, publish, the history, restore,
	// duplicate, and delete refused while a library uses the workflow.
	editorFlow := starter.Flow
	editorFlow.Name = "Holidays"
	writeGolden(t, "workflows", "WorkflowRow[]", http.MethodGet, "/api/v2/workflows",
		e.do(http.MethodGet, "/api/v2/workflows", "", withSession(cookie), false))
	writeGolden(t, "workflow", "WorkflowDraft", http.MethodGet, "/api/v2/workflows/1",
		e.do(http.MethodGet, "/api/v2/workflows/1", "", withSession(cookie), false))
	rec = e.do(http.MethodPost, "/api/v2/workflows",
		mustJSON(t, map[string]any{"name": "Holidays", "flow": editorFlow}), write(withSession(cookie)), false)
	writeGolden(t, "workflow-create", "WorkflowCreated", http.MethodPost, "/api/v2/workflows", rec)
	editorFlow.Steps[0].Settings = json.RawMessage(`{"format":"hevc","quality":4,"useGpu":false,"ifGpuFails":"cpu"}`)
	writeGolden(t, "workflow-draft", "DraftSaved", http.MethodPut, "/api/v2/workflows/2/draft",
		e.do(http.MethodPut, "/api/v2/workflows/2/draft",
			mustJSON(t, map[string]any{"flow": editorFlow, "baseRev": 1}), write(withSession(cookie)), false))
	writeGolden(t, "workflow-publish", "WorkflowPublished", http.MethodPost, "/api/v2/workflows/2/publish",
		e.do(http.MethodPost, "/api/v2/workflows/2/publish",
			mustJSON(t, map[string]any{"flow": editorFlow}), write(withSession(cookie)), false))
	writeGolden(t, "workflow-revisions", "WorkflowRevision[]", http.MethodGet, "/api/v2/workflows/2/revisions",
		e.do(http.MethodGet, "/api/v2/workflows/2/revisions", "", withSession(cookie), false))
	writeGolden(t, "workflow-restore", "WorkflowRestored", http.MethodPost, "/api/v2/workflows/2/revisions/1/restore",
		e.do(http.MethodPost, "/api/v2/workflows/2/revisions/1/restore", "", write(withSession(cookie)), false))
	writeGolden(t, "workflow-duplicate", "WorkflowRow", http.MethodPost, "/api/v2/workflows/2/duplicate",
		e.do(http.MethodPost, "/api/v2/workflows/2/duplicate", "", write(withSession(cookie)), false))
	// The guided builder (S2-4): the answers a workflow opens on, "new" for the defaults, the save
	// that turns them into the draft, and "What this does" from the starter's answers.
	writeGolden(t, "workflow-answers", "GuideAnswers", http.MethodGet, "/api/v2/workflows/1/answers",
		e.do(http.MethodGet, "/api/v2/workflows/1/answers", "", withSession(cookie), false))
	writeGolden(t, "workflow-answers-new", "GuideAnswers", http.MethodGet, "/api/v2/workflows/new/answers",
		e.do(http.MethodGet, "/api/v2/workflows/new/answers", "", withSession(cookie), false))
	guidedAnswers := flow.DefaultAnswers()
	guidedAnswers["quality"] = 4
	writeGolden(t, "workflow-answers-put", "AnswersSaved", http.MethodPut, "/api/v2/workflows/2/answers",
		e.do(http.MethodPut, "/api/v2/workflows/2/answers",
			mustJSON(t, map[string]any{"answers": guidedAnswers}), write(withSession(cookie)), false))
	writeGolden(t, "workflow-preview", "GuidePreview", http.MethodPost, "/api/v2/workflows/preview",
		e.do(http.MethodPost, "/api/v2/workflows/preview",
			mustJSON(t, map[string]any{"answers": starterGuided}), write(withSession(cookie)), false))
	writeGolden(t, "workflow-delete", "WorkflowDeleted", http.MethodDelete, "/api/v2/workflows/1",
		e.do(http.MethodDelete, "/api/v2/workflows/1", "", write(withSession(cookie)), false))

	// Summary and attention (R1-9a).
	writeGolden(t, "summary", "Summary", http.MethodGet, "/api/v2/summary",
		e.do(http.MethodGet, "/api/v2/summary", "", withSession(cookie), false), dir)
	writeGolden(t, "attention", "Attention[]", http.MethodGet, "/api/v2/attention",
		e.do(http.MethodGet, "/api/v2/attention", "", withSession(cookie), false), dir)
}
