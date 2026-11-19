// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func restoreTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenAs(filepath.Join(t.TempDir(), "syp.db"), "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func restoreExec(t *testing.T, s *Store, q string, args ...any) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func TestRestoreRoundTripPutsTheDataBack(t *testing.T) {
	s := restoreTestStore(t)
	pw, nonce := []byte{1, 2, 3, 0xff}, []byte{9, 8, 7}
	restoreExec(t, s, `INSERT INTO app_meta(key,value) VALUES('probe','before')`)
	restoreExec(t, s, `INSERT INTO shares(id,type,address,name,encrypted_password,auth_nonce,created,updated) VALUES(1,'smb','//h/x','n',?,?,'c','u')`, pw, nonce)
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.backupDir(), b.ID+".json.gz")

	// Change the data after the backup.
	restoreExec(t, s, `UPDATE app_meta SET value='after' WHERE key='probe'`)
	restoreExec(t, s, `INSERT INTO app_meta(key,value) VALUES('extra','x')`)
	restoreExec(t, s, `DELETE FROM shares`)

	p, err := s.PreviewBackup(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Contents.Shares != 1 || p.FormatVersion != backupFormat || p.AppVersion != "0.9.0" || len(p.Warnings) != 1 || p.Warnings[0] != workerSignInWarning {
		t.Fatalf("preview %+v", p)
	}

	got, err := s.RestoreBackup(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Shares != 1 {
		t.Errorf("restored contents %+v", got)
	}
	var probe string
	if err := s.db.QueryRow(`SELECT value FROM app_meta WHERE key='probe'`).Scan(&probe); err != nil || probe != "before" {
		t.Errorf("probe = %q, %v; want before", probe, err)
	}
	var extra int
	s.db.QueryRow(`SELECT COUNT(*) FROM app_meta WHERE key='extra'`).Scan(&extra)
	if extra != 0 {
		t.Errorf("a row added after the backup survived the restore")
	}
	var gotPW, gotNonce []byte
	if err := s.db.QueryRow(`SELECT encrypted_password, auth_nonce FROM shares WHERE id=1`).Scan(&gotPW, &gotNonce); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotPW, pw) || !bytes.Equal(gotNonce, nonce) {
		t.Errorf("BLOB columns came back as %v, %v", gotPW, gotNonce)
	}
}

func TestRestoreRefusesWhileAJobRuns(t *testing.T) {
	s := restoreTestStore(t)
	restoreExec(t, s, `INSERT INTO app_meta(key,value) VALUES('probe','before')`)
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.backupDir(), b.ID+".json.gz")
	restoreExec(t, s, `UPDATE app_meta SET value='after' WHERE key='probe'`)
	restoreExec(t, s, `INSERT INTO jobs(library,path,status,size_before,size_after,created,updated) VALUES('/m','/m/x.mkv','running',1,1,'t','t')`)

	if _, err := s.RestoreBackup(path); !errors.Is(err, ErrJobsRunning) {
		t.Fatalf("restore with a running job: %v, want ErrJobsRunning", err)
	}
	var probe string
	s.db.QueryRow(`SELECT value FROM app_meta WHERE key='probe'`).Scan(&probe)
	if probe != "after" {
		t.Errorf("a refused restore changed the data: probe = %q", probe)
	}
	if n, err := s.CountRunning(); err != nil || n != 1 {
		t.Errorf("CountRunning = %d, %v; want 1", n, err)
	}
}

func TestRestoreRefusesWhatIsNotABackupAndANewerSchema(t *testing.T) {
	s := restoreTestStore(t)
	junk := filepath.Join(t.TempDir(), "junk.json.gz")
	if err := os.WriteFile(junk, []byte("hello, not a backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PreviewBackup(junk); !errors.Is(err, ErrNotABackup) {
		t.Errorf("preview of junk: %v, want ErrNotABackup", err)
	}
	if _, err := s.RestoreBackup(junk); !errors.Is(err, ErrNotABackup) {
		t.Errorf("restore of junk: %v, want ErrNotABackup", err)
	}

	// A backup of a schema one step ahead of this database.
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.backupDir(), b.ID+".json.gz")
	restoreExec(t, s, `PRAGMA user_version = `+strconv.Itoa(latestVersion()-1))
	var tooNew *TooNewError
	if _, err := s.PreviewBackup(path); !errors.As(err, &tooNew) {
		t.Errorf("preview of a newer schema: %v, want TooNewError", err)
	}
	if _, err := s.RestoreBackup(path); !errors.As(err, &tooNew) {
		t.Errorf("restore of a newer schema: %v, want TooNewError", err)
	}
}

