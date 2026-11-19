// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"compress/gzip"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Backup reasons (UI-API-CONTRACT-v2.md "Backup"). Every reason but manual is automatic: only the newest
// backupKeep of those stay.
const (
	ReasonManual          = "manual"
	ReasonBeforeUpdate    = "before_update"
	ReasonBeforeMigration = "before_migration"
	ReasonBeforeImport    = "before_import"
)

// backupFormat is the file format's version: a newer one is refused by a restore (phase 2).
const backupFormat = 1

// defaultBackupKeep is the number of automatic backups kept when the setting is not stored.
const defaultBackupKeep = 10

// ErrNoBackup means there is no backup with that id.
var ErrNoBackup = errors.New("no such backup")

// backupID is "<yyyymmdd-hhmmss>-<4 hex>-<reason>", which is also the file's name before ".json.gz".
var backupID = regexp.MustCompile(`^\d{8}-\d{6}-[0-9a-f]{4}-(manual|before_update|before_migration|before_import)$`)

// BackupInfo is one backup in the data folder (the contract's Backup).
type BackupInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  string `json:"createdAt"`
	Reason     string `json:"reason"`
	AppVersion string `json:"appVersion"`
	SizeBytes  int64  `json:"sizeBytes"`
}

// backupHeader is what comes first in the file. Everything else is the tables, after it, so a listing reads
// only the start of the file.
type backupHeader struct {
	FormatVersion int    `json:"formatVersion"`
	AppVersion    string `json:"appVersion"`
	CreatedAt     string `json:"createdAt"`
	Reason        string `json:"reason"`
	SchemaVersion int    `json:"schemaVersion"`
}

// backupDir is the folder beside the database where backups are kept.
func (s *Store) backupDir() string { return filepath.Join(filepath.Dir(s.path), "backups") }

// WriteBackup saves every table of the database to a new file in the backups folder and returns it. The
// tables are read in one read transaction, so the copy is consistent. For an automatic reason, the
// automatic backups beyond the newest backupKeep are then deleted.
func (s *Store) WriteBackup(reason string) (BackupInfo, error) {
	now := s.now().UTC()
	if err := os.MkdirAll(s.backupDir(), 0o755); err != nil {
		return BackupInfo{}, err
	}
	var rnd [2]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return BackupInfo{}, err
	}
	id := fmt.Sprintf("%s-%x-%s", now.Format("20060102-150405"), rnd, reason)
	if !backupID.MatchString(id) {
		return BackupInfo{}, fmt.Errorf("unknown backup reason %q", reason)
	}
	final := filepath.Join(s.backupDir(), id+".json.gz")
	tmp := final + ".tmp" // not listed until it is whole
	if err := s.dumpTo(tmp, reason, now); err != nil {
		os.Remove(tmp)
		return BackupInfo{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return BackupInfo{}, err
	}
	if reason != ReasonManual {
		s.pruneAutomatic()
	}
	return backupInfoOf(final, id)
}

// dumpTo writes the database as a gzipped JSON object: the header fields, then "tables" with each table's
// rows as objects keyed by column name.
func (s *Store) dumpTo(path, reason string, now time.Time) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // the file holds the whole database
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	gz := gzip.NewWriter(f)
	defer func() {
		if cerr := gz.Close(); err == nil {
			err = cerr
		}
	}()
	tx, err := s.db.Begin() // one connection: nothing else uses the database until this read is done
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var schema int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&schema); err != nil {
		return err
	}
	h, err := json.Marshal(backupHeader{FormatVersion: backupFormat, AppVersion: s.appVersion, CreatedAt: now.Format(time.RFC3339), Reason: reason, SchemaVersion: schema})
	if err != nil {
		return err
	}
	// h is a complete object; drop its closing brace and add the tables.
	if _, err := gz.Write(h[:len(h)-1]); err != nil {
		return err
	}
	if _, err := io.WriteString(gz, `,"tables":{`); err != nil {
		return err
	}
	names, err := tableNames(tx)
	if err != nil {
		return err
	}
	for i, name := range names {
		key, _ := json.Marshal(name)
		if _, err := io.WriteString(gz, sep(i)+string(key)+":["); err != nil {
			return err
		}
		if err := writeRows(gz, tx, name); err != nil {
			return err
		}
		if _, err := io.WriteString(gz, "]"); err != nil {
			return err
		}
	}
	_, err = io.WriteString(gz, "}}")
	return err
}

func sep(i int) string {
	if i == 0 {
		return ""
	}
	return ","
}

