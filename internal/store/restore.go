// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// A restore copies the set-up from a backup into the live database, in one transaction: any failure rolls all of it
// back. It upserts by primary key and deletes nothing, so a row the live database has and the backup does not is kept
// and listed. Jobs, history, the trash and files are never touched, and neither are the secret tables.

// ErrNotABackup is a file that is not a backup of this app.
var ErrNotABackup = errors.New("not a backup")

// ErrBackupTooNew is a backup from a newer schema than this app knows.
var ErrBackupTooNew = errors.New("backup is from a newer schema")

// setUpTables are restored, upserted by primary key, in this order. A workflow's revisions come after the workflow.
var setUpTables = []string{"app_settings", "workflows", "workflow_revisions", "libraries"}

// secretTables are never restored from a backup, and a download has them removed.
var secretTables = []string{"auth", "tokens", "sessions"}

// Contents is what a backup holds, counted.
type Contents struct {
	Settings    bool `json:"settings"`
	Workflows   int  `json:"workflows"`
	Libraries   int  `json:"libraries"`
	Shares      int  `json:"shares"`
	Connections int  `json:"connections"`
	Jobs        int  `json:"jobs"`
}

// Kept is a library or workflow the live set-up has and the backup does not. It is kept, and listed.
type Kept struct {
	Type string `json:"type"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// RestoreReport is what a restore did.
type RestoreReport struct {
	Restored        Contents
	KeptNotInBackup []Kept
}

// CurrentSchema is the live database's schema version.
func (db *DB) CurrentSchema(ctx context.Context) (int, error) {
	var v int
	err := db.Read().QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v)
	return v, err
}

// ValidateBackup opens the file read-only and checks it is a backup of this app that can be restored: a SQLite file
// that passes its check, with the set-up tables, and a schema no newer than liveSchema. It answers the counts.
func ValidateBackup(ctx context.Context, path string, liveSchema int) (Contents, int, error) {
	head := make([]byte, 16)
	f, err := os.Open(path)
	if err != nil {
		return Contents{}, 0, err
	}
	n, _ := io.ReadFull(f, head)
	f.Close()
	if n < 16 || string(head) != "SQLite format 3\x00" {
		return Contents{}, 0, ErrNotABackup
	}
	raw, err := sql.Open("sqlite", dbURI(path, "mode=ro"))
	if err != nil {
		return Contents{}, 0, ErrNotABackup
	}
	defer raw.Close()
	var check string
	if err := raw.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&check); err != nil || check != "ok" {
		return Contents{}, 0, ErrNotABackup
	}
	var schema int
	if err := raw.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&schema); err != nil {
		return Contents{}, 0, ErrNotABackup
	}
	if schema > liveSchema {
		return Contents{}, schema, ErrBackupTooNew
	}
	for _, t := range []string{"app_settings", "workflows", "libraries"} {
		var ok int
		if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, t).Scan(&ok); err != nil || ok == 0 {
			return Contents{}, schema, ErrNotABackup
		}
	}
	var c Contents
	if err := countContents(ctx, raw, "", &c); err != nil {
		return Contents{}, schema, ErrNotABackup
	}
	return c, schema, nil
}

// countContents counts the set-up and jobs of a database; schema is "" for the database itself or the attached alias.
func countContents(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, schema string, c *Contents) error {
	prefix := ""
	if schema != "" {
		prefix = schema + "."
	}
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM `+prefix+`app_settings`).Scan(&n); err != nil {
		return err
	}
	c.Settings = n > 0
	for _, pair := range []struct {
		table string
		into  *int
	}{{"workflows", &c.Workflows}, {"libraries", &c.Libraries}, {"jobs", &c.Jobs}} {
		if err := q.QueryRowContext(ctx, `SELECT count(*) FROM `+prefix+pair.table).Scan(pair.into); err != nil {
			if strings.Contains(err.Error(), "no such table") {
				*pair.into = 0
				continue
			}
			return err
		}
	}
	return nil
}

// StripSecrets writes a copy of the backup at src to dst with the secret tables removed, then compacts it, so the
// removed rows are not left in free pages either.
func StripSecrets(ctx context.Context, src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	raw, err := sql.Open("sqlite", dbURI(dst, ""))
	if err != nil {
		os.Remove(dst)
		return err
	}
	defer raw.Close()
	for _, t := range secretTables {
		var ok int
		if err := raw.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, t).Scan(&ok); err != nil {
			os.Remove(dst)
			return err
		}
		if ok == 0 {
			continue
		}
		if _, err := raw.ExecContext(ctx, `DELETE FROM `+t); err != nil {
			os.Remove(dst)
			return err
		}
	}
	if _, err := raw.ExecContext(ctx, `VACUUM`); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

