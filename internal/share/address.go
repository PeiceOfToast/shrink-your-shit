// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package share

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Type is how a share is reached: SMB (what Windows calls a shared folder) or NFS.
type Type string

const (
	SMB Type = "smb"
	NFS Type = "nfs"
)

// Address is a checked share address: the source the helper mounts and the folder inside it a
// library watches. The person types one of //server/share[/sub], \\server\share[\sub],
// smb://server/share[/sub], server:/export[/sub] or nfs://server/export[/sub]; the address alone
// decides the type, so the form never asks.
type Address struct {
	Type   Type
	Source string // //server/share for SMB, server:/export for NFS: exactly what mount gets
	Sub    string // folder inside the share, "" for its root
}

// String is the normalized address, source and subfolder together: what the page shows back.
func (a Address) String() string {
	if a.Sub == "" {
		return a.Source
	}
	return a.Source + "/" + a.Sub
}

// DefaultName is the share's name when the person does not give one: the last folder they typed.
func (a Address) DefaultName() string {
	last := a.Source
	if a.Sub != "" {
		last = a.Sub
	}
	if i := strings.LastIndexByte(last, '/'); i >= 0 {
		last = last[i+1:]
	}
	return last
}

// ParseAddress reads what the person typed into an Address. hint is the optional type the caller
// sent ("" means none); a hint that disagrees with the address is refused rather than obeyed, so a
// mislabeled form cannot make the helper run the wrong mount command.
func ParseAddress(raw, hint string) (Address, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Address{}, addressError("Type the share's address.")
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return Address{}, addressError("The share's address has a character that can't be used.")
		}
	}
	want, err := parseType(hint)
	if err != nil {
		return Address{}, err
	}
	lower := strings.ToLower(s)
	var got Type
	var a Address
	switch {
	case strings.HasPrefix(lower, "smb://"):
		got = SMB
		a, err = parseSMB(s[len("smb://"):])
	case strings.HasPrefix(lower, "nfs://"):
		got = NFS
		a, err = parseNFS(s[len("nfs://"):])
	case strings.HasPrefix(s, `\\`), strings.HasPrefix(s, "//"):
		got = SMB
		a, err = parseSMB(s)
	case strings.Contains(s, ":"):
		got = NFS
		a, err = parseNFS(s)
	default:
		return Address{}, addressError("A share's address looks like //server/share or server:/export.")
	}
	if err != nil {
		return Address{}, err
	}
	if want != "" && want != got {
		return Address{}, addressError(fmt.Sprintf("That address is an %s share, not %s.", strings.ToUpper(string(got)), article(want)))
	}
	return a, nil
}

func article(t Type) string {
	if t == SMB {
		return "an SMB one"
	}
	return "an NFS one"
}

func parseType(hint string) (Type, error) {
	switch Type(strings.ToLower(strings.TrimSpace(hint))) {
	case "":
		return "", nil
	case SMB:
		return SMB, nil
	case NFS:
		return NFS, nil
	}
	return "", &InvalidError{Code: "invalid_type", Field: "type", Message: "The share's type must be smb or nfs."}
}

