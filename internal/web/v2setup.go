// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/discover"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// ServerFinder lists the servers on this network that announce themselves. main supplies discover.Finder.
type ServerFinder interface {
	Servers() []discover.Found
}

// WorkerJoiner is how this install joins a server as its worker. main supplies remote.Joiner. web does not import
// remote: remote's tests import web, so that import would be a cycle. Failures carry a JoinCode (see joinCode).
type WorkerJoiner interface {
	// Pair pairs with a code: the worker's id, the name the server gives it, and its token.
	Pair(ctx context.Context, serverURL, code, name string) (id, workerName, token string, err error)
	// Ask asks the server to let this computer in: the request's id, the secret to poll it with, and when it expires.
	Ask(ctx context.Context, serverURL, name, osName string, port int) (id, secret, expiresAt string, err error)
	// Wait polls until the person answers; allowed, it returns the worker's id, name and token.
	Wait(ctx context.Context, serverURL, id, secret, expiresAt string, report func(state string)) (workerID, workerName, token string, err error)
	// Save keeps a worker's credential in dir.
	Save(dir, serverURL, id, name, token string) error
	// Forget removes the credential Save kept in dir, when the role could not be set after it.
	Forget(dir string)
}

// joinTicket is a pending join: what Wait polls with.
type joinTicket struct{ id, secret, expiresAt string }

// joinCode is the code of a failed join ("" for none). Errors from WorkerJoiner carry one.
func joinCode(err error) string {
	var c interface{ JoinCode() string }
	if errors.As(err, &c) {
		return c.JoinCode()
	}
	return ""
}

// SavedRole is the install's saved role: "worker", "server", "unset", or "" when none is saved yet.
func SavedRole(store *jobs.Store) string {
	role, _, _ := store.Meta(setupRoleKey)
	return role
}

// setupRoleKey is the app_meta key the install's role is kept under. It is not a setting: the settings
// PATCH cannot change it, only POST /api/v2/setup/role can.
const setupRoleKey = "setupRole"

// setupTimeout bounds the first contact with a server, which the page waits for.
var setupTimeout = 10 * time.Second

// setupJoin is the join the setup page shows. It lives in memory: a restart ends it, and the saved role stays.
type setupJoin struct {
	serverName, serverURL, state, expiresAt string
}

// setupWaits holds the one join this install waits on. A new request replaces it; gen tells a finished wait
// whether it still matters.
type setupWaits struct {
	store  *jobs.Store
	joiner WorkerJoiner
	onRole func(role string)
	mu     sync.Mutex
	cur    *setupJoin
	gen    int
	cancel context.CancelFunc
}

// stopLocked ends the wait and forgets the join. Call with mu held.
func (sw *setupWaits) stopLocked() {
	if sw.cancel != nil {
		sw.cancel()
	}
	sw.cancel = nil
	sw.gen++
	sw.cur = nil
}

// replace ends any wait and shows j instead (nil shows none).
func (sw *setupWaits) replace(j *setupJoin) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	sw.stopLocked()
	sw.cur = j
	sw.changedLocked()
}

// changedLocked tells the live stream the setup page moved. Call with mu held.
func (sw *setupWaits) changedLocked() {
	if err := sw.store.RecordEvent("setup.changed", map[string]any{"type": "setup.changed"}); err != nil {
		log.Printf("setup: recording the change: %v", err)
	}
}

// view is the setup page's answer for the given role.
func (sw *setupWaits) view(role string) map[string]any {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	var join any
	if j := sw.cur; j != nil {
		var exp any
		if j.expiresAt != "" {
			exp = j.expiresAt
		}
		join = map[string]any{"serverName": j.serverName, "serverUrl": j.serverURL, "state": j.state, "expiresAt": exp}
	}
	return map[string]any{"role": role, "join": join}
}

// set records a wait's state, if that wait still matters.
func (sw *setupWaits) set(gen int, state string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.gen == gen && sw.cur != nil {
		sw.cur.state = state
		sw.changedLocked()
	}
}

// wait polls the server until the person answers, then keeps the answer: the token is saved in dir and the
// role becomes worker when it is allowed. A wait that was replaced or cancelled changes nothing.
func (sw *setupWaits) wait(ctx context.Context, gen int, serverURL string, t joinTicket, dir string) {
	id, name, token, err := sw.joiner.Wait(ctx, serverURL, t.id, t.secret, t.expiresAt, func(state string) { sw.set(gen, state) })
	if ctx.Err() != nil {
		return
	}
	switch code := joinCode(err); {
	case err == nil:
		sw.allowed(gen, serverURL, id, name, token, dir)
	case code == "denied":
		sw.set(gen, "denied")
	case code == "expired":
		sw.set(gen, "expired")
	default:
		log.Printf("setup: the join to %s ended: %v", serverURL, err)
		sw.set(gen, "expired")
	}
}

