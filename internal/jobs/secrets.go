// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

// SealSecret seals a secret an integration keeps (an API key) the way share passwords are sealed.
func SealSecret(plaintext string) (ct, nonce []byte, err error) {
	return encryptSharePassword(plaintext)
}

// OpenSecret opens what SealSecret sealed.
func OpenSecret(ct, nonce []byte) (string, error) { return decryptSharePassword(ct, nonce) }
