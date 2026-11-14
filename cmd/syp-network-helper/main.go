//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// syp-network-helper mounts and unmounts network shares (SMB/CIFS and NFS) for Shrink Your Shit on
// Linux. It is the only part of the app that runs as root, and only when the person chose network
// shares at install (user review 11-13 item D, PR D2). It is never setuid.
//
// The service unit runs it as root with `-user <service user>`. It listens on a Unix socket that
// only root and the service user's group can open, and serves one request at a time:
//
//	{"cmd":"mount","kind":"cifs","address":"//nas/media","mountPoint":"/var/lib/syp/shares/media",
//	 "options":{"vers":"3.1.1"},"username":"me","password":"secret"}
//	{"cmd":"unmount","mountPoint":"..."}   {"cmd":"status","mountPoint":"..."}
//
// Security rules (validate.go holds the checks; this file only wires them):
//  1. The caller must be root or the service user (SO_PEERCRED). If its identity can't be read,
//     it is refused. If the service user can't be found at start, the helper doesn't start.
//  2. Every request's mount point must be directly inside the shares folder (validateMountPoint),
//     for unmount and status too, so nothing else on the system can be unmounted.
//  3. Only cifs and nfs, with an address of the right shape (validateAddress).
//  4. Only allowlisted options with checked values (validateOptions). nosuid, nodev and noexec
//     are always forced, and for cifs the files belong to the service user (mountOptions).
//  5. The shares folder and each mount point must be plain root-owned folders, never symlinks,
//     for mount, unmount and status (prepareMountPoint, checkExistingMountPoint).
//  6. The password goes in a 0600 file in a root-only folder, only for the length of the mount
//     call, and is deleted right after. It is never logged.
//  7. mount(8) and umount(8) run with an argument list (no shell) and a time limit.
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
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	socketPath = "/run/shrink-your-shit/helper.sock"
	credDir    = "/run/shrink-your-shit/credentials"
	maxRequest = 64 << 10         // a request is a few hundred bytes; refuse anything bigger
	connLimit  = 15 * time.Second // to read the request and write the answer
	mountLimit = 60 * time.Second // a mount or unmount that takes longer is stopped
)

type request struct {
	Cmd        string            `json:"cmd"`
	Kind       string            `json:"kind"`
	Address    string            `json:"address"`
	MountPoint string            `json:"mountPoint"`
	Options    map[string]string `json:"options"`
	Username   string            `json:"username"`
	Password   string            `json:"password"`
}

type answerError struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type answer struct {
	OK    bool         `json:"ok"`
	Error *answerError `json:"error,omitempty"`
}

func fail(code, msg string) answer { return answer{Error: &answerError{Code: code, Message: msg}} }

// service is the one user (besides root) allowed to call the helper.
type service struct{ uid, gid int }

func main() {
	userName := flag.String("user", "", "the service user that may call the helper (required)")
	flag.BoolVar(&nfsNoLock, "nfs-nolock", false, "mount NFS with nolock (for containers, which have no rpc.statd)")
	flag.Parse()
	if *userName == "" {
		log.Fatal("-user is required")
	}
	u, err := user.Lookup(*userName)
	if err != nil {
		log.Fatalf("service user %q: %v", *userName, err) // rule 1: never run without knowing who may call
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil || uid == 0 {
		log.Fatalf("service user %q has no usable uid/gid", *userName)
	}
	svc := service{uid, gid}

	// The socket's folder is root:<service group> 0750 and the socket 0660, so only root and the
	// service user's group can connect at all; SO_PEERCRED then checks the exact user.
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		log.Fatalf("socket folder: %v", err)
	}
	if err := os.Chown(dir, 0, gid); err != nil {
		log.Fatalf("socket folder: %v", err)
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		log.Fatalf("socket folder: %v", err)
	}
	_ = os.Remove(socketPath)
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	if err := os.Chown(socketPath, 0, gid); err != nil {
		log.Fatalf("socket: %v", err)
	}
	if err := os.Chmod(socketPath, 0o660); err != nil {
		log.Fatalf("socket: %v", err)
	}
	log.Printf("listening on %s for %s", socketPath, *userName)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-stop; ln.Close() }()

	for { // one request at a time: mounts are rare, and nothing can race on the same folder
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			log.Printf("accept: %v", err)
			continue
		}
		serve(conn.(*net.UnixConn), svc)
	}
	_ = os.Remove(socketPath)
}

