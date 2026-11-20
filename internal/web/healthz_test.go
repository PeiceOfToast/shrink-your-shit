// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// User item 9: /healthz is "ok" only when the database answers, and only an "ok" settles an update.
func TestHealthzNeedsTheDatabase(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	a, _, err := OpenAccess(t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	healthy := 0
	srv := httptest.NewServer(Handler(st, a, Engine{Healthy: func() { healthy++ }}))
	t.Cleanup(srv.Close)
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/healthz"}); code != 200 || body != "ok\n" || healthy != 1 {
		t.Fatalf("healthz with a working database: %d %q, settled %d times", code, body, healthy)
	}
	st.Close() // the program is up but its database is gone
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/healthz"}); code != 503 || body != "database not answering\n" || healthy != 1 {
		t.Errorf("healthz with a closed database: %d %q, settled %d times", code, body, healthy)
	}
}
