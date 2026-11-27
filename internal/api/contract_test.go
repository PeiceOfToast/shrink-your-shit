package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// writeGolden writes internal/api/testdata/contract/<slug>.json from a real response. The UI's `npm run contract`
// checks each golden's body against the exported type named here, so a golden that drifts from types.ts fails CI.
func writeGolden(t *testing.T, slug, typeName, method, path string, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s %s: body is not JSON: %v", method, path, err)
		}
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
	writeGolden(t, "setup-get", "Setup", http.MethodGet, "/api/v2/setup",
		e.do(http.MethodGet, "/api/v2/setup", "", nil, false))
	writeGolden(t, "setup-post", "SetupDone", http.MethodPost, "/api/v2/setup",
		e.do(http.MethodPost, "/api/v2/setup", `{"account":{"password":"`+testPassword+`"},"library":null}`, write(nil), false))
	writeGolden(t, "health", "Health", http.MethodGet, "/api/v2/health",
		e.do(http.MethodGet, "/api/v2/health", "", nil, false))
	writeGolden(t, "signin", "none", http.MethodPost, "/api/v2/signin",
		e.do(http.MethodPost, "/api/v2/signin", `{"password":"`+testPassword+`"}`, write(nil), false))
	writeGolden(t, "signout", "none", http.MethodPost, "/api/v2/signout",
		e.do(http.MethodPost, "/api/v2/signout", "", write(nil), false))
}
