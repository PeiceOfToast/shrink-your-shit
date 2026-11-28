// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

// The app's half of the helper protocol (S3-7 slice 4): one JSON request per connection, the same
// Request and Response the server decodes. A Client is a share.Mounter, so the share service mounts
// through it exactly as it does through a test's fake.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/share"
)

// SocketPath is where deploy/install.sh's syp-mount.socket listens.
const SocketPath = "/run/syp/mount.sock"

const (
	// clientCap bounds a request's or an answer's size. The server refuses a request past the same
	// cap; an answer is small by design.
	clientCap = 64 << 10
	// clientTimeout bounds a request the caller did not bound itself. The helper bounds its own
	// work shorter than this (30 s for a mount), so a wedged helper cannot hold the app past it.
	clientTimeout = 60 * time.Second
)

// Client asks the root helper over its unix socket to mount or unmount a share. It is share.Mounter.
type Client struct {
	Socket string
}

// NewClient returns a client for the helper listening on socket.
func NewClient(socket string) *Client { return &Client{Socket: socket} }

// Mount asks the helper to mount the share and answers the point it chose. The address's subfolder
// is not sent: the helper mounts the whole share, and a library watches the folder inside it.
func (c *Client) Mount(ctx context.Context, spec share.Spec) (string, error) {
	resp, err := c.do(ctx, Request{
		Action: ActionMount, ID: spec.ID,
		Type: string(spec.Address.Type), Source: spec.Address.Source,
		Username: spec.Username, Domain: spec.Domain, Guest: spec.Guest,
		Password: string(spec.Password),
	})
	if err != nil {
		return "", err
	}
	if !resp.OK {
		return "", mountErrorOf(resp)
	}
	if resp.Point == "" {
		return "", &share.MountError{
			Reason:  share.ReasonMountFailed,
			Message: "The share's mount service did not say where it mounted the share.",
		}
	}
	return resp.Point, nil
}

// Unmount asks the helper to remove the share's mount. Nothing mounted answers share.ErrNotMounted,
// as the share service's Delete expects.
func (c *Client) Unmount(ctx context.Context, spec share.Spec) error {
	resp, err := c.do(ctx, Request{Action: ActionUnmount, ID: spec.ID})
	if err != nil {
		return err
	}
	if resp.OK {
		return nil
	}
	if resp.Reason == ReasonNotMounted {
		return share.ErrNotMounted
	}
	return mountErrorOf(resp)
}

// do sends one request and reads one answer.
func (c *Client) do(ctx context.Context, req Request) (Response, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, clientTimeout)
		defer cancel()
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return Response{}, notAnswering(ctx, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, notAnswering(ctx, err)
	}
	// The helper reads one request until EOF (its own read is io.ReadAll), so the write side must
	// close or it waits for input until its request deadline and answers "could not be read".
	if cw, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
	}
	body, err := io.ReadAll(io.LimitReader(conn, clientCap+1))
	if err != nil {
		return Response{}, notAnswering(ctx, err)
	}
	if len(body) > clientCap {
		return Response{}, unreadable()
	}
	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return Response{}, unreadable()
	}
	return resp, nil
}

// notAnswering is the page's sentence when the helper is not there or did not answer in time.
func notAnswering(ctx context.Context, err error) error {
	// The connection's own read deadline is the request's, and a net timeout is not a context error.
	var timedOut net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		(errors.As(err, &timedOut) && timedOut.Timeout()) {
		return &share.MountError{Reason: share.ReasonTimeout,
			Message: "The share's mount service did not answer in time."}
	}
	return &share.MountError{Reason: share.ReasonMountFailed,
		Message: "The share's mount service is not answering. Try again in a moment."}
}

func unreadable() error {
	return &share.MountError{Reason: share.ReasonMountFailed,
		Message: "The share's mount service gave an answer this app can't read."}
}

// mountErrorOf turns the helper's refusal into the page's reason and sentence. The protocol's own
// codes are not the page's vocabulary, so they become a mount failure with the helper's sentence;
// a mount's own codes (bad_credentials and the rest) pass through.
func mountErrorOf(resp Response) error {
	reason := resp.Reason
	switch reason {
	case "", ReasonInvalidRequest, ReasonRefused:
		reason = share.ReasonMountFailed
	}
	message := resp.Message
	if message == "" {
		message = "The share could not be mounted."
	}
	return &share.MountError{Reason: reason, Message: message}
}
