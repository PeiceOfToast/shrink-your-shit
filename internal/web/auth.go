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
	"strings"
	"sync"
	"time"
)

// Access to /api/* is one shared token. It is required whenever a token is stored, and one is made
// and stored the first time the program listens beyond this computer. On loopback with no token,
// /api/* is open. A browser signs in once (POST /api/login) and then sends a session cookie; scripts
// send "Authorization: Bearer <token>". The cookie's value is derived from the token, so sessions
// survive a restart and making a new token ends all of them.

// TokenFile is the token's file name inside the data folder.
const TokenFile = "access-token"

const sessionCookie = "syp_session"

// afterTokenStored runs (in tests only) between storing a new token and using it.
var afterTokenStored func()

// Access holds the token and checks requests against it.
type Access struct {
	dir   string
	mu    sync.RWMutex
	token string // "" = none stored (only possible on loopback)

	// FailDelay is how long a wrong token waits before the answer, to slow down guessing.
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

// OpenAccess loads the token stored in dataDir. When exposed (listening beyond loopback) and none is
// stored, it makes and stores one and reports created, so the caller can show the sign-in address once.
func OpenAccess(dataDir string, exposed bool) (a *Access, created bool, err error) {
	tok, err := ReadToken(dataDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	a = &Access{dir: dataDir, token: tok, FailDelay: 500 * time.Millisecond}
	if tok == "" && exposed {
		if _, err := a.rotate(); err != nil {
			return nil, false, err
		}
		created = true
	}
	return a, created, nil
}

// Token is the current token ("" when none is stored).
func (a *Access) Token() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.token
}

// Required reports whether /api/* needs the token.
func (a *Access) Required() bool { return a.Token() != "" }

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
	f, err := os.CreateTemp(a.dir, ".access-token-*") // created with mode 0600
	if err != nil {
		return "", err
	}
	_, err = f.WriteString(tok + "\n")
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), filepath.Join(a.dir, TokenFile))
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if afterTokenStored != nil {
		afterTokenStored()
	}
	a.token = tok
	return tok, nil
}

func sessionValue(tok string) string {
	m := hmac.New(sha256.New, []byte(tok))
	m.Write([]byte("syp-session-v1"))
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

func setSession(w http.ResponseWriter, tok string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sessionValue(tok), Path: "/",
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": e})
}

func writes(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// guard wraps the /api/* handler. Writes must be JSON and, unless they carry a Bearer token, come
// from this site's own pages (Origin / Sec-Fetch-Site against Host): a cookie is sent by the browser
// on its own, so another site must not be able to make one. Every /api/* request except login and
// logout then needs the token, when one is stored.
func (a *Access) guard(next http.Handler) http.Handler {
	sameSite := http.NewCrossOriginProtection()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-SYP-API", "1") // the API version (UI-API-CONTRACT.md), on every /api answer
		b := bearer(r)
		if writes(r.Method) {
			mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if mt != "application/json" {
				apiError(w, http.StatusForbidden, "csrf", "Changes must be sent as JSON (Content-Type: application/json). Nothing was changed.")
				return
			}
			if b == "" && sameSite.Check(r) != nil {
				apiError(w, http.StatusForbidden, "csrf", "This change did not come from this server's own page, so it was refused. Nothing was changed.")
				return
			}
		}
		if r.URL.Path == "/api/login" || r.URL.Path == "/api/logout" {
			next.ServeHTTP(w, r)
			return
		}
		tok := a.Token()
		if tok == "" {
			next.ServeHTTP(w, r)
			return
		}
		if b != "" {
			if same(b, tok) {
				next.ServeHTTP(w, r)
				return
			}
			time.Sleep(a.FailDelay)
		} else if c, err := r.Cookie(sessionCookie); err == nil && same(c.Value, sessionValue(tok)) {
			next.ServeHTTP(w, r)
			return
		}
		apiError(w, http.StatusUnauthorized, "auth", "Sign in with the access token to continue.")
	})
}

// routes adds the sign-in and token endpoints to the /api mux.
func (a *Access) routes(api *http.ServeMux) {
	api.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Token string }
		json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in)
		tok := a.Token()
		if tok == "" { // nothing to sign in to
			writeJSON(w, map[string]bool{"ok": true})
			return
		}
		if !same(strings.TrimSpace(in.Token), tok) {
			time.Sleep(a.FailDelay)
			apiError(w, http.StatusUnauthorized, "bad_token", "That token doesn't match this server. Check you copied all of it.")
			return
		}
		setSession(w, tok)
		writeJSON(w, map[string]bool{"ok": true})
	})
	api.HandleFunc("POST /api/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
			HttpOnly: true, SameSite: http.SameSiteStrictMode})
		writeJSON(w, map[string]bool{"ok": true})
	})
	api.HandleFunc("GET /api/settings/token", func(w http.ResponseWriter, r *http.Request) {
		tok := a.Token()
		if tok == "" {
			apiError(w, http.StatusNotFound, "no_token", "No access token is set: this server only listens on this computer, so it needs none.")
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
		setSession(w, tok) // whoever made the new token stays signed in; everyone else must sign in again
		writeJSON(w, map[string]string{"token": tok})
	})
}
