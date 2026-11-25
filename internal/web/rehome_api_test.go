// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"strings"
	"testing"
)

// U-B: Empty trash sends what it could not reach as a list, [] when there is nothing, never null.
func TestEmptyTrashSendsUnreachableAsAList(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, body := post(t, srv, "POST", "/api/v2/trash/purge", `{}`)
	if code != 200 {
		t.Fatalf("empty trash: status %d: %s", code, body)
	}
	if !strings.Contains(body, `"unreachable":[]`) {
		t.Errorf("want \"unreachable\":[] beside deletedFiles: %s", body)
	}
}

// U-B: forgetting an id that is not a kept original is a 404, and nothing is changed.
func TestForgetAnUnknownOriginalIsNotFound(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, body := post(t, srv, "POST", "/api/v2/originals/999999/forget", "")
	if code != 404 {
		t.Errorf("forget unknown id: status %d, want 404: %s", code, body)
	}
}
