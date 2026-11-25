// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

// The sign-in choice (contract-signin-choice). "home": requests from a private address need no sign-in,
// everything else does. "password": everyone signs in, with the password or the access token. The
// choice and the password's bcrypt hash live in signin.json in the data folder, beside the token.

// SigninFile is the sign-in choice's file name inside the data folder.
const SigninFile = "signin.json"

const (
	ModeHome     = "home"     // requests from a private address need no sign-in
	ModePassword = "password" // everyone signs in, with the password or the access token
)

const (
	minPassword     = 8  // characters
	maxPassword     = 72 // bytes: bcrypt reads no more
	loginMaxFails   = 5  // failed sign-ins from one address within loginWindow
	loginWindow     = time.Minute
	loginRetryAfter = 60 // seconds: what a locked address is told
)

type signinState struct {
	Mode         string `json:"mode"`
	PasswordHash string `json:"passwordHash,omitempty"`
}

// fileStamp tells whether signin.json changed since it was read (reset-password runs in another process).
type fileStamp struct {
	mod  time.Time
	size int64
}

func (s fileStamp) same(o fileStamp) bool { return s.size == o.size && s.mod.Equal(o.mod) }

func stampOf(dir string) (fileStamp, error) {
	fi, err := os.Stat(filepath.Join(dir, SigninFile))
	if err != nil {
		return fileStamp{}, err
	}
	return fileStamp{fi.ModTime(), fi.Size()}, nil
}

// readSignin returns the stored choice; ok is false when there is no file yet.
func readSignin(dir string) (s signinState, ok bool, err error) {
	b, err := os.ReadFile(filepath.Join(dir, SigninFile))
	if errors.Is(err, fs.ErrNotExist) {
		return signinState{}, false, nil
	}
	if err != nil {
		return signinState{}, false, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return signinState{}, false, errors.New(SigninFile + " cannot be read: " + err.Error())
	}
	if s.Mode != ModeHome && s.Mode != ModePassword {
		return signinState{}, false, errors.New(SigninFile + " has an unknown mode: " + s.Mode)
	}
	return s, true, nil
}

func writeSignin(dir string, s signinState) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileAtomic(dir, SigninFile, append(b, '\n'))
}

// passwordCheck says why a password cannot be used: a code and a sentence, or "" when it can.
func passwordCheck(p string) (code, msg string) {
	switch {
	case utf8.RuneCountInString(p) < minPassword:
		return "password_too_short", "Use at least 8 characters."
	case len(p) > maxPassword:
		return "password_too_long", "Use at most 72 bytes. Some accented letters and symbols take more than one."
	}
	return "", ""
}

