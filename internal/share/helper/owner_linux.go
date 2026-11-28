// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkOwnedPath refuses a path that anyone but owner (or root) could swap: path and every folder
// above it, up to and including top, must be owned by root or owner and writable by its owner alone.
// Sticky folders get no exception: an entry the syp user made there is still the syp user's to swap.
// The helper acts by path, so a writable parent would let the caller replace a folder with a link
// between a check and a mkdir, chown or mount.
func checkOwnedPath(path string, owner uint32, top string) error {
	top = filepath.Clean(top)
	for p := filepath.Clean(path); ; p = filepath.Dir(p) {
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("%s: no owner information", p)
		}
		if st.Uid != 0 && st.Uid != owner {
			return fmt.Errorf("%s is owned by uid %d, not root", p, st.Uid)
		}
		// A symbolic link is always 0777 on Linux, so this refuses links too.
		if fi.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s can be written by others than its owner", p)
		}
		if p == top || p == filepath.Dir(p) {
			return nil
		}
	}
}
