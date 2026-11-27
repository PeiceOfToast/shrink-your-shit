// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package safety

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/PeiceOfToast/shrink-your-shit/internal/fsx"
)

// Ident is what a file is, for a replace: its file identity, size, times, link count and a sample hash. Recovery
// finds a file by these, never by its name (L-SAFE-9).
type Ident struct {
	FileID string
	Size   int64
	MTime  int64 // unix nanoseconds
	CTime  int64 // unix nanoseconds on Linux; 0 where the OS gives no change time here
	Links  int
	Hash   string
}

// sampleSize is the 1 MiB read from the start, the middle and the end (L-SAFE-9).
const sampleSize = 1 << 20

// identify reads a file's identity. The hash is the size plus the samples; a file read through one open handle
// is the same file the identity was taken from.
func identify(path string) (Ident, error) {
	f, err := os.Open(path)
	if err != nil {
		return Ident{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Ident{}, err
	}
	if fi.IsDir() {
		return Ident{}, fmt.Errorf("%s is a folder", path)
	}
	id, err := fsx.FileID(path)
	if err != nil {
		return Ident{}, err
	}
	links, err := fsx.LinkCount(path)
	if err != nil {
		links = 1 // not every file system counts links; the hard-link skip then does not apply
	}
	hash, err := sampleHash(f, fi.Size())
	if err != nil {
		return Ident{}, err
	}
	return Ident{
		FileID: id, Size: fi.Size(), MTime: fi.ModTime().UnixNano(), CTime: ctimeOf(fi),
		Links: links, Hash: hash,
	}, nil
}

// sampleHash is the size and up to 1 MiB each from the start, the middle and the end of the file.
func sampleHash(f *os.File, size int64) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%d:", size)
	starts := []int64{0, size/2 - sampleSize/2, size - sampleSize}
	buf := make([]byte, sampleSize)
	for _, off := range starts {
		if off < 0 {
			off = 0
		}
		n := int64(sampleSize)
		if off+n > size {
			n = size - off
		}
		if n <= 0 {
			continue
		}
		if _, err := f.ReadAt(buf[:n], off); err != nil && err != io.EOF {
			return "", err
		}
		h.Write(buf[:n])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// matches reports whether the file at path is the file id describes: the same size, mtime and sample hash. The
// file identity is not required, so a file that was moved and renamed still matches (recovery finds it by these).
func matches(path string, id Ident) bool {
	got, err := identify(path)
	if err != nil {
		return false
	}
	return got.Size == id.Size && got.MTime == id.MTime && got.Hash == id.Hash
}

// unchanged is the L-SAFE-5 check at swap time: the same file (identity), size, mtime and change time, with the
// hard-link skip. A file that changed is kept, and nothing is swapped.
func unchanged(path string, want Ident) error {
	got, err := identify(path)
	if err != nil {
		return ErrSourceChanged
	}
	if got.FileID != want.FileID || got.Size != want.Size || got.MTime != want.MTime || got.CTime != want.CTime {
		return ErrSourceChanged
	}
	return nil
}

// Hook for tests: an identity read between two steps lets a test change the file in the window.
var afterIdentify = func(string) {}
