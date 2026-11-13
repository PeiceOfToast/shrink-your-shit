// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ShareKeySize for AES-256.
const ShareKeySize = 32

// ShareNonceSize for AES-256-GCM (12 bytes is the GCM standard).
const ShareNonceSize = 12

// Share is one network share record (table shares, migration 39).
type Share struct {
	ID                int64
	Type              string // "smb" | "nfs"
	Address           string // "//server/share" or "server:/export"
	Name              string
	Guest             bool
	Username          string
	HasPassword       bool
	EncryptedPassword []byte
	Nonce             []byte
	Tag               []byte
	Created, Updated  string
}

// ShareMount is a share's mount on one platform (table share_mounts).
type ShareMount struct {
	ID        int64
	ShareID   int64
	MountPath string
	Platform  string // "linux" | "windows" | "macos" | "docker"
	Status    string // "connected" | "not_connected" | "connecting" | "waiting_setup"
	Error     string
	MountedAt string
	Since     string
}

// shareKeyPath returns the path to the share encryption key file, outside the data folder.
// Linux service: /etc/shrink-your-shit/share-key
// Linux user install / macOS: ~/.config/shrink-your-shit/share-key
// Windows: DPAPI (handled separately)
func shareKeyPath() string {
	// /etc is only for the system service install
	if os.Geteuid() == 0 || os.Getuid() == 0 {
		return "/etc/shrink-your-shit/share-key"
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "~"
	}
	return filepath.Join(home, ".config", "shrink-your-shit", "share-key")
}

// loadOrCreateShareKey reads the share encryption key, creating a new 256-bit key if none exists.
func loadOrCreateShareKey() ([]byte, error) {
	path := shareKeyPath()
	b, err := os.ReadFile(path)
	if err == nil && len(b) == ShareKeySize {
		return b, nil
	}
	// generate a new key
	key := make([]byte, ShareKeySize)
	rand.Read(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// encryptSharePassword encrypts plaintext with AES-256-GCM using the share key, returning
// (ciphertext, nonce, tag). nil plaintext returns nil. The nonce is prepended to the ciphertext
// in the blob stored in the database; the tag is the last 16 bytes of the GCM output.
func encryptSharePassword(plaintext string) ([]byte, []byte, []byte, error) {
	if plaintext == "" {
		return nil, nil, nil, nil
	}
	key, err := loadOrCreateShareKey()
	if err != nil {
		return nil, nil, nil, err
	}
	bc, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, nil, err
	}
	// NewGCM generates a random 12-byte nonce and prepends it to the ciphertext
	aead, err := cipher.NewGCM(bc)
	if err != nil {
		return nil, nil, nil, err
	}
	// Seal returns the ciphertext with nonce+tag appended.
	var out = aead.Seal([]byte(plaintext), nil, nil, make([]byte, len(plaintext)+ShareNonceSize+16))
	// out = nonce (12) + ciphertext + tag (16)
	if len(out) < ShareNonceSize + 16 {
		return nil, nil, nil, errors.New("seal output too short")
	}
	nonce := out[:ShareNonceSize]
	ctLen := len(plaintext)
	return out[ShareNonceSize:ShareNonceSize+ctLen], nonce, out[ShareNonceSize+ctLen:ShareNonceSize+ctLen+16], nil
}

// decryptSharePassword decrypts ciphertext+nonce+tag with AES-256-GCM. Returns "" for nil blob.
func decryptSharePassword(ct, nonce, tag []byte) (string, error) {
	if ct == nil || nonce == nil || tag == nil {
		return "", nil
	}
	key, err := loadOrCreateShareKey()
	if err != nil {
		return "", err
	}
	bc, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	// Rebuild the combined blob: nonce + ciphertext + tag
	combined := make([]byte, len(nonce)+len(ct)+len(tag))
	for i, b := range nonce { combined[i] = b }
	off := len(nonce)
	for i, b := range ct { combined[off+i] = b }
	off += len(ct)
	for i, b := range tag { combined[off+i] = b }
	aead, err := cipher.NewGCM(bc)
	if err != nil {
		return "", err
	}
	// Open reads the ciphertext+nonce+tag and returns the plaintext.
	pt, err := aead.Open(combined, nil, nil, make([]byte, len(combined)))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(pt)), nil
}

