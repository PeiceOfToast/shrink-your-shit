// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package integrations

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

const (
	plexToken        = "plex-token-123"
	jellyKey         = "jelly-key-456"
	plexIdent        = `{"MediaContainer":{"version":"1.41.3.9314-a0bfb8370"}}`
	plexSecs         = `{"MediaContainer":{"size":1,"Directory":[{"key":"1","title":"Movies","Location":[{"id":1,"path":"/data/movies"}]}]}}`
	jellyInfoJSON    = `{"Version":"10.10.3","ServerName":"home"}`
	jellyFoldersJSON = `[{"Name":"Movies","ItemId":"lib1","Locations":["/data/movies"]}]`
)

// sent is one call as the fake saw it: what the app sent, not what it meant.
type sent struct {
	Method, Path, Query, Auth string
}

// fakeMedia answers routes (path -> JSON) when the auth header carries want; otherwise it answers 401. It records
// every call.
type fakeMedia struct {
	srv    *httptest.Server
	mu     sync.Mutex
	calls  []sent
	posted []string // the bodies of the POST calls
}

func newFakeMedia(t *testing.T, header, want string, routes map[string]string) *fakeMedia {
	t.Helper()
	f := &fakeMedia{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, sent{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get(header)})
		if r.Method == "POST" {
			f.posted = append(f.posted, string(b))
		}
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

// saveInstance saves an instance named Home and returns its id.
func saveInstance(t *testing.T, h harness, k Kind, addr, key string, maps []Mapping) string {
	t.Helper()
	in, err := h.svc.Create(NewIntegration{Kind: k, Name: "Home", URL: addr, APIKey: key, PathMappings: maps})
	if err != nil {
		t.Fatal(err)
	}
	return in.ID
}

// tellOnce tells the instances about one replaced file (from was replaced by file) and returns the job's steps.
func tellOnce(t *testing.T, h harness, from, file string) []jobs.Step {
	t.Helper()
	id := h.job(t, from)
	h.svc.Tell(id, from, file)
	h.svc.Wait()
	steps, err := h.st.Steps(id)
	if err != nil {
		t.Fatal(err)
	}
	return steps
}

func TestPlexScansTheFilesFolderInItsSection(t *testing.T) {
	f := newFakeMedia(t, "X-Plex-Token", plexToken, map[string]string{
		"/library/sections":           plexSecs,
		"/library/sections/1/refresh": "",
	})
	h := newHarness(t)
	saveInstance(t, h, Plex, f.srv.URL, plexToken, nil)
	file := "/data/movies/Film/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || steps[0].Step != RefreshStep || !steps[0].OK || steps[0].Words != "Told Plex (Home) to scan the folder." {
		t.Fatalf("steps = %+v", steps)
	}
	want := []sent{
		{"GET", "/library/sections", "", plexToken},
		{"GET", "/library/sections/1/refresh", "path=%2Fdata%2Fmovies%2FFilm", plexToken},
	}
	if got := f.seen(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %+v, want %+v", got, want)
	}
	if strings.Contains(h.logs.String(), plexToken) {
		t.Errorf("the token reached the log: %q", h.logs.String())
	}
}

func TestPlexWarnsWhenNoSectionHoldsTheFolder(t *testing.T) {
	f := newFakeMedia(t, "X-Plex-Token", plexToken, map[string]string{
		"/library/sections":           plexSecs,
		"/library/sections/1/refresh": "",
	})
	h := newHarness(t)
	saveInstance(t, h, Plex, f.srv.URL, plexToken, nil)
	file := "/elsewhere/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || steps[0].OK || steps[0].Words != "Plex has no library for this folder; nothing to scan." {
		t.Fatalf("steps = %+v, want one warning", steps)
	}
	if got := f.seen(); len(got) != 1 || got[0].Path != "/library/sections" {
		t.Errorf("calls = %+v, want only the sections read", got)
	}
}

func TestPlexWrongTokenIsAWarning(t *testing.T) {
	f := newFakeMedia(t, "X-Plex-Token", plexToken, map[string]string{"/library/sections": plexSecs})
	h := newHarness(t)
	saveInstance(t, h, Plex, f.srv.URL, "nope", nil)
	file := "/data/movies/Film/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || steps[0].OK || steps[0].Words != "Plex answered 401: the token is wrong." {
		t.Fatalf("steps = %+v, want the 401 warning", steps)
	}
}

