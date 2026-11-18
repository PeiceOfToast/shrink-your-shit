// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package helper communicates with the syp-network-helper (the root helper)
// over its Unix socket. Used by v2shares.go to mount/unmount/check shares.
package helper

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// HelperSocket is the default Unix socket path.
// Tests override this var to point at a temp socket.
var HelperSocket = "/run/shrink-your-shit/helper.sock"

// HelperTimeout is the dial timeout for socket connections.
const HelperTimeout = 5 * time.Second

// Command is sent over the Unix socket to the helper.
type Command struct {
	Cmd        string            `json:"cmd"` // "mount" | "unmount" | "status"
	Kind       string            `json:"kind,omitempty"`
	Address    string            `json:"address,omitempty"`
	MountPoint string            `json:"mountPoint"`
	Options    map[string]string `json:"options,omitempty"`
	Username   string            `json:"username,omitempty"`
	Password   string            `json:"password,omitempty"`
}

// Response from the helper.
type Response struct {
	OK    bool `json:"ok"`
	Error *struct {
		Code    string `json:"code,omitempty"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Pid int `json:"pid,omitempty"`
}

// ErrNotMounted is returned by Status when the mount is not active.
var ErrNotMounted = errors.New("not mounted")

// call sends a command to the helper and reads the JSON response.
func call(cmd Command) (Response, error) {
	conn, err := net.DialTimeout("unix", HelperSocket, HelperTimeout)
	if err != nil {
		return Response{}, fmt.Errorf("helper: %w", err)
	}
	defer conn.Close()
	if err := json.NewEncoder(conn).Encode(cmd); err != nil {
		return Response{}, fmt.Errorf("helper encode: %w", err)
	}
	var res Response
	if err := json.NewDecoder(conn).Decode(&res); err != nil {
		return Response{}, fmt.Errorf("helper decode: %w", err)
	}
	return res, nil
}

// MountError is a refusal or failure the helper reported, with its code.
type MountError struct {
	Code    string
	Message string
}

func (e *MountError) Error() string { return e.Message }

// answerError turns a helper answer that is not OK into a *MountError.
func answerError(res Response) error {
	if res.OK {
		return nil
	}
	if res.Error == nil {
		return &MountError{Code: "unknown", Message: "the helper failed"}
	}
	return &MountError{Code: res.Error.Code, Message: res.Error.Message}
}

// SplitDomain takes a Windows sign-in name apart: DOMAIN\user gives user and DOMAIN. user@domain and a
// plain name stay as they are, with no domain (cifs takes user@domain itself).
func SplitDomain(name string) (user, domain string) {
	if d, u, ok := strings.Cut(name, `\`); ok && d != "" && u != "" {
		return u, d
	}
	return name, ""
}

// Mount tells the helper to mount a share. A DOMAIN\user name becomes username=user with domain=DOMAIN.
func Mount(kind, address, mountPoint string, options map[string]string, username, password string) error {
	user, domain := SplitDomain(username)
	if domain != "" {
		withDomain := make(map[string]string, len(options)+1)
		for k, v := range options {
			withDomain[k] = v
		}
		withDomain["domain"] = domain
		options = withDomain
	}
	res, err := call(Command{
		Cmd: "mount", Kind: kind, Address: address,
		MountPoint: mountPoint, Options: options,
		Username: user, Password: password,
	})
	if err != nil {
		return err
	}
	return answerError(res)
}

// Unmount tells the helper to unmount a share.
func Unmount(mountPoint string) error {
	res, err := call(Command{Cmd: "unmount", MountPoint: mountPoint})
	if err != nil {
		return err
	}
	return answerError(res)
}

// Status checks whether a mount is active.
func Status(mountPoint string) error {
	res, err := call(Command{Cmd: "status", MountPoint: mountPoint})
	if err != nil {
		return err
	}
	if !res.OK {
		if res.Error != nil && res.Error.Code == "not_mounted" {
			return ErrNotMounted
		}
		return errors.New("helper: " + res.Error.Message)
	}
	return nil
}

// HelperInstalled returns true if the helper socket is reachable.
func HelperInstalled() bool {
	conn, err := net.DialTimeout("unix", HelperSocket, 100*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// SanitiseName makes a share name safe for directory use.
func SanitiseName(name string) string {
	r := strings.ToLower(name)
	r = strings.ReplaceAll(r, " ", "-")
	var out []rune
	for _, c := range r {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}
