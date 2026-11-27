// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package api serves the HTTP and SSE routes, decoding requests and encoding responses.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PeiceOfToast/shrink-your-shit/internal/events"
	"github.com/PeiceOfToast/shrink-your-shit/internal/flow"
	"github.com/PeiceOfToast/shrink-your-shit/internal/jobs"
	"github.com/PeiceOfToast/shrink-your-shit/internal/library"
	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
	"github.com/PeiceOfToast/shrink-your-shit/internal/system"
)

const (
	sessionCookie = "syp_session"
	sessionMaxAge = 30 * 24 * 60 * 60 // seconds; the server renews the session on use
	maxBody       = 64 << 10
)

// Deps is the world behind the routes: each module's API, bound when the store is open and
// migrated. Before that the server answers 503 starting.
type Deps struct {
	Access *system.Access
	Events *events.Bus
	DB     *store.DB
	// Workflows and Catalog write setup's one transaction; Jobs and Scanner are what the library
	// routes read and start. They are interfaces so a test can fail one step or record a scan.
	Workflows Workflows
	Catalog   *library.Catalog
	Jobs      *jobs.Queue
	Scanner   Scanner
	// Wake asks the worker to pick up newly queued jobs. Stage 1 has no worker loop; cmd wires a
	// no-op. nil means no wake.
	Wake func()
	// DiskFree reads the free space of the volume that holds a folder. nil reports 0.
	DiskFree func(path string) (uint64, error)
}

// Workflows is flow.Store as the api uses it.
type Workflows interface {
	CreateFromStarterTx(ctx context.Context, tx *sql.Tx, starterID, name string) (int64, int, error)
	Get(ctx context.Context, id int64) (flow.Workflow, error)
	// S2-3: the list, create, the editor's draft, publish, revisions and restore, duplicate, delete.
	List(ctx context.Context) ([]flow.ListItem, error)
	Create(ctx context.Context, f flow.Flow, madeWith string) (int64, int, error)
	Draft(ctx context.Context, id int64) (flow.DraftInfo, error)
	SaveDraft(ctx context.Context, id int64, f flow.Flow, baseRev int) (int, error)
	// S2-4: the guided page's save. baseRev is the draft revision the page read, when it sends
	// one; the save refuses both a stale revision and a draft the editor changed out from under it.
	SaveGuidedDraft(ctx context.Context, id int64, f flow.Flow, baseRev *int) (int, error)
	Publish(ctx context.Context, id int64, given *flow.Flow, vc flow.ValidateContext) (int, error)
	Revisions(ctx context.Context, id int64) ([]flow.RevisionInfo, error)
	Restore(ctx context.Context, id int64, rev int) (int, error)
	Duplicate(ctx context.Context, id int64) (flow.Workflow, error)
	Delete(ctx context.Context, id int64) error
}

// Scanner is library.Scanner as the api uses it: one scan of one library.
type Scanner interface {
	Scan(ctx context.Context, lib library.Library) (library.ScanResult, error)
}

// Server is the api's door: the gate every request passes, and the /api/v2 routes behind it.
type Server struct {
	version string
	ready   atomic.Pointer[Deps]
	ui      fs.FS // the built web interface; nil answers every page with the JSON 404
	csrf    http.CrossOriginProtection
	mux     *http.ServeMux
	jobs    atomic.Pointer[jobDeps]
	// stopped closes when the server shuts down, so open event streams end. pingEvery overrides the 15 s ping in tests.
	stopped   chan struct{}
	closeOnce sync.Once
	pingEvery time.Duration
}

