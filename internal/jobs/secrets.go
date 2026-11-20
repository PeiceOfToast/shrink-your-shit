// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"crypto/aes"
	"crypto/cipher"
)

// SealSecret seals a secret an integration keeps (an API key) the way share passwords are sealed.
func SealSecret(plaintext string) (ct, nonce []byte, err error) {
	return encryptSharePassword(plaintext)
}

// OpenSecret opens what SealSecret sealed.
func OpenSecret(ct, nonce []byte) (string, error) { return decryptSharePassword(ct, nonce) }

// openSecretHere opens what SealSecret sealed with the share key this computer already has. Unlike
// OpenSecret it never makes a key: a value sealed on another machine stays unreadable, and the caller
// reports that instead of running without it.
func openSecretHere(ct, nonce []byte) (string, bool) {
	key := readShareKeyHere()
	if key == nil || !sealedOpens(key, ct, nonce) {
		return "", false
	}
	bc, err := aes.NewCipher(key)
	if err != nil {
		return "", false
	}
	gcm, err := cipher.NewGCM(bc)
	if err != nil {
		return "", false
	}
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", false
	}
	return string(plain), true
}
