// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

// linuxEnvFile is where install.sh keeps the Linux service's settings.
const linuxEnvFile = "/etc/default/shrink-your-shit"

// tokenCommand is "shrink-your-shit token": print the access token and the sign-in address. It finds
// the installed service's data folder by itself, so nobody has to pass flags.
func tokenCommand(out, errOut io.Writer) int {
	env := envFile(linuxEnvFile)
	var denied error
	for _, dir := range tokenDirs(env) {
		tok, err := web.ReadToken(dir)
		if errors.Is(err, fs.ErrPermission) {
			denied = err
			continue
		}
		if err != nil || tok == "" {
			continue
		}
		addr := firstNonEmpty(os.Getenv("SYP_ADDR"), env["SYP_ADDR"], "127.0.0.1:8484")
		fmt.Fprintf(out, "Access token: %s\nSign-in address: %s\n", tok, signInURL(addr, tok))
		return 0
	}
	if denied != nil {
		fmt.Fprintf(errOut, "Cannot read the access token (%v).\nRun it as the user the service runs as, or with sudo: sudo shrink-your-shit token\n", denied)
		return 1
	}
	fmt.Fprintln(errOut, "No access token is set. That means the web page only listens on this computer and needs no sign-in.")
	return 1
}

// tokenDirs lists where a data folder may be, most specific first: SYP_DATA, the Linux service's (from
// its settings file, then its usual place), the Windows installer's, then this user's default.
func tokenDirs(env map[string]string) []string {
	dirs := []string{os.Getenv("SYP_DATA"), env["SYP_DATA"], "/var/lib/shrink-your-shit"}
	if la := os.Getenv("LOCALAPPDATA"); la != "" {
		dirs = append(dirs, filepath.Join(la, "shrink-your-shit", "data"))
	}
	var out []string
	for _, d := range append(dirs, defaultDataDir()) {
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

// envFile reads KEY=VALUE lines (missing file: empty).
func envFile(path string) map[string]string {
	m := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return m
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "="); ok && !strings.HasPrefix(k, "#") {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// signInURL is the web address for addr with the token in it. "All addresses" (0.0.0.0, ::) becomes
// this machine's first network address, which is how other machines reach it.
func signInURL(addr, tok string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = addr, "8484"
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = lanAddress()
	}
	return "http://" + net.JoinHostPort(host, port) + "/?token=" + url.QueryEscape(tok)
}

func lanAddress() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
			return n.IP.String()
		}
	}
	return "127.0.0.1"
}
