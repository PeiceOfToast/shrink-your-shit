// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrSystemFolder: a library folder that holds the computer's own files, or every user's (slice 23,
// SAF-4). Converting there could damage the system or other people's files, so it is never a library.
var ErrSystemFolder = errors.New("This is a system folder, a whole disk or a whole home folder, not a folder of videos. Nothing was changed. Pick the folder that holds your videos.")

// SystemFolder reports whether p (an absolute path, as stored) is refused as a library: the root of the
// disk, a home folder or the folder of all home folders, or a system folder or anything inside one.
func SystemFolder(p string) bool {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs // "/" on Windows is the current drive's root
	}
	home, _ := os.UserHomeDir()
	return systemFolder(p, runtime.GOOS, home, os.Getenv("SystemDrive"), os.Getenv("SystemRoot"))
}

// systemFolder is SystemFolder for any system, so each one's list is tested everywhere.
func systemFolder(p, goos, home, sysDrive, sysRoot string) bool {
	norm := func(s string) string {
		if goos == "windows" {
			s = strings.ReplaceAll(s, `\`, "/")
		}
		if goos == "windows" || goos == "darwin" { // names are not case sensitive there
			s = strings.ToLower(s)
		}
		s = path.Clean(s)
		if len(s) == 2 && s[1] == ':' {
			s += "/" // "C:" is the drive's root
		}
		return s
	}
	if goos == "windows" {
		s := strings.ReplaceAll(p, `\`, "/")
		if DevicePath(p) { // other names for any path, the system's too: never a library (review 75e86eb)
			return true
		}
		if strings.HasPrefix(s, "//") { // a UNC path: a bare \\host is refused; a share (\\nas\movies) is fine
			parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' })
			return len(parts) <= 1
		}
		// A drive root: only the system drive's (where "/" resolves to) is refused, below; a whole data
		// drive (D:\) is a normal place for videos (lead, 13:59Z).
	}
	p = norm(p)
	if p == "" || p == "." {
		return false
	}
	var exact, under []string
	switch goos {
	case "windows":
		if sysDrive == "" {
			sysDrive = "C:"
		}
		if sysRoot == "" {
			sysRoot = sysDrive + `\Windows`
		}
		exact = []string{sysDrive + `\`, sysDrive + `\Users`}
		under = []string{sysRoot, sysDrive + `\Program Files`, sysDrive + `\Program Files (x86)`, sysDrive + `\ProgramData`}
	default:
		exact = []string{"/", "/home", "/Users", "/root", "/var", "/private", "/private/var", "/Volumes"}
		under = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32", "/boot", "/dev",
			"/proc", "/sys", "/run", "/snap", "/System", "/Library", "/Applications", "/private/etc"}
	}
	if home != "" {
		exact = append(exact, home)
	}
	for _, e := range exact {
		if p == norm(e) {
			return true
		}
	}
	for _, u := range under {
		u = norm(u)
		if p == u || strings.HasPrefix(p, strings.TrimSuffix(u, "/")+"/") {
			return true
		}
	}
	return false
}

// SystemDirForTest is a system folder on the running system (/etc, or %SystemRoot%), for tests in any
// package to check the refusal with a real one.
func SystemDirForTest() string {
	if runtime.GOOS == "windows" {
		if r := os.Getenv("SystemRoot"); r != "" {
			return r
		}
		return `C:\Windows`
	}
	return "/etc"
}

// DevicePath reports whether p is in a Windows device namespace (\\?\..., \\.\...): other names for
// ordinary paths, system folders included. A library folder is chosen by its ordinary full path, so
// these are refused (invalid_path), not translated (lead, 14:06Z).
func DevicePath(p string) bool {
	s := strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	return strings.HasPrefix(s, "//?/") || strings.HasPrefix(s, "//./")
}
