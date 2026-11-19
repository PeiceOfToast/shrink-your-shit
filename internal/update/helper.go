// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"time"
)

// HelperSocket is syp-update-helper's socket (SYP_UPDATE_SOCKET overrides it, for a test install).
var HelperSocket = func() string {
	if s := os.Getenv("SYP_UPDATE_SOCKET"); s != "" {
		return s
	}
	return "/run/shrink-your-shit/update.sock"
}()

// HelperRequest is what the app sends the update helper. It never names a file: only a version.
type HelperRequest struct {
	Cmd     string `json:"cmd"` // "status" | "install" | "rollback"
	Version string `json:"version,omitempty"`
}

// HelperAnswer is the update helper's answer. State and Error describe the last operation.
type HelperAnswer struct {
	OK       bool   `json:"ok"`
	Code     string `json:"code,omitempty"`    // when !OK: bad_request, not_newer, busy, no_previous, not_allowed
	Message  string `json:"message,omitempty"` // when !OK
	State    string `json:"state"`             // idle | downloading | installing | failed
	Error    string `json:"error,omitempty"`   // the last operation's failure
	Previous bool   `json:"previous"`          // a previous program is kept, so a roll back is possible
}

// HelperError is a refusal the helper answered with.
type HelperError struct{ Code, Message string }

func (e *HelperError) Error() string { return e.Message }

// Helper talks to syp-update-helper over its socket.
type Helper struct{ Socket string }

// Call sends one request. A refusal is a *HelperError; the answer is returned either way.
func (h Helper) Call(req HelperRequest) (HelperAnswer, error) {
	sock := h.Socket
	if sock == "" {
		sock = HelperSocket
	}
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return HelperAnswer{}, fmt.Errorf("update helper: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return HelperAnswer{}, fmt.Errorf("update helper: %w", err)
	}
	var a HelperAnswer
	if err := json.NewDecoder(conn).Decode(&a); err != nil {
		return HelperAnswer{}, fmt.Errorf("update helper: %w", err)
	}
	if !a.OK {
		return a, &HelperError{Code: a.Code, Message: a.Message}
	}
	return a, nil
}
