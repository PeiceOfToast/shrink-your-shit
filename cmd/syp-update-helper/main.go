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

	mu      sync.Mutex
	busy    bool
	gen     int    // changes when an operation starts or ends: a version read across one is stale
	state   string // idle | downloading | installing | failed
	err     string
	prevVer string        // the version of <bin>.previous, read after each operation (not on every status call)
	done    chan struct{} // closed when the current operation ends (tests wait on it)
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
	if err := repairLeftovers(h.bin); err != nil { // a crash during an install or roll back left files in the wrong names
		log.Printf("update leftovers: %v", err)
	}
	go h.readPrevious() // a helper restarted after an update still knows what its previous version is

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
	// -version (up to runLimit) must not hold up status calls. h.gen, checked under the lock, refuses an
	// install whose read overlapped another install or roll back (it could have read the replaced program).
	var cur string
	var curErr error
	gen := -1
	if req.Cmd == "install" && update.ValidVersion(req.Version) {
		h.mu.Lock()
		gen = h.gen
		h.mu.Unlock()
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
		if h.gen != gen { // another install or roll back started or ended meanwhile: cur may be stale
			return refuse("busy", "the installed program changed while its version was read; try again")
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
	h.gen++
	log.Printf("%s %s", req.Cmd, req.Version)
	go func() {
		err := work()
		h.readPrevious() // busy is still set, so nothing else runs meanwhile
		h.mu.Lock()
		defer h.mu.Unlock()
		h.busy, h.state, h.err = false, "idle", ""
		h.gen++
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
	a := update.HelperAnswer{OK: true, State: st, Error: h.err, Previous: exists(h.bin + ".previous")}
	if a.Previous {
		a.PreviousVersion = h.prevVer
	}
	return a
}

// readPrevious asks <bin>.previous its version. It runs after each operation and once at start, so a
// status call only reads the kept value.
func (h *helper) readPrevious() {
	v := ""
	if exists(h.bin + ".previous") {
		if s, err := h.version(h.bin + ".previous"); err == nil {
			v = s
		}
	}
	h.mu.Lock()
	h.prevVer = v
	h.mu.Unlock()
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
	// The copy before the previous one (the version a roll back should reach after an automatic roll back)
	// moves aside until the new program answers. Without this, linking the current program as prev
	// would lose it, and an automatic roll back would leave nothing to roll back to.
	older := prev + ".older"
	hadPrev := exists(prev)
	restoreOlder := func() error {
		if !hadPrev {
			return nil
		}
		return os.Rename(older, prev)
	}
	if hadPrev {
		if err := os.Rename(prev, older); err != nil {
			os.Remove(next)
			return err
		}
	}
	// prev becomes a second name for the current program, so <bin> never goes missing: the only move that
	// touches <bin> is next -> bin, which replaces it in one step or not at all.
	if err := link(h.bin, prev); err != nil {
		// no hard links on this disk: keep the previous program as a copy instead
		if cerr := copyFile(h.bin, prev); cerr != nil {
			os.Remove(next)
			return errors.Join(err, cerr, restoreOlder())
		}
	}
	if err := os.Rename(next, h.bin); err != nil { // <bin> is still the current program; nothing changed
		os.Remove(next)
		return errors.Join(err, os.Remove(prev), restoreOlder())
	}
	if h.restartHealthy() {
		if hadPrev {
			os.Remove(older)
		}
		return nil
	}
	if err := os.Rename(prev, h.bin); err != nil { // rule 3: the new program goes, the previous comes back
		return fmt.Errorf("v%s did not answer, and the previous version could not be put back: %v", version, err)
	}
	msg := fmt.Sprintf("v%s did not answer within %s; rolled back to the previous version", version, h.wait)
	if err := restoreOlder(); err != nil { // and the copy before it is kept as the previous one again; the restart still happens
		msg += fmt.Sprintf(", but the version before it could not be kept as the previous one: %v", err)
	}
	return h.afterUndo(msg)
}

// rollback swaps the program and the previous one, and swaps them back if the previous does not answer.
func (h *helper) rollback() error {
	if err := exchange(h.bin, h.bin+".previous"); err != nil {
		return err
	}
	if h.restartHealthy() {
		return nil
	}
	if err := exchange(h.bin, h.bin+".previous"); err != nil {
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

// exchange swaps a and b in one step, so neither name is ever missing. Tests replace it.
var exchange = func(a, b string) error {
	err := unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b, unix.RENAME_EXCHANGE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.ENOTSUP) {
		return fmt.Errorf("this disk can't swap the programs in one step, so nothing was changed: %v", err)
	}
	return err
}

// link makes prev a second name for the program. Tests replace it to see a disk without hard links.
var link = os.Link

// copyFile writes a copy of src to dst the way install needs it: under a temp name, synced, then renamed.
func copyFile(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + copyingSuffix
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(fi.Mode().Perm())
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

const copyingSuffix = ".copying"

// repairLeftovers fixes what a crash during an install left behind (the install's order: previous -> older,
// link the program as previous, rename the new program in). Two cases:
//   - previous is the same file as the program (the crash came between the link and the rename): previous
//     is removed, which leaves the program as it was.
//   - previous is missing but older is there: older is the version before the last install; it is put back
//     as previous so a roll back still reaches it.
func repairLeftovers(bin string) error {
	prev := bin + ".previous"
	os.Remove(prev + copyingSuffix) // a copy that never got renamed
	if sameFile(prev, bin) {
		if err := os.Remove(prev); err != nil {
			return err
		}
	}
	if !exists(prev) && exists(prev+".older") {
		return os.Rename(prev+".older", prev)
	}
	return nil
}

func sameFile(a, b string) bool {
	fa, err1 := os.Stat(a)
	fb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(fa, fb)
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
