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
const HelperSocket = "/run/shrink-your-shit/helper.sock"

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

// Mount tells the helper to mount a share.
func Mount(kind, address, mountPoint string, options map[string]string, username, password string) error {
	res, err := call(Command{
		Cmd: "mount", Kind: kind, Address: address,
		MountPoint: mountPoint, Options: options,
		Username: username, Password: password,
	})
	if err != nil {
		return err
	}
	if !res.OK && res.Error != nil {
		return errors.New(res.Error.Message)
	}
	return nil
}

// Unmount tells the helper to unmount a share.
func Unmount(mountPoint string) error {
	res, err := call(Command{Cmd: "unmount", MountPoint: mountPoint})
	if err != nil {
		return err
	}
	if !res.OK && res.Error != nil {
		return errors.New(res.Error.Message)
	}
	return nil
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
