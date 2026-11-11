// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/preview"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

type fakeEngine struct {
	scans   int
	stopped []int64
}

func libServer(t *testing.T, exposed bool) (*httptest.Server, *jobs.Store, *Access, *fakeEngine) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.SetPaused(false) // the test's worker is started (S2: a new install starts stopped)
	t.Cleanup(func() { st.Close() })
	a, _, _ := OpenAccess(t.TempDir(), exposed)
	a.FailDelay = 0
	fe := &fakeEngine{}
	next := time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC)
	srv := httptest.NewServer(Handler(st, a, Engine{
		ScanNow: func() { fe.scans++ }, NextScan: func() time.Time { return next }, RequeueJob: func(id int64) { fe.stopped = append(fe.stopped, id); st.Release(id) }, // as a worker puts it back
		Preview: &preview.Previewer{Store: st, Exec: h264Probe, FFprobe: "ffprobe", Probes: 1}}))
	t.Cleanup(srv.Close)
	return srv, st, a, fe
}

// h264Probe says every file is 1080p24 H.264 at 8 Mbps with one audio track.
var h264Probe = &media.FakeExec{Handler: func(name string, args []string) (string, string, error) {
	return `{"streams":[{"index":0,"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"24/1","r_frame_rate":"24/1","bit_rate":"8000000"},
 {"index":1,"codec_type":"audio","codec_name":"aac"}],"format":{"format_name":"matroska,webm","duration":"1.0","size":"1000000","bit_rate":"8000000"}}`, "", nil
}}

func post(t *testing.T, srv *httptest.Server, method, path, body string) (int, string) {
	t.Helper()
	code, b, _ := do(t, nil, srv, call{method: method, path: path, body: body, ctype: "application/json"})
	return code, b
}

func TestLibraryLifecycle(t *testing.T) {
	srv, st, _, fe := libServer(t, false)
	dir := t.TempDir()
	code, body := post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":" Movies ","path":%q,"startFrom":"better"}`, dir))
	var lib libraryItem
	json.Unmarshal([]byte(body), &lib)
	if code != 201 || lib.Name != "Movies" || lib.State != "not_started" || lib.Started || lib.NextScan != nil ||
		lib.SettingKind != "slider" || lib.SettingLabel != "Better quality" || lib.FlowID == 0 {
		t.Fatalf("create: %d %s", code, body)
	}
	if f, _ := st.GetFlow(lib.FlowID); f.Name != "Movies" || f.Template.Params.Quality != 4 {
		t.Errorf("flow %+v: named after the library, at slider position 4", f)
	}
	if fe.scans != 0 {
		t.Error("creating a library must not scan")
	}
	id := fmt.Sprint(lib.ID)
	code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
	var list struct{ Items []libraryItem }
	json.Unmarshal([]byte(body), &list)
	if code != 200 || len(list.Items) != 1 || list.Items[0].ID != lib.ID {
		t.Fatalf("list: %d %s", code, body)
	}
	if code, body = post(t, srv, "POST", "/api/libraries/"+id+"/scan", "{}"); code != 200 || fe.scans != 1 {
		t.Fatalf("scan: %d %s, scans %d", code, body, fe.scans)
	}
	code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/libraries/" + id})
	json.Unmarshal([]byte(body), &lib)
	if !lib.Started || lib.State != "idle" || lib.NextScan == nil || *lib.NextScan != "2026-11-07T03:00:00Z" {
		t.Errorf("after start: %s", body)
	}
	other := t.TempDir()
	code, body = post(t, srv, "PATCH", "/api/libraries/"+id, fmt.Sprintf(`{"name":"Films","path":%q}`, other))
	json.Unmarshal([]byte(body), &lib)
	if real, _ := jobs.RealPath(other); code != 200 || lib.Name != "Films" || lib.Path != real {
		t.Errorf("patch: %d %s", code, body)
	}
	if f, _ := st.GetFlow(lib.FlowID); f.Name != "Movies" {
		t.Errorf("renaming a library never renames its flow: %q", f.Name)
	}
	// a running job is stopped, queued ones cancelled
	running, _ := st.Enqueue(jobs.Job{Library: other, LibraryID: lib.ID, Path: other + "/a.mkv"})
	st.Claim("w")
	if code, _ = post(t, srv, "DELETE", "/api/libraries/"+id, ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if !slices.Equal(fe.stopped, []int64{running}) {
		t.Errorf("stopped %v, want [%d]", fe.stopped, running)
	}
	if code, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/libraries/" + id}); code != 404 || errCode(body) != "not_found" {
		t.Errorf("after delete: %d %s", code, body)
	}
	if _, err := st.GetFlow(lib.FlowID); err != nil {
		t.Error("the flow stays after the library is removed")
	}
}