// parseSMB reads //server/share[/sub] with either separator. The source stops at the share name:
// the subfolder only narrows what a library watches, it is not mounted on its own.
func parseSMB(s string) (Address, error) {
	s = strings.ReplaceAll(s, `\`, "/")
	s = strings.TrimPrefix(s, "//")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return Address{}, addressError("An SMB address looks like //server/share.")
	}
	if !validHost(parts[0]) {
		return Address{}, addressError("The server name in that address can't be used.")
	}
	for _, part := range parts[1:] {
		if !validSegment(part) {
			return Address{}, addressError("The folder names in that address can't be used.")
		}
	}
	return Address{
		Type:   SMB,
		Source: "//" + parts[0] + "/" + parts[1],
		Sub:    strings.Join(parts[2:], "/"),
	}, nil
}

// parseNFS reads server:/export[/sub] or the nfs://server/export form. The export is the mount
// source; the rest is the subfolder a library watches.
func parseNFS(s string) (Address, error) {
	var host, path string
	if i := strings.IndexByte(s, ':'); i >= 0 {
		host, path = s[:i], s[i+1:]
	} else if i := strings.IndexByte(s, '/'); i >= 0 {
		host, path = s[:i], s[i:]
	} else {
		return Address{}, addressError("An NFS address looks like server:/export.")
	}
	if !validHost(host) {
		return Address{}, addressError("The server name in that address can't be used.")
	}
	path = strings.Trim(path, "/")
	if path == "" {
		return Address{}, addressError("An NFS address looks like server:/export.")
	}
	parts := strings.Split(path, "/")
	for _, part := range parts {
		if !validSegment(part) {
			return Address{}, addressError("The folder names in that address can't be used.")
		}
	}
	return Address{
		Type:   NFS,
		Source: host + ":/" + parts[0],
		Sub:    strings.Join(parts[1:], "/"),
	}, nil
}

// validHost is an IPv4 address or a DNS name: no colon (so no IPv6 bracket form), no control
// characters, and labels that start or end with a hyphen are refused because mount would read
// them as an option.
func validHost(h string) bool {
	if h == "" || len(h) > 253 || h[0] == '-' || strings.Contains(h, ":") {
		return false
	}
	for _, r := range h {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if ip := net.ParseIP(h); ip != nil {
		return ip.To4() != nil
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func validSegment(p string) bool {
	if p == "" || len(p) > 255 || p == "." || p == ".." {
		return false
	}
	if strings.TrimSpace(p) != p {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == ':' {
			return false
		}
	}
	return true
}

// SafeOptions is what the helper adds to every mount, whatever the share's options say. NFS is
// soft with a short timeout (question 5): a hard mount that loses its server blocks a job forever,
// while a soft one answers EIO, which the engine already treats as a failure or a requeue.
func SafeOptions(t Type) []string {
	opts := []string{"nosuid", "nodev", "noexec"}
	if t == NFS {
		opts = append(opts, "soft", "timeo=100", "retrans=2")
	}
	return opts
}

// CheckOptions refuses an option the helper does not understand. The helper rebuilds the list from
// parsed name and value rather than passing the person's text through, so this allow-list is the
// whole vocabulary it accepts.
func CheckOptions(t Type, opts []string) error {
	for _, opt := range opts {
		if err := checkOption(t, opt); err != nil {
			return err
		}
	}
	return nil
}

func checkOption(t Type, opt string) error {
	if opt == "" || strings.TrimSpace(opt) != opt || strings.Contains(opt, ",") {
		return fmt.Errorf("the mount option %q can't be used", opt)
	}
	name, value, hasValue := strings.Cut(opt, "=")
	if strings.Contains(strings.ToLower(name), "password") {
		return fmt.Errorf("a mount option may not carry a password")
	}
	oneOf := func(values ...string) bool {
		for _, v := range values {
			if value == v {
				return true
			}
		}
		return false
	}
	switch t {
	case SMB:
		switch name {
		case "guest":
			if !hasValue {
				return nil
			}
		case "vers":
			if hasValue && oneOf("2.0", "2.1", "3.0", "3.02", "3.1.1") {
				return nil
			}
		case "domain":
			if hasValue && value != "" && !strings.ContainsAny(value, `/\`) {
				return nil
			}
		}
	case NFS:
		switch name {
		case "tcp", "soft":
			if !hasValue {
				return nil
			}
		case "vers":
			if hasValue && oneOf("3", "4", "4.1", "4.2") {
				return nil
			}
		case "timeo", "retrans":
			if hasValue {
				if n, err := strconv.Atoi(value); err == nil && n >= 1 && n <= 600 {
					return nil
				}
			}
		}
	}
	return fmt.Errorf("the mount option %q is not one this app uses", opt)
}

func addressError(message string) error {
	return &InvalidError{Code: "invalid_address", Field: "address", Message: message}
}
