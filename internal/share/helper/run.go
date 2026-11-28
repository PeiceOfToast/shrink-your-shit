// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package helper

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// Options are the helper's fixed places. syp-mount.service passes none of them: they exist so the
// same binary can be tested, and run as root by hand on a test computer, without touching the real
// folders.
type Options struct {
	Socket    string
	Shares    string
	Record    string
	RunDir    string
	MountBin  string
	UmountBin string
	MountInfo string
}

// DefaultOptions are the installed places from the plan.
func DefaultOptions() Options {
	return Options{
		Socket:    "/run/syp/mount.sock",
		Shares:    "/var/lib/syp/shares",
		Record:    "/var/lib/syp-helper",
		RunDir:    "/run/syp",
		MountBin:  "/bin/mount",
		UmountBin: "/bin/umount",
		MountInfo: "/proc/self/mountinfo",
	}
}

// Main is `syp helper`: parse the flags and serve until SIGTERM or SIGINT. It returns the process's
// exit code.
func Main(args []string) int {
	opts := DefaultOptions()
	fs := flag.NewFlagSet("syp helper", flag.ContinueOnError)
	fs.StringVar(&opts.Socket, "socket", opts.Socket, "the unix socket to serve on (socket activation with LISTEN_FDS)")
	fs.StringVar(&opts.Shares, "shares", opts.Shares, "the folder mount points are made under")
	fs.StringVar(&opts.Record, "record", opts.Record, "the root-only folder holding mounts.json")
	fs.StringVar(&opts.RunDir, "run-dir", opts.RunDir, "the folder for the socket and temporary credentials files")
	fs.StringVar(&opts.MountBin, "mount", opts.MountBin, "the mount program")
	fs.StringVar(&opts.UmountBin, "umount", opts.UmountBin, "the umount program")
	fs.StringVar(&opts.MountInfo, "mountinfo", opts.MountInfo, "the mount table to read")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := Run(ctx, opts); err != nil {
		fmt.Fprintln(os.Stderr, "syp helper:", err)
		return 1
	}
	return 0
}

// Run starts the helper. It insists on root, finds the syp user (whose uid may talk and whose files
// a share's mount is made for), checks the fixed places, and serves the socket until ctx is done.
func Run(ctx context.Context, opts Options) error {
	if os.Geteuid() != 0 {
		return errors.New("the helper must run as root")
	}
	u, err := user.Lookup("syp")
	if err != nil {
		return fmt.Errorf("the syp user is missing: %w", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("the syp user's uid: %w", err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("the syp user's gid: %w", err)
	}
	root, err := makeRoot(opts.Shares)
	if err != nil {
		return err
	}
	if err := os.Chown(root, 0, gid); err != nil {
		return fmt.Errorf("the shares folder's owner: %w", err)
	}
	recordDir, err := makeDir(opts.Record, 0o700)
	if err != nil {
		return fmt.Errorf("the mount record's folder: %w", err)
	}
	runDir, err := makeDir(opts.RunDir, 0o750)
	if err != nil {
		return fmt.Errorf("the run folder: %w", err)
	}
	if err := os.Chown(runDir, 0, gid); err != nil {
		return fmt.Errorf("the run folder's owner: %w", err)
	}
	for _, bin := range []string{opts.MountBin, opts.UmountBin} {
		if fi, err := os.Stat(bin); err != nil || fi.IsDir() {
			return fmt.Errorf("%s is missing", bin)
		}
	}
	if _, err := os.Stat(opts.MountInfo); err != nil {
		return fmt.Errorf("the mount table %s: %w", opts.MountInfo, err)
	}
	srv := &Server{
		Root: root, RecordDir: recordDir, RunDir: runDir,
		MountBin: opts.MountBin, UmountBin: opts.UmountBin, MountInfo: opts.MountInfo,
		UID: uid, GID: gid,
		PeerOK: func(peer uint32) bool { return peer == 0 || int(peer) == uid },
	}
	ln, err := listen(opts.Socket, gid)
	if err != nil {
		return err
	}
	defer ln.Close()
	return srv.Serve(ctx, ln)
}

// listen takes the socket systemd made when it passed LISTEN_FDS, and otherwise makes one itself.
func listen(path string, gid int) (net.Listener, error) {
	if os.Getenv("LISTEN_FDS") == "1" && os.Getenv("LISTEN_PID") == strconv.Itoa(os.Getpid()) {
		f := os.NewFile(3, "syp-mount.socket")
		if f == nil {
			return nil, errors.New("socket activation was promised but no socket arrived")
		}
		defer f.Close()
		return net.FileListener(f)
	}
	if path == "" {
		return nil, errors.New("the socket's path is empty")
	}
	// A helper that was killed leaves its socket file behind; the next start replaces it.
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the old socket: %w", err)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := os.Chown(path, 0, gid); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// makeRoot opens the shares folder: it must exist as a real folder, and it is stored as the path
// its symlinks resolve to, so every derived point is under the one real folder.
func makeRoot(path string) (string, error) {
	real, err := makeDir(path, 0o750)
	if err != nil {
		return "", fmt.Errorf("the shares folder: %w", err)
	}
	return real, nil
}

// makeDir ensures a real (not symlinked) folder exists and answers its resolved path.
func makeDir(path string, mode os.FileMode) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s is not an absolute path", path)
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symbolic link", path)
	}
	if err := os.MkdirAll(path, mode); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	fi, err := os.Lstat(real)
	if err != nil {
		return "", err
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("%s is not a folder", path)
	}
	return real, nil
}
