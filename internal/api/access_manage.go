// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package api

import (
	"errors"
	"net/http"

	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
)

// The access routes (page 11, Access): the password, and the access token and its rotation. Each needs the person's
// session cookie. A bearer token is enough for the other routes, but not for these: the token is the thing these change.

// sessionOf is the live session cookie the request carries, or "" with false when it has none.
func sessionOf(r *http.Request, rs *Deps) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return "", false
	}
	live, err := rs.Access.LookupSession(r.Context(), c.Value)
	if err != nil || !live {
		return "", false
	}
	return c.Value, true
}

// refuseWithoutSession answers the 401 a bearer-only call gets, and reports whether it did.
func refuseWithoutSession(w http.ResponseWriter, r *http.Request, rs *Deps) (string, bool) {
	session, ok := sessionOf(r, rs)
	if !ok {
		writeError(w, http.StatusUnauthorized, "signed_out", "Sign in first.", "", 0)
		return "", false
	}
	return session, true
}

// postAccessPassword is POST /api/v2/access/password {password}: sets a new password and ends every other session. The
// session that made the change stays signed in.
func (s *Server) postAccessPassword(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	session, ok := refuseWithoutSession(w, r, rs)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	err := rs.Access.ChangePassword(r.Context(), session, body.Password)
	switch {
	case errors.Is(err, system.ErrPasswordTooShort):
		writeError(w, http.StatusBadRequest, "password_too_short", "Use at least 8 characters.", "password", 0)
		return
	case errors.Is(err, system.ErrPasswordTooLong):
		writeError(w, http.StatusBadRequest, "password_too_long", "Use at most 72 bytes; fewer if the characters are wider.", "password", 0)
		return
	case err != nil:
		internalError(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getAccessToken is GET /api/v2/access/token: the current token. 404 no_token before one exists.
func (s *Server) getAccessToken(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	if _, ok := refuseWithoutSession(w, r, rs); !ok {
		return
	}
	token, err := rs.Access.Token(r.Context())
	if errors.Is(err, system.ErrNoToken) {
		writeError(w, http.StatusNotFound, "no_token", "There is no access token yet.", "", 0)
		return
	}
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// postAccessTokenRotate is POST /api/v2/access/token/rotate: a new token. The old one stops working at once, and every
// other session ends (L-ACC-1); the session that rotated stays signed in.
func (s *Server) postAccessTokenRotate(w http.ResponseWriter, r *http.Request) {
	rs := s.ready.Load()
	if rs == nil {
		writeError(w, http.StatusServiceUnavailable, "starting", "Starting up…", "", 0)
		return
	}
	session, ok := refuseWithoutSession(w, r, rs)
	if !ok {
		return
	}
	token, err := rs.Access.RotateToken(r.Context(), session)
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}