// LoadShares returns all network share records.
func (s *Store) LoadShares() ([]Share, error) {
	rows, err := s.db.Query(`SELECT id, type, address, name, guest, username,
		encrypted_password, auth_nonce, auth_tag, created, updated FROM shares ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []Share
	for rows.Next() {
		var sh Share
		rows.Scan(&sh.ID, &sh.Type, &sh.Address, &sh.Name, &sh.Guest, &sh.Username,
			&sh.EncryptedPassword, &sh.Nonce, &sh.Tag, &sh.Created, &sh.Updated)
		sh.HasPassword = sh.EncryptedPassword != nil
		out = append(out, sh)
	}
	return out, nil
}

// SaveShare inserts a new share and returns its id.
func (s *Store) SaveShare(sh Share, password string) (Share, error) {
	if sh.Name == "" {
		return sh, errors.New("name is required")
	}
	if sh.Address == "" {
		return sh, errors.New("address is required")
	}
	ct, nonce, tag, err := encryptSharePassword(password)
	if err != nil {
		return sh, err
	}
	now := s.now().UTC().Format(time.RFC3339)
	guest := 0
	if sh.Guest { guest = 1 }
	r, err := s.db.Exec(`INSERT INTO shares(type, address, name, guest, username,
		encrypted_password, auth_nonce, auth_tag, created, updated)
		VALUES(?,?,?,?,?,?,?,?,?,?)`,
		sh.Type, sh.Address, sh.Name, guest, sh.Username,
		ct, nonce, tag, now, now)
	if err != nil {
		return sh, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return sh, err
	}
	sh.ID = id
	sh.Created = now
	sh.Updated = now
	sh.HasPassword = ct != nil
	return sh, nil
}

// UpdateShare updates a share's editable fields.
func (s *Store) UpdateShare(id int64, name, address, typeStr, username string, guest bool, password string) error {
	name = strings.TrimSpace(name)
	var sets []string
	var args []any
	if name != "" {
		sets = append(sets, "name=?")
		args = append(args, name)
	}
	if address != "" {
		sets = append(sets, "address=?")
		args = append(args, address)
	}
	if typeStr != "" {
		sets = append(sets, "type=?")
		args = append(args, typeStr)
	}
	// username: explicitly set to empty string to clear it, or keep existing
	sets = append(sets, "guest=?, username=?")
	args = append(args, guest, username)
	if password != "" {
		ct, nonce, tag, err := encryptSharePassword(password)
		if err != nil {
			return err
		}
		sets = append(sets, "encrypted_password=?, auth_nonce=?, auth_tag=?")
		args = append(args, ct, nonce, tag)
	}
	now := s.now().UTC().Format(time.RFC3339)
	sets = append(sets, "updated=?")
	args = append(args, now)
	_, err := s.db.Exec(`UPDATE shares SET `+strings.Join(sets, ",")+` WHERE id=?`, append(args, id)...)
	return err
}

// DeleteShare deletes a share and its mount records.
func (s *Store) DeleteShare(id int64) error {
	_, err := s.db.Exec(`DELETE FROM share_mounts WHERE share_id=?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM shares WHERE id=?`, id)
	return err
}

// ShareByID returns one share by id.
func (s *Store) ShareByID(id int64) (*Share, error) {
	row := s.db.QueryRow(`SELECT id, type, address, name, guest, username,
		encrypted_password, auth_nonce, auth_tag, created, updated FROM shares WHERE id=?`, id)
	if row == nil {
		return nil, sql.ErrNoRows
	}
	var sh Share
	row.Scan(&sh.ID, &sh.Type, &sh.Address, &sh.Name, &sh.Guest, &sh.Username,
		&sh.EncryptedPassword, &sh.Nonce, &sh.Tag, &sh.Created, &sh.Updated)
	sh.HasPassword = sh.EncryptedPassword != nil
	return &sh, nil
}

// ShareMountsByShare returns the mount records for a share.
func (s *Store) ShareMountsByShare(shareID int64) ([]ShareMount, error) {
	rows, err := s.db.Query(`SELECT id, share_id, mount_path, platform, status, error, mounted_at, since
		FROM share_mounts WHERE share_id=?`, shareID)
	if err != nil {
		return nil, err
	}
	var out []ShareMount
	for rows.Next() {
		var m ShareMount
		rows.Scan(&m.ID, &m.ShareID, &m.MountPath, &m.Platform, &m.Status, &m.Error, &m.MountedAt, &m.Since)
		out = append(out, m)
	}
	return out, nil
}

// SetShareMountStatus updates a mount record's status.
func (s *Store) SetShareMountStatus(shareID int64, platform, status, error string) error {
	now := s.now().UTC().Format(time.RFC3339)
	r, err := s.db.Exec(`UPDATE share_mounts SET status=?, error=?, since=COALESCE(since,?), mounted_at=CASE WHEN ?='connected' THEN ? ELSE mounted_at END
		WHERE share_id=? AND platform=?`, status, error, now, status, now, shareID, platform)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		_, err = s.db.Exec(`INSERT INTO share_mounts(share_id, mount_path, platform, status, error, since)
			VALUES(?,'',?,?,?,?)`, shareID, platform, status, error, now)
		return err
	}
	return nil
}