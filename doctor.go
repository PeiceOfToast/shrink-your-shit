// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/user"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/doctor"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/web"
)

// doctorCommand is "shrink-your-shit doctor": the setup checks, as text. It asks the running service
// for its health report, so the folder checks are done as the user the service runs as (with sudo on
// Linux, this command itself runs as root). If the service does not answer, it checks what it can from
// here and says so. Exit status 1 if any check is bad.
func doctorCommand(out, errOut io.Writer) int {
	env := envFile(linuxEnvFile)
	addr, tok := serviceAddr(env)
	h, err := askService(addr, tok)
	if err == nil {
		fmt.Fprintf(out, "shrink-your-shit %s, as the running service sees it (%s):\n\n", h.Version, localURL(addr))
		return printHealth(out, h)
	}
	who := "this user"
	if u, uerr := user.Current(); uerr == nil {
		who = u.Username
	}
	fmt.Fprintf(errOut, "The service did not answer at %s (%v).\nChecking from this terminal instead, as %s. The service may run as another user, so its folder checks can differ; library folders are only checked by the running service.\n\n", localURL(addr), err, who)
	ffmpeg, _ := resolveTool("ffmpeg", os.Getenv("SYP_FFMPEG"))
	ffprobe, _ := resolveTool("ffprobe", os.Getenv("SYP_FFPROBE"))
	chk := &doctor.Checker{Exec: media.OSExec{}, FFmpeg: ffmpeg, FFprobe: ffprobe}
	data := firstNonEmpty(os.Getenv("SYP_DATA"), env["SYP_DATA"], defaultDataDir())
	return printHealth(out, doctor.Health{Checks: chk.Health(context.Background(), data, nil), Version: version})
}

// serviceAddr is where the installed program listens and its access token ("" if none is found).
func serviceAddr(env map[string]string) (addr, tok string) {
	addr = firstNonEmpty(os.Getenv("SYP_ADDR"), installedAddr(env), "127.0.0.1:8484")
	for _, dir := range tokenDirs(env) {
		if t, err := web.ReadToken(dir); err == nil && t != "" {
			return addr, t
		}
	}
	return addr, ""
}

// askService fetches the health report from the running program.
func askService(addr, tok string) (doctor.Health, error) {
	var h doctor.Health
	req, err := http.NewRequest("GET", localURL(addr)+"/api/system/health", nil)
	if err != nil {
		return h, err
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := &http.Client{Timeout: 2 * time.Minute} // the hardware test encodes and folder walks take a moment
	resp, err := c.Do(req)
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return h, fmt.Errorf("it answered %s", resp.Status)
	}
	return h, json.NewDecoder(resp.Body).Decode(&h)
}

// localURL is the address to reach the program from this machine ("all addresses" means loopback).
func localURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		host, port = "127.0.0.1", "8484"
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func printHealth(out io.Writer, h doctor.Health) int {
	mark := map[string]string{"ok": "ok  ", "warn": "WARN", "bad": "BAD "}
	for _, c := range h.Checks {
		fmt.Fprintf(out, "[%s] %s: %s\n", mark[c.Status], c.Label, c.Detail)
	}
	if h.Bad() {
		fmt.Fprintln(out, "\nSomething needs fixing (BAD above).")
		return 1
	}
	fmt.Fprintln(out, "\nAll checks passed.")
	return 0
}
