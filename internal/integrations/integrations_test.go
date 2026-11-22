// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

const goodKey = "good-key-123"

// fakeArr is a Sonarr-shaped server: it checks the key, lists one series at seriesPath and records commands.
type fakeArr struct {
	mu         sync.Mutex
	seriesPath string
	commands   []map[string]any
	srv        *httptest.Server
}

func newFakeArr(t *testing.T, seriesPath string) *fakeArr {
	t.Helper()
	f := &fakeArr{seriesPath: seriesPath}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != goodKey {
			http.Error(w, `{"message":"Unauthorized"}`, http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/v3/system/status":
			io.WriteString(w, `{"version":"4.0.1"}`)
		case r.Method == "GET" && r.URL.Path == "/api/v3/rootfolder":
			io.WriteString(w, `[{"path":"/tv"},{"path":"/nowhere/at/all"}]`)
		case r.Method == "GET" && r.URL.Path == "/api/v3/series":
			b, _ := json.Marshal([]map[string]any{{"id": 7, "title": "The Show", "year": 2019, "path": f.seriesPath, "originalLanguage": map[string]any{"name": "English"}}})
			w.Write(b)
		case r.Method == "GET" && r.URL.Path == "/api/v3/movie": // FB2b: the same fake serves a film under /films
			b, _ := json.Marshal([]map[string]any{{"id": 3, "title": "The Film", "year": 2021, "path": "/films/The Film (2021)", "originalLanguage": map[string]any{"name": "French"}}})
			w.Write(b)
		case r.Method == "GET" && r.URL.Path == "/api/v3/episodefile": // FB2b: the show's one file
			b, _ := json.Marshal([]map[string]any{{"id": 70, "seriesId": 7, "seasonNumber": 1, "path": f.seriesPath + "/s01e01.mkv"}})
			w.Write(b)
		case r.Method == "GET" && r.URL.Path == "/api/v3/episode":
			if r.URL.Query().Get("episodeFileId") != "70" {
				io.WriteString(w, `[]`)
				return
			}
			io.WriteString(w, `[{"id":700,"episodeNumber":1,"seasonNumber":1}]`)
		case r.Method == "POST" && r.URL.Path == "/api/v3/command":
			var c map[string]any
			json.NewDecoder(r.Body).Decode(&c)
			f.mu.Lock()
			f.commands = append(f.commands, c)
			f.mu.Unlock()
			io.WriteString(w, `{"id":1}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeArr) commandsSeen() []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.commands...)
}

type logBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuf) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.WriteString(fmt.Sprintf(format, args...) + "\n")
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type harness struct {
	st   *jobs.Store
	svc  *Service
	logs *logBuf
}

func newHarness(t *testing.T) harness {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	logs := &logBuf{}
	svc := New(st, Options{Client: &http.Client{}, Logf: logs.logf})
	return harness{st: st, svc: svc, logs: logs}
}

func (h harness) job(t *testing.T, path string) int64 {
	t.Helper()
	id, err := h.st.Enqueue(jobs.Job{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func strp(s string) *string { return &s }

func boolp(b bool) *bool { return &b }

func mustCode(t *testing.T, err error, code string) {
	t.Helper()
	var inv Invalid
	if !errors.As(err, &inv) || inv.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

func TestCreateRefusesBadInput(t *testing.T) {
	h := newHarness(t)
	cases := []struct {
		name string
		in   NewIntegration
		code string
	}{
		{"kind", NewIntegration{Kind: "emby", URL: "http://x", APIKey: "k"}, "invalid_kind"},
		{"url scheme", NewIntegration{Kind: Sonarr, URL: "ftp://x", APIKey: "k"}, "invalid_url"},
		{"url no host", NewIntegration{Kind: Sonarr, URL: "http://", APIKey: "k"}, "invalid_url"},
		{"no key", NewIntegration{Kind: Sonarr, URL: "http://x"}, "missing_key"},
		{"relative mapping", NewIntegration{Kind: Sonarr, URL: "http://x", APIKey: "k",
			PathMappings: []Mapping{{TheirPath: "tv", OurPath: "/mnt/tv"}}}, "invalid_mapping"},
	}
	for _, c := range cases {
		_, err := h.svc.Create(c.in)
		t.Run(c.name, func(t *testing.T) { mustCode(t, err, c.code) })
	}
}

func TestKeyNeverInAnyResponseOrLog(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	it, err := h.svc.Create(NewIntegration{Kind: Sonarr, Name: "Home", URL: arr.srv.URL, APIKey: goodKey,
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !it.HasKey {
		t.Fatal("HasKey = false after a key was saved")
	}
	res, err := h.svc.Test(TestIn{ID: it.ID})
	if err != nil || !res.OK {
		t.Fatalf("Test by id: %+v, %v", res, err)
	}
	id := h.job(t, "/mnt/media/tv/The Show/s01e01.mkv")
	h.svc.Tell(id, "", "/mnt/media/tv/The Show/s01e01.mkv")
	h.svc.Wait()

	list, _ := h.svc.List()
	b, _ := json.Marshal(list)
	steps, _ := h.st.Steps(id)
	sb, _ := json.Marshal(steps)
	for name, s := range map[string]string{"list": string(b), "steps": string(sb), "log": h.logs.String()} {
		if strings.Contains(s, goodKey) {
			t.Errorf("the key appears in %s: %s", name, s)
		}
	}
}

func TestPathMappingFindsTheSeriesAndRescans(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	it, _ := h.svc.Create(NewIntegration{Kind: Sonarr, Name: "Home", URL: arr.srv.URL, APIKey: goodKey,
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}})
	file := "/mnt/media/tv/The Show/s01e01.mkv"
	id := h.job(t, file)
	h.svc.Tell(id, "", file)
	h.svc.Wait()

	cmds := arr.commandsSeen()
	if len(cmds) != 1 || cmds[0]["name"] != "RescanSeries" || cmds[0]["seriesId"] != float64(7) {
		t.Fatalf("commands = %v, want one RescanSeries for series 7", cmds)
	}
	steps, _ := h.st.Steps(id)
	if len(steps) != 1 || steps[0].Step != RefreshStep || !steps[0].OK || steps[0].Words != "Sonarr rescanned The Show." {
		t.Fatalf("steps = %+v", steps)
	}
	got, _ := h.svc.List()
	if got[0].ID != it.ID || got[0].LastResult == nil || !got[0].LastResult.OK {
		t.Fatalf("lastResult = %+v", got[0].LastResult)
	}
}

// An instance without the file's show or movie stays quiet: with a Sonarr and a Radarr both set up, the one
// that does not hold the file must not put a warning on every job.
func TestAnInstanceWithoutTheFileStaysQuiet(t *testing.T) {
	arr := newFakeArr(t, "/elsewhere/The Show")
	h := newHarness(t)
	h.svc.Create(NewIntegration{Kind: Sonarr, Name: "Home", URL: arr.srv.URL, APIKey: goodKey,
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}})
	file := "/mnt/media/tv/The Show/s01e01.mkv"
	id := h.job(t, file)
	h.svc.Tell(id, "", file)
	h.svc.Wait()

	if steps, _ := h.st.Steps(id); len(steps) != 0 {
		t.Fatalf("an instance without the file left steps: %+v", steps)
	}
	if len(arr.commandsSeen()) != 0 {
		t.Fatal("a rescan was sent for a file the instance does not have")
	}
	list, _ := h.svc.List()
	if list[0].FailingSince != nil {
		t.Fatal("an unknown path must not start failingSince")
	}
	if lr := list[0].LastResult; lr == nil || !lr.OK || lr.Message != "Sonarr doesn't have this file's show." {
		t.Fatalf("lastResult = %+v", lr)
	}
	if strings.Contains(h.logs.String(), "doesn't have") {
		t.Fatalf("an instance without the file wrote to the app log: %s", h.logs.String())
	}
}

func TestWrongKeySaysSo(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	res, err := h.svc.Test(TestIn{Kind: Sonarr, URL: strp(arr.srv.URL), APIKey: strp("wrong")})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Message != "Sonarr answered 401: the API key is wrong." {
		t.Fatalf("result = %+v", res)
	}
}

func TestFailingSinceAfterThreeFailuresAndClearsOnSuccess(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	it, _ := h.svc.Create(NewIntegration{Kind: Sonarr, Name: "Home", URL: arr.srv.URL, APIKey: "wrong",
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}})
	file := "/mnt/media/tv/The Show/s01e01.mkv"
	for i := 1; i <= 2; i++ {
		h.svc.Tell(h.job(t, file), "", file)
		h.svc.Wait()
	}
	if list, _ := h.svc.List(); list[0].FailingSince != nil {
		t.Fatalf("failingSince set after 2 failures: %v", list[0].FailingSince)
	}
	h.svc.Tell(h.job(t, file), "", file)
	h.svc.Wait()
	list, _ := h.svc.List()
	if list[0].FailingSince == nil {
		t.Fatal("failingSince not set after 3 failures")
	}
	first := list[0].FailingSince.UTC()

	h.svc.Update(it.ID, Patch{APIKey: strp(goodKey)})
	h.svc.Tell(h.job(t, file), "", file)
	h.svc.Wait()
	list, _ = h.svc.List()
	if list[0].FailingSince != nil {
		t.Fatalf("failingSince not cleared by a success (was %v)", first)
	}
}

func TestUpdateWithoutKeyKeepsIt(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	it, _ := h.svc.Create(NewIntegration{Kind: Sonarr, Name: "Home", URL: arr.srv.URL, APIKey: goodKey})
	got, err := h.svc.Update(it.ID, Patch{Name: strp("Renamed")})
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || !got.HasKey {
		t.Fatalf("update = %+v", got)
	}
	res, _ := h.svc.Test(TestIn{ID: it.ID})
	if !res.OK {
		t.Fatalf("the kept key no longer works: %+v", res)
	}
}

func TestTestFormsSavedOverrideAndNew(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	it, _ := h.svc.Create(NewIntegration{Kind: Sonarr, Name: "Home", URL: arr.srv.URL, APIKey: goodKey,
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}})

	saved, err := h.svc.Test(TestIn{ID: it.ID})
	if err != nil || !saved.OK || saved.Version == nil || *saved.Version != "4.0.1" {
		t.Fatalf("saved: %+v, %v", saved, err)
	}
	if len(saved.Folders) != 2 || saved.Folders[0].OurPath == nil || *saved.Folders[0].OurPath != "/mnt/media/tv" {
		t.Fatalf("folders = %+v", saved.Folders)
	}

	over, err := h.svc.Test(TestIn{ID: it.ID, APIKey: strp("nope")})
	if err != nil || over.OK {
		t.Fatalf("override with a wrong key should fail: %+v, %v", over, err)
	}
	if list, _ := h.svc.List(); list[0].LastResult == nil || !list[0].LastResult.OK {
		t.Fatal("a Test with an override must not change the saved last result")
	}

	fresh, err := h.svc.Test(TestIn{Kind: Radarr, URL: strp(arr.srv.URL), APIKey: strp(goodKey)})
	if err != nil || !fresh.OK {
		t.Fatalf("new instance: %+v, %v", fresh, err)
	}
	if _, err := h.svc.Test(TestIn{ID: "missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id error = %v", err)
	}
}

func TestDeleteAndNotFound(t *testing.T) {
	h := newHarness(t)
	it, _ := h.svc.Create(NewIntegration{Kind: Sonarr, URL: "http://127.0.0.1:1", APIKey: "k"})
	if err := h.svc.Delete(it.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Delete(it.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
	if _, err := h.svc.Update(it.ID, Patch{Name: strp("x")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update after delete = %v, want ErrNotFound", err)
	}
}

func TestDisabledInstanceIsNotTold(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	h.svc.Create(NewIntegration{Kind: Sonarr, URL: arr.srv.URL, APIKey: goodKey, Enabled: boolp(false),
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}})
	file := "/mnt/media/tv/The Show/s01e01.mkv"
	id := h.job(t, file)
	h.svc.Tell(id, "", file)
	h.svc.Wait()
	if steps, _ := h.st.Steps(id); len(steps) != 0 {
		t.Fatalf("a disabled instance recorded steps: %+v", steps)
	}
}

// A redirect is not followed, so the key never reaches the address it points at.
func TestRedirectNeverCarriesTheKey(t *testing.T) {
	var hits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		io.WriteString(w, `{"version":"4.0.1"}`)
	}))
	t.Cleanup(other.Close)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(first.Close)

	h := newHarness(t)
	res, err := h.svc.Test(TestIn{Kind: Sonarr, URL: strp(first.URL), APIKey: strp(goodKey)})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Message, "redirect") {
		t.Fatalf("result = %+v, want ok false with a redirect message", res)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the redirect target got %d request(s); it must get none", n)
	}
	if strings.Contains(res.Message, goodKey) {
		t.Fatal("the key appears in the message")
	}
}

// /tv maps to /mnt/media/tv only at a path boundary: /mnt/media/tv2 is not inside it, and a series
// folder that is a prefix of the file's folder is not its series.
func TestPathMappingRespectsTheBoundary(t *testing.T) {
	maps := []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}
	if got := toTheirs("/mnt/media/tv2/X.mkv", maps); got != "/mnt/media/tv2/X.mkv" {
		t.Fatalf("toTheirs(/mnt/media/tv2/X.mkv) = %q, want unchanged", got)
	}
	if got := toTheirs("/mnt/media/tv/X.mkv", maps); got != "/tv/X.mkv" {
		t.Fatalf("toTheirs(/mnt/media/tv/X.mkv) = %q", got)
	}
	if got, ok := toOurs("/tv2/X.mkv", maps); ok {
		t.Fatalf("toOurs(/tv2/X.mkv) = %q, want no mapping", got)
	}

	arr := newFakeArr(t, "/tv/The Sho")
	h := newHarness(t)
	h.svc.Create(NewIntegration{Kind: Sonarr, Name: "Home", URL: arr.srv.URL, APIKey: goodKey, PathMappings: maps})
	file := "/mnt/media/tv/The Show/s01e01.mkv"
	id := h.job(t, file)
	h.svc.Tell(id, "", file)
	h.svc.Wait()
	if n := len(arr.commandsSeen()); n != 0 {
		t.Fatalf("a rescan went to a series whose folder is only a prefix of the file's (%d)", n)
	}
	if steps, _ := h.st.Steps(id); len(steps) != 0 {
		t.Fatalf("steps = %+v, want none (the instance does not have the file)", steps)
	}
}

// A certificate the machine can't check is named as that, not as "Could not reach".
func TestCertificateFailureSaysSo(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	h := newHarness(t)
	res, err := h.svc.Test(TestIn{Kind: Sonarr, URL: strp(srv.URL), APIKey: strp("k")})
	if err != nil {
		t.Fatal(err)
	}
	want := "Could not make a secure connection to " + strings.TrimPrefix(srv.URL, "https://") + " (certificate check failed)."
	if res.OK || res.Message != want {
		t.Fatalf("result = %+v, want %q", res, want)
	}
}

// FB2a: a workflow's tell card tells only the kinds it names, says whom it told, and says when none of
// them is connected and on.
func TestTellKindsTellsOnlyThoseKinds(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	h.svc.Create(NewIntegration{Kind: Sonarr, URL: arr.srv.URL, APIKey: goodKey, Name: "TV box",
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/mnt/media/tv"}}})
	file := "/mnt/media/tv/The Show/s01e01.mkv"
	id := h.job(t, file)
	if words, err := h.svc.TellKinds(id, "", file, []Kind{Plex, Jellyfin}); err == nil || !strings.Contains(err.Error(), "none of these is connected") {
		t.Fatalf("media servers: %q %v", words, err)
	}
	h.svc.Wait()
	if steps, _ := h.st.Steps(id); len(steps) != 0 {
		t.Fatalf("a kind that wasn't asked for was told: %+v", steps)
	}
	words, err := h.svc.TellKinds(id, "", file, []Kind{Sonarr, Radarr})
	if err != nil || words != "Told TV box" {
		t.Fatalf("downloaders: %q %v", words, err)
	}
	h.svc.Wait()
	if steps, _ := h.st.Steps(id); len(steps) == 0 {
		t.Fatal("the downloader was not told")
	}
}

// FB2b: "Ask the downloader for details" reads the show (title, year, language) and the episode's numbers from
// Sonarr, a film from Radarr; a file no instance owns leaves every value empty and says so; only the kinds
// asked for are asked.
func TestLookupAnswersFromTheInstanceThatOwnsTheFile(t *testing.T) {
	arr := newFakeArr(t, "/tv/The Show")
	h := newHarness(t)
	h.svc.Create(NewIntegration{Kind: Sonarr, URL: arr.srv.URL, APIKey: goodKey, Name: "TV box",
		PathMappings: []Mapping{{TheirPath: "/tv", OurPath: "/data/tv"}}})
	h.svc.Create(NewIntegration{Kind: Radarr, URL: arr.srv.URL, APIKey: goodKey, Name: "Film box",
		PathMappings: []Mapping{{TheirPath: "/films", OurPath: "/data/films"}}})
	vals, words, err := h.svc.Lookup("/data/tv/The Show/s01e01.mkv", []Kind{Sonarr, Radarr})
	if err != nil || vals["title_clean"] != "The Show" || vals["year"] != "2019" || vals["season"] != "1" || vals["episode"] != "1" || vals["original_language"] != "English" || words != "Sonarr: The Show (2019) S01E01" {
		t.Errorf("episode: %v %q %v", vals, words, err)
	}
	vals, words, err = h.svc.Lookup("/data/films/The Film (2021)/The Film.mkv", []Kind{Sonarr, Radarr})
	if err != nil || vals["title_clean"] != "The Film" || vals["year"] != "2021" || vals["season"] != "" || vals["original_language"] != "French" || words != "Radarr: The Film (2021)" {
		t.Errorf("film: %v %q %v", vals, words, err)
	}
	vals, words, err = h.svc.Lookup("/data/films/The Film (2021)/The Film.mkv", []Kind{Sonarr})
	if err != nil || vals["title_clean"] != "" || !strings.Contains(words, "No connected downloader knows") {
		t.Errorf("only Sonarr asked: %v %q %v", vals, words, err)
	}
	if _, _, err := h.svc.Lookup("/data/x.mkv", []Kind{Plex}); err == nil {
		t.Error("no downloader of that kind: want an error")
	}
}

// Per server (11-22): a Plex tell card tells the Plex connection, and the run's global tell still tells Jellyfin,
// with Plex told once and not again. The run's record is cleared once the rest has passed it.
func TestRestTellSkipsOnlyTheConnectionACardTold(t *testing.T) {
	plex := newFakeMedia(t, "X-Plex-Token", plexToken, map[string]string{
		"/library/sections":           plexSecs,
		"/library/sections/1/refresh": "",
	})
	auth := `MediaBrowser Token="` + jellyKey + `"`
	jelly := newFakeMedia(t, "Authorization", auth, map[string]string{
		"/Library/VirtualFolders": jellyFoldersJSON,
		"/Library/Media/Updated":  "",
		"/Items":                  `{"Items":[]}`,
	})
	h := newHarness(t)
	saveInstance(t, h, Plex, plex.srv.URL, plexToken, nil)
	saveInstance(t, h, Jellyfin, jelly.srv.URL, jellyKey, nil)
	file := "/data/movies/Film/film.mkv"
	id := h.job(t, file)
	if words, err := h.svc.TellKinds(id, file, file, []Kind{Plex}); err != nil || words != "Told Home" {
		t.Fatalf("the card: %q %v", words, err)
	}
	if words, err := h.svc.TellRest(id, file, file, []Kind{Plex, Jellyfin}); err != nil || words != "Told Home" {
		t.Fatalf("the rest: %q %v", words, err)
	}
	h.svc.Wait()
	refreshes := 0
	for _, c := range plex.seen() {
		if c.Path == "/library/sections/1/refresh" {
			refreshes++
		}
	}
	if refreshes != 1 {
		t.Errorf("Plex was told %d times, want 1", refreshes)
	}
	if len(jelly.posted) != 1 {
		t.Errorf("Jellyfin got %d updates, want 1 (the rest of the run)", len(jelly.posted))
	}
	if got := h.svc.told[id]; got != nil {
		t.Errorf("the run's record was not cleared: %v", got)
	}
}
