// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

func settingsOf(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	return m
}

func TestSettingsDefaultsAndFirstRun(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	m := settingsOf(t, body)
	want := map[string]any{"firstRunDone": false, "firstRunStep": 1.0,
		"keepOriginalsDays": 7.0, "parallelJobs": 1.0, "lowPriority": true, "scanSchedule": "02:00", "queueOrder": "newest", "authRequired": false}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	if code != 200 || m["trashPath"] == "" || m["integrations"] == nil || m["quietHours"] == nil {
		t.Errorf("%d %s", code, body)
	}
	if m["controlLevel"] != "basic" || m["perLibraryFlow"] != false { // retired, shown for one release (ui-shop 04:37Z)
		t.Errorf("a fresh install's retired level: %s", body)
	}
	// a fresh install with a library from -library or Docker's /media still has first-run to do
	srv2, st2, _, _ := libServer(t, false)
	st2.EnsureLibraries([]string{t.TempDir()}, rules.LegacyTemplate(rules.Default(), "replace"))
	_, body, _ = do(t, nil, srv2, call{method: "GET", path: "/api/settings"})
	if m := settingsOf(t, body); m["firstRunDone"] != false {
		t.Errorf("fresh install with a flag library: %s", body)
	}
	// an upgrade: first-run done
	srv3, st3, _, _ := libServer(t, false)
	st3.SetMeta("install", "upgrade")
	_, body, _ = do(t, nil, srv3, call{method: "GET", path: "/api/settings"})
	if m := settingsOf(t, body); m["firstRunDone"] != true || m["controlLevel"] != "standard" {
		t.Errorf("upgrade: %s", body)
	}
	// decided once: later changes to the install do not reset the stored settings
	st3.SetMeta("install", "fresh")
	_, body, _ = do(t, nil, srv3, call{method: "GET", path: "/api/settings"})
	if settingsOf(t, body)["firstRunDone"] != true {
		t.Errorf("stored settings must stay: %s", body)
	}
}