func tableNames(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(`SELECT name FROM sqlite_master WHERE type='table' AND substr(name,1,7) <> 'sqlite_' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// writeRows writes the rows of one table as a comma-separated list of objects, without the brackets.
func writeRows(w io.Writer, tx *sql.Tx, table string) error {
	rows, err := tx.Query(`SELECT * FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	n := 0
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make(map[string]any, len(cols))
		for i, c := range cols {
			row[c] = vals[i] // a []byte is written as base64 by encoding/json
		}
		b, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, sep(n)); err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		n++
	}
	return rows.Err()
}

// ListBackups returns the backups in the backups folder, newest first. A file that is not a readable
// backup is left out (and logged): it is not one this program can show or restore.
func (s *Store) ListBackups() ([]BackupInfo, error) {
	matches, _ := filepath.Glob(filepath.Join(s.backupDir(), "*.json.gz"))
	out := []BackupInfo{}
	for _, m := range matches {
		id := strings.TrimSuffix(filepath.Base(m), ".json.gz")
		if !backupID.MatchString(id) {
			continue
		}
		b, err := backupInfoOf(m, id)
		if err != nil {
			log.Printf("backup %s: %v", id, err)
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// BackupFile returns the path of a backup and what it is, for a download.
func (s *Store) BackupFile(id string) (string, BackupInfo, error) {
	if !backupID.MatchString(id) {
		return "", BackupInfo{}, ErrNoBackup
	}
	path := filepath.Join(s.backupDir(), id+".json.gz")
	b, err := backupInfoOf(path, id)
	if errors.Is(err, os.ErrNotExist) {
		return "", BackupInfo{}, ErrNoBackup
	}
	return path, b, err
}

// DeleteBackup removes one backup. A manual one stays until it is deleted; an automatic one is also
// deleted by the keep setting.
func (s *Store) DeleteBackup(id string) error {
	if !backupID.MatchString(id) {
		return ErrNoBackup
	}
	err := os.Remove(filepath.Join(s.backupDir(), id+".json.gz"))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNoBackup
	}
	return err
}

// pruneAutomatic deletes the automatic backups beyond the newest backupKeep.
func (s *Store) pruneAutomatic() {
	list, err := s.ListBackups()
	if err != nil {
		return
	}
	keep, n := s.backupKeep(), 0
	for _, b := range list { // newest first
		if b.Reason == ReasonManual {
			continue
		}
		if n++; n > keep {
			os.Remove(filepath.Join(s.backupDir(), b.ID+".json.gz"))
		}
	}
}

// removeStaleFiles removes what a crash can leave in the backups folder: a backup that was being written
// (*.json.gz.tmp) and a previewed copy (preview-*.part). It runs when the store opens, before anything of
// this program writes there.
func (s *Store) removeStaleFiles() {
	for _, pat := range []string{"*.json.gz.tmp", "preview-*.part"} {
		m, _ := filepath.Glob(filepath.Join(s.backupDir(), pat))
		for _, f := range m {
			os.Remove(f)
		}
	}
}

// backupKeep is the backupKeep setting (kept in app_meta with the other settings, which this package
// reads but does not own), or the default when it is not stored.
func (s *Store) backupKeep() int {
	v, ok, err := s.Meta("settings")
	var st struct {
		BackupKeep int `json:"backupKeep"`
	}
	if err == nil && ok {
		json.Unmarshal([]byte(v), &st)
	}
	if st.BackupKeep < 1 {
		return defaultBackupKeep
	}
	return st.BackupKeep
}

// backupName is the file name a person sees for a backup. Seconds are in it, so two made in the same minute
// do not share a name.
func backupName(created time.Time) string {
	return "shrink-your-shit-backup-" + created.Format("2006-01-02-150405") + ".json.gz"
}

// backupInfoOf reads a backup's header (not its tables) and its size.
func backupInfoOf(path, id string) (BackupInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return BackupInfo{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return BackupInfo{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return BackupInfo{}, err
	}
	defer gz.Close()
	dec := json.NewDecoder(gz)
	if _, err := dec.Token(); err != nil { // {
		return BackupInfo{}, err
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return BackupInfo{}, err
		}
		key, _ := t.(string)
		if key == "tables" {
			break
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return BackupInfo{}, err
		}
		fields[key] = v
	}
	raw, _ := json.Marshal(fields)
	var h backupHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return BackupInfo{}, err
	}
	if h.FormatVersion < 1 || h.FormatVersion > backupFormat {
		return BackupInfo{}, fmt.Errorf("backup format %d, this program reads %d", h.FormatVersion, backupFormat)
	}
	created, err := time.Parse(time.RFC3339, h.CreatedAt)
	if err != nil {
		return BackupInfo{}, err
	}
	return BackupInfo{
		ID: id, Name: backupName(created),
		CreatedAt: h.CreatedAt, Reason: h.Reason, AppVersion: h.AppVersion, SizeBytes: st.Size(),
	}, nil
}
