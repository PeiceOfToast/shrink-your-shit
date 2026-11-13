// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEncryptionRoundTrip encrypts a password and decrypts it back.
func TestEncryptionRoundTrip(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	orig := "MySecretPassword123!"
	ct, nonce, err := encryptSharePassword(orig)
	if err != nil {
		t.Fatal(err)
	}
	if ct == nil || nonce == nil {
		t.Fatal("no ciphertext or nonce")
	}
	got, err := decryptSharePassword(ct, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if got != orig {
		t.Errorf("round trip: %q, want %q", got, orig)
	}
}

// TestEncryptionWrongKey fails when the key file is replaced.
func TestEncryptionWrongKey(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	ct, nonce, err := encryptSharePassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	// Replace the key with a different one
	key := make([]byte, 32)
	rand.Read(key)
	os.WriteFile(filepath.Join(home, ".config", "shrink-your-shit", "share-key"), key, 0o600)

	_, err = decryptSharePassword(ct, nonce)
	if err == nil {
		t.Error("decrypt with wrong key should fail")
	}
}

// TestEncryptionDifferentOutputs: two encryptions of the same password differ (nonce).
func TestEncryptionDifferentOutputs(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	ct1, _, _ := encryptSharePassword("same")
	ct2, _, _ := encryptSharePassword("same")
	if ct1 == nil || ct2 == nil {
		t.Fatal("encrypt failed")
	}
	if len(ct1) == len(ct2) {
		match := true
		for i := 0; i < len(ct1); i++ {
			if ct1[i] != ct2[i] { match = false; break }
		}
		if match {
			t.Error("same password encrypted twice gives same ciphertext")
		}
	}
}

// TestSaveShareRoundTrip creates and reads back a share; password never in JSON.
func TestSaveShareRoundTrip(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sh := Share{Type: "smb", Address: `\\nas\media`, Name: "NAS Videos", Username: "user"}
	saved, err := s.SaveShare(sh, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID <= 0 {
		t.Fatal("no id")
	}
	if !saved.HasPassword {
		t.Error("hasPassword is false")
	}
	got, err := s.ShareByID(saved.ID)
	if err != nil || got == nil {
		t.Fatal("read back failed")
	}
	pw, err := decryptSharePassword(got.EncryptedPassword, got.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	if pw != "secret" {
		t.Errorf("decrypted: %q, want secret", pw)
	}
	// Password must never appear in JSON
	jBytes, _ := json.Marshal(saved)
	j := strings.ToLower(string(jBytes))
	if strings.Contains(j, "secret") {
		t.Error("password leaked in JSON output")
	}
}

// TestUpdateSharePassword updates a password; empty keeps the old one.
func TestUpdateSharePassword(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sh := Share{Type: "smb", Address: `\\nas\old`, Name: "Old"}
	saved, _ := s.SaveShare(sh, "oldpw")

	err = s.UpdateShare(saved.ID, "", "", "", "", false, "newpw")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.ShareByID(saved.ID)
	pw, _ := decryptSharePassword(got.EncryptedPassword, got.Nonce)
	if pw != "newpw" {
		t.Errorf("after update: %q, want newpw", pw)
	}
	// Empty password keeps the old one
	err = s.UpdateShare(saved.ID, "", "", "", "", false, "")
	if err != nil {
		t.Fatal(err)
	}
	got, _ = s.ShareByID(saved.ID)
	pw, _ = decryptSharePassword(got.EncryptedPassword, got.Nonce)
	if pw != "newpw" {
		t.Errorf("after empty update: %q, want newpw unchanged", pw)
	}
}

// TestDeleteShare removes a share and its mounts.
func TestDeleteShare(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sh := Share{Type: "smb", Address: `\\nas\share`, Name: "Test"}
	saved, _ := s.SaveShare(sh, "pw")
	_ = s.SetShareMountStatus(saved.ID, "linux", "connected", "")

	err = s.DeleteShare(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	sh2, err := s.ShareByID(saved.ID)
	_ = err
	if sh2 != nil {
		t.Error("share still exists after delete")
	}
	// Mounts deleted too
	mounts, _ := s.ShareMountsByShare(saved.ID)
	if len(mounts) > 0 {
		t.Error("mounts still exist after share delete")
	}
}

// TestKeyPathOutsideData checks the key is outside the data dir.
func TestKeyPathOutsideData(t *testing.T) {
	p := shareKeyPath()
	if strings.Contains(p, "data") && !strings.Contains(p, ".config") {
		t.Errorf("key path %q looks like it contains a data dir", p)
	}
	if !strings.HasSuffix(p, "share-key") {
		t.Errorf("key path %q doesn't end with share-key", p)
	}
}

// TestLoadSharesEmpty returns [] for a fresh database.
func TestLoadSharesEmpty(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	all, err := s.LoadShares()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("expected 0 shares, got %d", len(all))
	}
}

// TestGuestShare has no password and preserves the guest flag.
func TestGuestShare(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	os.MkdirAll(home, 0o755)
	os.Setenv("HOME", home)
	defer os.Unsetenv("HOME")

	s, err := Open(filepath.Join(t.TempDir(), "syp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	sh := Share{Type: "smb", Address: `\\nas\guest`, Name: "Guest", Guest: true}
	saved, err := s.SaveShare(sh, "")
	if err != nil {
		t.Fatal(err)
	}
	if !saved.Guest {
		t.Error("guest flag not preserved")
	}
	got, _ := s.ShareByID(saved.ID)
	if got.HasPassword {
		t.Error("guest share has hasPassword=true")
	}
}