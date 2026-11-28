// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package helper

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestPeerUIDIsTheCallersUID: SO_PEERCRED reports the kernel's answer, not anything the client
// sent. On this test's machine that is the test's own uid.
func TestPeerUIDIsTheCallersUID(t *testing.T) {
	dir, err := os.MkdirTemp("", "syp-peer-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan uint32, 1)
	failed := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			failed <- err
			return
		}
		defer conn.Close()
		uid, err := peerUID(conn.(*net.UnixConn))
		if err != nil {
			failed <- err
			return
		}
		got <- uid
	}()
	conn, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	select {
	case err := <-failed:
		t.Fatal(err)
	case uid := <-got:
		if int(uid) != os.Geteuid() {
			t.Errorf("peer uid = %d, want %d", uid, os.Geteuid())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no peer credential")
	}
}
