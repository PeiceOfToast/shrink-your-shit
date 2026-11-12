// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/shrink-your-shit/shrink-your-shit/internal/media"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
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
		`ALTER TABLE jobs DROP COLUMN started`, `ALTER TABLE jobs DROP COLUMN output_mtime`,
		`ALTER TABLE originals DROP COLUMN aside`, `ALTER TABLE jobs DROP COLUMN stop_requested`, `DROP TABLE sidecars`, // and migrations 8, 9 and 10
		`ALTER TABLE originals DROP COLUMN new_path`,                                     // and 11
		`ALTER TABLE jobs DROP COLUMN attempts`, `ALTER TABLE jobs DROP COLUMN retry_at`, // and 12
		`ALTER TABLE libraries DROP COLUMN last_scan`, `ALTER TABLE libraries DROP COLUMN last_scan_videos`, // and 15
		// (16 changes rows only)
		// (13 changes rows only)
		`ALTER TABLE flows DROP COLUMN description`, `ALTER TABLE flows DROP COLUMN status`, `ALTER TABLE flows DROP COLUMN favourite`,
		`ALTER TABLE flows DROP COLUMN tags`, `DROP TABLE workflow_revisions`, // and 20
		`DROP TRIGGER events_job_insert`, `DROP TRIGGER events_job_status`, `DROP TRIGGER events_job_removed`, `DROP INDEX jobs_claim`, `DROP TABLE events`, `ALTER TABLE jobs DROP COLUMN priority`, // and 21
		`ALTER TABLE jobs DROP COLUMN error_code`, // and 23
		`DROP TABLE workers`,                      // and 24
		`PRAGMA user_version=6`} {
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
	want := map[string]string{"/m/c.mkv": "format_not_converted",
		"/m/f.mkv": "format_not_converted",
		"/m/g.mkv": "no_video", "/m/i.mkv": "cannot_keep_track", "/m/j.mkv": "unreadable", "/m/l.mkv": ""}
	for p, r := range want {
		if f, _ := s.GetFile(p); f == nil || f.Reason != r {
			t.Errorf("%s: %+v, want reason %q", p, f, r)
		}
	}
	// an unmapped skip is forgotten (7); so are the skips always standardize removed (13): already
	// efficient (d, e) and unknown bitrate (h), so the next scan looks at those files again; and S4's
	// (16) already HEVC (a, b, mapped by 7), which Basic now repackages
	for _, p := range []string{"/m/k.mkv", "/m/d.mkv", "/m/e.mkv", "/m/h.mkv", "/m/a.mkv", "/m/b.mkv"} {
		if f, _ := s.GetFile(p); f != nil {
			t.Errorf("%s should be forgotten: %+v", p, f)
		}
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
	if skipped != 5 {
		t.Errorf("%d skipped, want the 5 mapped ones still skipped (out.mkv is the conversion's own output; a and b are forgotten by 16)", skipped)
	}
}

// Migration 13 (always standardize) runs once: jobs kept only for saving too little are queued again
// with their tries reset, other kept jobs stay; file decisions of the removed skips are forgotten, other
// skips stay. A second start changes nothing more.
func TestAlwaysStandardizeMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.SetPaused(false) // claims below (S2: a new database starts with its worker stopped)
	kept := map[string]string{
		"/m/less.mkv":    "the new file (1.0 MB) saves less than the required 20% of the original (1.1 MB)",
		"/m/bigger.mkv":  "the new file (2.0 MB) is not smaller than the original (1.1 MB)",
		"/m/picture.mkv": "the picture of the new file is too different from the original (similarity 0.800 at 0:10, 0.95 is required)",
	}
	ids := map[string]int64{}
	for p, why := range kept {
		id, _ := s.Enqueue(Job{Path: p})
		s.Claim("w")
		s.Keep(id, "kept_original", why)
		ids[p] = id
	}
	for p, reason := range map[string]string{"/m/small.mkv": "already_efficient", "/m/wns.mkv": "would_not_shrink",
		"/m/unk.mkv": "unknown_bitrate", "/m/hevc.mkv": "already_hevc"} {
		s.PutFile(File{Path: p, Decision: "skip: x", Reason: reason})
	}
	rewind15(s)                           // migration 15's columns go, so it can run again
	rewind20(s)                           // migration 20's columns and table go, so it can run again
	s.db.Exec(`PRAGMA user_version = 12`) // as an install from before always standardize left it
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]Status{"/m/less.mkv": Queued, "/m/bigger.mkv": Queued, "/m/picture.mkv": Kept} {
		j, _ := s.Get(ids[p])
		if j.Status != want || (want == Queued && (j.Attempts != 0 || j.Error != "" || j.RetryAt != "")) {
			t.Errorf("%s: %+v", p, j)
		}
	}
	for p, want := range map[string]bool{"/m/small.mkv": false, "/m/wns.mkv": false, "/m/unk.mkv": false, "/m/hevc.mkv": false} { // hevc: forgotten by S4's 16
		if f, _ := s.GetFile(p); (f != nil) != want {
			t.Errorf("%s: record %+v, want kept %v", p, f, want)
		}
	}
	// a second start: the migration does not run again
	// (set back to kept directly: claiming "the next job" would pick whichever was queued first)
	s.db.Exec(`UPDATE jobs SET status='kept', error=? WHERE id=?`, kept["/m/less.mkv"], ids["/m/less.mkv"])
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if j, _ := s.Get(ids["/m/less.mkv"]); j.Status != Kept {
		t.Errorf("queued again on a second start: %+v", j)
	}
}