func TestLibraryValidation(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "inner"), 0o755)
	file := filepath.Join(dir, "f.txt")
	os.WriteFile(file, nil, 0o644)
	st.AddLibrary("Existing", dir, rules.SliderTemplate(3), false)
	cases := []struct {
		body      string
		code      int
		errorCode string
	}{
		{`{"name":"","path":"/tmp"}`, 400, "invalid_name"},
		{`{"name":"x","path":"relative/path"}`, 400, "invalid_path"},
		{fmt.Sprintf(`{"name":"x","path":%q}`, filepath.Join(dir, "missing")), 400, "not_a_folder"},
		{fmt.Sprintf(`{"name":"x","path":%q}`, file), 400, "not_a_folder"},
		{fmt.Sprintf(`{"name":"x","path":%q}`, dir), 409, "path_taken"},
		{fmt.Sprintf(`{"name":"x","path":%q}`, filepath.Join(dir, "inner")), 409, "path_overlaps"},
		{fmt.Sprintf(`{"name":"x","path":%q}`, filepath.Dir(dir)), 409, "path_overlaps"},
		{fmt.Sprintf(`{"name":"x","path":%q,"startFrom":"huge"}`, t.TempDir()), 400, "invalid_start_from"},
		{`not json`, 400, "invalid_json"},
	}
	for _, c := range cases {
		if code, body := post(t, srv, "POST", "/api/libraries", c.body); code != c.code || errCode(body) != c.errorCode {
			t.Errorf("%s: %d %s, want %d %s", c.body, code, body, c.code, c.errorCode)
		}
	}
	for _, p := range []string{"/api/libraries/999", "/api/libraries/abc"} {
		if code, body, _ := do(t, nil, srv, call{method: "GET", path: p}); code != 404 || errCode(body) != "not_found" {
			t.Errorf("%s: %d %s", p, code, body)
		}
	}
	if code, body := post(t, srv, "POST", "/api/libraries/999/scan", "{}"); code != 404 {
		t.Errorf("scan unknown: %d %s", code, body)
	}
	if code, body := post(t, srv, "PATCH", "/api/libraries/1", `{"name":"  "}`); code != 400 || errCode(body) != "invalid_name" {
		t.Errorf("blank rename: %d %s", code, body)
	}
}

func TestLibraryEndpointsNeedSignIn(t *testing.T) {
	srv, _, a, _ := libServer(t, true)
	for _, k := range []call{
		{method: "GET", path: "/api/libraries"},
		{method: "POST", path: "/api/libraries", body: "{}", ctype: "application/json"},
		{method: "DELETE", path: "/api/libraries/1", ctype: "application/json"},
		{method: "GET", path: "/api/fs/dirs"},
	} {
		if code, body, _ := do(t, nil, srv, k); code != 401 || errCode(body) != "auth" {
			t.Errorf("%s %s: %d %s", k.method, k.path, code, body)
		}
	}
	if code, _, _ := do(t, nil, srv, call{method: "GET", path: "/api/libraries", bearer: a.Token()}); code != 200 {
		t.Errorf("with the token: %d", code)
	}
	if code, body, _ := do(t, nil, srv, call{method: "POST", path: "/api/libraries", body: "{}", ctype: "text/plain", bearer: a.Token()}); code != 403 || errCode(body) != "csrf" {
		t.Errorf("non-JSON write: %d %s", code, body)
	}
}

