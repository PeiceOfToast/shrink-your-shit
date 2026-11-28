// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package system

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNoToken is the answer before the access token exists (page 11's no_token).
var ErrNoToken = errors.New("there is no access token")

// ChangePassword sets a new password, under the same rules and hashing as setup's, and ends every session except
// keepSession, the one that made the change (L-ACC-1). The access token is not changed. Nothing secret is logged.
func (a *Access) ChangePassword(ctx context.Context, keepSession, password string) error {
	if err := checkPasswordRules(password); err != nil {
		return err
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := a.now().Unix()
	keep := digest(keepSession)
	if keepSession == "" {
		keep = ""
	}
	return a.db.Tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE auth SET password_hash = ?, set_at = ? WHERE id = 1`, hash, now)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNoAccount
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash <> ?`, keep)
		return err
	})
}

// Token is the current access token, opened from its sealed copy. It is a secret: callers must not log or store it
// anywhere but the response they answer with.
func (a *Access) Token(ctx context.Context) (string, error) {
	a.mu.Lock()
	key := a.key
	a.mu.Unlock()
	if key == nil {
		return "", ErrNoAccount
	}
	var sealed []byte
	err := a.db.Read().QueryRowContext(ctx, `SELECT sealed FROM tokens WHERE id = ?`, accessTokenID).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoToken
	}
	if err != nil {
		return "", fmt.Errorf("read the token: %w", err)
	}
	plain, err := openSealed(key, sealed)
	if err != nil {
		return "", fmt.Errorf("open the token: %w", err)
	}
	return string(plain), nil
}

// openSealed reverses seal: the nonce comes first, then the ciphertext.
func openSealed(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(sealed) < ns {
		return nil, errors.New("sealed value is too short")
	}
	return gcm.Open(nil, sealed[:ns], sealed[ns:], nil)
}