// Migration 14 (S2): every install starts with its worker stopped; the one-time notice only for an
// existing install (a version from 1 to 13 before the update, whatever rows it has: migration 13 may
// have just removed some, review e1cd926) whose worker was running. A brand-new database gets none; a
// paused one gets none.
// Starting (or Got it) clears it, and it stays cleared after a restart.
func TestWorkersStartStoppedMigration(t *testing.T) {
	fresh := open(t)
	fresh.SetPaused(true) // open() starts it for tests; as migrated, before that:
	newDB, err := Open(filepath.Join(t.TempDir(), "new.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { newDB.Close() }) // closed before TempDir is removed (Windows will not remove an open file)
	if !newDB.Paused() || newDB.StoppedByUpdate() || newDB.BasicChangedByUpdate() {
		t.Errorf("a new install: paused %v, notice %v", newDB.Paused(), newDB.StoppedByUpdate())
	}
	upgrade := func(paused string) *Store {
		path := filepath.Join(t.TempDir(), "syp.db")
		s, _ := Open(path) // no libraries and no jobs: an existing install is told by its version, not its rows
		s.SetMeta("paused", paused)
		s.db.Exec(`DELETE FROM app_meta WHERE key='stopped_by_update'`)
		rewind15(s)                           // migration 15's columns go, so it can run again
		rewind20(s)                           // migration 20's columns and table go, so it can run again
		s.db.Exec(`PRAGMA user_version = 13`) // as an install from before S2 left it
		s.Close()
		s, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	if s := upgrade("0"); !s.Paused() || !s.StoppedByUpdate() || s.BasicChangedByUpdate() {
		t.Errorf("a running install after the update: paused %v, notice %v", s.Paused(), s.StoppedByUpdate())
	}
	if s := upgrade("1"); !s.Paused() || s.StoppedByUpdate() {
		t.Errorf("a paused install after the update: paused %v, notice %v", s.Paused(), s.StoppedByUpdate())
	}
	s := upgrade("0")
	s.SetMeta("basic_changed_by_update", "1") // as S4's migration will
	if err := s.ClearStoppedByUpdate(); err != nil || s.StoppedByUpdate() || s.BasicChangedByUpdate() {
		t.Errorf("cleared: %v %v %v", err, s.StoppedByUpdate(), s.BasicChangedByUpdate())
	}
}

// rewind20 takes away what migrations 20 to 23 added, for tests that set user_version below it.
func rewind20(s *Store) {
	for _, q := range []string{`ALTER TABLE flows DROP COLUMN description`, `ALTER TABLE flows DROP COLUMN status`,
		`ALTER TABLE flows DROP COLUMN favourite`, `ALTER TABLE flows DROP COLUMN tags`, `DROP TABLE workflow_revisions`,
		`DROP TRIGGER events_job_insert`, `DROP TRIGGER events_job_status`, `DROP TRIGGER events_job_removed`, `DROP INDEX jobs_claim`, `DROP TABLE events`, `ALTER TABLE jobs DROP COLUMN priority`, // and 21
		`ALTER TABLE jobs DROP COLUMN error_code`, // and 23
		`DROP TABLE workers`} {                    // and 24
		s.db.Exec(q)
	}
}

// rewind15 takes away what migration 15 added, for tests that set user_version below it.
func rewind15(s *Store) {
	s.db.Exec(`ALTER TABLE libraries DROP COLUMN last_scan`)
	s.db.Exec(`ALTER TABLE libraries DROP COLUMN last_scan_videos`)
}

// Migration 16 (S4, design's call): an existing Basic (slider) flow keeps every audio track
// (audio keep_all) and the install gets the notice's Basic sentence; a new install neither.
func TestExistingBasicFlowsMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l, _ := s.AddLibrary("Movies", "/m", rules.SliderTemplate(3), true)
	s.db.Exec(`UPDATE flows SET template=json_remove(template, '$.params.audio')`) // as before S4
	s.db.Exec(`DELETE FROM app_meta WHERE key='basic_changed_by_update'`)
	s.PutFile(File{Path: "/m/hevc.mp4", Size: 1, Decision: "skip: x", Reason: "already_hevc"})
	s.PutFile(File{Path: "/m/av1.mkv", Size: 1, Decision: "skip: x", Reason: "already_efficient_x"})
	rewind20(s) // migration 20's columns and table go, so it can run again
	s.db.Exec(`PRAGMA user_version = 15`)
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if r, _ := s.GetFile("/m/hevc.mp4"); r != nil {
		t.Errorf("an HEVC file skipped before S4 is still decided: %+v", r) // Basic repackages it now
	}
	if r, _ := s.GetFile("/m/av1.mkv"); r == nil {
		t.Error("another skip was forgotten too")
	}
	f, _ := s.GetFlow(l.FlowID)
	if f.Template.Kind != rules.KindSlider || f.Template.Params.Audio != "keep_all" || !s.BasicChangedByUpdate() {
		t.Errorf("existing Basic flow: %+v, notice %v", f.Template, s.BasicChangedByUpdate())
	}
	fresh := open(t)
	if fresh.BasicChangedByUpdate() {
		t.Error("a new install got the Basic sentence")
	}
}

// Migration 17 (S3): a Standard flow's skipHdr becomes includeDolbyVision (skipping HDR: Dolby Vision left
// as it is; converting HDR: Dolby Vision included), and the files skipped as hdr_dv are decided again.
func TestHDR10KeptMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	skip, conv := rules.DefaultSelectors(3), rules.DefaultSelectors(3)
	skip.Speed, conv.Speed = "slower", "slower" // not the slider's values, so they are saved as Standard flows
	a, _ := s.AddLibrary("Skips HDR", "/a", skip.Template(), true)
	b, _ := s.AddLibrary("Converts HDR", "/b", conv.Template(), true)
	c, _ := s.AddLibrary("Basic", "/c", rules.SliderTemplate(3), true)
	// as before S3: skipHdr true and false, and no includeDolbyVision
	s.db.Exec(`UPDATE flows SET template=json_set(json_remove(template, '$.selectors.includeDolbyVision'), '$.selectors.skipHdr', json('true')) WHERE id=?`, a.FlowID)
	s.db.Exec(`UPDATE flows SET template=json_set(json_remove(template, '$.selectors.includeDolbyVision'), '$.selectors.skipHdr', json('false')) WHERE id=?`, b.FlowID)
	s.PutFile(File{Path: "/a/hdr.mkv", Size: 1, Decision: "skip: x", Reason: "hdr_dv"})
	s.PutFile(File{Path: "/a/x.mkv", Size: 1, Decision: "skip: x", Reason: "no_video"})
	rewind20(s) // migration 20's columns and table go, so it can run again
	s.db.Exec(`PRAGMA user_version = 16`)
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id, want := range map[int64]bool{a.FlowID: false, b.FlowID: true} {
		var raw string
		s.db.QueryRow(`SELECT template FROM flows WHERE id=?`, id).Scan(&raw)
		f, _ := s.GetFlow(id)
		if f.Template.Selectors == nil || f.Template.Selectors.IncludeDolbyVision != want || strings.Contains(raw, "skipHdr") {
			t.Errorf("flow %d: %s, want includeDolbyVision %v", id, raw, want)
		}
	}
	if f, _ := s.GetFlow(c.FlowID); f.Template.Kind != rules.KindSlider || f.Template.Selectors != nil {
		t.Errorf("Basic flow changed: %+v", f.Template)
	}
	if r, _ := s.GetFile("/a/hdr.mkv"); r != nil {
		t.Errorf("an HDR file skipped before S3 is still decided: %+v", r)
	}
	if r, _ := s.GetFile("/a/x.mkv"); r == nil {
		t.Error("another skip was forgotten too")
	}
}

// Migration 19 (S6): Standard flows' saved values in S6's terms; the notes line where the audio's result
// changes; already_hevc decisions forgotten.
func TestStandardReworkMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	type was struct{ audio, cap string }
	old := map[string]was{"/a": {"keep_all", "none"}, "/b": {"my_languages", "10"}, "/c": {"aac", "4"}, "/d": {"lossless_only", "40"}, "/e": {"stereo_aac", "low"}}
	ids := map[string]int64{}
	for p, w := range old {
		sel := rules.DefaultSelectors(3)
		sel.Speed, sel.MaxBitrate = "slower", w.cap // /e is saved in S6's terms already
		l, _ := s.AddLibrary(p, p, sel.Template(), true)
		ids[p] = l.FlowID
		if p != "/e" { // as saved before S6
			s.db.Exec(`UPDATE flows SET template=json_set(json_remove(template, '$.selectors.onlyMyLanguages'), '$.selectors.audio', ?, '$.selectors.maxBitrate', ?) WHERE id=?`, w.audio, w.cap, l.FlowID)
		}
	}
	s.PutFile(File{Path: "/a/x.mkv", Size: 1, Decision: "skip: x", Reason: "already_hevc"})
	rewind20(s) // migration 20's columns and table go, so it can run again
	s.db.Exec(`PRAGMA user_version = 18`)
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for p, want := range map[string]string{
		"/a": "as_is false none []",
		"/b": "as_is true high []",
		"/c": "stereo_aac false medium [Audio choices changed in this update: your flow now uses Stereo AAC.]",
		"/d": "eac3_surround false high [Audio choices changed in this update: your flow now uses Surround E-AC-3.]",
		"/e": "stereo_aac true low []", // already S6's: untouched
	} {
		f, _ := s.GetFlow(ids[p])
		sel := f.Template.Selectors
		if got := fmt.Sprint(sel.Audio, " ", sel.OnlyMyLanguages, " ", sel.MaxBitrate, " ", sel.Notes); got != want {
			t.Errorf("%s: %s, want %s", p, got, want)
		}
	}
	if r, _ := s.GetFile("/a/x.mkv"); r != nil {
		t.Errorf("already_hevc kept: %+v", r)
	}
}