func TestFsDirsListsFoldersOnly(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	dir := t.TempDir()
	for _, d := range []string{"b", "a", ".hidden"} {
		os.Mkdir(filepath.Join(dir, d), 0o755)
	}
	os.WriteFile(filepath.Join(dir, "movie.mkv"), []byte("x"), 0o644)
	if runtime.GOOS != "windows" {
		os.Symlink(filepath.Join(dir, "a"), filepath.Join(dir, "link-to-a"))
		os.Symlink(filepath.Join(dir, "movie.mkv"), filepath.Join(dir, "link-to-file"))
	}
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/fs/dirs?path=" + url.QueryEscape(filepath.Join(dir, "b", ".."))})
	var out struct {
		Path   string
		Parent *string
		Dirs   []string
	}
	json.Unmarshal([]byte(body), &out)
	want := []string{".hidden", "a", "b"}
	if runtime.GOOS != "windows" {
		want = []string{".hidden", "a", "b", "link-to-a"}
	}
	if code != 200 || out.Path != dir || !slices.Equal(out.Dirs, want) || out.Parent == nil || *out.Parent != filepath.Dir(dir) {
		t.Errorf("%d %s, want path %s dirs %v", code, body, dir, want)
	}
	if strings.Contains(body, "movie.mkv") || strings.Contains(body, "size") {
		t.Error("files and sizes must never be listed")
	}
	for p, want := range map[string]string{
		"relative":                      "invalid_path",
		filepath.Join(dir, "nope"):      "not_found",
		filepath.Join(dir, "movie.mkv"): "not_a_folder",
	} {
		if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/fs/dirs?path=" + url.QueryEscape(p)}); errCode(body) != want {
			t.Errorf("%s: %d %s, want %s", p, code, body, want)
		}
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(dir, "a")
		os.Chmod(locked, 0)
		t.Cleanup(func() { os.Chmod(locked, 0o755) })
		if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/fs/dirs?path=" + url.QueryEscape(locked)}); code != 403 || errCode(body) != "not_readable" {
			t.Errorf("unreadable folder: %d %s", code, body)
		}
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/fs/dirs"}); code != 200 || !strings.Contains(body, `"dirs"`) {
		t.Errorf("top level: %d %s", code, body)
	}
}

func TestLibraryListShapeAndVersionHeader(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	code, body, resp := do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
	if code != 200 || !strings.Contains(body, `"total":1`) || !strings.Contains(body, `"id":"1"`) || !strings.Contains(body, `"flowId":"1"`) {
		t.Errorf("want total and string ids (UI-API-CONTRACT.md): %s", body)
	}
	if resp.Header.Get("X-SYP-API") != "1" {
		t.Errorf("X-SYP-API = %q", resp.Header.Get("X-SYP-API"))
	}
	srv2, _, _, _ := libServer(t, true)
	if _, _, resp := do(t, nil, srv2, call{method: "GET", path: "/api/libraries"}); resp.StatusCode != 401 || resp.Header.Get("X-SYP-API") != "1" {
		t.Errorf("a refused /api answer carries the version too: %d %q", resp.StatusCode, resp.Header.Get("X-SYP-API"))
	}
	if _, _, resp := do(t, nil, srv, call{method: "GET", path: "/healthz"}); resp.Header.Get("X-SYP-API") != "" {
		t.Error("only /api answers carry X-SYP-API")
	}
}

func TestLinkedFolderIsStoredAsTheRealFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making links needs extra rights on Windows")
	}
	srv, _, _, _ := libServer(t, false)
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "videos")
	os.Symlink(real, link)
	code, body := post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":"Linked","path":%q}`, link))
	var lib libraryItem
	json.Unmarshal([]byte(body), &lib)
	want, _ := filepath.EvalSymlinks(real)
	if code != 201 || lib.Path != want {
		t.Errorf("%d %s: want the real folder %s stored", code, body, want)
	}
	if code, body = post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":"Again","path":%q}`, real)); code != 409 || errCode(body) != "path_taken" {
		t.Errorf("the same folder by its real name: %d %s", code, body)
	}
}

// The same folder by two names (a link; macOS's /var for /private/var; a Windows short name) is one
// library folder: the second name is taken, and a folder inside it overlaps.
func TestOneFolderByTwoNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("making links needs extra rights on Windows")
	}
	srv, st, _, _ := libServer(t, false)
	real := t.TempDir()
	os.Mkdir(filepath.Join(real, "inner"), 0o755)
	alias := filepath.Join(t.TempDir(), "alias")
	os.Symlink(real, alias)
	l, err := st.AddLibrary("Via alias", alias, rules.SliderTemplate(3), false) // not through the API
	if want, _ := jobs.RealPath(real); err != nil || l.Path != want {
		t.Fatalf("stored %q %v, want the real folder %q", l.Path, err, want)
	}
	for _, c := range []struct{ path, code string }{
		{real, "path_taken"},
		{alias, "path_taken"},
		{filepath.Join(alias, "inner"), "path_overlaps"},
		{filepath.Join(real, "inner"), "path_overlaps"},
	} {
		if code, body := post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":"x","path":%q}`, c.path)); code != 409 || errCode(body) != c.code {
			t.Errorf("%s: %d %s, want 409 %s", c.path, code, body, c.code)
		}
	}
	other, _ := st.AddLibrary("Other", t.TempDir(), rules.SliderTemplate(3), false)
	if code, body := post(t, srv, "PATCH", fmt.Sprintf("/api/libraries/%d", other.ID), fmt.Sprintf(`{"path":%q}`, alias)); code != 409 || errCode(body) != "path_taken" {
		t.Errorf("PATCH to the other name of a used folder: %d %s", code, body)
	}
}

