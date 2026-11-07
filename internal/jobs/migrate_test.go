// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// legacySchema is exactly what the program created before migrations existed (slice 7b and
// earlier): the tables, no user_version. Do not edit it; it stands for databases already out there.
const legacySchema = `
CREATE TABLE IF NOT EXISTS files (
  path TEXT PRIMARY KEY, size INTEGER, mtime INTEGER, codec TEXT, decision TEXT);
CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  library TEXT NOT NULL, path TEXT NOT NULL,
  status TEXT NOT NULL, progress REAL NOT NULL DEFAULT 0,
  reason TEXT NOT NULL DEFAULT '', error TEXT NOT NULL DEFAULT '',
  worker TEXT NOT NULL DEFAULT '', duration REAL NOT NULL DEFAULT 0,
  size_before INTEGER NOT NULL DEFAULT 0, size_after INTEGER NOT NULL DEFAULT 0,
  output_path TEXT NOT NULL DEFAULT '',
  created TEXT NOT NULL, updated TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS jobs_status ON jobs(status);`

// rawDB opens a database file directly, outside Store.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func version(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// legacyDB builds a database the way the old code did, with some data, and closes it.
func legacyDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "syp.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for i := 1; i <= 3; i++ {
		if _, err := db.Exec(`INSERT INTO jobs(library,path,status,progress,reason,size_before,created,updated)
VALUES(?,?,?,?,?,?,?,?)`, "/lib", fmt.Sprintf("/lib/%d.mkv", i), "done", 100, "old reason", 1000*i, now, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO files(path,size,mtime,codec,decision) VALUES('/lib/1.mkv',1000,5,'h264','queued')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func backups(path string) []string {
	m, _ := filepath.Glob(path + ".bak-v*")
	return m
}

func TestMigrationVersionsAreConsecutive(t *testing.T) {
	for i, m := range migrations {
		if m.version != i+1 {
			t.Errorf("migration %d has version %d; versions must be 1,2,3... in order", i, m.version)
		}
		if strings.TrimSpace(m.sql) == "" || m.name == "" {
			t.Errorf("migration %d needs a name and SQL", m.version)
		}
	}
}

func TestFreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, s.db); v != latestVersion() {
		t.Errorf("version %d, want %d", v, latestVersion())
	}
	for _, tbl := range []string{"jobs", "files", "app_meta"} {
		if !tableExists(t, s.db, tbl) {
			t.Errorf("table %s missing", tbl)
		}
	}
	if up, _ := s.Upgraded(); up {
		t.Error("a new database is a fresh install, not an upgrade")
	}
	if _, legacy, _ := s.Meta("legacy_flows"); legacy {
		t.Error("a new database has no Legacy flows to name")
	}
	s.Close()
	if b := backups(path); len(b) != 0 {
		t.Errorf("a brand-new database needs no backup, found %v", b)
	}
}

func TestUpgradeFromLegacyKeepsData(t *testing.T) {
	path := legacyDB(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := version(t, s.db); v != latestVersion() {
		t.Errorf("version %d, want %d", v, latestVersion())
	}
	if n := count(t, s.db, "jobs"); n != 3 {
		t.Errorf("jobs: %d rows after upgrade, want 3", n)
	}
	list, err := s.List(10)
	if err != nil || len(list) != 3 || list[0].Reason != "old reason" {
		t.Errorf("jobs unreadable after upgrade: %v %+v", err, list)
	}
	if f, _ := s.GetFile("/lib/1.mkv"); f == nil || f.Codec != "h264" {
		t.Errorf("files row lost: %+v", f)
	}
	if !tableExists(t, s.db, "app_meta") {
		t.Error("new table not created")
	}
	s.Close()

	// The program still works on the upgraded database.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.Enqueue(Job{Library: "/lib", Path: "/lib/new.mkv"}); err != nil {
		t.Errorf("cannot write after upgrade: %v", err)
	}

	// A backup of the OLD database exists and still has the old data and the old version.
	b := backups(path)
	if len(b) != 1 || !strings.HasSuffix(b[0], ".bak-v0") {
		t.Fatalf("backups: %v", b)
	}
	old := rawDB(t, b[0])
	if version(t, old) != 0 || count(t, old, "jobs") != 3 || tableExists(t, old, "app_meta") {
		t.Error("backup is not a copy of the database from before the upgrade")
	}
}