func TestPatchSettings(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	code, body := post(t, srv, "PATCH", "/api/settings", `{"controlLevel":"advanced","firstRunStep":3,"quietHours":{"from":"22:00","to":"06:30"},"queueOrder":"largest","trashPath":"ignored","authRequired":true}`)
	m := settingsOf(t, body)
	q, _ := m["quietHours"].(map[string]any)
	if code != 200 || m["controlLevel"] != "advanced" || m["firstRunStep"] != 3.0 || q["from"] != "22:00" || q["to"] != "06:30" ||
		m["queueOrder"] != "largest" || m["authRequired"] != false || strings.Contains(body, "ignored") {
		t.Fatalf("%d %s", code, body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	if m := settingsOf(t, body); m["firstRunStep"] != 3.0 || m["controlLevel"] != "advanced" { // stored for one release (lead 04:39Z)
		t.Errorf("stored: %s", body)
	}
	for _, c := range []struct{ patch, code string }{
		{`{"firstRunStep":0}`, "invalid_setting"},
		{`{"firstRunStep":6}`, "invalid_setting"},
		{`{"keepOriginalsDays":366}`, "invalid_setting"},
		{`{"keepOriginalsDays":-1}`, "invalid_setting"},
		{`{"parallelJobs":0}`, "invalid_setting"},
		{`{"parallelJobs":9}`, "invalid_setting"},
		{`{"quietHours":{"from":"","to":"06:00"}}`, "invalid_setting"},
		{`{"quietHours":{"from":"25:00","to":"06:00"}}`, "invalid_setting"},
		{`{"scanSchedule":"2am"}`, "invalid_setting"},
		{`{"queueOrder":"random"}`, "invalid_setting"},
		{`{"lowPriority":"yes"}`, "invalid_setting"},
		{`{"integrations":{"sonarr":{"url":"http://x"}}}`, "not_available"},
		{`{"colour":"blue"}`, "unknown_setting"},
		{`[1,2]`, "invalid_json"},
	} {
		if code, body := post(t, srv, "PATCH", "/api/settings", c.patch); code != 400 || errCode(body) != c.code {
			t.Errorf("%s: %d %s, want 400 %s", c.patch, code, body, c.code)
		}
	}
	// a refused patch changes nothing, and a partial quietHours keeps the other end
	post(t, srv, "PATCH", "/api/settings", `{"parallelJobs":2,"queueOrder":"nope"}`)
	post(t, srv, "PATCH", "/api/settings", `{"quietHours":{"to":"07:00"}}`)
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	m = settingsOf(t, body)
	q, _ = m["quietHours"].(map[string]any)
	if m["parallelJobs"] != 1.0 || q["from"] != "22:00" || q["to"] != "07:00" {
		t.Errorf("after refused and partial patches: %s", body)
	}
	if code, body = post(t, srv, "PATCH", "/api/settings", `{"quietHours":{"from":"","to":""},"scanSchedule":""}`); code != 200 {
		t.Errorf("clearing quiet hours and the scan time: %d %s", code, body)
	}
}

func TestSettingsAndFlowsNeedSignIn(t *testing.T) {
	srv, _, a, _ := libServer(t, true)
	for _, k := range []call{
		{method: "GET", path: "/api/settings"},
		{method: "PATCH", path: "/api/settings", body: "{}", ctype: "application/json"},
		{method: "GET", path: "/api/flows"},
		{method: "POST", path: "/api/flows", body: `{"name":"x"}`, ctype: "application/json"},
		{method: "PUT", path: "/api/libraries/1/flow", body: `{"flowId":1}`, ctype: "application/json"},
	} {
		if code, body, _ := do(t, nil, srv, k); code != 401 || errCode(body) != "auth" {
			t.Errorf("%s %s: %d %s", k.method, k.path, code, body)
		}
	}
	if code, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings", bearer: a.Token()}); code != 200 || !strings.Contains(body, `"authRequired":true`) {
		t.Errorf("signed in: %d %s", code, body)
	}
}

// The retired controlLevel and perLibraryFlow stay in storage for one release (a rollback finds them),
// through a save of other settings, and GET shows them as stored (ui-shop 04:37Z: the page before the
// Flows tab reads them).
func TestRetiredSettingsAreKeptStored(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetMeta("settings", `{"controlLevel":"advanced","perLibraryFlow":true,"firstRunDone":true,"firstRunStep":5,"keepOriginalsDays":7,"parallelJobs":1,"lowPriority":true,"scanSchedule":"02:00","queueOrder":"newest","languages":[]}`)
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"quietHours":{"from":"22:00","to":"06:00"}}`); code != 200 || !strings.Contains(body, `"controlLevel":"advanced"`) || !strings.Contains(body, `"perLibraryFlow":true`) {
		t.Fatalf("patch: %d %s", code, body)
	}
	raw, _, _ := st.Meta("settings")
	if !strings.Contains(raw, `"controlLevel":"advanced"`) || !strings.Contains(raw, `"perLibraryFlow":true`) || !strings.Contains(raw, `"22:00"`) {
		t.Errorf("stored: %s", raw)
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"}); !strings.Contains(body, `"controlLevel":"advanced"`) || !strings.Contains(body, `"perLibraryFlow":true`) {
		t.Errorf("GET shows them as stored: %s", body)
	}
}

// An install whose settings were first saved by dev between #91 and this fix has no level stored: GET
// shows "basic" and false, as a fresh install had (ui-shop 04:37Z).
func TestRetiredSettingsWhenNoneStored(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetMeta("settings", `{"firstRunDone":true,"firstRunStep":5,"keepOriginalsDays":7,"parallelJobs":1,"lowPriority":true,"scanSchedule":"02:00","queueOrder":"newest","languages":[]}`)
	_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	if m := settingsOf(t, body); m["controlLevel"] != "basic" || m["perLibraryFlow"] != false {
		t.Errorf("%s", body)
	}
}

// For one release a PATCH stores the retired controlLevel and perLibraryFlow, so the page before the
// Flows tab works (lead 04:39Z); a flow's kind is its own, whatever they say.
func TestPatchStoresRetiredSettings(t *testing.T) {
	srv, _, _, _ := libServer(t, false)
	kinds := func() string {
		t.Helper()
		_, b, _ := do(t, nil, srv, call{method: "GET", path: "/api/flows"})
		var v struct{ Items []struct{ Name, Kind string } }
		json.Unmarshal([]byte(b), &v)
		return fmt.Sprint(v.Items)
	}
	post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":"Before","path":%q}`, t.TempDir()))
	before := kinds()
	code, body := post(t, srv, "PATCH", "/api/settings", `{"controlLevel":"advanced","perLibraryFlow":true}`)
	if m := settingsOf(t, body); code != 200 || m["controlLevel"] != "advanced" || m["perLibraryFlow"] != true {
		t.Fatalf("patch: %d %s", code, body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	if m := settingsOf(t, body); m["controlLevel"] != "advanced" || m["perLibraryFlow"] != true {
		t.Errorf("GET: %s", body)
	}
	if after := kinds(); after != before {
		t.Errorf("flow kinds changed: %s, before %s", after, before)
	}
	post(t, srv, "POST", "/api/libraries", fmt.Sprintf(`{"name":"After","path":%q}`, t.TempDir()))
	if before != "[{Before basic}]" {
		t.Fatalf("flows before: %s", before)
	}
	if got := kinds(); got != "[{Before basic} {After basic}]" { // "advanced" made no Advanced flow
		t.Errorf("flows after: %s", got)
	}
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"controlLevel":"expert"}`); code != 400 || errCode(body) != "invalid_setting" {
		t.Errorf("bad level: %d %s", code, body)
	}
}

// First run's step 1 (design, 02:41Z): the library's name and folder wait in the settings (the folder
// needn't exist), null clears them, a wrong type is refused, and making any library clears them.
func TestFirstRunLibrary(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	get := func() map[string]any {
		t.Helper()
		_, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"})
		return settingsOf(t, body)
	}
	if v, ok := get()["firstRunLibrary"]; !ok || v != nil {
		t.Errorf("new install: %v %v", v, ok)
	}
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"firstRunLibrary":{"name":"Movies","path":"/media/not-there-yet"}}`); code != 200 {
		t.Fatalf("patch %d %s", code, body)
	}
	if v, _ := get()["firstRunLibrary"].(map[string]any); v["name"] != "Movies" || v["path"] != "/media/not-there-yet" {
		t.Errorf("stored %v", v)
	}
	if code, _ := post(t, srv, "PATCH", "/api/settings", `{"firstRunLibrary":{"name":5}}`); code != 400 {
		t.Errorf("wrong type: %d", code)
	}
	post(t, srv, "PATCH", "/api/settings", `{"firstRunLibrary":null}`)
	if get()["firstRunLibrary"] != nil {
		t.Error("null didn't clear it")
	}
	post(t, srv, "PATCH", "/api/settings", `{"firstRunLibrary":{"name":"Movies","path":"/m"}}`)
	st.AddLibrary("Movies", t.TempDir(), rules.SliderTemplate(3), false)
	if get()["firstRunLibrary"] != nil {
		t.Error("making a library didn't clear it")
	}
}
