// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package integrations

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A Jellyfin whose item search fails (500) after the update was taken: the step failed, so its words must not say
// Jellyfin was told, and must say what failed.
func TestJellyfinSearchFailureIsNotReadAsTold(t *testing.T) {
	auth := `MediaBrowser Token="` + jellyKey + `"`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != auth {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/Library/VirtualFolders":
			io.WriteString(w, `[{"Name":"Movies","ItemId":"lib1","Locations":["/data/movies"]}]`)
		case "/Library/Media/Updated":
			io.WriteString(w, "")
		case "/Items":
			http.Error(w, `{"error":"search broke"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	h := newHarness(t)
	saveInstance(t, h, Jellyfin, srv.URL, jellyKey, nil)
	file := "/data/movies/Film/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 {
		t.Fatalf("steps = %+v", steps)
	}
	if steps[0].OK {
		t.Errorf("a failed search is recorded as ok: %+v", steps[0])
	}
	if strings.Contains(steps[0].Words, "Told") {
		t.Errorf("step words %q say Jellyfin was told, but the search failed", steps[0].Words)
	}
	if want := "Couldn't tell Jellyfin (Home)"; !strings.HasPrefix(steps[0].Words, want) {
		t.Errorf("step words %q, want them to start %q and say the search failed", steps[0].Words, want)
	}
}