// peerUID reads the connecting process's uid from the kernel. ok is false if it can't be read.
func peerUID(c *net.UnixConn) (uid int, ok bool) {
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

func serve(c *net.UnixConn, svc service) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(connLimit))
	reply := func(a answer) { _ = json.NewEncoder(c).Encode(a) }

	uid, ok := peerUID(c) // rule 1: fail closed
	if !ok || (uid != 0 && uid != svc.uid) {
		reply(fail("not_allowed", "this user may not use the helper"))
		return
	}
	var req request
	if err := json.NewDecoder(io.LimitReader(c, maxRequest)).Decode(&req); err != nil {
		reply(fail("bad_request", "the request could not be read"))
		return
	}
	if err := validateMountPoint(req.MountPoint); err != nil { // rule 2, for every command
		reply(fail("bad_request", err.Error()))
		return
	}
	switch req.Cmd {
	case "mount":
		reply(mount(req, svc))
	case "unmount":
		reply(unmount(req.MountPoint))
	case "status":
		reply(status(req.MountPoint))
	default:
		reply(fail("bad_request", "unknown command"))
	}
}

func mount(req request, svc service) answer {
	if err := validateAddress(req.Kind, req.Address); err != nil { // rule 3
		return fail("bad_request", err.Error())
	}
	if err := validateOptions(req.Options); err != nil { // rule 4
		return fail("bad_request", err.Error())
	}
	if err := validateCredentials(req.Username, req.Password); err != nil {
		return fail("bad_request", err.Error())
	}
	if req.Kind == "nfs" && (req.Username != "" || req.Password != "") {
		return fail("bad_request", "NFS has no username or password")
	}
	if err := prepareMountPoint(req.MountPoint, 0); err != nil { // rule 5
		return fail("mount_failed", err.Error())
	}
	if isMounted(req.MountPoint) {
		return answer{OK: true} // already connected
	}
	credPath := ""
	if req.Kind == "cifs" && req.Username != "" { // rule 6
		p, err := writeCredentials(req.Username, req.Password)
		if err != nil {
			return fail("mount_failed", "could not prepare the sign-in: "+err.Error())
		}
		credPath = p
		defer os.Remove(credPath)
	}
	opts := mountOptions(req.Kind, req.Options, svc.uid, svc.gid, credPath)
	log.Printf("mount %s %s on %s", req.Kind, req.Address, req.MountPoint) // never the password
	// rule 7: "--" ends mount's options, so nothing after it can be read as one.
	if out, err := run("mount", "-t", req.Kind, "-o", opts, "--", req.Address, req.MountPoint); err != nil {
		return fail("mount_failed", "mount: "+out)
	}
	return answer{OK: true}
}

// writeCredentials writes the cifs credentials file (0600, in a 0700 root folder) and returns its path.
func writeCredentials(username, password string) (string, error) {
	if err := os.MkdirAll(credDir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(credDir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(credDir, "share-") // created 0600
	if err != nil {
		return "", err
	}
	_, werr := fmt.Fprintf(f, "username=%s\npassword=%s\n", username, password)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(f.Name())
		return "", errors.Join(werr, cerr)
	}
	return f.Name(), nil
}

func unmount(mp string) answer {
	if err := checkExistingMountPoint(mp, 0); err != nil { // rule 5, for unmount too
		return fail("unmount_failed", err.Error())
	}
	if isMounted(mp) {
		if out, err := run("umount", "--", mp); err != nil {
			return fail("unmount_failed", "umount: "+out)
		}
	}
	_ = os.Remove(mp) // only succeeds on an empty folder: never deletes anything on the share
	return answer{OK: true}
}

func status(mp string) answer {
	if err := checkExistingMountPoint(mp, 0); err != nil {
		return fail("bad_request", err.Error())
	}
	if !isMounted(mp) {
		return fail("not_mounted", "not mounted")
	}
	return answer{OK: true}
}

func isMounted(mp string) bool {
	_, err := run("mountpoint", "-q", "--", mp)
	return err == nil
}

// run starts a program with an argument list (never a shell) and a time limit, and returns its
// combined output, trimmed, for the error message.
func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), mountLimit)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	msg := string(out)
	if len(msg) > 500 {
		msg = msg[:500]
	}
	if ctx.Err() != nil {
		return "timed out", ctx.Err()
	}
	return msg, err
}
