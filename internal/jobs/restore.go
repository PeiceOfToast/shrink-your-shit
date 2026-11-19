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
	"path/filepath"
	"strings"
)

// MaxBackupBytes is the largest backup that can be previewed or restored. A variable so tests can lower it.
var MaxBackupBytes int64 = 200 << 20

// MaxRestoreBytes is the most decompressed data a backup can hold and still be previewed or restored. The app's own
// backups are gzipped JSON that can be much bigger than the upload cap. A variable so tests can lower it.
var MaxRestoreBytes int64 = 4 << 30

var (
	// ErrNotABackup means the file is not one of this program's backups.
	ErrNotABackup = errors.New("this file isn't a backup")
	// ErrJobsRunning means a job is converting, so nothing is restored.
	ErrJobsRunning = errors.New("files are converting")
	// ErrTooLarge means the file is over MaxBackupBytes.
	ErrTooLarge = errors.New("backup too large")
)

// maxBackupRow caps the decompressed bytes of one row (one JSON value), so a single huge value is refused
// before it is held in memory. Real rows are far smaller.
const maxBackupRow = 16 << 20

// boundedReader counts the decompressed bytes of a backup: all of them up to MaxRestoreBytes,
// and, between resets, the bytes of the one row being decoded (maxBackupRow).
type boundedReader struct {
	r     io.Reader
	total int64 // decompressed bytes so far
	row   int64 // decompressed bytes since the last reset
}

// Read hands out no more than the bytes still allowed, so a value over the limit never reaches the decoder
// whole: the decoder gets ErrTooLarge instead of the rest of it.
func (b *boundedReader) Read(p []byte) (int, error) {
	room := maxBackupRow - b.row
	if left := MaxRestoreBytes - b.total; left < room {
		room = left
	}
	if room <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > room {
		p = p[:room]
	}
	n, err := b.r.Read(p)
	b.total += int64(n)
	b.row += int64(n)
	return n, err
}

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
	// SecretsUnreadable is true when a password or token in the backup won't open on this computer.
	SecretsUnreadable bool     `json:"secretsUnreadable,omitempty"`
	Warnings          []string `json:"warnings"`
}

// PreviewBackup reads the backup file at path and says what it holds. Nothing in the database changes.
// RestoreNeed is the free space a restore of the backup at path needs beside the database: the backup's decompressed
// size, plus the database file as it is now. It returns the directory that space is taken from. It reads the whole
// backup once and changes nothing.
func (s *Store) RestoreNeed(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return 0, "", ErrNotABackup
	}
	defer gz.Close()
	n, err := io.Copy(io.Discard, gz)
	if err != nil {
		return 0, "", ErrNotABackup
	}
	if n > MaxRestoreBytes {
		return 0, "", ErrTooLarge
	}
	var dbSize int64
	if st, err := os.Stat(s.path); err == nil {
		dbSize = st.Size()
	}
	return n + dbSize, filepath.Dir(s.path), nil
}

func (s *Store) PreviewBackup(path string) (BackupPreview, error) {
	sc := backupScan{key: readShareKeyHere()}
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
	warnings := libraryWarnings(sc.libraryPaths)
	if sc.unreadable {
		warnings = append(warnings, secretsWarning)
	}
	return BackupPreview{FormatVersion: h.FormatVersion, AppVersion: h.AppVersion, CreatedAt: h.CreatedAt,
		Contents: sc.c, SecretsUnreadable: sc.unreadable, Warnings: append(warnings, workerSignInWarning)}, nil
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
		if keptTables[n] { // workers and their pairings: live state, kept as it is
			continue
		}
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

// backupScan counts a backup's contents as its rows go past. key is this computer's share key (nil if none):
// unreadable is set when a sealed password, token or URL in the backup does not open with it.
type backupScan struct {
	c            BackupContents
	libraryPaths []string
	key          []byte
	unreadable   bool
}

// secretsWarning is the preview line for a backup whose sealed secrets this computer cannot open.
const secretsWarning = "Passwords and tokens in this backup won't work here; enter them again after restoring."

// sealed checks one sealed value. Empty means nothing was sealed, so there is nothing to open.
func (sc *backupScan) sealed(ct, nonce []byte) {
	if len(ct) > 0 && len(nonce) > 0 && !sealedOpens(sc.key, ct, nonce) {
		sc.unreadable = true
	}
}

// blob reads a []byte column or field as the backup holds it: base64 text, or nothing.
func blob(v any) []byte {
	s, _ := v.(string)
	b, _ := base64.StdEncoding.DecodeString(s)
	return b
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
		sc.sealed(blob(row["encrypted_password"]), blob(row["auth_nonce"]))
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
				Items []struct {
					Sealed []byte `json:"sealed"`
					Nonce  []byte `json:"nonce"`
				} `json:"items"`
			}
			if json.Unmarshal([]byte(value), &doc) == nil {
				sc.c.Connections = len(doc.Items)
				for _, it := range doc.Items {
					sc.sealed(it.Sealed, it.Nonce)
				}
			}
		case "notifications":
			var doc struct {
				Targets []struct {
					CT    []byte `json:"ct"`
					Nonce []byte `json:"nonce"`
				} `json:"targets"`
			}
			if json.Unmarshal([]byte(value), &doc) == nil {
				for _, t := range doc.Targets {
					sc.sealed(t.CT, t.Nonce)
				}
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
	br := &boundedReader{r: gz}
	dec := json.NewDecoder(br)
	dec.UseNumber()
	if err := expectDelim(dec, '{'); err != nil {
		return backupHeader{}, err
	}
	fields := map[string]json.RawMessage{}
	for {
		if !dec.More() {
			return backupHeader{}, ErrNotABackup // no tables
		}
		br.row = 0
		t, err := dec.Token()
		if err != nil {
			return backupHeader{}, notABackup(err)
		}
		key, _ := t.(string)
		if key == "tables" {
			break
		}
		var v json.RawMessage
		br.row = 0
		if err := dec.Decode(&v); err != nil {
			return backupHeader{}, notABackup(err)
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
		br.row = 0
		t, err := dec.Token()
		if err != nil {
			return h, notABackup(err)
		}
		name, _ := t.(string)
		if err := expectDelim(dec, '['); err != nil {
			return h, err
		}
		for dec.More() {
			var row map[string]any
			br.row = 0
			if err := dec.Decode(&row); err != nil {
				return h, notABackup(err)
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

// notABackup maps a decoding error: a size limit stays a size limit, anything else means it is not a backup.
func notABackup(err error) error {
	if errors.Is(err, ErrTooLarge) {
		return ErrTooLarge
	}
	return ErrNotABackup
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	t, err := dec.Token()
	if err != nil {
		return notABackup(err)
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

// keptTables are the per-worker state tables a restore never touches: live state, so a worker paired after the
// backup keeps its path mappings and pairing requests, a worker removed after it gets none back, and the rows the
// backup holds for them are ignored. History that names a worker (job events, encoder test results) is restored.
var keptTables = map[string]bool{"workers": true, "worker_pairings": true, "worker_paths": true, "join_requests": true}

// workerSignInWarning is the preview's note about the worker tables (the restore keeps them as they are).
const workerSignInWarning = "Workers are kept as they are now. A restore doesn't add, remove or change workers, so pair again any worker that isn't listed."

func (r *restorer) insert(table string, row map[string]any) error {
	if !r.exists[table] || keptTables[table] {
		return nil // a table this database does not have, or live worker state that is kept as it is
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