// RestoreSetUp copies the set-up of the backup at src into this database in one transaction. step, when set, is called
// before each table; a non-nil error from it rolls the whole restore back (the tests use this to fail a restore midway).
func (db *DB) RestoreSetUp(ctx context.Context, src string, step func(table string) error) (RestoreReport, error) {
	var rep RestoreReport
	conn, err := db.write.Conn(ctx)
	if err != nil {
		return rep, err
	}
	defer conn.Close()
	// ATTACH cannot run inside a transaction, so it runs on this connection first.
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS src`, dbURI(src, "mode=ro")); err != nil {
		return rep, fmt.Errorf("open the backup: %w", err)
	}
	defer conn.ExecContext(context.Background(), `DETACH DATABASE src`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return rep, err
	}
	for _, table := range setUpTables {
		if step != nil {
			if err := step(table); err != nil {
				tx.Rollback()
				return rep, err
			}
		}
		if err := restoreTable(ctx, tx, table); err != nil {
			tx.Rollback()
			return rep, err
		}
	}
	if err := countContents(ctx, tx, "src", &rep.Restored); err != nil {
		tx.Rollback()
		return rep, err
	}
	for _, k := range []struct {
		table, typ string
	}{{"libraries", "library"}, {"workflows", "workflow"}} {
		kept, err := keptRows(ctx, tx, k.table, k.typ)
		if err != nil {
			tx.Rollback()
			return rep, err
		}
		rep.KeptNotInBackup = append(rep.KeptNotInBackup, kept...)
	}
	if err := tx.Commit(); err != nil {
		return RestoreReport{}, err
	}
	return rep, nil
}

// restoreTable upserts the rows of one set-up table from the backup. Only the columns both sides have are copied, so a
// backup from an older schema restores what it holds. A table the backup predates is skipped.
func restoreTable(ctx context.Context, tx *sql.Tx, table string) error {
	live, err := tableColumns(ctx, tx, "main", table)
	if err != nil {
		return err
	}
	if len(live) == 0 {
		return fmt.Errorf("the set-up table %s is missing", table)
	}
	srcCols, err := tableColumns(ctx, tx, "src", table)
	if err != nil {
		return err
	}
	if len(srcCols) == 0 {
		return nil
	}
	inSrc := map[string]bool{}
	for _, c := range srcCols {
		inSrc[c.name] = true
	}
	var common []string
	var pk []string
	for _, c := range live {
		if !inSrc[c.name] {
			continue
		}
		common = append(common, quoteIdent(c.name))
		if c.pk > 0 {
			pk = append(pk, quoteIdent(c.name))
		}
	}
	if len(pk) == 0 {
		return fmt.Errorf("the set-up table %s has no key to match rows by", table)
	}
	var sets []string
	for _, c := range live {
		if inSrc[c.name] && c.pk == 0 {
			sets = append(sets, fmt.Sprintf("%s = excluded.%s", quoteIdent(c.name), quoteIdent(c.name)))
		}
	}
	conflict := "DO NOTHING"
	if len(sets) > 0 {
		conflict = "DO UPDATE SET " + strings.Join(sets, ", ")
	}
	cols := strings.Join(common, ", ")
	q := fmt.Sprintf(`INSERT INTO main.%[1]s (%[2]s) SELECT %[2]s FROM src.%[1]s WHERE true ON CONFLICT (%[3]s) %[4]s`,
		table, cols, strings.Join(pk, ", "), conflict)
	if _, err := tx.ExecContext(ctx, q); err != nil {
		return fmt.Errorf("restore %s: %w", table, err)
	}
	return nil
}

// keptRows lists the rows of a live table the backup does not have. Nothing is deleted; these are shown.
func keptRows(ctx context.Context, tx *sql.Tx, table, typ string) ([]Kept, error) {
	srcCols, err := tableColumns(ctx, tx, "src", table)
	if err != nil {
		return nil, err
	}
	where := ""
	if len(srcCols) > 0 {
		where = fmt.Sprintf(" WHERE id NOT IN (SELECT id FROM src.%s)", table)
	}
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`SELECT id, name FROM main.%s%s ORDER BY id`, table, where))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Kept
	for rows.Next() {
		var k Kept
		if err := rows.Scan(&k.ID, &k.Name); err != nil {
			return nil, err
		}
		k.Type = typ
		out = append(out, k)
	}
	return out, rows.Err()
}

type column struct {
	name string
	pk   int
}

// tableColumns reads a table's columns in one schema ("main" or "src"); none when the table is not there.
func tableColumns(ctx context.Context, tx *sql.Tx, schema, table string) ([]column, error) {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf(`PRAGMA %s.table_info(%s)`, schema, quoteIdent(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out = append(out, column{name: name, pk: pk})
	}
	return out, rows.Err()
}

func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
