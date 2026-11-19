//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// syp-update-helper installs a newer release of Shrink Your Shit when the person clicks Update on the
// web page, on a Linux install made by install.sh (the program is root's, the service runs as the
// service user). It is opt-in (install.sh --update-helper) and never setuid.
//
// The service unit runs it as root. It listens on a Unix socket only root and the service user's group
// can open, and takes one operation at a time:
//
//	{"cmd":"status"}   {"cmd":"install","version":"1.2.3"}   {"cmd":"rollback"}
//
// Security rules:
//  1. The caller must be root or the service user (SO_PEERCRED); if its identity can't be read, it is refused.
//  2. A request names only a version, never a path or URL. The version must be a plain x.y.z newer than
//     the installed program's. The helper downloads that release from -release-base (set by root in the
//     unit, never by a request or the environment), checks it against the release's checksums.txt
//     (SHA-256) and takes only the program out of the archive.
//  3. The previous program is kept as <bin>.previous. If the new one does not answer -health with "ok"
//     within two minutes of the restart, the previous one is put back and the service restarted again.
//  4. Programs run with an argument list (no shell) and a time limit. The installed program is asked
//     its version as the service user, not as root.
//  5. The helper never touches the service's data folder: the app backs up and restores its database itself.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
)

const (
	maxRequest = 4 << 10
	connLimit  = 15 * time.Second
	runLimit   = 90 * time.Second // systemctl restart, <bin> -version
	fetchLimit = 15 * time.Minute // the download
)

// helper is one install's update helper. The funcs are the outside world; tests replace them.
type helper struct {
	bin, base  string
	client     *http.Client
	restart    func() error
	healthy    func() bool
	version    func(bin string) (string, error)
	wait, poll time.Duration // how long a restarted program has to answer, and how often it is asked

	mu    sync.Mutex
	busy  bool
	state string // idle | downloading | installing | failed
	err   string
	done  chan struct{} // closed when the current operation ends (tests wait on it)
}

func main() {
	userName := flag.String("user", "", "the service user that may call the helper (required)")
	bin := flag.String("bin", "/usr/local/bin/shrink-your-shit", "the installed program")
	unit := flag.String("unit", "shrink-your-shit", "the systemd service to restart")
	health := flag.String("health", "http://127.0.0.1:8484/healthz", "the service's health address (loopback)")
	base := flag.String("release-base", update.ReleaseBase, "where releases are downloaded from: <base>/v<version>/<file>")
	sock := flag.String("socket", "/run/shrink-your-shit/update.sock", "the socket the app calls")
	flag.Parse()
	if *userName == "" {
		log.Fatal("-user is required")
	}
	u, err := user.Lookup(*userName)
	if err != nil {
		log.Fatalf("service user %q: %v", *userName, err)
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil || uid == 0 {
		log.Fatalf("service user %q has no usable uid/gid", *userName)
	}
	h := &helper{bin: *bin, base: *base, client: &http.Client{Timeout: fetchLimit}, wait: 2 * time.Minute, poll: 2 * time.Second,
		restart: func() error { _, err := run(nil, "systemctl", "restart", *unit); return err },
		healthy: func() bool { return healthy(*health) },
		version: func(b string) (string, error) {
			out, err := run(&syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}, b, "-version")
			if f := strings.Fields(out); err == nil && len(f) == 2 {
				return f[1], nil
			}
			return "", fmt.Errorf("could not read the installed version: %v %s", err, out)
		}}

	dir := filepath.Dir(*sock)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Fatalf("socket folder: %v", err)
	}
	if err := errors.Join(os.Chown(dir, 0, gid), os.Chmod(dir, 0o750)); err != nil {
		log.Fatalf("socket folder: %v", err)
	}
	_ = os.Remove(*sock)
	ln, err := net.Listen("unix", *sock)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if err := errors.Join(os.Chown(*sock, 0, gid), os.Chmod(*sock, 0o660)); err != nil {
		log.Fatalf("socket: %v", err)
	}
	log.Printf("listening on %s for %s; program %s, service %s", *sock, *userName, *bin, *unit)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stop; ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			log.Printf("accept: %v", err)
			continue
		}
		go serve(conn.(*net.UnixConn), uid, h) // a slow request never holds up a status call
	}
	_ = os.Remove(*sock)
}

func serve(c *net.UnixConn, svcUID int, h *helper) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(connLimit))
	reply := func(a update.HelperAnswer) { _ = json.NewEncoder(c).Encode(a) }
	if uid, ok := callerUID(c); !ok || (uid != 0 && uid != svcUID) { // rule 1: fail closed
		reply(update.HelperAnswer{Code: "not_allowed", Message: "this user may not use the update helper"})
		return
	}
	var req update.HelperRequest
	if err := json.NewDecoder(io.LimitReader(c, maxRequest)).Decode(&req); err != nil {
		reply(update.HelperAnswer{Code: "bad_request", Message: "the request could not be read"})
		return
	}
	reply(h.handle(req))
}

// callerUID is peerUID; tests replace it to be a caller of their choosing.
var callerUID = peerUID

func peerUID(c *net.UnixConn) (int, bool) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil {
		return 0, false
	}
	return int(cred.Uid), true
}

