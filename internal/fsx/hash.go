// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package fsx

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
)

// SHA256 is the hash of the whole file. A subtitle file is small, so its whole content identifies
// it (L-AUD-25).
func SHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SampleHash identifies a file's content cheaply, also for a 50 GB file: SHA-256 over its size and
// three 1 MiB samples (start, middle, end). The sidecar journal uses it to tell whether the new
// video was placed while a sidecar record was still open (L-AUD-25); a full hash would read the
// whole file on every run.
func SampleHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const chunk = 1 << 20
	size := st.Size()
	h := sha256.New()
	if err := binary.Write(h, binary.BigEndian, size); err != nil {
		return "", err
	}
	for _, at := range []int64{0, size/2 - chunk/2, size - chunk} {
		if at < 0 {
			at = 0
		}
		if _, err := io.Copy(h, io.NewSectionReader(f, at, min(chunk, size-at))); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
