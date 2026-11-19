// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The Windows update watcher is a copy of this program that the app starts (WindowsInstaller). The app's
// installer would stop the app with its children, which would take the watcher too, so the watcher first
// stops the app itself, then runs the new installer silently. If the new program does not answer its health
// address within watchWait, the watcher runs the previous installer; that program settles the marker as a
// roll back, the same as the Linux helper's swap does.

const (
	watchWait  = 2 * time.Minute  // how long a started program has to answer its health address
	watchPoll  = 2 * time.Second  // how often it is asked meanwhile
	setupLimit = 20 * time.Minute // an installer run that takes longer is given up
)

// Watcher decides how a Windows update ends. The fields after Health are the outside world; tests replace them.
type Watcher struct {
	PID                int    // the app's process
	From, To           string // the running version, and the version asked for
	NewSetup, OldSetup string // the verified installers for To and From
	DataDir            string
	Health             string // the app's health address, on loopback
	Wait, Poll         time.Duration

	StopApp  func(pid int) error
	RunSetup func(setup string) error
	Healthy  func(url string) bool
	Logf     func(format string, args ...any)
}

// Watch runs the update. It returns nil when a program is serving and will settle the marker itself (the new
// one, or the previous one after a roll back). It returns an error only after writing a failed result.
func (w *Watcher) Watch() error {
	defer os.Remove(w.NewSetup)
	defer os.Remove(w.OldSetup)
	if err := w.StopApp(w.PID); err != nil {
		return w.fail(fmt.Sprintf("Update to %s not started: the running program could not be stopped (%v)", w.To, err))
	}
	err := w.RunSetup(w.NewSetup)
	if err == nil && w.healthyWithin() {
		return nil
	}
	w.logf("update to %s did not come up (%v); putting %s back", w.To, err, w.From)
	if err := w.RunSetup(w.OldSetup); err != nil {
		return w.fail(fmt.Sprintf("Update to %s failed, and %s could not be put back (%v)", w.To, w.From, err))
	}
	if w.healthyWithin() {
		return nil // the previous program settles the marker: "Update to <to> failed; rolled back to <from>"
	}
	return w.fail(fmt.Sprintf("Update to %s failed; rolled back to %s, but it did not start either", w.To, w.From))
}

// healthyWithin asks the health address until it answers "ok" or the wait is over.
func (w *Watcher) healthyWithin() bool {
	end := time.Now().Add(w.wait())
	for {
		if w.Healthy(w.Health) {
			return true
		}
		if !time.Now().Before(end) {
			return false
		}
		time.Sleep(w.poll())
	}
}

func (w *Watcher) wait() time.Duration {
	if w.Wait > 0 {
		return w.Wait
	}
	return watchWait
}

func (w *Watcher) poll() time.Duration {
	if w.Poll > 0 {
		return w.Poll
	}
	return watchPoll
}

// fail records the failed update for the app to show, and keeps the marker so the next start still settles it.
func (w *Watcher) fail(msg string) error {
	w.logf("%s", msg)
	if err := WriteResult(w.DataDir, Result{OK: false, Kind: "update", From: w.From, To: w.To, Message: msg, At: time.Now().UTC()}); err != nil {
		w.logf("the result above could not be saved, so the app shows a generic message at its next start: %v", err)
		return err
	}
	return fmt.Errorf("%s", msg)
}

func (w *Watcher) logf(format string, args ...any) {
	if w.Logf != nil {
		w.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// RunWatcher is `syp update-watch`: the arguments the app's installer gave it. Its log goes to update/watch.log.
// It returns the exit status.
func RunWatcher(args []string) int {
	fs := flag.NewFlagSet("update-watch", flag.ContinueOnError)
	w := Watcher{StopApp: stopProcess, RunSetup: runSetup, Healthy: healthy}
	fs.IntVar(&w.PID, "pid", 0, "the app's process")
	fs.StringVar(&w.From, "from", "", "the running version")
	fs.StringVar(&w.To, "to", "", "the version asked for")
	fs.StringVar(&w.NewSetup, "new", "", "the installer for the new version")
	fs.StringVar(&w.OldSetup, "old", "", "the installer for the running version")
	fs.StringVar(&w.DataDir, "data", "", "the data folder")
	fs.StringVar(&w.Health, "health", "", "the health address")
	if err := fs.Parse(args); err != nil || w.PID <= 0 || w.From == "" || w.To == "" || w.NewSetup == "" || w.OldSetup == "" || w.DataDir == "" || w.Health == "" {
		fmt.Fprintln(os.Stderr, "update-watch: the arguments are missing or wrong")
		return 2
	}
	if f, err := os.OpenFile(filepath.Join(w.DataDir, updateDir, "watch.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		defer f.Close()
		log.SetOutput(io.MultiWriter(f))
	}
	if err := w.Watch(); err != nil {
		return 1
	}
	return 0
}

// runSetup runs an installer silently and waits for it. The installer's own log is kept next to it.
func runSetup(setup string) error {
	ctx, cancel := context.WithTimeout(context.Background(), setupLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, setup, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/LOG="+setup+".log")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v", filepath.Base(setup), err)
	}
	return nil
}

// healthy reports whether url answers 200 with "ok".
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
