// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

//go:build linux

package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// checkOwnedPath refuses a folder that anyone but owner (or root) could swap: the folder and every
// folder above it must be owned by root or owner and writable by its owner alone. A sticky folder
// (like /tmp) is allowed, because there only an entry's owner can rename it. The helper mounts by
// path, so a writable parent would let the caller replace the shares folder with a link between a
// check and a mount, and the helper would make folders, chown and mount wherever it pointed.
func checkOwnedPath(path string, owner uint32) error {
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
		if fi.Mode().Perm()&0o022 != 0 && fi.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("%s can be written by others than its owner", p)
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}