// handle answers at once; an install or roll back goes on in the background (the app is restarted under it).
func (h *helper) handle(req update.HelperRequest) update.HelperAnswer {
	// The installed program is asked its version before the lock is taken: a program that hangs on
	// -version (up to runLimit) must not hold up status calls.
	var cur string
	var curErr error
	if req.Cmd == "install" && update.ValidVersion(req.Version) {
		cur, curErr = h.version(h.bin)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	refuse := func(code, msg string) update.HelperAnswer {
		a := h.statusLocked()
		a.OK, a.Code, a.Message = false, code, msg
		return a
	}
	switch req.Cmd {
	case "status":
		return h.statusLocked()
	case "install", "rollback":
	default:
		return refuse("bad_request", "unknown command")
	}
	if h.busy {
		return refuse("busy", "an update is already in progress")
	}
	var work func() error
	if req.Cmd == "install" {
		if !update.ValidVersion(req.Version) { // rule 2
			return refuse("bad_request", "the version must be a plain x.y.z")
		}
		if curErr != nil {
			return refuse("bad_request", curErr.Error())
		}
		if !update.Newer(req.Version, cur) {
			return refuse("not_newer", fmt.Sprintf("v%s is not newer than the installed v%s", req.Version, cur))
		}
		v := req.Version
		h.state = "downloading"
		work = func() error { return h.install(v) }
	} else {
		if !exists(h.bin + ".previous") {
			return refuse("no_previous", "there is no previous version to roll back to")
		}
		h.state = "installing"
		work = h.rollback
	}
	h.busy, h.err, h.done = true, "", make(chan struct{})
	log.Printf("%s %s", req.Cmd, req.Version)
	go func() {
		err := work()
		h.mu.Lock()
		defer h.mu.Unlock()
		h.busy, h.state, h.err = false, "idle", ""
		if err != nil {
			h.state, h.err = "failed", err.Error()
			log.Printf("%s failed: %v", req.Cmd, err)
		} else {
			log.Printf("%s done", req.Cmd)
		}
		close(h.done)
	}()
	return h.statusLocked()
}

func (h *helper) statusLocked() update.HelperAnswer {
	st := h.state
	if st == "" {
		st = "idle"
	}
	return update.HelperAnswer{OK: true, State: st, Error: h.err, Previous: exists(h.bin + ".previous")}
}

func (h *helper) setState(s string) { h.mu.Lock(); h.state = s; h.mu.Unlock() }

func (h *helper) install(version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchLimit)
	defer cancel()
	next := h.bin + ".new"
	if err := update.Fetch(ctx, h.client, h.base, version, "linux", runtime.GOARCH, next); err != nil {
		return err
	}
	h.setState("installing")
	prev := h.bin + ".previous"
	if err := os.Rename(h.bin, prev); err != nil { // the running program keeps its file open; nothing stops
		os.Remove(next)
		return err
	}
	if err := os.Rename(next, h.bin); err != nil {
		return errors.Join(err, os.Rename(prev, h.bin))
	}
	if h.restartHealthy() {
		return nil
	}
	if err := os.Rename(prev, h.bin); err != nil { // rule 3: the new program goes, the previous comes back
		return fmt.Errorf("v%s did not answer, and the previous version could not be put back: %v", version, err)
	}
	return h.afterUndo(fmt.Sprintf("v%s did not answer within %s; rolled back to the previous version", version, h.wait))
}

// rollback swaps the program and the previous one, and swaps them back if the previous does not answer.
func (h *helper) rollback() error {
	if err := swap(h.bin, h.bin+".previous"); err != nil {
		return err
	}
	if h.restartHealthy() {
		return nil
	}
	if err := swap(h.bin, h.bin+".previous"); err != nil {
		return fmt.Errorf("the previous version did not answer, and could not be swapped back: %v", err)
	}
	return h.afterUndo(fmt.Sprintf("the previous version did not answer within %s; kept the current one", h.wait))
}

func (h *helper) afterUndo(msg string) error {
	if !h.restartHealthy() {
		msg += ", but it does not answer either (see: journalctl -u the service)"
	}
	return errors.New(msg)
}

// restartHealthy restarts the service and waits up to h.wait for its health address to answer "ok".
func (h *helper) restartHealthy() bool {
	if err := h.restart(); err != nil {
		log.Printf("restart: %v", err)
	}
	for end := time.Now().Add(h.wait); ; {
		if h.healthy() {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(h.poll)
	}
}

func swap(a, b string) error {
	tmp := a + ".swap"
	if err := os.Rename(a, tmp); err != nil {
		return err
	}
	if err := os.Rename(b, a); err != nil {
		return errors.Join(err, os.Rename(tmp, a))
	}
	return os.Rename(tmp, b)
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func healthy(url string) bool {
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return resp.StatusCode == http.StatusOK && strings.TrimSpace(string(b)) == "ok"
}

// run starts a program with an argument list (never a shell), a time limit and, if cred is set, as that user.
func run(cred *syscall.Credential, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if cred != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: cred}
	}
	out, err := cmd.CombinedOutput()
	msg := strings.TrimSpace(string(out))
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if ctx.Err() != nil {
		return "timed out", ctx.Err()
	}
	return msg, err
}
