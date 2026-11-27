// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package api serves the HTTP and SSE routes, decoding requests and encoding responses.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
)

const (
	sessionCookie = "syp_session"
	sessionMaxAge = 30 * 24 * 60 * 60 // seconds; the server renews the session on use
	maxBody       = 64 << 10
)

// readyState exists once the store is open and migrated. Before that the server answers 503 starting.
type readyState struct {
	access *system.Access
	events *events.Bus
}

// Server is the api's door: the gate every request passes, and the /api/v2 routes behind it.
type Server struct {
	version string
	ready   atomic.Pointer[readyState]
	ui      fs.FS // the built web interface; nil answers every page with the JSON 404
	csrf    http.CrossOriginProtection
	mux     *http.ServeMux
}

// New makes a server that answers 503 starting until Ready is called.
func New(version string) *Server {
	s := &Server{version: version, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/v2/health", s.health)
	s.mux.HandleFunc("GET /api/v2/setup", s.getSetup)
	s.mux.HandleFunc("POST /api/v2/setup", s.postSetup)
	s.mux.HandleFunc("POST /api/v2/signin", s.signin)
	s.mux.HandleFunc("POST /api/v2/signout", s.signout)
	s.mux.HandleFunc("/", s.static)
	return s
}

// Ready lets requests through. events receives a warning for each refused sign-in; it may be nil.
func (s *Server) Ready(a *system.Access, bus *events.Bus) {
	s.ready.Store(&readyState{access: a, events: bus})
}

// ServeHTTP is the gate: starting, setup, the ?token= link, the bearer token, CSRF and the session,
// in that order. Then the route.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	ctx := r.Context()
	apiPath := strings.HasPrefix(r.URL.Path, "/api/")
	unsafe := !(r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions)

	needs, err := rs.access.NeedsAccount(ctx)
	if err != nil {
		internalError(w)
		return
	}
	if needs {
		// Before setup the pages themselves answer (the first-run page is one of them) and so does the
		// allow-list; every other API route says set up first (backend-plan §12).
		if unsafe && !s.csrfOK(r) {
			writeError(w, http.StatusForbidden, "csrf", "That request came from another site, or is missing its header.", "", 0)
			return
		}
		if !apiPath ||
			r.URL.Path == "/api/v2/setup" && (r.Method == http.MethodGet || r.Method == http.MethodPost) ||
			r.URL.Path == "/api/v2/health" && r.Method == http.MethodGet {
			s.mux.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusForbidden, "setup_needed", "Set up first.", "", 0)
		return
	}

	if r.Method == http.MethodGet && !apiPath && r.URL.Query().Has("token") {
		s.swapToken(w, r, rs)
		return
	}

	byBearer := false
	if token, ok := bearerToken(r); ok {
		if err := rs.access.CheckBearer(ctx, token); err != nil {
			if errors.Is(err, system.ErrBadToken) {
				writeError(w, http.StatusUnauthorized, "bad_token", "That token isn't right.", "", 0)
				return
			}
			internalError(w)
			return
		}
		byBearer = true // bearer calls skip the CSRF check (L-ACC-2)
	}

	if unsafe && !byBearer && !s.csrfOK(r) {
		writeError(w, http.StatusForbidden, "csrf", "That request came from another site, or is missing its header.", "", 0)
		return
	}

	authed := byBearer
	if !authed {
		if c, err := r.Cookie(sessionCookie); err == nil {
			live, err := rs.access.LookupSession(ctx, c.Value)
			if err != nil {
				internalError(w)
				return
			}
			authed = live
		}
	}
	if !authed && apiPath && !publicRoute(r.URL.Path) {
		writeError(w, http.StatusUnauthorized, "signed_out", "Sign in first.", "", 0)
		return
	}
	s.mux.ServeHTTP(w, r.WithContext(context.WithValue(ctx, authedKey{}, authed)))
}

// authedKey marks a request that carries a valid session or bearer token, for handlers that answer more to a signed-in caller.
type authedKey struct{}

func isAuthed(r *http.Request) bool {
	v, _ := r.Context().Value(authedKey{}).(bool)
	return v
}

// publicRoute is what answers without a session once setup is done: health, setup, sign-in, sign-out.
// The pages (everything outside /api) answer too; the data behind them does not.
func publicRoute(path string) bool {
	switch path {
	case "/api/v2/health", "/api/v2/setup", "/api/v2/signin", "/api/v2/signout":
		return true
	}
	return false
}

// csrfOK is true for a request the browser sent from this origin with the X-SYP header the UI always sends.
func (s *Server) csrfOK(r *http.Request) bool {
	if r.Header.Get("X-SYP") == "" {
		return false
	}
	return s.csrf.Check(r) == nil
}

