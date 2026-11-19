// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// The app (as the service user) writes a Marker in its data folder before it asks the root helper to
// swap the program. The next start reads it: a program whose version is still Marker.From was rolled
// back (or the swap never happened); any other version is the one switched to. Root never touches the
// data folder: the database is put back by the app itself (RestoreDB), before the store is opened.

const markerFile, resultFile = "update.json", "update-result.json"

// Marker is an update or roll back in progress.
type Marker struct {
	Kind   string    `json:"kind"`   // "update" or "rollback"
	From   string    `json:"from"`   // the version running when it started
	To     string    `json:"to"`     // the version asked for ("" for a roll back to an unknown version)
	Backup string    `json:"backup"` // a database copy from before the update ("" if none)
	Resume bool      `json:"resume"` // workers were turned off for it: turn them on again
	At     time.Time `json:"at"`
}

// Result is how the last update or roll back ended, shown until the next one.
type Result struct {
	OK      bool      `json:"ok"`
	Kind    string    `json:"kind"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Message string    `json:"message"`
	Backup  string    `json:"backup,omitempty"` // after a good update: the copy a manual roll back restores
	At      time.Time `json:"at"`
}

// WriteMarker saves m in dataDir.
func WriteMarker(dataDir string, m Marker) error {
	return writeJSON(filepath.Join(dataDir, markerFile), m)
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
	r := Result{OK: switched, Kind: m.Kind, From: m.From, To: version, At: now}
	switch {
	case m.Kind == "rollback" && switched:
		r.Message = fmt.Sprintf("Rolled back from v%s to v%s", m.From, version)
	case m.Kind == "rollback":
		r.Message = fmt.Sprintf("Roll back failed; still on v%s", version)
	case switched:
		r.Message = fmt.Sprintf("Updated from v%s to v%s", m.From, version)
		r.Backup = m.Backup
	default:
		r.To = m.To
		r.Message = fmt.Sprintf("Update to v%s failed; rolled back to v%s", m.To, version)
	}
	if err := writeJSON(filepath.Join(dataDir, resultFile), r); err != nil {
		return r, err
	}
	return r, RemoveMarker(dataDir)
}

// RestoreDB puts the backup in place of the database at db. The database it replaces is kept as
// <db>.rolled-back, and its -wal and -shm files are removed (they belong to it, not to the backup).
func RestoreDB(backup, db string) error {
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
	if err := os.Rename(db, db+".rolled-back"); err != nil && !os.IsNotExist(err) {
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