func TestPlexThatDoesNotAnswerIsAWarning(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
	}))
	t.Cleanup(slow.Close)
	h := newHarness(t)
	h.svc = New(h.st, Options{Client: &http.Client{Timeout: 50 * time.Millisecond}, Logf: h.logs.logf})
	saveInstance(t, h, Plex, slow.URL, plexToken, nil)
	file := "/data/movies/Film/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || steps[0].OK || !strings.HasPrefix(steps[0].Words, "Plex didn't answer in") ||
		!strings.Contains(steps[0].Words, noNextScan+".") {
		t.Fatalf("steps = %+v, want a warning that Plex did not answer", steps)
	}
}

func TestJellyfinIsToldTheFileChangedUnderItsMapping(t *testing.T) {
	auth := `MediaBrowser Token="` + jellyKey + `"`
	f := newFakeMedia(t, "Authorization", auth, map[string]string{
		"/Library/VirtualFolders": jellyFoldersJSON,
		"/Library/Media/Updated":  "",
		"/Items":                  `{"Items":[]}`,
	})
	h := newHarness(t)
	saveInstance(t, h, Jellyfin, f.srv.URL, jellyKey, []Mapping{{TheirPath: "/data/movies", OurPath: "/mnt/media/movies"}})
	file := "/mnt/media/movies/Film/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || !steps[0].OK || steps[0].Words != "Told Jellyfin (Home) the file changed." {
		t.Fatalf("steps = %+v", steps)
	}
	var body struct {
		Updates []jellyUpdate `json:"Updates"`
	}
	if len(f.posted) != 1 {
		t.Fatalf("posted %d bodies, want 1", len(f.posted))
	}
	if err := json.Unmarshal([]byte(f.posted[0]), &body); err != nil {
		t.Fatal(err)
	}
	want := []jellyUpdate{{Path: "/data/movies/Film/film.mkv", UpdateType: "Modified"}}
	if !reflect.DeepEqual(body.Updates, want) {
		t.Errorf("updates = %+v, want %+v", body.Updates, want)
	}
	want2 := []sent{
		{"GET", "/Library/VirtualFolders", "", auth},
		{"POST", "/Library/Media/Updated", "", auth},
		{"GET", "/Items", jellyListQuery(0), auth},
	}
	if got := f.seen(); !reflect.DeepEqual(got, want2) {
		t.Errorf("calls = %+v, want %+v", got, want2)
	}
}