// swapToken signs the browser in with a ?token= link and redirects to the same page without the token,
// so the token stays out of the address bar and history (§12). A refused token still redirects; the page
// then shows its sign-in form.
func (s *Server) swapToken(w http.ResponseWriter, r *http.Request, rs *readyState) {
	token := r.URL.Query().Get("token")
	cookie, err := rs.access.SignInToken(r.Context(), remoteHost(r), token, r.UserAgent())
	if err == nil {
		setSessionCookie(w, r, cookie, sessionMaxAge)
	} else {
		s.refused(rs, r, err)
	}
	u := *r.URL
	q := u.Query()
	q.Del("token")
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"ok": true, "starting": false}
	if isAuthed(r) {
		body["version"] = s.version // the version is for a signed-in caller only (backend-plan §17)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) getSetup(w http.ResponseWriter, r *http.Request) {
	needs, err := s.ready.Load().access.NeedsAccount(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needsAccount": needs, "starting": false})
}

// fixedStarterID is stage 1's one starter (build-order stage 1 item 5). The Start page always sends a
// starter, so setup accepts this one; item 9 makes the workflow from it in the same call.
const fixedStarterID = "shrink-videos"

// postSetup sets the password and signs the browser in. The library part is not built yet: a non-null
// library, or any starter but the fixed one, is refused, so nothing is half set up.
func (s *Server) postSetup(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	var body struct {
		Account struct {
			Password string `json:"password"`
		} `json:"account"`
		Library json.RawMessage `json:"library"`
		Starter json.RawMessage `json:"starter"`
	}
	if !decode(w, r, &body) {
		return
	}
	if !isNull(body.Library) {
		writeError(w, http.StatusBadRequest, "invalid_request", "A library can't be set up yet. Send library as null.", "library", 0)
		return
	}
	if !isNull(body.Starter) {
		var st struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body.Starter, &st) != nil || st.ID != fixedStarterID {
			writeError(w, http.StatusBadRequest, "invalid_request", "Only the Shrink videos starter can be set up for now.", "starter", 0)
			return
		}
	}
	ctx := r.Context()
	err := rs.access.SetPassword(ctx, body.Account.Password)
	switch {
	case errors.Is(err, system.ErrSetupDone):
		writeError(w, http.StatusConflict, "setup_done", "Setup is already done.", "", 0)
		return
	case errors.Is(err, system.ErrInvalidPassword):
		writeError(w, http.StatusBadRequest, "invalid_password", "A password needs at least 8 characters and at most 72 bytes.", "account.password", 0)
		return
	case err != nil:
		internalError(w)
		return
	}
	cookie, err := rs.access.CreateSession(ctx, r.UserAgent(), remoteHost(r))
	if err != nil {
		internalError(w)
		return
	}
	setSessionCookie(w, r, cookie, sessionMaxAge)
	writeJSON(w, http.StatusOK, map[string]any{"scanStarted": false})
}

func (s *Server) signin(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	var body struct {
		Password *string `json:"password"`
		Token    *string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	if (body.Password == nil) == (body.Token == nil) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Send a password or a token, not both.", "", 0)
		return
	}
	ctx := r.Context()
	var cookie string
	var err error
	if body.Password != nil {
		cookie, err = rs.access.SignInPassword(ctx, remoteHost(r), *body.Password, r.UserAgent())
	} else {
		cookie, err = rs.access.SignInToken(ctx, remoteHost(r), *body.Token, r.UserAgent())
	}
	if err != nil {
		s.refusedWrite(w, rs, r, err)
		return
	}
	setSessionCookie(w, r, cookie, sessionMaxAge)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) signout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.ready.Load().access.EndSession(r.Context(), c.Value); err != nil {
			internalError(w)
			return
		}
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// refusedWrite answers a failed sign-in with the envelope and its status.
func (s *Server) refusedWrite(w http.ResponseWriter, rs *readyState, r *http.Request, err error) {
	var locked *system.LockedError
	switch {
	case errors.As(err, &locked):
		w.Header().Set("Retry-After", strconv.Itoa(locked.RetryAfter))
		writeError(w, http.StatusTooManyRequests, "too_many_tries", "Too many tries. Wait a minute, then try again.", "", locked.RetryAfter)
	case errors.Is(err, system.ErrBadPassword):
		s.refused(rs, r, err)
		writeError(w, http.StatusUnauthorized, "bad_password", "That password isn't right.", "", 0)
	case errors.Is(err, system.ErrBadToken):
		s.refused(rs, r, err)
		writeError(w, http.StatusUnauthorized, "bad_token", "That token isn't right.", "", 0)
	case errors.Is(err, system.ErrNoAccount):
		writeError(w, http.StatusForbidden, "setup_needed", "Set up first.", "", 0)
	default:
		internalError(w)
	}
}

// refused leaves one warning in the system log. It names the address and the reason, never the secret.
func (s *Server) refused(rs *readyState, r *http.Request, err error) {
	if rs.events == nil {
		return
	}
	reason := "a wrong password"
	switch {
	case errors.Is(err, system.ErrBadToken):
		reason = "a wrong token"
	case errors.Is(err, system.ErrBadPassword):
	default:
		return // a locked-out attempt is refused without a new warning
	}
	_ = rs.events.Emit(events.Event{
		Kind:    events.KindWarning,
		Level:   events.LevelWarn,
		Source:  "api",
		Message: "sign-in refused from " + remoteHost(r) + ": " + reason,
	})
}

// bearerToken returns the token from an Authorization: Bearer header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const scheme = "bearer "
	if len(h) <= len(scheme) || !strings.EqualFold(h[:len(scheme)], scheme) {
		return "", false
	}
	return strings.TrimSpace(h[len(scheme):]), true
}

// setSessionCookie sets the session cookie: HttpOnly, SameSite=Lax, Path /, Secure over TLS (L-ACC-1).
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   maxAge,
	})
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func isNull(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null"
}

// decode reads one JSON body of at most maxBody bytes. It writes the 400 itself and returns false on error.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "The request is not valid JSON.", "", 0)
		return false
	}
	return true
}

func notFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "not_found", "There is no such page.", "", 0)
}

func internalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Try again.", "", 0)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes the error envelope {error:{code, message, field?, retryAfter?}} (page 00).
func writeError(w http.ResponseWriter, status int, code, message, field string, retryAfter int) {
	body := struct {
		Error struct {
			Code       string `json:"code"`
			Message    string `json:"message"`
			Field      string `json:"field,omitempty"`
			RetryAfter int    `json:"retryAfter,omitempty"`
		} `json:"error"`
	}{}
	body.Error.Code = code
	body.Error.Message = message
	body.Error.Field = field
	body.Error.RetryAfter = retryAfter
	writeJSON(w, status, body)
}
