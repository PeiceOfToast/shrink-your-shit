// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// fakeWindows makes the app act as on Windows, with the Windows connect and disconnect calls faked.
// It returns the calls made, as "unc|user|password" and "unc".
func fakeWindows(t *testing.T, connect func(unc, user, pw string) error) (connects, disconnects *[]string) {
	t.Helper()
	var c, d []string
	oldOn, oldConnect, oldDisconnect, oldAnswers := onWindows, winConnect, winDisconnect, shareAnswers
	onWindows = func() bool { return true }
	winConnect = func(unc, user, pw string) error {
		c = append(c, unc+"|"+user+"|"+pw)
		return connect(unc, user, pw)
	}
	winDisconnect = func(unc string) error {
		d = append(d, unc)
		return nil
	}
	t.Cleanup(func() {
		onWindows, winConnect, winDisconnect, shareAnswers = oldOn, oldConnect, oldDisconnect, oldAnswers
	})
	return &c, &d
}

// D3b: the reason codes a failed Windows connect shows, on every OS.
func TestWinMountReasonCodes(t *testing.T) {
	cases := []struct {
		code uint32
		want string
	}{
		{53, "host_not_found"},
		{67, "share_not_found"},
		{1326, "auth_failed"},
		{86, "auth_failed"},
		{5, "access_denied"},
		{1231, "host_unreachable"},
		{64, "host_unreachable"},
		{9999, "mount_failed"},
	}
	for _, c := range cases {
		var r struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		reason := winMountReason(&winError{Code: c.code, Words: "system words"})
		if err := json.Unmarshal([]byte(reason), &r); err != nil {
			t.Fatalf("error %d: reason is not JSON: %v", c.code, err)
		}
		if r.Code != c.want {
			t.Errorf("error %d: want %s, got %s", c.code, c.want, r.Code)
		}
		if r.Message == "" {
			t.Errorf("error %d: empty message", c.code)
		}
		if c.want == "mount_failed" && !strings.Contains(r.Message, "system words") {
			t.Errorf("error %d: the system's words are missing: %q", c.code, r.Message)
		}
	}
}

// POST connects the share as the person (deviceless, with the user and password), and GET shows it
// connected at its \\server\share path. The password never appears in the page.
func TestWindowsShareConnectsAsThePerson(t *testing.T) {
	connects, _ := fakeWindows(t, func(string, string, string) error { return nil })
	t.Setenv("HOME", t.TempDir())
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\movies","name":"Movies","username":"bob","password":"hunter2-secret"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	if len(*connects) != 1 || (*connects)[0] != `\\nas\movies|bob|hunter2-secret` {
		t.Fatalf("winConnect calls: %q", *connects)
	}
	code, resp, _ = do(t, nil, srv, call{method: "GET", path: "/api/v2/shares"})
	if code != http.StatusOK {
		t.Fatalf("GET: %d %s", code, resp)
	}
	if strings.Contains(resp, "hunter2-secret") {
		t.Errorf("the password is in the page: %s", resp)
	}
	var list struct {
		Items []struct {
			Status string `json:"status"`
			Path   string `json:"path"`
		} `json:"items"`
	}
	json.Unmarshal([]byte(resp), &list)
	if len(list.Items) != 1 || list.Items[0].Status != "connected" || list.Items[0].Path != `\\nas\movies` {
		t.Errorf("want one connected share at \\\\nas\\movies, got %+v", list.Items)
	}
}

// A guest share connects with no user and no password.
func TestWindowsGuestConnectsWithoutCredentials(t *testing.T) {
	connects, _ := fakeWindows(t, func(string, string, string) error { return nil })
	t.Setenv("HOME", t.TempDir())
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\movies","name":"Guest","guest":true,"username":"bob","password":"x"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	if len(*connects) != 1 || (*connects)[0] != `\\nas\movies||` {
		t.Errorf("a guest connect must send no user or password, got %q", *connects)
	}
}

// A refused logon is shown as auth_failed, with the share not connected.
func TestWindowsAuthRefusalNamesTheReason(t *testing.T) {
	fakeWindows(t, func(string, string, string) error {
		return &winError{Code: 1326, Words: "The user name or password is incorrect."}
	})
	t.Setenv("HOME", t.TempDir())
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\movies","name":"Movies","username":"bob","password":"wrong"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created struct {
		Status string         `json:"status"`
		Reason map[string]any `json:"reason"`
	}
	json.Unmarshal([]byte(resp), &created)
	if created.Status != "not_connected" {
		t.Errorf("want not_connected, got %q", created.Status)
	}
	if created.Reason["code"] != "auth_failed" {
		t.Errorf("want auth_failed, got %v", created.Reason)
	}
}

// Windows already holding a connection to the share counts as connected when its path answers, and as
// not connected when it does not.
func TestWindowsAlreadyConnectedCountsWhenPathAnswers(t *testing.T) {
	for _, answers := range []bool{true, false} {
		fakeWindows(t, func(string, string, string) error { return &winError{Code: 1219} })
		shareAnswers = func(string) bool { return answers }
		t.Setenv("HOME", t.TempDir())
		srv, _, _ := server(t, false)

		code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
			body:  `{"address":"\\\\nas\\movies","name":"Movies"}`,
			ctype: "application/json"})
		if code != http.StatusCreated {
			t.Fatalf("answers=%v POST: %d %s", answers, code, resp)
		}
		var created struct {
			Status string `json:"status"`
		}
		json.Unmarshal([]byte(resp), &created)
		want := "not_connected"
		if answers {
			want = "connected"
		}
		if created.Status != want {
			t.Errorf("answers=%v: want %s, got %q", answers, want, created.Status)
		}
	}
}

// NFS is refused on Windows, and nothing is connected.
func TestWindowsRefusesNFS(t *testing.T) {
	connects, _ := fakeWindows(t, func(string, string, string) error { return nil })
	t.Setenv("HOME", t.TempDir())
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"nas:/export","name":"Exports"}`,
		ctype: "application/json"})
	if code != http.StatusBadRequest || !strings.Contains(resp, "nfs_unsupported") {
		t.Errorf("want 400 nfs_unsupported, got %d %s", code, resp)
	}
	if len(*connects) != 0 {
		t.Errorf("an NFS share must not be connected: %q", *connects)
	}
}

// DELETE disconnects the share at its UNC path.
func TestWindowsDeleteDisconnects(t *testing.T) {
	_, disconnects := fakeWindows(t, func(string, string, string) error { return nil })
	t.Setenv("HOME", t.TempDir())
	srv, _, _ := server(t, false)

	code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
		body:  `{"address":"\\\\nas\\movies","name":"Movies"}`,
		ctype: "application/json"})
	if code != http.StatusCreated {
		t.Fatalf("POST: %d %s", code, resp)
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal([]byte(resp), &created)
	code, resp, _ = do(t, nil, srv, call{method: "DELETE", path: "/api/v2/shares/" + created.ID,
		body: "{}", ctype: "application/json"})
	if code != http.StatusNoContent {
		t.Fatalf("DELETE: %d %s", code, resp)
	}
	if len(*disconnects) != 1 || (*disconnects)[0] != `\\nas\movies` {
		t.Errorf("winDisconnect calls: %q", *disconnects)
	}
}
