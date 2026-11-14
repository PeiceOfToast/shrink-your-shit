// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package remote

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Joiner is the worker side of joining a server, for the setup page. It is the Client with each failure coded
// (JoinCode), so the page can answer each one without importing this package: remote's tests import web.
type Joiner struct {
	Version string
}

// JoinFailure is a failed join. Code is one of not_a_server, refused, unreachable, pairing_invalid, not_local,
// denied or expired; Msg is the server's own words when it gave some (refused).
type JoinFailure struct {
	Code, Msg string
}

// JoinCode is the failure's code.
func (e *JoinFailure) JoinCode() string { return e.Code }

func (e *JoinFailure) Error() string { return e.Msg }

func (j *Joiner) client(serverURL string) *Client {
	return &Client{Server: serverURL, Version: j.Version}
}

// fail codes err for the page; nil stays nil.
func fail(err error) error {
	if err == nil {
		return nil
	}
	var e *APIError
	switch {
	case errors.Is(err, ErrJoinDenied):
		return &JoinFailure{Code: "denied"}
	case errors.Is(err, ErrJoinExpired):
		return &JoinFailure{Code: "expired"}
	case errors.Is(err, ErrNotWorkerAPI):
		return &JoinFailure{Code: "not_a_server"}
	case errors.As(err, &e) && e.Code == "":
		return &JoinFailure{Code: "not_a_server"}
	case errors.As(err, &e) && (e.Code == "pairing_invalid" || e.Code == "not_local"):
		return &JoinFailure{Code: e.Code}
	case errors.As(err, &e):
		return &JoinFailure{Code: "refused", Msg: e.Message}
	}
	return &JoinFailure{Code: "unreachable"}
}

// Pair pairs with a code: the worker's id, the name the server gives it, and its token.
func (j *Joiner) Pair(ctx context.Context, serverURL, code, name string) (id, workerName, token string, err error) {
	st, err := j.client(serverURL).Pair(ctx, code, name)
	if err != nil {
		return "", "", "", fail(err)
	}
	return st.WorkerID, st.Name, st.Token, nil
}

// Ask asks the server to let this computer in: the request's id, the secret to poll it with, and when it expires.
func (j *Joiner) Ask(ctx context.Context, serverURL, name, osName string, port int) (id, secret, expiresAt string, err error) {
	a, err := j.client(serverURL).Ask(ctx, name, osName, port)
	if err != nil {
		return "", "", "", fail(err)
	}
	return a.ID, a.Secret, a.ExpiresAt, nil
}

// Wait polls the server until the person answers. Allowed, it returns the worker's id, name and token.
func (j *Joiner) Wait(ctx context.Context, serverURL, id, secret, expiresAt string, report func(state string)) (workerID, workerName, token string, err error) {
	st, err := j.client(serverURL).Wait(ctx, Asked{ID: id, Secret: secret, ExpiresAt: expiresAt}, report)
	if err != nil {
		return "", "", "", fail(err)
	}
	return st.WorkerID, st.Name, st.Token, nil
}

// Save keeps a worker's credential in dir, where the worker command reads it, with the server's name.
func (j *Joiner) Save(dir, serverURL, serverName, id, name, token string) error {
	return State{Server: serverURL, ServerName: serverName, WorkerID: id, Name: name, Token: token}.Save(dir)
}

// Saved reads the saved credential in dir: the server's address and the name it shows for itself ("" for a state
// saved before that was kept). ok is false when nothing usable is saved. The token is never returned.
func (j *Joiner) Saved(dir string) (serverURL, serverName string, ok bool) {
	st, err := Load(dir)
	if err != nil || st.Server == "" {
		return "", "", false
	}
	return st.Server, st.ServerName, true
}

// Forget removes the credential in dir, so the worker command can't start on it.
func (j *Joiner) Forget(dir string) {
	os.Remove(filepath.Join(dir, StateFile))
}
