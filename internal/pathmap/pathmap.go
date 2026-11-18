// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package pathmap maps a server path to a remote worker's path through a shared folder (V7c): a
// mapping says the server's /media/movies is the worker's \\nas\movies (or /mnt/movies). Prefix-safe
// ("/media" never maps "/media2"), and each side's path is cleaned in its own style: the server's as
// this system's, the worker's as Windows (a drive or a UNC path, or any backslash) or POSIX.
package pathmap

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

var drive = regexp.MustCompile(`^[A-Za-z]:`)

// WindowsStyle reports whether a worker path is a Windows one.
func WindowsStyle(p string) bool {
	return strings.Contains(p, `\`) || drive.MatchString(p)
}

// CleanWorker is a worker path cleaned in its own style: Windows (separators \, "\\server\share"
// kept, no trailing separator but a drive's root "D:\") or POSIX (path.Clean).
func CleanWorker(p string) string {
	if !WindowsStyle(p) {
		return path.Clean(p)
	}
	p = strings.ReplaceAll(p, "/", `\`)
	unc := strings.HasPrefix(p, `\\`)
	parts := []string{}
	for _, s := range strings.Split(p, `\`) {
		switch s {
		case "", ".":
		case "..":
			if len(parts) > 0 && !(len(parts) == 1 && drive.MatchString(parts[0])) {
				parts = parts[:len(parts)-1]
			}
		default:
			parts = append(parts, s)
		}
	}
	out := strings.Join(parts, `\`)
	switch {
	case unc:
		out = `\\` + out
	case len(parts) == 1 && drive.MatchString(parts[0]):
		out += `\`
	}
	return out
}

// Map is serverPath (a path on the server) as the worker sees it through the mapping server -> worker;
// ok false when serverPath isn't inside server (prefix-safe) or isn't absolute.
func Map(serverPath, server, worker string) (string, bool) {
	sp, sv := filepath.Clean(serverPath), filepath.Clean(server)
	if !filepath.IsAbs(sp) || !filepath.IsAbs(sv) {
		return "", false
	}
	rel, err := filepath.Rel(sv, sp)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	w := CleanWorker(worker)
	if rel == "." {
		return w, true
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if WindowsStyle(w) {
		return strings.TrimSuffix(w, `\`) + `\` + strings.Join(parts, `\`), true
	}
	return path.Join(append([]string{w}, parts...)...), true
}