// New makes a server that answers 503 starting until Ready is called.
func New(version string) *Server {
	s := &Server{version: version, mux: http.NewServeMux(), stopped: make(chan struct{})}
	s.mux.HandleFunc("GET /api/v2/health", s.health)
	s.mux.HandleFunc("GET /api/v2/setup", s.getSetup)
	s.mux.HandleFunc("POST /api/v2/setup", s.postSetup)
	s.mux.HandleFunc("POST /api/v2/signin", s.signin)
	s.mux.HandleFunc("POST /api/v2/signout", s.signout)
	s.mux.HandleFunc("GET /api/v2/jobs", s.listJobs)
	s.mux.HandleFunc("GET /api/v2/jobs/{id}", s.getJob)
	s.mux.HandleFunc("GET /api/v2/jobs/{id}/neighbours", s.neighbours)
	s.mux.HandleFunc("GET /api/v2/jobs/{id}/events", s.jobLog)
	s.mux.HandleFunc("GET /api/v2/jobs/{id}/history", s.jobHistory)
	s.mux.HandleFunc("GET /api/v2/events", s.stream)
	// Access (R1-9a): the first-run page's calls, the languages and the guide.
	s.mux.HandleFunc("GET /api/v2/languages", s.getLanguages)
	s.mux.HandleFunc("GET /api/v2/workflows/starters", s.getStarters)
	s.mux.HandleFunc("GET /api/v2/workflows/starters/{id}", s.getStarter)
	s.mux.HandleFunc("GET /api/v2/workflows/guide", s.getGuide)
	// The flow editor's check (S2-2).
	s.mux.HandleFunc("POST /api/v2/workflows/{id}/validate", s.validateWorkflow)
	// Workflows (S2-3): the list, create, the editor's draft, publish, revisions, duplicate, delete.
	s.mux.HandleFunc("GET /api/v2/workflows", s.listWorkflows)
	s.mux.HandleFunc("POST /api/v2/workflows", s.createWorkflow)
	// The guided builder's "What this does" (S2-4). Literal, so it wins over the {rest...} subtree;
	// the answers themselves are below the subtree, beside the draft they save.
	s.mux.HandleFunc("POST /api/v2/workflows/preview", s.previewWorkflow)
	// Everything below /workflows/ is the {id} subtree. It is one handler per method because Go's
	// mux cannot rank "{id}/revisions" against the starters' literal "/workflows/starters/{id}"
	// (both match "/workflows/starters/revisions"), and neither is more specific. The trailing
	// wildcard is a subset of the literal routes, so those still win.
	s.mux.HandleFunc("GET /api/v2/workflows/{rest...}", s.workflowSubtree)
	s.mux.HandleFunc("PUT /api/v2/workflows/{rest...}", s.workflowSubtree)
	s.mux.HandleFunc("POST /api/v2/workflows/{rest...}", s.workflowSubtree)
	s.mux.HandleFunc("DELETE /api/v2/workflows/{rest...}", s.workflowSubtree)
	s.mux.HandleFunc("GET /api/v2/folders", s.getFolders)
	s.mux.HandleFunc("POST /api/v2/libraries/check", s.checkLibraryFolder)
	// Libraries (R1-9a).
	s.mux.HandleFunc("GET /api/v2/libraries", s.getLibraries)
	s.mux.HandleFunc("POST /api/v2/libraries", s.postLibrary)
	s.mux.HandleFunc("GET /api/v2/libraries/{id}", s.getLibrary)
	s.mux.HandleFunc("POST /api/v2/libraries/{id}/scan", s.scanLibrary)
	s.mux.HandleFunc("POST /api/v2/libraries/scan", s.scanAllLibraries)
	// The steps' cards (S2-1): the flow editor's form, from the step registry.
	s.mux.HandleFunc("GET /api/v2/step-definitions", s.getStepDefinitions)
	// Summary and attention (R1-9a).
	s.mux.HandleFunc("GET /api/v2/summary", s.getSummary)
	s.mux.HandleFunc("GET /api/v2/attention", s.getAttention)
	s.mux.HandleFunc("/", s.static)
	return s
}

// Ready lets requests through. Events receives a warning for each refused sign-in; it may be nil.
func (s *Server) Ready(d Deps) {
	s.ready.Store(&d)
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

	needs, err := rs.Access.NeedsAccount(ctx)
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
		if !apiPath || setupRouteIsAllowed(r.Method, r.URL.Path) {
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
		if err := rs.Access.CheckBearer(ctx, token); err != nil {
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
			live, err := rs.Access.LookupSession(ctx, c.Value)
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

// setupAllowList is what answers before an account exists, with no cookie: /setup and /health, and
// the first-run page's own calls (page 13). Each is the same trust as POST /setup itself. Once setup
// is done they need a session like every other route.
var setupAllowList = []struct{ method, path string }{
	{http.MethodGet, "/api/v2/setup"},
	{http.MethodPost, "/api/v2/setup"},
	{http.MethodGet, "/api/v2/health"},
	{http.MethodGet, "/api/v2/languages"},
	{http.MethodGet, "/api/v2/workflows/starters"},
	{http.MethodGet, "/api/v2/workflows/guide"},
	{http.MethodGet, "/api/v2/folders"},
	{http.MethodPost, "/api/v2/libraries/check"},
}

// setupRouteIsAllowed reports whether a method and path may answer before setup. The starter detail
// route takes an id, so it matches by prefix.
func setupRouteIsAllowed(method, path string) bool {
	for _, row := range setupAllowList {
		if row.method == method && row.path == path {
			return true
		}
	}
	return method == http.MethodGet && strings.HasPrefix(path, "/api/v2/workflows/starters/")
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
func (s *Server) swapToken(w http.ResponseWriter, r *http.Request, rs *Deps) {
	token := r.URL.Query().Get("token")
	cookie, err := rs.Access.SignInToken(r.Context(), remoteHost(r), token, r.UserAgent())
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
	needs, err := s.ready.Load().Access.NeedsAccount(r.Context())
	if err != nil {
		internalError(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"needsAccount": needs, "starting": false})
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
		cookie, err = rs.Access.SignInPassword(ctx, remoteHost(r), *body.Password, r.UserAgent())
	} else {
		cookie, err = rs.Access.SignInToken(ctx, remoteHost(r), *body.Token, r.UserAgent())
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
		if err := s.ready.Load().Access.EndSession(r.Context(), c.Value); err != nil {
			internalError(w)
			return
		}
	}
	setSessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// refusedWrite answers a failed sign-in with the envelope and its status.
func (s *Server) refusedWrite(w http.ResponseWriter, rs *Deps, r *http.Request, err error) {
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
func (s *Server) refused(rs *Deps, r *http.Request, err error) {
	if rs.Events == nil {
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
	_ = rs.Events.Emit(events.Event{
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
