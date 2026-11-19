// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"compress/gzip"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// MaxBackupBytes is the largest backup that can be previewed or restored.
const MaxBackupBytes = 200 << 20

var (
	// ErrNotABackup means the file is not one of this program's backups.
	ErrNotABackup = errors.New("this file isn't a backup")
	// ErrJobsRunning means a job is converting, so nothing is restored.
	ErrJobsRunning = errors.New("files are converting")
	// ErrTooLarge means the file is over MaxBackupBytes.
	ErrTooLarge = errors.New("backup too large")
)

// TooNewError is a backup whose schema is newer than this database's, so this program cannot restore it.
type TooNewError struct {
	BackupVersion string
	ThisVersion   string
}

func (e *TooNewError) Error() string {
	return fmt.Sprintf("backup from version %s is newer than this app (%s)", e.BackupVersion, e.ThisVersion)
}

// BackupContents counts what a backup holds (the contract's BackupContents).
type BackupContents struct {
	Settings    bool `json:"settings"`
	Workflows   int  `json:"workflows"`
	Libraries   int  `json:"libraries"`
	Shares      int  `json:"shares"`
	Connections int  `json:"connections"`
	Jobs        int  `json:"jobs"`
}

// BackupPreview says what a backup file holds, before anything is restored.
type BackupPreview struct {
	FormatVersion int            `json:"formatVersion"`
	AppVersion    string         `json:"appVersion"`
	CreatedAt     string         `json:"createdAt"`
	Contents      BackupContents `json:"contents"`
	Warnings      []string       `json:"warnings"`
}

// PreviewBackup reads the backup file at path and says what it holds. Nothing in the database changes.
func (s *Store) PreviewBackup(path string) (BackupPreview, error) {
	var sc backupScan
	h, err := walkBackup(path, sc.add)
	if err != nil {
		return BackupPreview{}, err
	}
	live, err := s.userVersion()
	if err != nil {
		return BackupPreview{}, err
	}
	if err := checkFormat(h, live, s.appVersion); err != nil {
		return BackupPreview{}, err
	}
	return BackupPreview{FormatVersion: h.FormatVersion, AppVersion: h.AppVersion, CreatedAt: h.CreatedAt,
		Contents: sc.c, Warnings: libraryWarnings(sc.libraryPaths)}, nil
}

// RestoreBackup replaces the contents of every table with the rows of the backup at path, in one transaction:
// if anything fails, nothing changes. A job that is converting refuses it (ErrJobsRunning). The caller makes
// the before_import backup first. Columns the backup lacks keep their defaults; columns it has that this
// database lacks are left out.
func (s *Store) RestoreBackup(path string) (BackupContents, error) {
	h, err := walkBackup(path, nil)
	if err != nil {
		return BackupContents{}, err
	}
	tx, err := s.db.Begin() // the only connection: nothing else may touch the database until this ends
	if err != nil {
		return BackupContents{}, err
	}
	defer tx.Rollback()
	var live int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&live); err != nil {
		return BackupContents{}, err
	}
	if err := checkFormat(h, live, s.appVersion); err != nil {
		return BackupContents{}, err
	}
	var running int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status=?`, Running).Scan(&running); err != nil {
		return BackupContents{}, err
	}
	if running > 0 {
		return BackupContents{}, ErrJobsRunning
	}
	if _, err := tx.Exec(`PRAGMA defer_foreign_keys=ON`); err != nil { // checked at commit, so the order of tables does not matter
		return BackupContents{}, err
	}
	names, err := tableNames(tx)
	if err != nil {
		return BackupContents{}, err
	}
	for _, n := range names {
		if _, err := tx.Exec(`DELETE FROM ` + quoteIdent(n)); err != nil {
			return BackupContents{}, err
		}
	}
	r := &restorer{tx: tx, tables: map[string]*liveTable{}, exists: map[string]bool{}}
	for _, n := range names {
		r.exists[n] = true
	}
	var sc backupScan
	if _, err := walkBackup(path, func(table string, row map[string]any) error {
		sc.add(table, row)
		return r.insert(table, row)
	}); err != nil {
		return BackupContents{}, err
	}
	if err := tx.Commit(); err != nil {
		return BackupContents{}, err
	}
	return sc.c, nil
}

// CountRunning is the number of jobs that are converting now.
func (s *Store) CountRunning() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE status=?`, Running).Scan(&n)
	return n, err
}

