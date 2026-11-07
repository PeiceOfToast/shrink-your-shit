// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
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
	for _, gone := range []string{"controlLevel", "perLibraryFlow"} { // each flow has its own control level
		if _, ok := m[gone]; ok {
			t.Errorf("%s is gone from the settings: %s", gone, body)
		}
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
	if m := settingsOf(t, body); m["firstRunDone"] != true {
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
	if code != 200 || m["controlLevel"] != nil || m["firstRunStep"] != 3.0 || q["from"] != "22:00" || q["to"] != "06:30" ||
		m["queueOrder"] != "largest" || m["authRequired"] != false || strings.Contains(body, "ignored") {
		t.Fatalf("%d %s", code, body)
	}
	_, body, _ = do(t, nil, srv, call{method: "GET", path: "/api/settings"})
	if m := settingsOf(t, body); m["firstRunStep"] != 3.0 || m["controlLevel"] != nil { // controlLevel: accepted, ignored
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
// through any settings save, but are never shown, and a PATCH cannot change them (review e9455d0).
func TestRetiredSettingsAreKeptStored(t *testing.T) {
	srv, st, _, _ := libServer(t, false)
	st.SetMeta("settings", `{"controlLevel":"advanced","perLibraryFlow":true,"firstRunDone":true,"firstRunStep":5,"keepOriginalsDays":7,"parallelJobs":1,"lowPriority":true,"scanSchedule":"02:00","queueOrder":"newest","languages":[]}`)
	if code, body := post(t, srv, "PATCH", "/api/settings", `{"quietHours":{"from":"22:00","to":"06:00"},"controlLevel":"basic","perLibraryFlow":false}`); code != 200 || strings.Contains(body, "controlLevel") || strings.Contains(body, "perLibraryFlow") {
		t.Fatalf("patch: %d %s", code, body)
	}
	raw, _, _ := st.Meta("settings")
	if !strings.Contains(raw, `"controlLevel":"advanced"`) || !strings.Contains(raw, `"perLibraryFlow":true`) || !strings.Contains(raw, `"22:00"`) {
		t.Errorf("stored: %s", raw)
	}
	if _, body, _ := do(t, nil, srv, call{method: "GET", path: "/api/settings"}); strings.Contains(body, "controlLevel") || strings.Contains(body, "perLibraryFlow") {
		t.Errorf("GET shows them: %s", body)
	}
}
