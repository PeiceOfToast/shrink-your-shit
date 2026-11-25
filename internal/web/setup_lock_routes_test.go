package web

import "testing"

// Before setup is finished, the routes the setup pages use take the token and refuse without it, from the LAN.
// A 400 with the token is a body check that ran after the sign-in check, so it still counts as accepted.
func TestSetupRoutesAcceptTheTokenBeforeSetup(t *testing.T) {
	a := newAccess(t, false)
	tok := a.Token()
	srv := peerServerAt(t, a, "192.168.1.7:5555", false)
	routes := []call{
		{method: "GET", path: "/api/v2/setup"},
		{method: "GET", path: "/api/v2/setup/servers"},
		{method: "POST", path: "/api/v2/setup/role", body: "{}", ctype: "application/json"},
		{method: "GET", path: "/api/settings"},
		{method: "PATCH", path: "/api/settings", body: "{}", ctype: "application/json"},
		{method: "GET", path: "/api/status"},
		{method: "GET", path: "/api/libraries"},
		{method: "POST", path: "/api/libraries/check", body: "{}", ctype: "application/json"},
		{method: "POST", path: "/api/libraries/first-files", body: "{}", ctype: "application/json"},
		{method: "GET", path: "/api/v2/libraries"},
		{method: "POST", path: "/api/v2/libraries", body: "{}", ctype: "application/json"},
		{method: "GET", path: "/api/v2/workflows"},
		{method: "GET", path: "/api/v2/workflows/guide"},
		{method: "GET", path: "/api/v2/workflow-variables"},
		{method: "POST", path: "/api/v2/workflows/guide/preview", body: "{}", ctype: "application/json"},
		{method: "GET", path: "/api/v2/workers"},
		{method: "GET", path: "/api/workers"},
		{method: "POST", path: "/api/v2/workers/pairings", body: "{}", ctype: "application/json"},
	}
	for _, r := range routes {
		if code, _, _ := do(t, nil, srv, r); code != 401 {
			t.Errorf("%s %s, LAN, no token, before setup: %d, want 401", r.method, r.path, code)
		}
		r.bearer = tok
		if code, body, _ := do(t, nil, srv, r); code == 401 {
			t.Errorf("%s %s, LAN, with the token, before setup: 401 %s", r.method, r.path, body)
		}
	}
}
