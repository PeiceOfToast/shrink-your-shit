// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package share

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	// Migration 4 makes the libraries table that migration 16 adds a column to.
	_ "github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// xorSealer is a reversible stand-in for the access token's sealer: the tests care that the store
// calls it (and that the plain password is not in the row), not that GCM works. internal/system's
// own test covers the real one.
type xorSealer struct{}

func (xorSealer) Seal(plain []byte) ([]byte, error) {
	out := append([]byte{0x7e}, plain...)
	for i := 1; i < len(out); i++ {
		out[i] ^= 0x5a
	}
	return out, nil
}

func (xorSealer) Unseal(sealed []byte) ([]byte, error) {
	if len(sealed) == 0 || sealed[0] != 0x7e {
		return nil, errors.New("not a sealed value")
	}
	out := append([]byte(nil), sealed[1:]...)
	for i := range out {
		out[i] ^= 0x5a
	}
	return out, nil
}

var testClock = time.Date(2026, 11, 28, 9, 0, 0, 0, time.UTC)

func openStore(t *testing.T) (*Store, *store.DB) {
	t.Helper()
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	s := Open(db, xorSealer{})
	s.Now = func() time.Time { return testClock }
	return s, db
}

// fakeMounter records the specs and answers what a test sets.
type fakeMounter struct {
	point   string
	fail    map[string]*MountError
	mounts  []Spec
	unmount []Spec
}

func newTestMounter(t *testing.T) *fakeMounter {
	return &fakeMounter{point: t.TempDir(), fail: map[string]*MountError{}}
}

func (m *fakeMounter) Mount(_ context.Context, spec Spec) (string, error) {
	m.mounts = append(m.mounts, spec)
	if err := m.fail[spec.Address.Source]; err != nil {
		return "", err
	}
	if spec.ID == 0 {
		return m.point, nil
	}
	return fmt.Sprintf("/var/lib/syp/shares/%d", spec.ID), nil
}

func (m *fakeMounter) Unmount(_ context.Context, spec Spec) error {
	m.unmount = append(m.unmount, spec)
	return nil
}

// TestStoreSealsThePassword: the row keeps no readable password, the flag says one is kept, and
// only the store's opener hands it back.
func TestStoreSealsThePassword(t *testing.T) {
	st, db := openStore(t)
	ctx := context.Background()
	addr, err := ParseAddress("//nas/media/Films", "")
	if err != nil {
		t.Fatal(err)
	}
	const password = "hunter2-hunter2"
	sh, err := st.Insert(ctx, NewShare{
		Name: "Films", Address: addr, Username: `WORKGROUP\viewer`, Password: []byte(password),
	})
	if err != nil {
		t.Fatal(err)
	}
	if sh.ID != 1 || sh.Address != "//nas/media" || sh.Sub != "Films" || !sh.HasPassword {
		t.Errorf("share = %+v", sh)
	}
	if !sh.Created.Equal(testClock) {
		t.Errorf("created = %v, want the clock", sh.Created)
	}
	var sealed []byte
	if err := db.Read().QueryRow(`SELECT password_sealed FROM shares WHERE id = 1`).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(password)) {
		t.Fatal("the stored password is readable in clear")
	}
	got, err := st.Password(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != password {
		t.Errorf("Password = %q, want the plain password", got)
	}

	// A failed mount keeps the last point and records the sentence; a good one clears both.
	if err := st.SetConnection(ctx, sh.ID, false, "", "The share did not answer."); err != nil {
		t.Fatal(err)
	}
	sh, err = st.Get(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Connected || sh.Reason != "The share did not answer." || sh.Since == nil {
		t.Errorf("after a drop: %+v", sh)
	}
	if err := st.SetConnection(ctx, sh.ID, true, "/var/lib/syp/shares/1", ""); err != nil {
		t.Fatal(err)
	}
	sh, err = st.Get(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sh.Connected || sh.Point != "/var/lib/syp/shares/1" || sh.Reason != "" {
		t.Errorf("after a reconnect: %+v", sh)
	}
}

// TestServiceCreateTestReconnectDelete walks the page's four calls on the fake helper.
func TestServiceCreateTestReconnectDelete(t *testing.T) {
	st, db := openStore(t)
	m := newTestMounter(t)
	svc := NewService(st, m)
	ctx := context.Background()

	// Test does not save, and a failed mount is a reason, not an error.
	m.fail["//nas/media"] = &MountError{Reason: ReasonUnreachable, Message: "The share did not answer."}
	answer, err := svc.Test(ctx, Input{Address: "//nas/media", Username: "viewer", Password: "hunter2-hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if answer.OK || answer.Reason != ReasonUnreachable || answer.Message == "" {
		t.Errorf("failed test = %+v", answer)
	}
	if list, err := svc.List(ctx); err != nil || len(list) != 0 {
		t.Errorf("a test saved a share: %v, %v", list, err)
	}

	// A share whose first mount failed is kept, not connected.
	sh, err := svc.Create(ctx, Input{Address: "//nas/media", Username: `WORKGROUP\viewer`, Password: "hunter2-hunter2"})
	if err != nil {
		t.Fatal(err)
	}
	if sh.Connected || sh.Reason != "The share did not answer." || sh.Name != "media" {
		t.Errorf("failed create = %+v", sh)
	}
	if id := fmt.Sprint(sh.ID); id != "1" {
		t.Errorf("id = %s", id)
	}
	if len(m.mounts) != 2 || m.mounts[1].Username != "viewer" || m.mounts[1].Domain != "WORKGROUP" {
		t.Errorf("mount specs = %+v", m.mounts)
	}

	// Reconnect after the server is back answers the share, connected.
	delete(m.fail, "//nas/media")
	sh, err = svc.Reconnect(ctx, sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !sh.Connected || sh.Point != "/var/lib/syp/shares/1" || sh.Reason != "" {
		t.Errorf("reconnect = %+v", sh)
	}

	// Delete is refused while a library uses the share.
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO libraries (name, path, share_id, watch) VALUES ('Films', '/mnt/films', 1, 'daily')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, sh.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("delete in use = %v, want ErrInUse", err)
	}
	if err := db.Tx(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE libraries SET share_id = NULL`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, sh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, sh.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted share = %v, want ErrNotFound", err)
	}
	if len(m.unmount) != 1 || m.unmount[0].ID != sh.ID {
		t.Errorf("unmount specs = %+v", m.unmount)
	}
}

// TestServiceTestAnswersMountFailure: a mount error's code and sentence come through untouched,
// so the page shows the helper's words rather than a generic refusal.
func TestServiceTestAnswersMountFailure(t *testing.T) {
	st, _ := openStore(t)
	m := newTestMounter(t)
	svc := NewService(st, m)
	m.fail["//nas/media"] = &MountError{Reason: ReasonBadCredentials, Message: "The server did not accept that username and password."}
	answer, err := svc.Test(context.Background(), Input{Address: "//nas/media", Username: "viewer", Password: "wrong-password"})
	if err != nil {
		t.Fatal(err)
	}
	if answer.OK || answer.Reason != ReasonBadCredentials || answer.Message != "The server did not accept that username and password." {
		t.Errorf("test = %+v", answer)
	}
	if _, err := svc.Test(context.Background(), Input{Address: "nas/media", Username: "viewer", Password: "pw"}); !isInvalid(err, "invalid_address") {
		t.Errorf("bad address = %v, want invalid_address", err)
	}
}

func isInvalid(err error, code string) bool {
	var invalid *InvalidError
	return errors.As(err, &invalid) && invalid.Code == code
}