func TestAdoptsVersion1DatabaseAndOnlyRunsNewSteps(t *testing.T) {
	path := legacyDB(t)
	db := rawDB(t, path)
	if _, err := db.Exec(`PRAGMA user_version = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v := version(t, s.db); v != latestVersion() || count(t, s.db, "jobs") != 3 || !tableExists(t, s.db, "app_meta") {
		t.Errorf("version %d", v)
	}
	if b := backups(path); len(b) != 1 || !strings.HasSuffix(b[0], ".bak-v1") {
		t.Errorf("backups: %v", b)
	}
}

func TestOpenTwiceIsIdempotent(t *testing.T) {
	path := legacyDB(t)
	for i := 0; i < 3; i++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if v := version(t, s.db); v != latestVersion() {
			t.Errorf("open %d: version %d", i, v)
		}
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM app_meta WHERE key='schema_created'`).Scan(&n)
		if n != 1 {
			t.Errorf("open %d: %d schema_created rows (migration ran twice?)", i, n)
		}
		s.Close()
	}
	if b := backups(path); len(b) != 1 {
		t.Errorf("only the first open upgrades, so one backup is expected: %v", b)
	}
}

func TestNewerDatabaseIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	db := rawDB(t, path)
	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	before, _ := os.ReadFile(path)

	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("a newer database must be refused")
	}
	if !errors.Is(err, ErrNewerDatabase) {
		t.Errorf("want ErrNewerDatabase, got %v", err)
	}
	if !strings.Contains(err.Error(), "newer") || !strings.Contains(err.Error(), "upgrade the program") {
		t.Errorf("message should tell the user what to do: %q", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Error("the refused database file was modified")
	}
	if b := backups(path); len(b) != 0 {
		t.Errorf("no backup should be made for a refused database: %v", b)
	}
	if v := version(t, rawDB(t, path)); v != 999 {
		t.Errorf("version changed to %d", v)
	}
}

// withMigrations swaps the migration list for the duration of a test.
func withMigrations(t *testing.T, ms []migration) {
	t.Helper()
	old := migrations
	migrations = ms
	t.Cleanup(func() { migrations = old })
}

func TestFailedStepRollsEverythingBack(t *testing.T) {
	path := legacyDB(t)
	withMigrations(t, append(append([]migration{}, migrations...),
		migration{3, "adds a table", `CREATE TABLE extra_ok (id INTEGER);`},
		migration{4, "breaks", `CREATE TABLE extra_bad (id INTEGER); THIS IS NOT SQL;`},
	))
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("the failing step must make Open fail")
	}
	if !strings.Contains(err.Error(), "step 4") || !strings.Contains(err.Error(), "left as it was") {
		t.Errorf("message should name the step and say the data is safe: %q", err)
	}
	db := rawDB(t, path)
	if v := version(t, db); v != 0 {
		t.Errorf("version is %d after a failed upgrade, want the old 0", v)
	}
	for _, tbl := range []string{"app_meta", "extra_ok", "extra_bad"} {
		if tableExists(t, db, tbl) {
			t.Errorf("table %s survived a rolled-back upgrade", tbl)
		}
	}
	if n := count(t, db, "jobs"); n != 3 {
		t.Errorf("jobs: %d rows, want 3", n)
	}
	if b := backups(path); len(b) != 1 {
		t.Errorf("a backup is made before trying: %v", b)
	}
	if !strings.Contains(err.Error(), ".bak-v0") {
		t.Errorf("message should say where the backup is: %q", err)
	}
}

func TestBackupsAreLimitedToTheNewest(t *testing.T) {
	path := legacyDB(t)
	// Old backups from earlier upgrades, oldest first.
	for i, name := range []string{"bak-v90", "bak-v91", "bak-v92", "bak-v93"} {
		f := path + "." + name
		if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(time.Duration(-10+i) * time.Hour)
		os.Chtimes(f, mt, mt)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	got := backups(path)
	if len(got) != keepBackups {
		t.Fatalf("kept %d backups, want %d: %v", len(got), keepBackups, got)
	}
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, ".bak-v0") {
		t.Errorf("the new backup must survive pruning: %v", got)
	}
	if strings.Contains(joined, "bak-v90") || strings.Contains(joined, "bak-v91") {
		t.Errorf("the oldest backups should have been removed: %v", got)
	}
}

func TestFailedBackupLeavesNoFileAndNoUpgrade(t *testing.T) {
	path := legacyDB(t)
	// A directory where the backup file should go makes the copy fail (like a full disk would).
	if err := os.Mkdir(path+".bak-v0", 0o755); err != nil {
		t.Fatal(err)
	}
	// os.Remove of an empty directory succeeds, so block that too with a file inside.
	if err := os.WriteFile(filepath.Join(path+".bak-v0", "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open must fail when the backup cannot be made")
	}
	if !strings.Contains(err.Error(), "not upgrading") {
		t.Errorf("message should say the upgrade did not happen: %q", err)
	}
	db := rawDB(t, path)
	if version(t, db) != 0 || tableExists(t, db, "app_meta") || count(t, db, "jobs") != 3 {
		t.Error("the database must be unchanged when the backup fails")
	}
}