// StageBackup copies src into a new file in the backups folder and returns its path. The name does not end in
// ".json.gz", so the file is never listed as a backup. A copy over MaxBackupBytes is removed (ErrTooLarge).
func (s *Store) StageBackup(src io.Reader) (string, error) {
	if err := os.MkdirAll(s.backupDir(), 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(s.backupDir(), "preview-*.part")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(src, MaxBackupBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxBackupBytes {
		err = ErrTooLarge
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// checkFormat refuses a file this program cannot read, and a schema newer than the database's.
func checkFormat(h backupHeader, live int, thisVersion string) error {
	if h.FormatVersion < 1 || h.FormatVersion > backupFormat {
		return ErrNotABackup
	}
	if h.SchemaVersion > live {
		return &TooNewError{BackupVersion: h.AppVersion, ThisVersion: thisVersion}
	}
	return nil
}

// backupScan counts a backup's contents as its rows go past.
type backupScan struct {
	c            BackupContents
	libraryPaths []string
}

func (sc *backupScan) add(table string, row map[string]any) error {
	switch table {
	case "flows":
		sc.c.Workflows++
	case "libraries":
		sc.c.Libraries++
		if p, ok := row["path"].(string); ok {
			sc.libraryPaths = append(sc.libraryPaths, p)
		}
	case "shares":
		sc.c.Shares++
	case "jobs":
		sc.c.Jobs++
	case "app_meta":
		key, _ := row["key"].(string)
		value, _ := row["value"].(string)
		switch key {
		case "settings":
			sc.c.Settings = true
		case "integrations":
			var doc struct {
				Items []json.RawMessage `json:"items"`
			}
			if json.Unmarshal([]byte(value), &doc) == nil {
				sc.c.Connections = len(doc.Items)
			}
		}
	}
	return nil
}

// libraryWarnings says which library folders this computer does not have.
func libraryWarnings(paths []string) []string {
	var missing []string
	for _, p := range paths {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, p)
		}
	}
	out := []string{}
	switch len(missing) {
	case 0:
	case 1:
		out = append(out, "1 library points at a folder this computer doesn't have: "+missing[0]+".")
	default:
		out = append(out, fmt.Sprintf("%d libraries point at folders this computer doesn't have: %s.",
			len(missing), strings.Join(missing, ", ")))
	}
	return out
}

// walkBackup reads the backup at path as a stream: it returns the header and calls fn for each row of each
// table, in the file's order, holding one row at a time. With fn nil it reads only the header.
func walkBackup(path string, fn func(table string, row map[string]any) error) (backupHeader, error) {
	f, err := os.Open(path)
	if err != nil {
		return backupHeader{}, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return backupHeader{}, ErrNotABackup
	}
	defer gz.Close()
	dec := json.NewDecoder(gz)
	dec.UseNumber()
	if err := expectDelim(dec, '{'); err != nil {
		return backupHeader{}, err
	}
	fields := map[string]json.RawMessage{}
	for {
		if !dec.More() {
			return backupHeader{}, ErrNotABackup // no tables
		}
		t, err := dec.Token()
		if err != nil {
			return backupHeader{}, ErrNotABackup
		}
		key, _ := t.(string)
		if key == "tables" {
			break
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return backupHeader{}, ErrNotABackup
		}
		fields[key] = v
	}
	raw, _ := json.Marshal(fields)
	var h backupHeader
	if err := json.Unmarshal(raw, &h); err != nil {
		return backupHeader{}, ErrNotABackup
	}
	if fn == nil {
		return h, nil
	}
	if err := expectDelim(dec, '{'); err != nil {
		return h, err
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return h, ErrNotABackup
		}
		name, _ := t.(string)
		if err := expectDelim(dec, '['); err != nil {
			return h, err
		}
		for dec.More() {
			var row map[string]any
			if err := dec.Decode(&row); err != nil {
				return h, ErrNotABackup
			}
			if err := fn(name, row); err != nil {
				return h, err
			}
		}
		if err := expectDelim(dec, ']'); err != nil {
			return h, err
		}
	}
	return h, expectDelim(dec, '}')
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return ErrNotABackup
	}
	if d, ok := t.(json.Delim); !ok || d != want {
		return ErrNotABackup
	}
	return nil
}

// liveTable is one table of the open database, as a restore writes to it.
type liveTable struct {
	sql  string   // INSERT for the columns both sides have
	cols []string // in the same order as the placeholders
	blob map[string]bool
	skip bool // no column in common: the backup's rows are left out
}

// restorer writes a backup's rows into the tables of the open transaction.
type restorer struct {
	tx     *sql.Tx
	tables map[string]*liveTable
	exists map[string]bool
}

func (r *restorer) insert(table string, row map[string]any) error {
	if !r.exists[table] {
		return nil // a table this database does not have
	}
	lt, ok := r.tables[table]
	if !ok {
		var err error
		if lt, err = r.describe(table, row); err != nil {
			return err
		}
		r.tables[table] = lt
	}
	if lt.skip {
		return nil
	}
	vals := make([]any, len(lt.cols))
	for i, c := range lt.cols {
		v, err := restoreValue(row[c], lt.blob[c])
		if err != nil {
			return fmt.Errorf("restore %s.%s: %w", table, c, err)
		}
		vals[i] = v
	}
	_, err := r.tx.Exec(lt.sql, vals...)
	return err
}

// describe works out which of the backup's columns this database has, and which are BLOBs.
func (r *restorer) describe(table string, row map[string]any) (*liveTable, error) {
	rows, err := r.tx.Query(`PRAGMA table_info(` + quoteIdent(table) + `)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	lt := &liveTable{blob: map[string]bool{}}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		if _, ok := row[name]; !ok {
			continue
		}
		lt.cols = append(lt.cols, name)
		lt.blob[name] = strings.Contains(strings.ToUpper(typ), "BLOB")
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(lt.cols) == 0 {
		lt.skip = true
		return lt, nil
	}
	marks := make([]string, len(lt.cols))
	names := make([]string, len(lt.cols))
	for i, c := range lt.cols {
		marks[i] = "?"
		names[i] = quoteIdent(c)
	}
	lt.sql = `INSERT INTO ` + quoteIdent(table) + ` (` + strings.Join(names, ",") + `) VALUES (` + strings.Join(marks, ",") + `)`
	return lt, nil
}

// restoreValue turns a value read from the file back into a database value. A BLOB column's value was written
// as base64 text, so it is decoded here.
func restoreValue(v any, blob bool) (any, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		return x.Float64()
	case string:
		if blob {
			return base64.StdEncoding.DecodeString(x)
		}
		return x, nil
	}
	return v, nil
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
