// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"golang.org/x/crypto/bcrypt"
)

// Access to /api/* is one shared token, or a password, or nothing, as signin.json says (signin.go).
// The token is required whenever one is stored, and one is made and stored the first time the program
// listens beyond this computer. A browser signs in once (POST /api/login) and then sends a session
// cookie; scripts send "Authorization: Bearer <token>". The cookie's value is derived from the token and
// the password's hash, so sessions survive a restart, and making a new token or a new password ends them.

// TokenFile is the token's file name inside the data folder.
const TokenFile = "access-token"

const sessionCookie = "syp_session"

// afterTokenStored runs (in tests only) between storing a new token and using it.
var afterTokenStored func()

// Access holds the token and the sign-in choice, and checks requests against them.
type Access struct {
	dir   string
	mu    sync.RWMutex
	token string // "" = none stored
	mode  string // ModeHome or ModePassword (signin.json)
	hash  string // the password's bcrypt hash, "" = no password set
	stamp fileStamp

	transport Transport   // who counts as a private address (set by Handler)
	login     pairLimiter // failed sign-ins per address
	setupDone func() bool // whether first-run setup is finished (set by Handler); nil counts as finished

	// FailDelay is how long a wrong token or password waits before the answer, to slow down guessing.
	FailDelay time.Duration
}

// ReadToken returns the token stored in dataDir ("" with an error if there is none).
func ReadToken(dataDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, TokenFile))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// OpenAccess loads the token and the sign-in choice stored in dataDir. It makes and stores a token when no
// token is stored and either the install is exposed (listening beyond loopback) or it is new (no sign-in
// choice yet), and reports created, so the caller can show the sign-in address once. An unfinished install
// with a choice and no token gets one from EnsureSetupToken at startup.
func OpenAccess(dataDir string, exposed bool) (a *Access, created bool, err error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil { // the sign-in choice is written here at once
		return nil, false, err
	}
	tok, err := ReadToken(dataDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	a = &Access{dir: dataDir, token: tok, FailDelay: 500 * time.Millisecond,
		login: pairLimiter{max: loginMaxFails, window: loginWindow}}
	s, ok, err := readSignin(dataDir)
	if err != nil {
		return nil, false, err
	}
	if !ok { // a new install listens at home; an install from before the choice keeps its token sign-in
		s = signinState{Mode: ModeHome}
		if tok != "" || os.Getenv("SYP_INSTALL") == "docker" { // a new Docker install asks for sign-in (Docker shows every client as private)
			s.Mode = ModePassword
		}
		if err := writeSignin(dataDir, s); err != nil {
			return nil, false, err
		}
	}
	a.mode, a.hash = s.Mode, s.PasswordHash
	if a.stamp, err = stampOf(dataDir); err != nil {
		return nil, false, err
	}
	if tok == "" && (exposed || !ok) { // a new install is locked until setup is done, so it needs a token to get in
		if _, err := a.rotate(); err != nil {
			return nil, false, err
		}
		created = true
	}
	return a, created, nil
}

// EnsureSetupToken makes a token for an install whose first-run setup is not finished and that has neither a
// token nor a password: its token file was lost, or a start died between the sign-in choice and the token,
// and then nothing could sign in. A finished install is left as it is, so this changes nothing after setup.
// Unreadable settings count as not finished (fail closed). It reports whether it made a token.
func (a *Access) EnsureSetupToken(store *jobs.Store) (bool, error) {
	if a.Token() != "" || a.hash != "" {
		return false, nil
	}
	s, err := LoadSettings(store)
	if err == nil && s.FirstRunDone {
		return false, nil
	}
	if _, err := a.rotate(); err != nil {
		return false, err
	}
	return true, nil
}

// Token is the current token ("" when none is stored).
func (a *Access) Token() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token
}

// Required reports whether /api/* needs a sign-in from some address (a token or a password is set).
func (a *Access) Required() bool { return a.current().required() }

// rotate makes a new token, stores it (owner-only file, replaced in one step) and starts using it,
// which ends every existing session. The lock covers storing and using, so the token in use is always
// the one in the file, also when two rotations race.
func (a *Access) rotate() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := base64.RawURLEncoding.EncodeToString(b)
	if err := writeFileAtomic(a.dir, TokenFile, []byte(tok+"\n")); err != nil {
		return "", err
	}
	if afterTokenStored != nil {
		afterTokenStored()
	}
	a.token = tok
	return tok, nil
}

// writeFileAtomic replaces dir/name in one step: a new owner-only file is synced, then renamed over it.
func writeFileAtomic(dir, name string, data []byte) error {
	f, err := os.CreateTemp(dir, "."+name+"-*") // created with mode 0600
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(dir, name))
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

func sessionValue(tok, hash string) string {
	m := hmac.New(sha256.New, []byte(tok+"\x00"+hash))
	m.Write([]byte("syp-session-v2"))
	return hex.EncodeToString(m.Sum(nil))
}

