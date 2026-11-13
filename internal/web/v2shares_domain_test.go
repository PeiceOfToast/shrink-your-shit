// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/shrink-your-shit/shrink-your-shit/internal/helper"
)

// A Windows sign-in name typed as DOMAIN\user reaches cifs as username=user with domain=DOMAIN.
// user@domain and a plain name are passed on as they are.
var domainCases = []struct {
	name, json, user, domainOpt string
}{
	{"domain", `CORP\\alice`, "username=alice,domain=CORP", "domain=CORP"},
	{"upn", "alice@corp.example", "username=alice@corp.example", ""},
	{"plain", "alice", "username=alice", ""},
}

// App mode: the helper gets the bare user, and domain= is a mount option only when there is a domain.
func TestSharesDomainUsernameMount(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("app mode (the mount helper) is Linux only")
	}
	for _, c := range domainCases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("HOME", filepath.Join(dir, "home"))
			os.MkdirAll(filepath.Join(dir, "home"), 0o755)
			sock := filepath.Join(dir, "helper.sock")
			saved := helper.HelperSocket
			helper.HelperSocket = sock
			defer func() { helper.HelperSocket = saved }()
			cmds := fakeHelper(t, sock, func(map[string]any) map[string]any {
				return map[string]any{"ok": true, "pid": 42}
			})
			srv, _, _ := server(t, false)
			code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
				body:  `{"address":"\\\\nas\\media","name":"Share","username":"` + c.json + `","password":"pw"}`,
				ctype: "application/json"})
			if code != http.StatusCreated {
				t.Fatalf("POST: %d %s", code, resp)
			}
			cmd := commandNamed(cmds(), "mount")
			if cmd == nil {
				t.Fatal("helper was never asked to mount")
			}
			wantUser := map[string]string{"domain": "alice", "upn": "alice@corp.example", "plain": "alice"}[c.name]
			if u, _ := cmd["username"].(string); u != wantUser {
				t.Errorf("username %q, want %q", u, wantUser)
			}
			opts, _ := cmd["options"].(map[string]any)
			d, has := opts["domain"]
			if c.domainOpt == "" && has {
				t.Errorf("unexpected domain option %v", d)
			}
			if c.domainOpt != "" && d != "CORP" {
				t.Errorf("domain option %v, want CORP", d)
			}
		})
	}
}

// Manual and Docker setup texts: the same split, so the text a user copies mounts the same way.
func TestSharesDomainUsernameSetupText(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows connects shares itself: no setup text")
	}
	for _, mode := range []string{"manual", "docker"} {
		for _, c := range domainCases {
			t.Run(mode+"/"+c.name, func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv("HOME", filepath.Join(dir, "home"))
				os.MkdirAll(filepath.Join(dir, "home"), 0o755)
				t.Setenv("APPDATA", filepath.Join(dir, "home"))
				if mode == "docker" {
					t.Setenv("container", "abc123")
				} else {
					t.Setenv("container", "")
				}
				srv, _, _ := server(t, false)
				code, resp, _ := do(t, nil, srv, call{method: "POST", path: "/api/v2/shares",
					body:  `{"address":"\\\\server\\share","name":"Share","username":"` + c.json + `"}`,
					ctype: "application/json"})
				if code != http.StatusCreated {
					t.Fatalf("POST: %d %s", code, resp)
				}
				if !strings.Contains(resp, c.user) {
					t.Errorf("setup text lacks %q: %s", c.user, resp)
				}
				if c.domainOpt == "" && strings.Contains(resp, "domain=") {
					t.Errorf("unexpected domain= in setup: %s", resp)
				}
				if c.domainOpt != "" && !strings.Contains(resp, c.domainOpt) {
					t.Errorf("setup lacks %s: %s", c.domainOpt, resp)
				}
				// The response echoes the stored name as JSON ("CORP\\alice"); the setup text must not carry it.
				if strings.Contains(resp, `username=CORP\\alice`) {
					t.Errorf("setup text carries the whole DOMAIN\\user: %s", resp)
				}
			})
		}
	}
}