func TestJellyfinGetsADeleteAndACreateWhenTheExtensionChanges(t *testing.T) {
	cases := []struct {
		name, from, file string
		want             []jellyUpdate
	}{
		{"extension changed", "/data/movies/Film/film.avi", "/data/movies/Film/film.mkv",
			[]jellyUpdate{{Path: "/data/movies/Film/film.avi", UpdateType: "Deleted"}, {Path: "/data/movies/Film/film.mkv", UpdateType: "Created"}}},
		{"same extension in other case", "/data/movies/Film/film.MP4", "/data/movies/Film/film.mp4",
			[]jellyUpdate{{Path: "/data/movies/Film/film.mp4", UpdateType: "Modified"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeMedia(t, "Authorization", `MediaBrowser Token="`+jellyKey+`"`, map[string]string{
				"/Library/VirtualFolders": jellyFoldersJSON,
				"/Library/Media/Updated":  "",
				"/Items":                  `{"Items":[]}`,
			})
			h := newHarness(t)
			saveInstance(t, h, Jellyfin, f.srv.URL, jellyKey, nil)
			steps := tellOnce(t, h, c.from, c.file)
			if len(steps) != 1 || !steps[0].OK {
				t.Fatalf("steps = %+v", steps)
			}
			var body struct {
				Updates []jellyUpdate `json:"Updates"`
			}
			if len(f.posted) != 1 {
				t.Fatalf("posted %d bodies, want 1", len(f.posted))
			}
			if err := json.Unmarshal([]byte(f.posted[0]), &body); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(body.Updates, c.want) {
				t.Errorf("updates = %+v, want %+v", body.Updates, c.want)
			}
		})
	}
}

// jellyListQuery is the query that lists the library lib1 from item start, a page at a time.
func jellyListQuery(start int) string {
	return url.Values{"ParentId": {"lib1"}, "Recursive": {"true"}, "IncludeItemTypes": {"Movie,Episode,Video"},
		"Fields": {"Path"}, "EnableImages": {"false"}, "EnableUserData": {"false"},
		"StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(jellyPage)}}.Encode()
}

// newJellyLibrary is a Jellyfin with one library, lib1, holding paths, listed a page at a time. Its title search finds
// nothing, as Jellyfin's would not for a file named for its release. It returns the server and the items refreshed.
func newJellyLibrary(t *testing.T, paths []string) (*httptest.Server, *[]string) {
	t.Helper()
	auth := `MediaBrowser Token="` + jellyKey + `"`
	var refreshed []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != auth {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/Library/VirtualFolders":
			io.WriteString(w, `[{"Name":"Movies","ItemId":"lib1","Locations":["/data/movies"]}]`)
		case r.URL.Path == "/Library/Media/Updated":
			io.WriteString(w, "")
		case r.URL.Path == "/Items":
			q := r.URL.Query()
			if q.Get("searchTerm") != "" || q.Get("ParentId") != "lib1" {
				io.WriteString(w, `{"Items":[],"TotalRecordCount":0}`)
				return
			}
			start, _ := strconv.Atoi(q.Get("StartIndex"))
			end := start + jellyPage
			if end > len(paths) {
				end = len(paths)
			}
			var items []map[string]string
			for i := start; i < end; i++ {
				items = append(items, map[string]string{"Id": "id" + strconv.Itoa(i), "Path": paths[i]})
			}
			json.NewEncoder(w).Encode(map[string]any{"Items": items, "TotalRecordCount": len(paths)})
		case strings.HasPrefix(r.URL.Path, "/Items/") && strings.HasSuffix(r.URL.Path, "/Refresh"):
			refreshed = append(refreshed, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/Items/"), "/Refresh"))
			io.WriteString(w, "")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &refreshed
}

// TestJellyfinRefreshesAReleaseNamedFileByItsPath: the title Jellyfin gave the file is not its name, and the item is
// still found and refreshed.
func TestJellyfinRefreshesAReleaseNamedFileByItsPath(t *testing.T) {
	file := "/data/movies/Some.Movie.2019.1080p.WEB-DL.x264-GRP.mkv"
	srv, refreshed := newJellyLibrary(t, []string{"/data/movies/Other/other.mkv", file})
	h := newHarness(t)
	saveInstance(t, h, Jellyfin, srv.URL, jellyKey, nil)
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || !steps[0].OK {
		t.Fatalf("steps = %+v", steps)
	}
	if want := []string{"id1"}; !reflect.DeepEqual(*refreshed, want) {
		t.Errorf("refreshed = %v, want %v", *refreshed, want)
	}
}

// TestJellyfinFindsTheItemOnALaterPage: a library of 1,200 items is listed in pages of 500, and the item on the third
// page is found; the walk stops there.
func TestJellyfinFindsTheItemOnALaterPage(t *testing.T) {
	var paths []string
	for i := 0; i < 1200; i++ {
		paths = append(paths, "/data/movies/Film"+strconv.Itoa(i)+"/film"+strconv.Itoa(i)+".mkv")
	}
	file := paths[1100]
	srv, refreshed := newJellyLibrary(t, paths)
	h := newHarness(t)
	saveInstance(t, h, Jellyfin, srv.URL, jellyKey, nil)
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || !steps[0].OK {
		t.Fatalf("steps = %+v", steps)
	}
	if want := []string{"id1100"}; !reflect.DeepEqual(*refreshed, want) {
		t.Errorf("refreshed = %v, want %v", *refreshed, want)
	}
}

// TestSamePathFoldsOnlyWindowsPaths: a Windows path matches without case and with either separator; a POSIX path
// must match exactly.
func TestSamePathFoldsOnlyWindowsPaths(t *testing.T) {
	cases := []struct {
		item, file string
		want       bool
	}{
		{`D:\Movies\Film\film.mkv`, `d:/movies/FILM/Film.MKV`, true},
		{`D:/Movies/Film/film.mkv`, `D:\Movies\Film\film.mkv`, true},
		{`D:\Movies\Film\film.mkv`, `D:\Movies\Other\film.mkv`, false},
		{"/data/movies/Film/film.mkv", "/data/movies/film/Film.mkv", false},
		{"/data/movies/Film/film.mkv", "/data/movies/Film/film.mkv", true},
	}
	for _, c := range cases {
		if got := samePath(c.item, c.file); got != c.want {
			t.Errorf("samePath(%q, %q) = %v, want %v", c.item, c.file, got, c.want)
		}
	}
}

func TestJellyfinRefreshesTheItemWithTheFilesPath(t *testing.T) {
	auth := `MediaBrowser Token="` + jellyKey + `"`
	f := newFakeMedia(t, "Authorization", auth, map[string]string{
		"/Library/VirtualFolders": jellyFoldersJSON,
		"/Library/Media/Updated":  "",
		"/Items":                  `{"Items":[{"Id":"other1","Path":"/data/other/Film/film.mkv"},{"Id":"abc123","Path":"/data/movies/Film/film.mkv"}]}`,
		"/Items/abc123/Refresh":   "",
	})
	h := newHarness(t)
	saveInstance(t, h, Jellyfin, f.srv.URL, jellyKey, nil)
	file := "/data/movies/Film/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || !steps[0].OK {
		t.Fatalf("steps = %+v", steps)
	}
	refresh := url.Values{"metadataRefreshMode": {"FullRefresh"}, "imageRefreshMode": {"None"},
		"replaceAllMetadata": {"false"}, "replaceAllImages": {"false"}}.Encode()
	want := []sent{
		{"GET", "/Library/VirtualFolders", "", auth},
		{"POST", "/Library/Media/Updated", "", auth},
		{"GET", "/Items", jellyListQuery(0), auth},
		{"POST", "/Items/abc123/Refresh", refresh, auth},
	}
	if got := f.seen(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %+v, want %+v", got, want)
	}
}

func TestJellyfinIsToldOnlyOfANewFileWhenTheOriginalIsKept(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "film.mp4")
	made := filepath.Join(dir, "film.hevc.mp4")
	if err := os.WriteFile(orig, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(made, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFakeMedia(t, "Authorization", `MediaBrowser Token="`+jellyKey+`"`, map[string]string{
		"/Library/VirtualFolders": jellyFoldersJSON,
		"/Library/Media/Updated":  "",
		"/Items":                  `{"Items":[]}`,
	})
	h := newHarness(t)
	saveInstance(t, h, Jellyfin, f.srv.URL, jellyKey, []Mapping{{TheirPath: "/data/movies", OurPath: dir}})
	steps := tellOnce(t, h, orig, made)
	if len(steps) != 1 || !steps[0].OK {
		t.Fatalf("steps = %+v", steps)
	}
	var body struct {
		Updates []jellyUpdate `json:"Updates"`
	}
	if len(f.posted) != 1 {
		t.Fatalf("posted %d bodies, want 1", len(f.posted))
	}
	if err := json.Unmarshal([]byte(f.posted[0]), &body); err != nil {
		t.Fatal(err)
	}
	want := []jellyUpdate{{Path: "/data/movies/film.hevc.mp4", UpdateType: "Created"}}
	if !reflect.DeepEqual(body.Updates, want) {
		t.Errorf("updates = %+v, want %+v", body.Updates, want)
	}
}

func TestJellyfinWarnsWhenNoLibraryHoldsTheFile(t *testing.T) {
	f := newFakeMedia(t, "Authorization", `MediaBrowser Token="`+jellyKey+`"`, map[string]string{
		"/Library/VirtualFolders": jellyFoldersJSON,
		"/Library/Media/Updated":  "",
	})
	h := newHarness(t)
	saveInstance(t, h, Jellyfin, f.srv.URL, jellyKey, nil)
	file := "/elsewhere/film.mkv"
	steps := tellOnce(t, h, file, file)
	if len(steps) != 1 || steps[0].OK || steps[0].Words != "Jellyfin has no library for this folder; nothing to scan." {
		t.Fatalf("steps = %+v, want one warning", steps)
	}
	if len(f.posted) != 0 {
		t.Errorf("posted %d bodies, want none", len(f.posted))
	}
}

func TestExtOfAndDirOfHandBothSeparators(t *testing.T) {
	if got := dirOf(`C:\media\Film\film.mkv`); got != `C:\media\Film` {
		t.Errorf("dirOf = %q", got)
	}
	if got := dirOf("/film.mkv"); got != "/" {
		t.Errorf("dirOf of a top file = %q, want /", got)
	}
	if got := extOf(`C:\media\Film.v2\film.MKV`); got != ".mkv" {
		t.Errorf("extOf = %q, want .mkv", got)
	}
	if got := extOf("/media/film"); got != "" {
		t.Errorf("extOf without one = %q, want none", got)
	}
}
