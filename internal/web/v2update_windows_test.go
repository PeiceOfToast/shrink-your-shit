// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"testing"
)

// On Windows the button is the helper's: the install is offered and asked for, and no roll back is offered (the
// watcher puts the previous version back by itself when an update fails).
func TestWindowsUpdateButtonIsTheHelpers(t *testing.T) {
	srv, u, _, fake := installServer(t, "windows")
	u.goos = "windows"
	_, body := getJSON(t, srv.URL+"/api/v2/update")
	if body["method"] != "helper" {
		t.Fatalf("method %v, want helper", body["method"])
	}
	if rb := body["rollback"].(map[string]any); rb["available"] != false {
		t.Errorf("rollback %v, want not available", rb)
	}
	if code, body := postJSON(t, srv.URL+"/api/v2/update/install", `{"when":"now"}`); code != http.StatusAccepted {
		t.Fatalf("install: %d %v", code, body)
	}
	if got := fake.installs(); len(got) != 1 {
		t.Errorf("helper got %v", got)
	}
	if code, body := postJSON(t, srv.URL+"/api/v2/update/rollback", `{}`); code != http.StatusConflict {
		t.Errorf("rollback: %d %v, want refused", code, body)
	}
}
