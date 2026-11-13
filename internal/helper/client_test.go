// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

func TestSplitDomain(t *testing.T) {
	for _, c := range []struct{ in, user, domain string }{
		{`CORP\alice`, "alice", "CORP"},
		{"alice@corp.example", "alice@corp.example", ""},
		{"alice", "alice", ""},
		{`\alice`, `\alice`, ""}, // no domain before the backslash: nothing to split
		{`CORP\`, `CORP\`, ""},   // no user after it: nothing to split
	} {
		u, d := SplitDomain(c.in)
		if u != c.user || d != c.domain {
			t.Errorf("SplitDomain(%q) = %q, %q; want %q, %q", c.in, u, d, c.user, c.domain)
		}
	}
}

// Mount sends the bare user and the domain as a mount option; the caller's options map is not changed.
func TestMountSendsDomainAsOption(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "h.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan Command, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var cmd Command
		json.NewDecoder(conn).Decode(&cmd)
		got <- cmd
		json.NewEncoder(conn).Encode(Response{OK: true})
	}()
	saved := HelperSocket
	HelperSocket = sock
	t.Cleanup(func() { HelperSocket = saved })

	opts := map[string]string{"vers": "3.1.1"}
	if err := Mount("cifs", "//nas/media", "/m", opts, `CORP\alice`, "pw"); err != nil {
		t.Fatal(err)
	}
	cmd := <-got
	if cmd.Username != "alice" || cmd.Options["domain"] != "CORP" || cmd.Options["vers"] != "3.1.1" {
		t.Errorf("sent %+v", cmd)
	}
	if _, changed := opts["domain"]; changed {
		t.Error("the caller's options map was changed")
	}
}
