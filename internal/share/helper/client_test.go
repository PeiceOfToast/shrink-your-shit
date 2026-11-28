// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
)

// answerOnce serves one connection the way the real helper does: it reads the request until the
// client closes its write side, then decodes it and answers. A client that leaves the write side open
// hangs here, which is exactly what the installed helper would do.
func answerOnce(t *testing.T, answer Response) (string, <-chan Request) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mount.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	requests := make(chan Request, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		body, err := io.ReadAll(conn)
		if err != nil {
			requests <- Request{}
			return
		}
		var req Request
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			requests <- Request{}
			return
		}
		requests <- req
		_ = json.NewEncoder(conn).Encode(answer)
	}()
	return path, requests
}

func requestSeen(t *testing.T, requests <-chan Request) Request {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(5 * time.Second):
		t.Fatal("the client sent no request")
		return Request{}
	}
}

// TestClientMountSendsTheShareAndAnswersThePoint pins the wire request: the share's row, its type and
// its root source (never the subfolder, which lives inside the mount), the sign-in, and no mount
// point of the app's own.
func TestClientMountSendsTheShareAndAnswersThePoint(t *testing.T) {
	path, requests := answerOnce(t, Response{OK: true, Point: "/var/lib/syp-shares/7"})
	client := NewClient(path)
	point, err := client.Mount(context.Background(), share.Spec{
		ID:       7,
		Address:  share.Address{Type: share.SMB, Source: "//nas/media", Sub: "Films"},
		Username: "viewer", Domain: "WORK", Password: []byte("share-password"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if point != "/var/lib/syp-shares/7" {
		t.Errorf("point = %q, want the helper's own", point)
	}
	req := requestSeen(t, requests)
	want := Request{
		Action: ActionMount, ID: 7, Type: "smb", Source: "//nas/media",
		Username: "viewer", Domain: "WORK", Password: "share-password",
	}
	if req.Action != want.Action || req.ID != want.ID || req.Type != want.Type ||
		req.Source != want.Source || req.Username != want.Username ||
		req.Domain != want.Domain || req.Password != want.Password || req.Guest != want.Guest {
		t.Errorf("request = %+v, want %+v", req, want)
	}
}

// TestClientUnmountAnswersNotMounted: the helper's not_mounted is the share package's error, which
// Delete already treats as "nothing to do".
func TestClientUnmountAnswersNotMounted(t *testing.T) {
	path, requests := answerOnce(t, Response{Reason: ReasonNotMounted, Message: "The share is not mounted."})
	client := NewClient(path)
	err := client.Unmount(context.Background(), share.Spec{ID: 7})
	if !errors.Is(err, share.ErrNotMounted) {
		t.Fatalf("err = %v, want share.ErrNotMounted", err)
	}
	if req := requestSeen(t, requests); req.Action != ActionUnmount || req.ID != 7 {
		t.Errorf("request = %+v, want an unmount of share 7", req)
	}
}

// TestClientKeepsAPageReason: a mount's own refusal (bad_credentials) reaches the page unchanged.
func TestClientKeepsAPageReason(t *testing.T) {
	path, _ := answerOnce(t, Response{
		Reason: share.ReasonBadCredentials, Message: "The server did not accept that username and password.",
	})
	_, err := NewClient(path).Mount(context.Background(), share.Spec{
		ID: 7, Address: share.Address{Type: share.SMB, Source: "//nas/media"},
	})
	var me *share.MountError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want a MountError", err)
	}
	if me.Reason != share.ReasonBadCredentials || me.Message != "The server did not accept that username and password." {
		t.Errorf("MountError = %+v, want the helper's reason and sentence", me)
	}
}

// TestClientTurnsAProtocolRefusalIntoAmountFailure: the helper's own codes (refused, invalid_request)
// are not the page's vocabulary; the sentence is what the person needs.
func TestClientTurnsAProtocolRefusalIntoAmountFailure(t *testing.T) {
	const sentence = "Something this app did not mount is at the share's point, so it was left alone."
	path, _ := answerOnce(t, Response{Reason: ReasonRefused, Message: sentence})
	_, err := NewClient(path).Mount(context.Background(), share.Spec{
		ID: 7, Address: share.Address{Type: share.SMB, Source: "//nas/media"},
	})
	var me *share.MountError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want a MountError", err)
	}
	if me.Reason != share.ReasonMountFailed || me.Message != sentence {
		t.Errorf("MountError = %+v, want mount_failed with the helper's sentence", me)
	}
}

// TestClientWithoutTheHelperSaysSo: a missing socket is an answer, not a hang.
func TestClientWithoutTheHelperSaysSo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.sock")
	_, err := NewClient(path).Mount(context.Background(), share.Spec{
		ID: 7, Address: share.Address{Type: share.SMB, Source: "//nas/media"},
	})
	var me *share.MountError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want a MountError", err)
	}
	if me.Message != "The share's mount service is not answering. Try again in a moment." {
		t.Errorf("message = %q", me.Message)
	}
}

// TestClientBoundsAHelperThatNeverAnswers: a helper that accepts and then says nothing answers the
// caller's deadline, so the page is not held.
func TestClientBoundsAHelperThatNeverAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wedged.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case conn := <-accepted:
			_ = conn.Close()
		case <-time.After(100 * time.Millisecond):
		}
	})
	go func() {
		if conn, err := ln.Accept(); err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = NewClient(path).Mount(ctx, share.Spec{
		ID: 7, Address: share.Address{Type: share.SMB, Source: "//nas/media"},
	})
	var me *share.MountError
	if !errors.As(err, &me) {
		t.Fatalf("err = %v, want a MountError", err)
	}
	if me.Reason != share.ReasonTimeout {
		t.Errorf("reason = %q, want timeout", me.Reason)
	}
}