func TestBackupFailureCleansUpPartialFile(t *testing.T) {
	// VACUUM INTO an unwritable place fails; no stray file remains at the target.
	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.path = filepath.Join(t.TempDir(), "no-such-dir", "syp.db")
	if _, err := s.backup(7); err == nil {
		t.Fatal("expected the backup to fail")
	}
	if _, err := os.Stat(s.path + ".bak-v7"); err == nil {
		t.Error("a failed backup left a file behind")
	}
}

// Migration 7 on an upgraded install: skips recorded before reason codes get their code from the
// decision text; a skip whose text maps to none is forgotten so the next scan records it afresh
// (review ce336a6: otherwise every existing skip stays out of the Skipped list).
func TestMigration7BackfillsSkipReasons(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// back to version 6: as an install from before 21b left it
	for _, q := range []string{`DROP INDEX files_reason`, `DROP INDEX jobs_output`, `ALTER TABLE files DROP COLUMN reason`,
		`ALTER TABLE jobs DROP COLUMN started`, `ALTER TABLE jobs DROP COLUMN output_mtime`, `PRAGMA user_version=6`} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	old := map[string]string{
		"/m/a.mkv": "skip: HEVC is already an efficient format",
		"/m/b.mkv": "skip: codec is hevc, rule wants h264",
		"/m/c.mkv": "skip: codec is mpeg4, rule wants h264",
		"/m/d.mkv": "skip: bitrate 3000 kbps is below 4000 kbps",
		"/m/e.mkv": "skip: already efficient: 0.022 bits per pixel, Smallest needs more than 0.050",
		"/m/f.mkv": "skip: vc1 is not a format the slider converts",
		"/m/g.mkv": "skip: it has no video",
		"/m/h.mkv": "skip: its bitrate, picture size or frame rate is unknown, so its saving cannot be judged",
		"/m/i.mkv": "skip: its hdmv_pgs_subtitle subtitles cannot be kept in mov,mp4,m4a,3gp,3g2,mj2",
		"/m/j.mkv": "unreadable",
		"/m/k.mkv": "skip: something a later version will say",
		"/m/l.mkv": "queued: H.264 at 9000 kbps",
	}
	for p, d := range old {
		s.db.Exec(`INSERT INTO files(path,size,mtime,codec,decision) VALUES(?,1,1,'',?)`, p, d)
	}
	// done jobs: one whose output has a file record of its size (its time is recovered), one without
	s.db.Exec(`INSERT INTO files(path,size,mtime,codec,decision) VALUES('/m/out.mkv',400,777,'hevc','skip: HEVC is already an efficient format')`)
	for _, p := range []string{"/m/out.mkv", "/m/nofile.mkv"} {
		s.db.Exec(`INSERT INTO jobs(library,path,status,size_before,size_after,output_path,created,updated) VALUES('/m',?,'done',1000,400,?,'t','t')`, p, p)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	want := map[string]string{"/m/a.mkv": "already_hevc", "/m/b.mkv": "already_hevc", "/m/c.mkv": "format_not_converted",
		"/m/d.mkv": "already_efficient", "/m/e.mkv": "already_efficient", "/m/f.mkv": "format_not_converted",
		"/m/g.mkv": "no_video", "/m/h.mkv": "unknown_bitrate", "/m/i.mkv": "cannot_keep_track", "/m/j.mkv": "unreadable", "/m/l.mkv": ""}
	for p, r := range want {
		if f, _ := s.GetFile(p); f == nil || f.Reason != r {
			t.Errorf("%s: %+v, want reason %q", p, f, r)
		}
	}
	if f, _ := s.GetFile("/m/k.mkv"); f != nil {
		t.Errorf("an unmapped skip is forgotten: %+v", f)
	}
	for p, want := range map[string]int64{"/m/out.mkv": 777, "/m/nofile.mkv": 0} {
		var got int64
		s.db.QueryRow(`SELECT output_mtime FROM jobs WHERE path=?`, p).Scan(&got)
		if got != want {
			t.Errorf("%s: output_mtime %d, want %d", p, got, want)
		}
	}
	// the recovered one hides its output from Skipped; the unknown one hides nothing
	var skipped int
	s.db.QueryRow(`SELECT COUNT(*) FROM files WHERE reason<>'' AND NOT ` + convertedSQL).Scan(&skipped)
	if skipped != 10 {
		t.Errorf("%d skipped, want the 10 mapped ones (out.mkv is the conversion's own output)", skipped)
	}
}
