// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build !windows

package jobs

// Linux and macOS keep the share key file as the raw key (its permissions and owner protect it).
func wrapShareKey(key []byte) ([]byte, error) { return key, nil }
func unwrapShareKey(b []byte) ([]byte, error) { return b, nil }