// allowed keeps an allowed join: its token in dir, and the role worker. The lock is held across both so a
// role change made meanwhile is not overwritten.
func (sw *setupWaits) allowed(gen int, serverURL, id, name, token, dir string) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	if sw.gen != gen || sw.cur == nil {
		return
	}
	if err := sw.joiner.Save(dir, serverURL, id, name, token); err != nil {
		log.Printf("setup: allowed, but the token couldn't be saved in %s: %v", dir, err)
		sw.cur.state = "not_saved"
		sw.changedLocked()
		return
	}
	if err := sw.store.SetMeta(setupRoleKey, "worker"); err != nil {
		log.Printf("setup: allowed, but the role couldn't be saved: %v", err)
		sw.joiner.Forget(dir)
		sw.cur.state = "not_saved"
		sw.changedLocked()
		return
	}
	if sw.onRole != nil {
		sw.onRole("worker")
	}
	sw.cur.state = "allowed"
	sw.changedLocked()
}

// hostName is this computer's name, as the worker command names it.
func hostName() string {
	if h, _ := os.Hostname(); h != "" {
		return h
	}
	return "worker"
}

// validServerURL says whether s is a web address a worker can reach a server at.
func validServerURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// setupServerError answers a failed first contact with the server.
func setupServerError(w http.ResponseWriter, serverURL string, err error) {
	switch joinCode(err) {
	case "not_a_server":
		apiError(w, http.StatusBadGateway, "not_a_server", "That address answered, but it isn't a shrink-your-shit server. Nothing was changed.")
	case "refused":
		apiError(w, http.StatusBadGateway, "server_refused", "The server refused: "+err.Error()+" Nothing was changed.")
	default:
		apiError(w, http.StatusBadGateway, "server_unreachable", "Can't reach "+serverURL+". Check the address and that the server is running. Nothing was changed.")
	}
}

