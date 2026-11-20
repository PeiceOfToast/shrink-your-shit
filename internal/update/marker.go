// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// The app (as the service user) writes a Marker in its data folder before it asks the root helper to
// swap the program. The next start reads it: a program whose version is still Marker.From was rolled
// back (or the swap never happened); any other version is the one switched to. Root never touches the
// data folder: the database is put back by the app itself (RestoreDB), before the store is opened.

const markerFile, resultFile = "update.json", "update-result.json"

// updateDir (in the data folder) holds what a Windows update needs while it runs: the installers and the watcher.
const updateDir = "update"

// RolledBackDB is where RestoreDB keeps the database it replaced (the app's database is syp.db). One kept
// before is never replaced: the next one is syp.db.rolled-back-2, -3 and so on.
const RolledBackDB = "syp.db.rolled-back"

// Marker is an update or roll back in progress.
type Marker struct {
	Kind   string    `json:"kind"`   // "update" or "rollback"
	From   string    `json:"from"`   // the version running when it started
	To     string    `json:"to"`     // the version asked for ("" for a roll back to an unknown version)
	Backup string    `json:"backup"` // a database copy from before the update ("" if none)
	Resume bool      `json:"resume"` // workers were turned off for it: turn them on again
	At     time.Time `json:"at"`
	// DataRestored: the start after it put Backup back, undoing the changes made since the update
	DataRestored bool `json:"dataRestored,omitempty"`
	// KeptAs: the file name the replaced (newer) database was kept as ("" = RolledBackDB)
	KeptAs string `json:"keptAs,omitempty"`
}

// Result is how the last update or roll back ended, shown until the next one.
type Result struct {
	OK      bool   `json:"ok"`
	Kind    string `json:"kind"`
	From    string `json:"from"`
	To      string `json:"to"`
	Message string `json:"message"`
	// RolledBack: the previous version is running again (an update that failed and was rolled back, or a
	// roll back that worked). OK stays false for a failed update even then.
	RolledBack bool `json:"rolledBack"`
	// DataRestored: the database went back to the copy from before the update (the newer one is kept)
	DataRestored bool      `json:"dataRestored"`
	Backup       string    `json:"backup,omitempty"` // after a good update: the copy a manual roll back restores
	At           time.Time `json:"at"`
}

// WriteMarker saves m in dataDir.
func WriteMarker(dataDir string, m Marker) error {
	return writeJSON(filepath.Join(dataDir, markerFile), m)
}

// WriteResult saves r as the result the app shows.
func WriteResult(dataDir string, r Result) error {
	return writeJSON(filepath.Join(dataDir, resultFile), r)
}

// RemoveMarker deletes the marker (an update that failed before the program was swapped).
func RemoveMarker(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, markerFile))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// ReadMarker is the marker in dataDir; nil when there is none (or it can't be read).
func ReadMarker(dataDir string) *Marker {
	var m Marker
	if readJSON(filepath.Join(dataDir, markerFile), &m) != nil {
		return nil
	}
	return &m
}

// LastResult is the stored result; nil when there is none.
func LastResult(dataDir string) *Result {
	var r Result
	if readJSON(filepath.Join(dataDir, resultFile), &r) != nil {
		return nil
	}
	return &r
}

// Settle records how the change in m ended for a program of version running now, and removes the
// marker. Call it once the program is serving (a new version that never gets this far is rolled back).
func Settle(dataDir string, m *Marker, version string, now time.Time) (Result, error) {
	switched := version != m.From
	r := Result{OK: switched, Kind: m.Kind, From: m.From, To: version, DataRestored: m.DataRestored, At: now}
	switch {
	case m.Kind == "rollback" && switched:
		r.Message = fmt.Sprintf("Rolled back from %s to %s", m.From, version)
		r.RolledBack = true
	case m.Kind == "rollback":
		r.Message = fmt.Sprintf("Roll back failed; still on %s", version)
	case switched:
		r.Message = fmt.Sprintf("Updated from %s to %s", m.From, version)
		r.Backup = m.Backup
	default:
		r.To = m.To
		r.Message = fmt.Sprintf("Update to %s failed; rolled back to %s", m.To, version)
		r.RolledBack = true
		if msg := watcherFailure(dataDir, m); msg != "" {
			r.Message = msg // the watcher wrote the detail (what failed, what was put back): keep it
			r.RolledBack = false
		} else if watcherLostResult(dataDir, m) {
			log.Printf("the update watcher left no result for the update to %s (see update/watch.log)", m.To)
		}
	}
	if m.DataRestored {
		r.Message += RestoredNote(m.KeptAs)
	}
	if err := writeJSON(filepath.Join(dataDir, resultFile), r); err != nil {
		return r, err
	}
	return r, RemoveMarker(dataDir)
}

// watcherFailure is the message the watcher wrote when this update failed, or "" when there is none. Only a
// failure for the same update, written after it started, counts: an older result must not be shown.
func watcherFailure(dataDir string, m *Marker) string {
	prev := LastResult(dataDir)
	if prev == nil || prev.OK || prev.Kind != "update" || prev.From != m.From || prev.To != m.To || prev.Message == "" || prev.At.Before(m.At) {
		return ""
	}
	return prev.Message
}

// watcherLostResult reports whether the watcher ran for this update (its log was written after it started) and
// logged that its result could not be saved, so the detail is only in that log.
func watcherLostResult(dataDir string, m *Marker) bool {
	path := filepath.Join(dataDir, updateDir, "watch.log")
	fi, err := os.Stat(path)
	if err != nil || fi.ModTime().Before(m.At) {
		return false
	}
	b, err := os.ReadFile(path)
	return err == nil && bytes.Contains(b, []byte(unsavedNote))
}

// RestoredNote ends a result's message when the database went back: where the newer one's work is.
func RestoredNote(keptAs string) string {
	if keptAs == "" {
		keptAs = RolledBackDB
	}
	return "; changes made since the update were undone: jobs done on the newer version are kept in " + keptAs
}

// RestoreDB puts the backup in place of the database at db and returns where the database it replaces
// was kept: <db>.rolled-back, or <db>.rolled-back-2, -3... when one is already there (a database kept
// before is never replaced). Its -wal and -shm files are removed (they belong to it, not to the backup).
func RestoreDB(backup, db string) (string, error) {
	kept := db + ".rolled-back"
	for n := 2; ; n++ {
		if _, err := os.Lstat(kept); os.IsNotExist(err) {
			break
		} else if err != nil {
			return "", err
		}
		kept = fmt.Sprintf("%s.rolled-back-%d", db, n)
	}
	return kept, restoreDB(backup, db, kept)
}

func restoreDB(backup, db, kept string) error {
	in, err := os.Open(backup)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(db), ".syp-restore-")
	if err != nil {
		return err
	}
	_, cerr := io.Copy(tmp, in)
	if err := errors.Join(cerr, tmp.Sync(), tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(db, kept); err != nil && !os.IsNotExist(err) {
		os.Remove(tmp.Name())
		return err
	}
	for _, s := range []string{"-wal", "-shm"} {
		if err := os.Remove(db + s); err != nil && !os.IsNotExist(err) {
			os.Remove(tmp.Name())
			return err
		}
	}
	return os.Rename(tmp.Name(), db)
}

func writeJSON(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
