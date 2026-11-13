// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const ShareKeySize = 32

type Share struct {
	ID                int64
	Type              string
	Address           string
	Name              string
	Guest             bool
	Username          string
	HasPassword       bool
	EncryptedPassword []byte
	Nonce             []byte
	Created, Updated  string
}

type ShareMount struct {
	ID        int64
	ShareID   int64
	MountPath string
	Platform  string
	Status    string
	Error     string
	MountedAt string
	Since     string
}

func shareKeyPath() string {
	if os.Geteuid() == 0 || os.Getuid() == 0 {
		return "/etc/shrink-your-shit/share-key"
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "~"
	}
	return filepath.Join(home, ".config", "shrink-your-shit", "share-key")
}

func loadOrCreateShareKey() ([]byte, error) {
	path := shareKeyPath()
	b, err := os.ReadFile(path)
	if err == nil && len(b) == ShareKeySize {
		return b, nil
	}
	key := make([]byte, ShareKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func encryptSharePassword(plaintext string) ([]byte, []byte, error) {
	if plaintext == "" {
		return nil, nil, nil
	}
	key, err := loadOrCreateShareKey()
	if err != nil {
		return nil, nil, err
	}
	bc, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(bc)
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	return ct, nonce, nil
}

func decryptSharePassword(ct, nonce []byte) (string, error) {
	if ct == nil || nonce == nil {
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
	gcm, err := cipher.NewGCM(bc)
	if err != nil {
		return "", err
	}
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}

func (s *Store) LoadShares() ([]Share, error) {
	rows, err := s.db.Query(`SELECT id, type, address, name, guest, username,
		encrypted_password, auth_nonce, created, updated FROM shares ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var out []Share
	for rows.Next() {
		var sh Share
		rows.Scan(&sh.ID, &sh.Type, &sh.Address, &sh.Name, &sh.Guest, &sh.Username,
			&sh.EncryptedPassword, &sh.Nonce, &sh.Created, &sh.Updated)
		sh.HasPassword = sh.EncryptedPassword != nil
		out = append(out, sh)
	}
	return out, nil
}

func (s *Store) SaveShare(sh Share, password string) (Share, error) {
	if sh.Name == "" {
		return sh, errors.New("name is required")
	}
	if sh.Address == "" {
		return sh, errors.New("address is required")
	}
	ct, nonce, err := encryptSharePassword(password)
	if err != nil {
		return sh, err
	}
	now := s.now().UTC().Format(time.RFC3339)
	guest := 0
	if sh.Guest { guest = 1 }
	r, err := s.db.Exec(`INSERT INTO shares(type, address, name, guest, username,
		encrypted_password, auth_nonce, created, updated) VALUES(?,?,?,?,?,?,?,?,?)`,
		sh.Type, sh.Address, sh.Name, guest, sh.Username, ct, nonce, now, now)
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
	sets = append(sets, "guest=?, username=?")
	args = append(args, guest, username)
	if password != "" {
		ct, nonce, err := encryptSharePassword(password)
		if err != nil {
			return err
		}
		sets = append(sets, "encrypted_password=?, auth_nonce=?")
		args = append(args, ct, nonce)
	}
	now := s.now().UTC().Format(time.RFC3339)
	sets = append(sets, "updated=?")
	args = append(args, now)
	_, err := s.db.Exec(`UPDATE shares SET `+strings.Join(sets, ",")+` WHERE id=?`, append(args, id)...)
	return err
}

func (s *Store) DeleteShare(id int64) error {
	_, err := s.db.Exec(`DELETE FROM share_mounts WHERE share_id=?`, id)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM shares WHERE id=?`, id)
	return err
}

func (s *Store) ShareByID(id int64) (*Share, error) {
	var sh Share
	err := s.db.QueryRow(`SELECT id, type, address, name, guest, username,
		encrypted_password, auth_nonce, created, updated FROM shares WHERE id=?`, id).Scan(
		&sh.ID, &sh.Type, &sh.Address, &sh.Name, &sh.Guest, &sh.Username,
		&sh.EncryptedPassword, &sh.Nonce, &sh.Created, &sh.Updated)
	if err != nil {
		return nil, nil
	}
	sh.HasPassword = sh.EncryptedPassword != nil
	return &sh, nil
}

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

// DecryptSharePassword returns the plaintext password for a share (used from the web package).
func (s *Store) DecryptSharePassword(id int64) (string, error) {
	sh, err := s.ShareByID(id)
	if err != nil || sh == nil {
		return "", err
	}
	return decryptSharePassword(sh.EncryptedPassword, sh.Nonce)
}

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