// ResetPassword sets the password of the server whose data folder is dataDir, and switches it to password
// mode. The token is kept. Browsers signed in with the old password are signed out; a running server
// uses the new one without a restart.
func ResetPassword(dataDir, password string) error {
	if code, msg := passwordCheck(password); code != "" {
		return errors.New(msg)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := writeSignin(dataDir, signinState{Mode: ModePassword, PasswordHash: string(h)}); err != nil {
		return err
	}
	return handToFolderOwner(dataDir)
}

// chownFile and ownerOf are seams for the root case. Run as root, the new file would stay root-owned and the
// server, which runs as the folder's owner, could not read it. Only unix has owners (see signin_owner_*.go).
var (
	chownFile = os.Chown
	ownerOf   = folderOwner
)

// handToFolderOwner gives signin.json to the owner of dataDir when the write ran as root.
func handToFolderOwner(dataDir string) error {
	uid, gid, ok := ownerOf(dataDir)
	if !ok {
		return nil
	}
	if err := chownFile(filepath.Join(dataDir, SigninFile), uid, gid); err != nil {
		return errors.New("the new password is saved, but " + SigninFile + " could not be given to the folder's owner, " +
			"so the running server cannot read it. Run reset-password as that user: " + err.Error())
	}
	return nil
}

// signinNow is what the sign-in choice says for one request.
type signinNow struct {
	mode, hash, tok string
}

// required: a token or a password is set, so /api/* needs one from some address.
func (s signinNow) required() bool { return s.tok != "" || s.hash != "" }

func (s signinNow) cookieOK(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && same(c.Value, sessionValue(s.tok, s.hash))
}

func (s signinNow) signedIn(r *http.Request) bool {
	if b := bearer(r); b != "" && s.tok != "" && same(b, s.tok) {
		return true
	}
	return s.cookieOK(r)
}

// open: this request may use the app without signing in: nothing is set to sign in to, or home mode
// and a private address (see Transport.PrivateClient).
func (s signinNow) open(r *http.Request, t Transport) bool {
	return !s.required() || (s.mode == ModeHome && t.PrivateClient(r))
}

// current returns the choice in use, re-reading signin.json first if it has changed.
func (a *Access) current() signinNow {
	a.refresh()
	a.mu.RLock()
	defer a.mu.RUnlock()
	return signinNow{a.mode, a.hash, a.token}
}

func (a *Access) refresh() {
	st, err := stampOf(a.dir)
	if err != nil {
		return // no file: keep what is in use
	}
	a.mu.RLock()
	unchanged := a.stamp.same(st)
	a.mu.RUnlock()
	if unchanged {
		return
	}
	s, ok, err := readSignin(a.dir)
	if err != nil || !ok {
		// A bad file changes nothing, and it is logged once per change rather than on every request.
		log.Printf("sign-in: %s cannot be used, so the sign-in choice in use stays: %v", SigninFile, err)
		a.mu.Lock()
		a.stamp = st
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	a.mode, a.hash, a.stamp = s.Mode, s.PasswordHash, st
	a.mu.Unlock()
}

// save stores the choice and starts using it.
func (a *Access) save(s signinState) error {
	if err := writeSignin(a.dir, s); err != nil {
		return err
	}
	st, err := stampOf(a.dir)
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.mode, a.hash, a.stamp = s.Mode, s.PasswordHash, st
	a.mu.Unlock()
	return nil
}

// view is the body of GET /api/auth (and of a mode change, where the caller has just signed in).
func (a *Access) view(r *http.Request, justSignedIn bool) map[string]any {
	now := a.current()
	return map[string]any{
		"mode":        now.mode,
		"passwordSet": now.hash != "",
		"signedIn":    justSignedIn || now.signedIn(r),
		"open":        now.open(r, a.transport),
	}
}

// setMode is POST /api/auth/mode: {"mode":"home"} or {"mode":"password","password":"..."}. It runs under the
// normal access rules, so only someone who may use the app can change the choice.
func (a *Access) setMode(w http.ResponseWriter, r *http.Request) {
	var in struct{ Mode, Password string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		apiError(w, http.StatusBadRequest, "bad_request", "The change could not be read. Nothing was changed.")
		return
	}
	now := a.current()
	s := signinState{Mode: in.Mode, PasswordHash: now.hash}
	switch in.Mode {
	case ModeHome:
		if !a.transport.PrivateClient(r) {
			apiError(w, http.StatusConflict, "would_lock_out", "You're connecting from outside your home network, so turning sign-in off would lock you out. Change it from a computer on your home network.")
			return
		}
	case ModePassword:
		if in.Password != "" {
			if code, msg := passwordCheck(in.Password); code != "" {
				apiError(w, http.StatusBadRequest, code, msg)
				return
			}
			h, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
			if err != nil {
				apiError(w, http.StatusInternalServerError, "password_not_saved", "The password could not be made, so nothing changed. "+err.Error())
				return
			}
			s.PasswordHash = string(h)
		} else if now.hash == "" {
			apiError(w, http.StatusBadRequest, "password_needed", "Pick a password first, so you can still sign in.")
			return
		}
	default:
		apiError(w, http.StatusBadRequest, "bad_mode", "Choose home or password.")
		return
	}
	if err := a.save(s); err != nil {
		apiError(w, http.StatusInternalServerError, "mode_not_saved", "The sign-in choice could not be saved, so it did not change. "+err.Error())
		return
	}
	setSession(w, now.tok, s.PasswordHash) // the caller stays signed in; other sessions end when the password changes
	writeJSON(w, a.view(r, true))
}
