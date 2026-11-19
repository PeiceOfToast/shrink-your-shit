// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
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
	if p.Contents.Shares != 1 || p.FormatVersion != backupFormat || p.AppVersion != "0.9.0" || len(p.Warnings) != 0 {
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
