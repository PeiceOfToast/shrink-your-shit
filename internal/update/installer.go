// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// fetchLimit is how long the downloads of an update may take together.
const fetchLimit = 15 * time.Minute

// WindowsInstaller is the update helper on Windows (the Linux one is Helper). A request to install downloads
// the new installer and the running version's (the roll back copy), each checked against checksums.txt, copies
// this program next to them and starts it as the watcher. The app's database backup and marker are already
// written by then (the updater does that before it asks).
type WindowsInstaller struct {
	DataDir string
	Current string       // the running version
	Base    string       // ReleaseBase when empty
	Client  *http.Client // a 15-minute limit when nil
	Health  string       // the app's health address; from SYP_ADDR when empty

	Self  func() (string, error)                // this program's path
	Start func(exe string, args []string) error // starts the watcher detached
	PID   int                                   // the app's process; this one when 0
}

// NewWindowsInstaller is the installer for the program running in dataDir as version current.
func NewWindowsInstaller(dataDir, current string) *WindowsInstaller {
	addr := os.Getenv("SYP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8484"
	}
	return &WindowsInstaller{DataDir: dataDir, Current: current, Health: healthURL(addr),
		Self: os.Executable, Start: startDetached}
}

// Call answers the update helper's requests. Only install does anything: a roll back on Windows happens by the
// watcher alone (no previous version is kept for the button).
func (w *WindowsInstaller) Call(req HelperRequest) (HelperAnswer, error) {
	switch req.Cmd {
	case "status":
		return HelperAnswer{OK: true, State: "idle"}, nil
	case "install":
		if err := w.install(req.Version); err != nil {
			return HelperAnswer{}, err
		}
		return HelperAnswer{OK: true, State: "installing"}, nil
	case "rollback":
		msg := "on Windows the previous version is put back only when an update fails"
		return HelperAnswer{Code: "no_previous", Message: msg}, &HelperError{Code: "no_previous", Message: msg}
	}
	return HelperAnswer{Code: "bad_request", Message: "unknown request"}, &HelperError{Code: "bad_request", Message: "unknown request"}
}

func (w *WindowsInstaller) install(version string) error {
	if !ValidVersion(version) || !ValidVersion(w.Current) {
		return &HelperError{Code: "bad_request", Message: "the versions must be plain x.y.z numbers"}
	}
	if !Newer(version, w.Current) {
		return &HelperError{Code: "not_newer", Message: "the update is not newer than the running version"}
	}
	dir := filepath.Join(w.DataDir, updateDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	base := w.Base
	if base == "" {
		base = ReleaseBase
	}
	client := w.Client
	if client == nil {
		client = &http.Client{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), fetchLimit)
	defer cancel()
	newSetup := filepath.Join(dir, "setup-"+version+".exe")
	oldSetup := filepath.Join(dir, "setup-"+w.Current+".exe")
	if err := FetchSetup(ctx, client, base, version, newSetup); err != nil {
		return fmt.Errorf("download the update: %w", err)
	}
	if err := FetchSetup(ctx, client, base, w.Current, oldSetup); err != nil {
		os.Remove(newSetup)
		return fmt.Errorf("download the running version's installer, to put it back if the update fails: %w", err)
	}
	self, err := w.Self()
	if err != nil {
		os.Remove(newSetup)
		os.Remove(oldSetup)
		return err
	}
	watch := filepath.Join(dir, "syp-update-watch.exe")
	if err := copyFile(self, watch); err != nil {
		os.Remove(newSetup)
		os.Remove(oldSetup)
		return err
	}
	pid := w.PID
	if pid == 0 {
		pid = os.Getpid()
	}
	args := []string{"update-watch", "-pid", strconv.Itoa(pid), "-from", w.Current, "-to", version,
		"-new", newSetup, "-old", oldSetup, "-data", w.DataDir, "-health", w.Health}
	if err := w.Start(watch, args); err != nil {
		os.Remove(newSetup)
		os.Remove(oldSetup)
		return fmt.Errorf("start the update watcher: %w", err)
	}
	return nil
}

// healthURL is the health address on loopback for a listen address (a wildcard host is asked on 127.0.0.1).
func healthURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8484/healthz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

// copyFile copies src to dst through a temporary name, so dst is either complete or absent.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".syp-watch-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, cerr := io.Copy(tmp, in)
	if err := errors.Join(cerr, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}