// Review 6cafc6f (P1): a 25b flow with Opus in MP4 (valid then) moves to MKV, with design's sentence,
// so it still runs.
func TestStandardReworkOpusMP4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "syp.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	sel := rules.DefaultSelectors(3)
	sel.Speed = "slower"
	l, _ := s.AddLibrary("M", "/m", sel.Template(), true)
	s.db.Exec(`UPDATE flows SET template=json_set(json_remove(template, '$.selectors.onlyMyLanguages'), '$.selectors.audio', 'opus', '$.selectors.fileType', 'mp4', '$.selectors.afterConverting', 'beside') WHERE id=?`, l.FlowID)
	rewind20(s) // migration 20's columns and table go, so it can run again
	s.db.Exec(`PRAGMA user_version = 18`)
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	f, _ := s.GetFlow(l.FlowID)
	sel2 := f.Template.Selectors
	if sel2.Audio != "stereo_opus" || sel2.FileType != "mkv" || len(sel2.Notes) != 2 || sel2.Notes[1] != "File type changed to MKV: MP4 files can't hold Opus." || sel2.Validate() != nil {
		t.Errorf("%+v %v", sel2, sel2.Validate())
	}
	if p := f.Template.Compile(media.Info{Container: "mov,mp4,m4a,3gp,3g2,mj2", Streams: []media.Stream{{Type: "video", Codec: "h264", Width: 640, Height: 360}}}); p.SkipCode == rules.SkipSettingCannotRun {
		t.Errorf("the migrated flow can't run: %q", p.Skip)
	}
}
