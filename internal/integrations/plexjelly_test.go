// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package integrations

import (
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const (
	plexToken        = "plex-token-123"
	jellyKey         = "jelly-key-456"
	plexIdent        = `{"MediaContainer":{"version":"1.41.3.9314-a0bfb8370"}}`
	plexSecs         = `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Movies","Location":[{"id":1,"path":"/data/movies"}]}]}}`
	jellyInfoJSON    = `{"Version":"10.10.3","ServerName":"home"}`
	jellyFoldersJSON = `[{"Name":"Movies","Locations":["/data/movies"]}]`
)

// sent is one call as the fake saw it: what the app sent, not what it meant.
type sent struct {
	Method, Path, Query, Auth string
}

// fakeMedia answers routes (path -> JSON) when the auth header carries want; otherwise it answers 401. It records
// every call.
type fakeMedia struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []sent
}

func newFakeMedia(t *testing.T, header, want string, routes map[string]string) *fakeMedia {
	t.Helper()
	f := &fakeMedia{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.calls = append(f.calls, sent{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get(header)})
		f.mu.Unlock()
		if r.Header.Get(header) != want {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMedia) seen() []sent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sent(nil), f.calls...)
}

func TestPlexTestSendsTheTokenAndReadsSections(t *testing.T) {
	f := newFakeMedia(t, "X-Plex-Token", plexToken, map[string]string{"/identity": plexIdent, "/library/sections": plexSecs})
	h := newHarness(t)
	res, err := h.svc.Test(TestIn{Kind: Plex, URL: strp(f.srv.URL), APIKey: strp(plexToken)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Message != "Connected to Plex 1.41.3." {
		t.Fatalf("test = %+v, want connected to Plex 1.41.3", res)
	}
	if res.Version == nil || *res.Version != "1.41.3" {
		t.Errorf("version = %v, want 1.41.3", res.Version)
	}
	if len(res.Folders) != 1 || res.Folders[0].TheirPath != "/data/movies" {
		t.Errorf("folders = %+v, want the section's location", res.Folders)
	}
	want := []sent{
		{"GET", "/identity", "", plexToken},
		{"GET", "/library/sections", "", plexToken},
	}
	if got := f.seen(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %+v, want %+v", got, want)
	}
}

func TestPlexTestWrongTokenIs401(t *testing.T) {
	f := newFakeMedia(t, "X-Plex-Token", plexToken, map[string]string{"/identity": plexIdent, "/library/sections": plexSecs})
	h := newHarness(t)
	res, err := h.svc.Test(TestIn{Kind: Plex, URL: strp(f.srv.URL), APIKey: strp("nope")})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Message != "Plex answered 401: the token is wrong." || res.Version != nil {
		t.Errorf("test = %+v, want the 401 message", res)
	}
	if strings.Contains(res.Message, "nope") {
		t.Errorf("message %q names the token", res.Message)
	}
}

func TestJellyfinTestSendsTheMediaBrowserHeader(t *testing.T) {
	auth := `MediaBrowser Token="` + jellyKey + `"`
	f := newFakeMedia(t, "Authorization", auth, map[string]string{"/System/Info": jellyInfoJSON, "/Library/VirtualFolders": jellyFoldersJSON})
	h := newHarness(t)
	res, err := h.svc.Test(TestIn{Kind: Jellyfin, URL: strp(f.srv.URL), APIKey: strp(jellyKey)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Message != "Connected to Jellyfin 10.10.3." {
		t.Fatalf("test = %+v, want connected to Jellyfin 10.10.3", res)
	}
	if len(res.Folders) != 1 || res.Folders[0].TheirPath != "/data/movies" {
		t.Errorf("folders = %+v, want the library's location", res.Folders)
	}
	want := []sent{
		{"GET", "/System/Info", "", auth},
		{"GET", "/Library/VirtualFolders", "", auth},
	}
	if got := f.seen(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %+v, want %+v", got, want)
	}
}

func TestJellyfinWrongKeyIs401(t *testing.T) {
	f := newFakeMedia(t, "Authorization", `MediaBrowser Token="`+jellyKey+`"`, map[string]string{"/System/Info": jellyInfoJSON})
	h := newHarness(t)
	res, err := h.svc.Test(TestIn{Kind: Jellyfin, URL: strp(f.srv.URL), APIKey: strp("nope")})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Message != "Jellyfin answered 401: the API key is wrong." {
		t.Errorf("test = %+v, want the 401 message", res)
	}
}

func TestPlexAndJellyfinSaveLikeTheArrs(t *testing.T) {
	h := newHarness(t)
	var ids []string
	for _, k := range []Kind{Plex, Jellyfin} {
		in, err := h.svc.Create(NewIntegration{Kind: k, URL: "http://10.0.0.5:32400", APIKey: "k"})
		if err != nil {
			t.Fatal(err)
		}
		if in.Name != kinds[k].label || !in.HasKey || in.Kind != k {
			t.Errorf("created %+v, want the %s kind with its key saved", in, k)
		}
		url := "http://10.0.0.9:8096"
		up, err := h.svc.Update(in.ID, Patch{URL: &url})
		if err != nil || up.URL != url || !up.HasKey {
			t.Errorf("update = %+v, %v; want the new address and the key kept", up, err)
		}
		ids = append(ids, in.ID)
	}
	list, err := h.svc.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v; want both", list, err)
	}
	for _, id := range ids {
		if err := h.svc.Delete(id); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := h.svc.List(); len(list) != 0 {
		t.Errorf("after delete: %+v, want none", list)
	}
}

func TestPlexVersionKeepsThreeParts(t *testing.T) {
	cases := map[string]string{
		"1.41.3.9314-a0bfb8370": "1.41.3",
		"1.40.0":                "1.40.0",
		"":                      "",
	}
	for in, want := range cases {
		if got := plexVersion(in); got != want {
			t.Errorf("plexVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
