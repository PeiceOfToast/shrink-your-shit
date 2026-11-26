// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// On Windows the watcher writes a pending record before it stops the app: what it is replacing and the
// installers it may still need. It is cleared once a program is serving, or once the watcher's failed result
// is saved. The installers are removed only after the record is gone, so a record never names a deleted file.
// A watcher killed in between (logoff, reboot) leaves the record: the next start finishes the bookkeeping.

const pendingFile = "pending.json"

type pendingUpdate struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	NewSetup string    `json:"newSetup"`
	OldSetup string    `json:"oldSetup"`
	At       time.Time `json:"at"`
}

func pendingPath(dataDir string) string { return filepath.Join(dataDir, updateDir, pendingFile) }

func writePending(dataDir string, p pendingUpdate) error {
	if err := os.MkdirAll(filepath.Join(dataDir, updateDir), 0o755); err != nil {
		return err
	}
	return writeJSON(pendingPath(dataDir), p)
}

// readPending is the record in dataDir; nil when there is none (or it can't be read).
func readPending(dataDir string) *pendingUpdate {
	var p pendingUpdate
	if readJSON(pendingPath(dataDir), &p) != nil {
		return nil
	}
	return &p
}

func clearPending(dataDir string) error {
	err := os.Remove(pendingPath(dataDir))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// RecoverPending finishes an update whose watcher was killed, at the start of a program running as running.
// It does nothing when there is no record. Otherwise:
//   - running is the new version: the update is done; only the record and installers are left to clear.
//   - the app's marker for this update is still there: the watcher had put the old version back, and Settle
//     (which runs next) writes the result. Nothing is written here.
//   - anything else: the update was cut off, and the result says so. The record is kept when that result
//     cannot be saved, so the next start tries again.
//
// The record is cleared before the installers are removed, so it never names a file that is gone.
func RecoverPending(dataDir, running string, now time.Time) error {
	p := readPending(dataDir)
	if p == nil {
		return nil
	}
	m := ReadMarker(dataDir)
	switch {
	case running == p.To:
		log.Printf("update to %s finished; clearing the record its watcher did not get to clear", p.To)
	case m != nil && m.To == p.To:
		log.Printf("update to %s did not finish; still on %s", p.To, running)
	default:
		msg := fmt.Sprintf("Update to %s was cut off before it finished; still on %s", p.To, running)
		r := Result{OK: false, Kind: "update", From: p.From, To: p.To, Message: msg, RolledBack: running == p.From, At: now}
		if err := WriteResult(dataDir, r); err != nil {
			return err
		}
		log.Print(msg)
	}
	if err := clearPending(dataDir); err != nil {
		return err
	}
	os.Remove(p.NewSetup)
	os.Remove(p.OldSetup)
	return nil
}
