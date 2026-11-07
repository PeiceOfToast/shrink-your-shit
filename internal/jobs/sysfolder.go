// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"errors"
	"os"
	"path"
	"runtime"
	"strings"
)

// ErrSystemFolder: a library folder that holds the computer's own files, or every user's (slice 23,
// SAF-4). Converting there could damage the system or other people's files, so it is never a library.
var ErrSystemFolder = errors.New("This is a system folder, a whole disk or a whole home folder, not a folder of videos. Nothing was changed. Pick the folder that holds your videos.")

// SystemFolder reports whether p (an absolute path, as stored) is refused as a library: the root of the
// disk, a home folder or the folder of all home folders, or a system folder or anything inside one.
func SystemFolder(p string) bool {
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