func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func setSession(w http.ResponseWriter, tok, hash string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sessionValue(tok, hash), Path: "/",
		MaxAge: 90 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

// apiError writes the contract's error object: {"error":{"code","message"}}.
func apiError(w http.ResponseWriter, status int, code, message string) {
	writeErrorWith(w, status, code, message, nil)
}

// writeErrorWith is apiError with more fields in the error object (e.g. usedBy).
func writeErrorWith(w http.ResponseWriter, status int, code, message string, more map[string]any) {
	e := map[string]any{"code": code, "message": message}
	for k, v := range more {
		e[k] = v
	}
	writeJSONStatus(w, status, map[string]any{"error": e})
}

func writes(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// guard wraps the /api/* handler. Writes must be JSON and, unless they carry a Bearer token, come
// from this site's own pages (Origin / Sec-Fetch-Site against Host): a cookie is sent by the browser
// on its own, so another site must not be able to make one. Every /api/* request except the sign-in
// state, login and logout then needs a token, a password's session, or a private address in home mode.
func (a *Access) guard(next http.Handler) http.Handler {
	sameSite := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-SYP-API", "1") // the API version (UI-API-CONTRACT.md), on every /api answer
		b := bearer(r)
		if writes(r.Method) {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			uploadPreview := mt == "multipart/form-data" && r.URL.Path == "/api/v2/backups/preview" // a backup file to look at
			if mt != "application/json" && !uploadPreview {
				apiError(w, http.StatusForbidden, "csrf", "Changes must be sent as JSON (Content-Type: application/json). Nothing was changed.")
				return
			}
			if b == "" && sameSite.Check(r) != nil {
				apiError(w, http.StatusForbidden, "csrf", "This change did not come from this server's own page, so it was refused. Nothing was changed.")
				return
			}
		}
		switch r.URL.Path {
		case "/api/login", "/api/logout", "/api/auth":
			next.ServeHTTP(w, r)
			return
		}
		now := a.current()
		if !now.required() {
			next.ServeHTTP(w, r)
			return
		}
		if b != "" {
			if same(b, now.tok) {
				next.ServeHTTP(w, r)
				return
			}
			time.Sleep(a.FailDelay)
		} else if now.cookieOK(r) {
			next.ServeHTTP(w, r)
			return
		}
		if now.open(r, a.transport) {
			next.ServeHTTP(w, r)
			return
		}
		apiError(w, http.StatusUnauthorized, "auth", "Sign in with the access token to continue.")
	})
}

// routes adds the sign-in, sign-in choice and token endpoints to the /api mux.
func (a *Access) routes(api *http.ServeMux) {
	api.HandleFunc("GET /api/auth", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, a.view(r, false))
	})
	api.HandleFunc("POST /api/auth/mode", a.setMode)
	api.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Password, Token string }
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
		now := a.current()
		client := a.transport.Client(r)
		if locked, _ := a.login.locked(client, time.Now()); locked {
			w.Header().Set("Retry-After", strconv.Itoa(loginRetryAfter))
			writeErrorWith(w, http.StatusTooManyRequests, "too_many_tries", "Too many tries. Wait a minute, then try again.",
				map[string]any{"retryAfter": loginRetryAfter})
			return
		}
		if !now.required() { // nothing to sign in to
			w.WriteHeader(http.StatusNoContent)
			return
		}
		code, msg := "bad_password", "That password isn't right."
		var ok bool
		switch {
		case in.Password != "":
			// bcrypt reads at most 72 bytes: a longer attempt is refused rather than matched on its start
			ok = now.hash != "" && len(in.Password) <= maxPassword &&
				bcrypt.CompareHashAndPassword([]byte(now.hash), []byte(in.Password)) == nil
		case in.Token != "":
			code, msg = "bad_token", "That token doesn't match this server. Check you copied all of it."
			ok = now.tok != "" && same(strings.TrimSpace(in.Token), now.tok)
		}
		if !ok {
			a.login.fail(client, time.Now())
			time.Sleep(a.FailDelay)
			apiError(w, http.StatusUnauthorized, code, msg)
			return
		}
		setSession(w, now.tok, now.hash)
		w.WriteHeader(http.StatusNoContent)
	})
	api.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writeJSON(w, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/settings/token", func(w http.ResponseWriter, r *http.Request) {
		tok := a.Token()
		if tok == "" {
			apiError(w, http.StatusNotFound, "no_token", "No access token is set, so there is none to show. Sign-in may use a password instead, or none while the server is open only on this computer.")
			return
		}
		writeJSON(w, map[string]string{"token": tok})
	})
	api.HandleFunc("POST /api/settings/token/rotate", func(w http.ResponseWriter, r *http.Request) {
		tok, err := a.rotate()
		if err != nil {
			apiError(w, http.StatusInternalServerError, "token_not_saved", "The new token could not be saved, so the old one still works. "+err.Error())
			return
		}
		setSession(w, tok, a.current().hash) // whoever made the new token stays signed in; everyone else must sign in again
		writeJSON(w, map[string]string{"token": tok})
	})
}