// v2Setup is the install's role (design ONE-COMMAND-SETUP.md section 11): the first page asks "Main
// server" or "Worker for another server". A worker asks a server to let it in (the person allows it on the
// server's Workers page) or pairs with a code; the saved role is worker once it is in.
//   - GET /api/v2/setup: {role: "unset" | "server" | "worker", join: null | {serverName, serverUrl, state,
//     expiresAt}}; state is waiting, allowed, denied, expired, unreachable or not_local.
//   - POST /api/v2/setup/role {role, serverUrl?, pairCode?}:
//     "server" or "unset" (unset cancels a waiting join); "worker" asks to join (202, state waiting) or, with
//     pairCode, pairs (state allowed). First contact is synchronous: server_unreachable, not_a_server,
//     server_refused (502); pair_code_invalid (400); not_local (200, state not_local, role unchanged).
//     invalid_server_url and invalid_role are 400; 409 role_change_busy while jobs run, for a change to worker
//     or unset.
func v2Setup(api *http.ServeMux, store *jobs.Store, ss *settingsStore, eng Engine) {
	sw := &setupWaits{store: store, joiner: eng.Joiner, onRole: eng.RoleChanged}
	workerDir := filepath.Join(eng.DataDir, "worker")
	// current is the saved role. When none is saved, an install that already has a library or has got past
	// the first step predates the role, so it is a server and never sees the enrollment page.
	current := func() (string, error) {
		saved, _, err := store.Meta(setupRoleKey)
		if err != nil || saved != "" {
			return saved, err
		}
		s, err := ss.load()
		if err != nil {
			return "", err
		}
		libs, err := store.Libraries()
		if err != nil {
			return "", err
		}
		if len(libs) > 0 || s.FirstRunStep > 1 {
			return "server", nil
		}
		return "unset", nil
	}
	// busy answers 409 and reports true while jobs run.
	busy := func(w http.ResponseWriter) bool {
		working, err := store.WorkingJobs()
		if err != nil {
			serverError(w, err)
			return true
		}
		if len(working) > 0 {
			writeErrorWith(w, http.StatusConflict, "role_change_busy",
				"Jobs are running, so the role can't be changed yet. Nothing was changed.", map[string]any{"runningJobs": len(working)})
			return true
		}
		return false
	}
	setRole := func(w http.ResponseWriter, role string) bool {
		if err := store.SetMeta(setupRoleKey, role); err != nil {
			serverError(w, err)
			return false
		}
		if eng.RoleChanged != nil {
			eng.RoleChanged(role)
		}
		if err := store.RecordEvent("setup.changed", map[string]any{"type": "setup.changed", "role": role}); err != nil {
			serverError(w, err)
			return false
		}
		return true
	}
	// answerWithRole writes the page's state for the saved role. Call with ss.mu held.
	answerWithRole := func(w http.ResponseWriter, status int) {
		role, err := current()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSONStatus(w, status, sw.view(role))
	}

	// pairWorker pairs with a code (no Allow needed). The request is synchronous: the answer is final.
	pairWorker := func(w http.ResponseWriter, r *http.Request, serverURL, code string) {
		ctx, cancel := context.WithTimeout(r.Context(), setupTimeout)
		defer cancel()
		id, name, token, err := eng.Joiner.Pair(ctx, serverURL, code, hostName())
		if err != nil {
			if joinCode(err) == "pairing_invalid" {
				apiError(w, http.StatusBadRequest, "pair_code_invalid", "This pairing code isn't valid (it works once, for 15 minutes). Make a new one on the server's Workers page. Nothing was changed.")
				return
			}
			setupServerError(w, serverURL, err)
			return
		}
		if err := eng.Joiner.Save(workerDir, serverURL, id, name, token); err != nil {
			apiError(w, http.StatusInternalServerError, "token_not_saved", "The server paired with this computer, but its token couldn't be saved here: "+err.Error()+" Nothing was changed.")
			return
		}
		ss.mu.Lock()
		defer ss.mu.Unlock()
		// The role is written last; if that fails, the saved token goes, so the worker command can't start on it.
		if !setRole(w, "worker") {
			eng.Joiner.Forget(workerDir)
			return
		}
		sw.replace(&setupJoin{serverName: hostOf(serverURL), serverURL: serverURL, state: "allowed"})
		answerWithRole(w, http.StatusOK)
	}

	// askWorker sends the join request and starts waiting for the person's answer in the background.
	askWorker := func(w http.ResponseWriter, r *http.Request, serverURL string) {
		ctx, cancel := context.WithTimeout(r.Context(), setupTimeout)
		defer cancel()
		id, secret, expiresAt, err := eng.Joiner.Ask(ctx, serverURL, hostName(), runtime.GOOS, eng.Port)
		if joinCode(err) == "not_local" {
			ss.mu.Lock()
			defer ss.mu.Unlock()
			sw.replace(&setupJoin{serverName: hostOf(serverURL), serverURL: serverURL, state: "not_local"})
			answerWithRole(w, http.StatusOK)
			return
		}
		if err != nil {
			setupServerError(w, serverURL, err)
			return
		}
		sw.mu.Lock()
		sw.stopLocked()
		wctx, wcancel := context.WithCancel(context.Background())
		sw.cancel = wcancel
		gen := sw.gen
		sw.cur = &setupJoin{serverName: hostOf(serverURL), serverURL: serverURL, state: "waiting", expiresAt: expiresAt}
		sw.changedLocked()
		sw.mu.Unlock()
		// The answer is written before the wait starts, so it shows the join as waiting, not a later state.
		ss.mu.Lock()
		answerWithRole(w, http.StatusAccepted)
		ss.mu.Unlock()
		go sw.wait(wctx, gen, serverURL, joinTicket{id, secret, expiresAt}, workerDir)
	}

	api.HandleFunc("GET /api/v2/setup", func(w http.ResponseWriter, r *http.Request) {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		role, err := current()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, sw.view(role))
	})
	// The servers on this network that announce themselves, so a worker's join page can offer them. Empty, not an
	// error, when none answer or multicast is unavailable.
	api.HandleFunc("GET /api/v2/setup/servers", func(w http.ResponseWriter, r *http.Request) {
		servers := []discover.Found{}
		if eng.Finder != nil {
			if found := eng.Finder.Servers(); found != nil {
				servers = found
			}
		}
		writeJSON(w, map[string]any{"servers": servers})
	})
	api.HandleFunc("POST /api/v2/setup/role", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Role      string `json:"role"`
			ServerURL string `json:"serverUrl"`
			PairCode  string `json:"pairCode"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		switch in.Role {
		case "server", "unset":
		case "worker":
			if !validServerURL(in.ServerURL) {
				apiError(w, http.StatusBadRequest, "invalid_server_url", "The server's address is like http://nas:8484. Nothing was changed.")
				return
			}
		default:
			apiError(w, http.StatusBadRequest, "invalid_role", "The role is server, worker or unset. Nothing was changed.")
			return
		}
		if in.Role == "worker" {
			// The request reaches a server, so no lock is held across it; only the checks and the save are.
			ss.mu.Lock()
			cur, err := current()
			if err != nil {
				ss.mu.Unlock()
				serverError(w, err)
				return
			}
			if cur != "unset" {
				ss.mu.Unlock()
				apiError(w, http.StatusConflict, "role_already_set", "This install is already a "+cur+", so it can't ask to join a server. Set its role to unset first. Nothing was changed.")
				return
			}
			blocked := busy(w)
			ss.mu.Unlock()
			if blocked {
				return
			}
			if eng.Joiner == nil {
				apiError(w, http.StatusInternalServerError, "setup_unavailable", "This install can't join a server. Nothing was changed.")
				return
			}
			if in.PairCode != "" {
				pairWorker(w, r, in.ServerURL, in.PairCode)
			} else {
				askWorker(w, r, in.ServerURL)
			}
			return
		}
		ss.mu.Lock()
		defer ss.mu.Unlock()
		if in.Role == "unset" && busy(w) {
			return
		}
		sw.replace(nil)
		if !setRole(w, in.Role) {
			return
		}
		answerWithRole(w, http.StatusOK)
	})
}

// hostOf is the server's address without its scheme: the name the page shows when the server gives none.
func hostOf(serverURL string) string {
	if u, err := url.Parse(serverURL); err == nil && u.Host != "" {
		return u.Host
	}
	return serverURL
}