// addRemoteWorker writes a remote worker row the way pairing does, with the sign-in token's hash.
func addRemoteWorker(t *testing.T, s *Store, id, token string) {
	t.Helper()
	restoreExec(t, s, `INSERT INTO workers(id, name, kind, token_hash, version, api_level, last_seen, capabilities, os, arch, enabled)
		VALUES(?,?,'remote',?,'1',1,'','','linux','amd64',1)`, id, id, hashOf(token))
}

func TestRestoreKeepsWorkerSignIns(t *testing.T) {
	s := restoreTestStore(t)
	addRemoteWorker(t, s, "gone", "tok-gone")
	restoreExec(t, s, `INSERT INTO worker_paths(worker_id, server, worker, mode) VALUES('gone','srv','w','map')`)
	addRemoteWorker(t, s, "kept", "tok-old")
	restoreExec(t, s, `INSERT INTO worker_pairings(code_hash, expires) VALUES('code-then', '2099-01-01T00:00:00Z')`)
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.backupDir(), b.ID+".json.gz")

	// After the backup: one worker is removed (revoked), one gets a new sign-in, and a pairing code is issued.
	if err := s.RemoveWorker("gone"); err != nil {
		t.Fatal(err)
	}
	// Its path mappings are gone too, so only the backup still has them.
	restoreExec(t, s, `DELETE FROM worker_paths WHERE worker_id='gone'`)
	restoreExec(t, s, `UPDATE workers SET token_hash=? WHERE id='kept'`, hashOf("tok-now"))
	restoreExec(t, s, `DELETE FROM worker_pairings WHERE code_hash='code-then'`)
	restoreExec(t, s, `INSERT INTO worker_pairings(code_hash, expires) VALUES('code-now', '2099-01-01T00:00:00Z')`)
	addRemoteWorker(t, s, "newer", "tok-newer") // paired after the backup
	restoreExec(t, s, `INSERT INTO worker_paths(worker_id, server, worker, mode) VALUES('newer','srv','w','map')`)
	restoreExec(t, s, `INSERT INTO join_requests(id, secret_hash, name, asked_ns, expires_ns) VALUES('jr-new','h','n',1,2)`)

	p, err := s.PreviewBackup(path)
	if err != nil {
		t.Fatal(err)
	}
	const wantWarning = "Workers are kept as they are now. A restore doesn't add, remove or change workers, so pair again any worker that isn't listed."
	if len(p.Warnings) == 0 || p.Warnings[len(p.Warnings)-1] != wantWarning {
		t.Errorf("the preview's worker warning: %v", p.Warnings)
	}
	if _, err := s.RestoreBackup(path); err != nil {
		t.Fatal(err)
	}
	// (a) a worker paired after the backup keeps its sign-in
	if _, err := s.WorkerByToken("tok-newer"); err != nil {
		t.Errorf("a worker paired after the backup can't sign in after a restore: %v", err)
	}
	// (b) a worker removed after the backup is not listed after a restore
	var gone int
	s.db.QueryRow(`SELECT COUNT(*) FROM workers WHERE id='gone'`).Scan(&gone)
	if gone != 0 {
		t.Errorf("a worker removed after the backup is listed again")
	}
	// (a) a worker paired after the backup keeps its path mappings; (b) a removed worker gets none back;
	// (c) a join request made after the backup is still there
	var paths, goneRows, joins int
	s.db.QueryRow(`SELECT COUNT(*) FROM worker_paths WHERE worker_id='newer'`).Scan(&paths)
	s.db.QueryRow(`SELECT COUNT(*) FROM worker_paths WHERE worker_id='gone'`).Scan(&goneRows)
	s.db.QueryRow(`SELECT COUNT(*) FROM join_requests WHERE id='jr-new'`).Scan(&joins)
	if paths != 1 || goneRows != 0 || joins != 1 {
		t.Errorf("worker paths of a newer worker %d (want 1), of a removed worker %d (want 0), a join request made after the backup %d (want 1)", paths, goneRows, joins)
	}
	if _, err := s.WorkerByToken("tok-gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a worker revoked after the backup signs in again: %v", err)
	}
	if _, err := s.WorkerByToken("tok-now"); err != nil {
		t.Errorf("the current sign-in stopped working: %v", err)
	}
	if _, err := s.WorkerByToken("tok-old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("the sign-in from the backup came back: %v", err)
	}
	var pairings int
	s.db.QueryRow(`SELECT COUNT(*) FROM worker_pairings WHERE code_hash='code-now'`).Scan(&pairings)
	if pairings != 1 {
		t.Errorf("a pairing code issued after the backup was lost")
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM worker_pairings WHERE code_hash='code-then'`).Scan(&pairings)
	if pairings != 0 {
		t.Errorf("a pairing code from the backup came back")
	}
}

// probeBackup writes a backup file at the header of a real backup, then one app_meta value of size bytes.
func probeBackup(t *testing.T, s *Store, size int) string {
	t.Helper()
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(s.backupDir(), b.ID+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	whole, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	head := whole[:bytes.Index(whole, []byte(`"tables":`))]
	path := filepath.Join(s.backupDir(), "probe.json.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := gzip.NewWriter(f)
	zw.Write(head)
	zw.Write([]byte(`"tables":{"app_meta":[{"key":"big","value":"`))
	chunk := bytes.Repeat([]byte("a"), 1<<20)
	for n := 0; n < size; n += len(chunk) {
		zw.Write(chunk)
	}
	zw.Write([]byte(`"}]}}`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestoreRefusesAHugeValueWithBoundedMemory(t *testing.T) {
	s := restoreTestStore(t)
	restoreExec(t, s, `INSERT INTO app_meta(key,value) VALUES('probe','before')`)
	path := probeBackup(t, s, 256<<20)

	// Each call is measured on its own: the value is 256 MiB, and a refused read must allocate a small
	// multiple of the row cap (16 MiB), not the value.
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := s.PreviewBackup(path)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("preview of a 256 MiB value: %v, want ErrTooLarge", err)
	}
	if used := after.TotalAlloc - before.TotalAlloc; used > 96<<20 {
		t.Errorf("previewing the value allocated %d MiB; it should stay bounded", used>>20)
	}
	runtime.ReadMemStats(&before)
	_, err = s.RestoreBackup(path)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("restore of a 256 MiB value: %v, want ErrTooLarge", err)
	}
	if used := after.TotalAlloc - before.TotalAlloc; used > 96<<20 {
		t.Errorf("restoring the value allocated %d MiB; it should stay bounded", used>>20)
	}
	var probe string
	s.db.QueryRow(`SELECT value FROM app_meta WHERE key='probe'`).Scan(&probe)
	if probe != "before" {
		t.Errorf("a refused restore changed the data: probe = %q", probe)
	}
}

func TestStagedCopyIsNotListedAsABackup(t *testing.T) {
	s := restoreTestStore(t)
	staged, err := s.StageBackup(strings.NewReader("copy"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(staged)
	list, err := s.ListBackups()
	if err != nil || len(list) != 0 {
		t.Errorf("list with a staged copy: %v, %v", list, err)
	}
	if b, err := os.ReadFile(staged); err != nil || string(b) != "copy" {
		t.Errorf("staged copy = %q, %v", b, err)
	}
}

func TestBackupFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not owner-only on Windows")
	}
	s := restoreTestStore(t)
	b, err := s.WriteBackup(ReasonManual)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := s.StageBackup(strings.NewReader("copy"))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(staged)
	for _, p := range []string{filepath.Join(s.backupDir(), b.ID+".json.gz"), staged} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if m := st.Mode().Perm(); m != 0o600 {
			t.Errorf("%s is %o, want 600", filepath.Base(p), m)
		}
	}
}

func TestStaleFilesAreRemovedWhenTheStoreOpens(t *testing.T) {
	s := restoreTestStore(t)
	if _, err := s.WriteBackup(ReasonManual); err != nil {
		t.Fatal(err)
	}
	torn := filepath.Join(s.backupDir(), "20261119-100000-abcd-manual.json.gz.tmp")
	part := filepath.Join(s.backupDir(), "preview-1.part")
	for _, p := range []string{torn, part} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	s2, err := OpenAs(s.path, "0.9.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	for _, p := range []string{torn, part} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was kept when the store opened", filepath.Base(p))
		}
	}
	if list, err := s2.ListBackups(); err != nil || len(list) != 1 {
		t.Errorf("the finished backup went: %v %v", list, err)
	}
}

func TestStageBackupRefusesOverTheLimit(t *testing.T) {
	s := restoreTestStore(t)
	old := MaxBackupBytes
	MaxBackupBytes = 10
	t.Cleanup(func() { MaxBackupBytes = old })
	if _, err := s.StageBackup(strings.NewReader("12345678901")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v, want ErrTooLarge", err)
	}
	if left, _ := filepath.Glob(filepath.Join(s.backupDir(), "preview-*.part")); len(left) != 0 {
		t.Errorf("an over-limit copy was kept: %v", left)
	}
	staged, err := s.StageBackup(strings.NewReader("1234567890"))
	if err != nil {
		t.Fatalf("exactly at the limit: %v", err)
	}
	os.Remove(staged)
}