// A library at a system folder is refused with a plain message (slice 23).
func TestLibraryAtASystemFolderIsRefused(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	root := "/"
	if runtime.GOOS == "windows" {
		root = os.Getenv("SystemDrive") + `\` // "/" is not a full path there
	}
	b, _ := json.Marshal(map[string]string{"name": "All", "path": root, "startFrom": "balanced"})
	code, body := post(t, srv, "POST", "/api/libraries", string(b))
	if code != 400 || errCode(body) != "system_folder" || !strings.Contains(body, "Pick the folder that holds your videos") {
		t.Errorf("%d %s", code, body)
	}
}

// The library card's status (slice A, DESIGN/REDESIGN A2), the first that applies: converting, waiting
// for a worker, scanning, scanned, not scanned; failed files (failed and kept); madeWith.
func TestLibraryCardStatus(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _, _ := OpenAccess(t.TempDir(), false)
	a.FailDelay = 0
	scanning := map[int64]int{}
	srv := httptest.NewServer(Handler(st, a, Engine{Scanning: func() map[int64]int { return scanning },
		NextScan: func() time.Time { return time.Date(2026, 11, 8, 2, 0, 0, 0, time.UTC) }}))
	defer srv.Close()
	l, _ := st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), true)
	card := func() map[string]any {
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
		var out struct{ Items []map[string]any }
		json.Unmarshal([]byte(body), &out)
		if len(out.Items) != 1 {
			t.Fatalf("%s", body)
		}
		return out.Items[0]
	}
	status := func() map[string]any { return card()["status"].(map[string]any) }
	if s := status(); s["kind"] != "not_scanned" || s["nextScan"] != "2026-11-08T02:00:00Z" || s["lastScan"] != nil {
		t.Errorf("new: %v", s)
	}
	if c := card(); c["madeWith"] != "slider" || c["failedFiles"] != 0.0 {
		t.Errorf("card: %v", c)
	}
	scanning[l.ID] = 120
	if s := status(); s["kind"] != "scanning" || s["foundSoFar"] != 120.0 {
		t.Errorf("scanning: %v", s)
	}
	delete(scanning, l.ID)
	st.SetLibraryScanned(l.ID, 1284)
	if s := status(); s["kind"] != "scanned" || s["videos"] != 1284.0 || s["lastScan"] == nil {
		t.Errorf("scanned: %v", s)
	}
	// queued while the worker is stopped: waiting for a worker
	a1, _ := st.Enqueue(jobs.Job{LibraryID: l.ID, Path: "/m/a.mkv"})
	st.Enqueue(jobs.Job{LibraryID: l.ID, Path: "/m/b.mkv"})
	if s := status(); s["kind"] != "waiting_for_worker" || s["toDo"] != 2.0 {
		t.Errorf("waiting: %v", s)
	}
	st.SetPaused(false)
	st.Claim("w")
	if s := status(); s["kind"] != "converting" || s["converting"] != 1.0 || s["toDo"] != 1.0 {
		t.Errorf("converting: %v", s)
	}
	st.Fail(a1, "x")
	b, _ := st.Claim("w")
	st.Keep(b.ID, "y")
	if c := card(); c["failedFiles"] != 2.0 {
		t.Errorf("failed and kept: %v", c["failedFiles"])
	}
}

// First files through the API (slice A): a library's, and a folder's that is not a library yet;
// refusals as the folder check says them.
func TestFirstFilesRoutes(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _, _ := OpenAccess(t.TempDir(), false)
	a.FailDelay = 0
	chk := &doctor.Checker{Exec: h264Probe, FFmpeg: "/usr/bin/ffmpeg", FFprobe: "/usr/bin/ffprobe", FreeSpace: func(string) (uint64, error) { return 1 << 40, nil }}
	srv := httptest.NewServer(Handler(st, a, Engine{Doctor: chk, FirstFiles: &preview.FirstLister{Exec: h264Probe, FFprobe: "ffprobe"}}))
	defer srv.Close()
	dir := t.TempDir()
	for _, n := range []string{"b.mkv", "a.mkv", "Extras/x.mkv"} {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0o755)
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	poll := func(method, path, body string) (int, map[string]any) {
		for i := 0; i < 500; i++ {
			code, b := post(t, srv, method, path, body)
			var r map[string]any
			json.Unmarshal([]byte(b), &r)
			if code != 200 || r["stillLooking"] == false {
				return code, r
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("still looking")
		return 0, nil
	}
	code, r := poll("POST", "/api/libraries/first-files", fmt.Sprintf(`{"path":%q}`, dir))
	items, _ := r["items"].([]any)
	if code != 200 || len(items) != 3 || r["total"] != 3.0 || items[0].(map[string]any)["name"] != "x.mkv" || items[1].(map[string]any)["codec"] != "H.264" {
		t.Fatalf("folder: %d %v", code, r) // A to Z by path: Extras/x.mkv first
	}
	sel := rules.DefaultSelectors(3)
	sel.Ignore = []string{"Extras"}
	l, _ := st.AddLibrary("Movies", dir, sel.Template(), true)
	code, r = poll("GET", fmt.Sprintf("/api/libraries/%d/first-files", l.ID), "")
	if items, _ := r["items"].([]any); code != 200 || len(items) != 2 || r["total"] != 2.0 {
		t.Errorf("library (Extras ignored): %d %v", code, r)
	}
	if code, _ := post(t, srv, "GET", "/api/libraries/999/first-files", ""); code != 404 {
		t.Errorf("unknown library: %d", code)
	}
	if code, body := post(t, srv, "POST", "/api/libraries/first-files", `{"path":"relative/x"}`); code != 400 || errCode(body) != "invalid_path" {
		t.Errorf("relative: %d %s", code, body)
	}
	if code, body := post(t, srv, "POST", "/api/libraries/first-files", fmt.Sprintf(`{"path":%q}`, filepath.Join(dir, "missing"))); code != 400 || errCode(body) != "unreadable" {
		t.Errorf("missing folder: %d %s", code, body)
	}
	empty := t.TempDir() // no videos: listed as nothing (200); the folder check says "no videos"
	if code, r := poll("POST", "/api/libraries/first-files", fmt.Sprintf(`{"path":%q}`, empty)); code != 200 || r["total"] != 0.0 {
		t.Errorf("empty: %d %v", code, r)
	}
}

// ui-shop's Libraries page (contract v2): a library carries its disk's free space; null when its
// folder can't be read.
func TestLibraryFreeSpace(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	ok, _ := st.AddLibrary("Here", t.TempDir(), rules.SliderTemplate(3), false)
	gone, _ := st.AddLibrary("Gone", filepath.Join(t.TempDir(), "missing"), rules.SliderTemplate(3), false)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/libraries"})
	var v struct{ Items []map[string]any }
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	free := map[string]any{}
	for _, it := range v.Items {
		free[it["id"].(string)] = it["freeBytes"]
	}
	if n, _ := free[strconv.FormatInt(ok.ID, 10)].(float64); n <= 0 {
		t.Errorf("free space %v", free)
	}
	if free[strconv.FormatInt(gone.ID, 10)] != nil {
		t.Errorf("a missing folder's free space %v", free)
	}
}

// Review 5053a73: a hung mount's free space can't stall the library list: a read past the time limit
// shows null, a path whose read is still stuck starts no other, and an answer that comes late is used.
func TestFreeSpaceTimeLimit(t *testing.T) {
	defer func(f func(string) (uint64, error), d time.Duration) { diskFree, freeTimeout = f, d }(diskFree, freeTimeout)
	freeTimeout = 50 * time.Millisecond
	release := make(chan struct{})
	var calls atomic.Int32
	diskFree = func(string) (uint64, error) {
		calls.Add(1)
		<-release // a hung NFS mount
		return 12345, nil
	}
	path := t.TempDir()
	start := time.Now()
	if b := freeBytes(path); b != nil || time.Since(start) > time.Second {
		t.Fatalf("hung: %v after %v", b, time.Since(start))
	}
	if b := freeBytes(path); b != nil || calls.Load() != 1 {
		t.Errorf("a second read while the first is stuck: %v, %d calls", b, calls.Load())
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if b := freeBytes(path); b != nil && *b == 12345 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the late answer was never used")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 1 {
		t.Errorf("%d reads, want 1 (cached)", calls.Load())
	}
}
