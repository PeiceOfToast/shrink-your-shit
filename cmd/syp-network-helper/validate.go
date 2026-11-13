//go:build linux

// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package main

// The helper's security rules, as pure functions so they can be tested without root.
// Every request is checked here before anything runs as root. Each rule fails closed:
// anything not explicitly allowed is refused.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// sharesDir is the only folder the helper mounts in: one entry per share, directly inside it.
// It must be owned by root and not writable by anyone else, so the service user can't plant a
// symlink there (a symlink "x -> /etc" would make root mount the share over /etc).
var sharesDir = "/var/lib/syp/shares"

// mountName is a share's folder name inside sharesDir: what the app's sanitiseName produces.
// Lower-case letters, digits, '.', '_' and '-', not starting with '.' or '-', at most 64.
var mountName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// validateMountPoint accepts only sharesDir/<mountName>, already in clean form: no "..",
// no "./", no trailing slash, no deeper path. Used for mount, unmount AND status, so the
// helper never unmounts or touches anything outside sharesDir.
func validateMountPoint(mp string) error {
	if mp == "" {
		return errors.New("mount point is required")
	}
	if filepath.Clean(mp) != mp {
		return errors.New("mount point must be a clean path")
	}
	if filepath.Dir(mp) != sharesDir {
		return fmt.Errorf("mount point must be directly inside %s", sharesDir)
	}
	if !mountName.MatchString(filepath.Base(mp)) {
		return errors.New("mount point name may only use a-z, 0-9, '.', '_' and '-'")
	}
	return nil
}

// host is a DNS name, an IPv4 address, or an IPv6 address in brackets. No user@, no port tricks.
const host = `(?:[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?|\[[0-9A-Fa-f:.]{2,45}\])`

// cifsAddress is //host/share[/path]; nfsAddress is host:/export[/path]. The path parts may hold
// spaces and most characters (they are one argv entry, never seen by a shell), but no control
// characters and no backslash. Neither form can start with '-', so mount(8) can't read it as an option.
var (
	cifsAddress = regexp.MustCompile(`^//` + host + `/[^\x00-\x1f\x7f\\/][^\x00-\x1f\x7f\\]*$`)
	nfsAddress  = regexp.MustCompile(`^` + host + `:/[^\x00-\x1f\x7f\\]*$`)
)

// validateAddress checks the share's address against its type.
func validateAddress(kind, addr string) error {
	if len(addr) > 1024 {
		return errors.New("the address is too long")
	}
	switch kind {
	case "cifs":
		if !cifsAddress.MatchString(addr) {
			return errors.New("an SMB address must look like //server/share")
		}
	case "nfs":
		if !nfsAddress.MatchString(addr) {
			return errors.New("an NFS address must look like server:/export")
		}
	default:
		return fmt.Errorf("unsupported share type %q (only cifs and nfs)", kind)
	}
	return nil
}

// Option values must match these patterns. The value is joined into mount(8)'s -o list, so a
// comma or '=' in a value would smuggle in another option (e.g. vers="3.0,suid"): the patterns
// allow neither. A flag option (soft, hard) takes no value at all.
var (
	number  = regexp.MustCompile(`^[0-9]{1,9}$`)
	version = regexp.MustCompile(`^[0-9]{1,2}(\.[0-9]{1,2}){0,2}$`)
	word    = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	domain  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
)

// allowedOptions is every option a request may carry, with the rule its value must meet
// (nil: a flag, no value). Anything else is refused, including suid, dev, exec, bind, user,
// uid, gid, credentials and file modes: the helper sets those itself (see mountOptions).
var allowedOptions = map[string]*regexp.Regexp{
	"vers": version, "soft": nil, "hard": nil, "timeo": number, "retrans": number, "retry": number,
	"rsize": number, "wsize": number, "echo_interval": number, "actimeo": number,
	"port": number, "sec": word, "domain": domain,
}

// validateOptions checks every option name against allowedOptions and every value against its rule.
func validateOptions(opts map[string]string) error {
	for k, v := range opts {
		rule, ok := allowedOptions[k]
		if !ok {
			return fmt.Errorf("mount option %q is not allowed", k)
		}
		if rule == nil {
			if v != "" {
				return fmt.Errorf("mount option %q takes no value", k)
			}
			continue
		}
		if !rule.MatchString(v) {
			return fmt.Errorf("mount option %s has a value that isn't allowed", k)
		}
	}
	return nil
}

// validateCredentials refuses a username or password that could add lines to the credentials
// file (a newline would let "password=x\ndomain=y" set another key) or end it early (NUL).
func validateCredentials(username, password string) error {
	if len(username) > 256 || len(password) > 1024 {
		return errors.New("username or password is too long")
	}
	if strings.ContainsAny(username, "\r\n\x00") || strings.ContainsAny(password, "\r\n\x00") {
		return errors.New("username and password can't contain line breaks")
	}
	return nil
}

// mountOptions builds the -o list: the person's validated options in a stable order, then the
// options the helper always forces. mount(8) defaults to suid, dev and exec, so leaving them out
// is not enough: nosuid, nodev and noexec are always added. For cifs, the files belong to the
// service user (uid/gid) so the app can read and write them; nfs keeps the server's owners.
func mountOptions(kind string, opts map[string]string, uid, gid int, credPath string) string {
	keys := make([]string, 0, len(opts))
	for k := range opts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		if opts[k] == "" {
			out = append(out, k)
		} else {
			out = append(out, k+"="+opts[k])
		}
	}
	out = append(out, "nosuid", "nodev", "noexec")
	if kind == "cifs" {
		out = append(out, "uid="+strconv.Itoa(uid), "gid="+strconv.Itoa(gid), "file_mode=0660", "dir_mode=0770")
		if credPath != "" {
			out = append(out, "credentials="+credPath)
		} else {
			out = append(out, "guest")
		}
	}
	return strings.Join(out, ",")
}

// checkSharesDir makes sure sharesDir is a real folder (not a symlink), owned by rootUID and not
// writable by group or others, so nobody but root can create or swap entries in it.
func checkSharesDir(rootUID int) error {
	fi, err := os.Lstat(sharesDir)
	if err != nil {
		return fmt.Errorf("shares folder: %w", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("shares folder is not a real folder")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != rootUID {
		return errors.New("shares folder must be owned by root")
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return errors.New("shares folder must not be writable by group or others")
	}
	return nil
}

// prepareMountPoint makes the share's folder if it isn't there, and refuses anything that isn't
// a plain folder owned by rootUID (a symlink or a file put there some other way).
func prepareMountPoint(mp string, rootUID int) error {
	if err := checkSharesDir(rootUID); err != nil {
		return err
	}
	if err := os.Mkdir(mp, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("mount point: %w", err)
	}
	fi, err := os.Lstat(mp)
	if err != nil {
		return fmt.Errorf("mount point: %w", err)
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("mount point is not a plain folder")
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != rootUID {
		return errors.New("mount point must be owned by root")
	}
	return nil
}

// checkExistingMountPoint is for unmount and status: umount(8) and mountpoint(1) follow symlinks,
// so a planted "x -> /home" could make root unmount /home. The shares folder must still be
// root-only, and the mount point, if it exists, must not be a symlink.
func checkExistingMountPoint(mp string, rootUID int) error {
	if err := checkSharesDir(rootUID); err != nil {
		return err
	}
	fi, err := os.Lstat(mp)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mount point: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return errors.New("mount point is not a plain folder")
	}
	return nil
}
