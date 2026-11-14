// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Join requests (one-command setup, E2). A new worker install asks to join from the server's own network; the
// person allows it on the Workers page instead of copying a pairing code. Allowing pairs exactly as a pairing
// code does (NewPairing then Pair), so there is one credential mechanism. Requests are kept in the store, so an
// allowed one survives a restart; the worker gets its token when it picks the allow up.

// JoinFor is how long a join request waits for the person's answer.
var JoinFor = 10 * time.Minute

// maxPendingJoins caps requests not yet answered or dismissed, so a machine on the network can't fill the list.
const maxPendingJoins = 50

// joinPort is the port a joining worker answers on: the server's default, which the Workers page links to.
const joinPort = "8484"

var (
	errJoinNotFound = errors.New("join request not found")
	errJoinExpired  = errors.New("join request expired")
	errJoinAnswered = errors.New("join request already answered")
	errJoinFull     = errors.New("too many join requests waiting")
)

// joinReq is one request. decision is "" while unanswered, else "allowed" or "denied".
type joinReq struct {
	id, secretHash     string
	name, os, arch     string
	version            string
	hardware           *string
	apiLevel           int
	capabilities       string
	address            netip.Addr
	askedAt, expiresAt time.Time
	decision           string
	workerID           string // set when allowed; the worker gets its token by polling with its secret
	workerName         string
	port               int // the web port the worker answers on; 0 means joinPort
}

type joinBook struct {
	store *jobs.Store
	mu    sync.Mutex // one answer at a time: allow, deny and dismiss check the request, then write it
}

func newJoinBook(store *jobs.Store) *joinBook {
	return &joinBook{store: store}
}

// joinRowOf is r as the store keeps it.
func joinRowOf(r *joinReq) jobs.JoinRequest {
	addr := ""
	if r.address.IsValid() {
		addr = r.address.String()
	}
	return jobs.JoinRequest{ID: r.id, SecretHash: r.secretHash, Name: r.name, OS: r.os, Arch: r.arch, Version: r.version,
		Hardware: r.hardware, APILevel: r.apiLevel, Capabilities: r.capabilities, Address: addr, Port: r.port,
		AskedNS: r.askedAt.UnixNano(), ExpiresNS: r.expiresAt.UnixNano(), Decision: r.decision,
		WorkerID: r.workerID, WorkerName: r.workerName}
}

// joinReqOf is a stored request as the book uses it.
func joinReqOf(row jobs.JoinRequest) joinReq {
	addr, _ := netip.ParseAddr(row.Address)
	return joinReq{id: row.ID, secretHash: row.SecretHash, name: row.Name, os: row.OS, arch: row.Arch, version: row.Version,
		hardware: row.Hardware, apiLevel: row.APILevel, capabilities: row.Capabilities, address: addr, port: row.Port,
		askedAt: time.Unix(0, row.AskedNS), expiresAt: time.Unix(0, row.ExpiresNS), decision: row.Decision,
		workerID: row.WorkerID, workerName: row.WorkerName}
}

func joinRandom(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func joinDigest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// changed tells the live stream that the Workers page's list moved.
func (b *joinBook) changed() {
	b.store.RecordEvent("workers.changed", map[string]any{"type": "workers.changed"})
}

// prune drops the requests whose window has passed. An allowed one nobody picked up takes its unseen worker with
// it. Unanswered ones stay (shown as expired) until the person dismisses them. Call with mu held.
func (b *joinBook) prune(now time.Time) {
	if err := b.store.PruneJoins(now.UnixNano()); err != nil {
		log.Printf("join: dropping expired requests failed: %v", err)
	}
}

// add records a request asked now and starts its window. It returns the id and the secret the worker polls with.
func (b *joinBook) add(r *joinReq, now time.Time) (id, secret string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(now)
	waiting, err := b.store.CountWaitingJoins()
	if err != nil {
		return "", "", err
	}
	if waiting >= maxPendingJoins {
		// Expired requests otherwise stay until dismissed, so 50 stale asks would lock out every new join.
		if err := b.store.DropOldestExpiredJoin(now.UnixNano()); err != nil {
			return "", "", err
		}
		if waiting, err = b.store.CountWaitingJoins(); err != nil {
			return "", "", err
		}
	}
	if waiting >= maxPendingJoins {
		return "", "", errJoinFull
	}
	id = "j-" + joinRandom(9)
	secret = joinRandom(32)
	r.id, r.secretHash, r.askedAt, r.expiresAt = id, joinDigest(secret), now, now.Add(JoinFor)
	if err := b.store.AddJoin(joinRowOf(r)); err != nil {
		return "", "", err
	}
	time.AfterFunc(JoinFor, b.changed) // the list shows it as expired then
	b.changed()
	return id, secret, nil
}

// state is what the worker and the person see: waiting, expired, allowed or denied. Call with mu held.
func (b *joinBook) state(r *joinReq, now time.Time) string {
	switch {
	case r.decision == "picked":
		return "allowed" // its token was handed out; a second poll finds nothing (PickUpJoin)
	case r.decision != "":
		return r.decision
	case !now.Before(r.expiresAt):
		return "expired"
	}
	return "waiting"
}

// get answers a poll: the state, and the request once answered. A wrong secret and an unknown id look the same
// (errJoinNotFound), so a guess learns nothing.
func (b *joinBook) get(id, secret string, now time.Time) (string, joinReq, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(now)
	row, err := b.store.Join(id)
	if errors.Is(err, jobs.ErrNotFound) {
		return "", joinReq{}, errJoinNotFound
	}
	if err != nil {
		return "", joinReq{}, err
	}
	req := joinReqOf(row)
	if subtle.ConstantTimeCompare([]byte(joinDigest(secret)), []byte(req.secretHash)) != 1 {
		return "", joinReq{}, errJoinNotFound
	}
	return b.state(&req, now), req, nil
}

// pending lists requests the person still has to act on (waiting or expired), newest first.
func (b *joinBook) pending(now time.Time) []joinReq {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(now)
	rows, err := b.store.JoinsWaiting()
	if err != nil {
		log.Printf("join: listing requests failed: %v", err)
	}
	out := []joinReq{}
	for _, row := range rows {
		out = append(out, joinReqOf(row))
	}
	return out
}

// waiting counts requests still open for an answer (not the expired ones). This is the summary badge.
func (b *joinBook) waiting(now time.Time) int {
	n := 0
	for _, r := range b.pending(now) {
		if now.Before(r.expiresAt) {
			n++
		}
	}
	return n
}

// allow pairs the worker the way a pairing code does. The worker gets its token when it next polls. A worker of the
// same name that already exists is removed only after the new one is paired, so a failed pairing leaves the old one
// as it was.
func (b *joinBook) allow(id string, now time.Time) (jobs.RemoteWorker, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	row, err := b.store.Join(id)
	if errors.Is(err, jobs.ErrNotFound) {
		return jobs.RemoteWorker{}, errJoinNotFound
	}
	if err != nil {
		return jobs.RemoteWorker{}, err
	}
	switch {
	case row.Decision != "":
		return jobs.RemoteWorker{}, errJoinAnswered
	case !now.Before(time.Unix(0, row.ExpiresNS)):
		return jobs.RemoteWorker{}, errJoinExpired
	}
	remotes, err := b.store.RemoteWorkers()
	if err != nil {
		return jobs.RemoteWorker{}, err
	}
	var old []string
	for _, rw := range remotes {
		if rw.Name == row.Name {
			old = append(old, rw.ID)
		}
	}
	code, _, err := b.store.NewPairing(now)
	if err != nil {
		return jobs.RemoteWorker{}, err
	}
	// The token Pair returns is not given out: the worker's real one is minted when it picks the allow up.
	wk, _, err := b.store.Pair(code, row.Name, row.Version, row.OS, row.Arch, row.APILevel, row.Capabilities, now)
	if err != nil {
		return jobs.RemoteWorker{}, err
	}
	if err := b.store.AnswerJoin(id, "allowed", wk.ID, wk.Name); err != nil {
		// The answer did not save, so the worker Pair made is taken back: nothing is left behind.
		if rmErr := b.store.RemoveWorker(wk.ID); rmErr != nil {
			log.Printf("join: %s was paired, the answer did not save, and removing it failed: %v", wk.ID, rmErr)
		}
		return jobs.RemoteWorker{}, err
	}
	// Allowing is the go-ahead: the worker starts on. Pairing by code does not do this (Pair leaves a new worker off
	// when every worker is off), so the person's allow is what turns it on.
	if err := b.store.SetWorkerEnabled(wk.ID, true); err != nil {
		log.Printf("join: %s is paired, but turning it on failed: %v", wk.ID, err)
	}
	// The card reads online only once the worker itself has called: Pair counted as a call, the allow is not one.
	if err := b.store.ClearSeen(wk.ID); err != nil {
		log.Printf("join: %s is paired, but clearing its last call failed: %v", wk.ID, err)
	}
	for _, oid := range old {
		if err := b.store.RemoveWorker(oid); err != nil && !errors.Is(err, jobs.ErrNotFound) {
			// The new worker is paired and the request is answered, so this is not a failed allow. The old
			// worker stays in the list until someone removes it.
			log.Printf("join: %s is paired, but removing the old worker %s failed: %v", wk.ID, oid, err)
		}
	}
	return wk, nil
}

// deny answers a request no. The worker learns so when it polls.
func (b *joinBook) deny(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.store.Join(id); errors.Is(err, jobs.ErrNotFound) {
		return errJoinNotFound
	} else if err != nil {
		return err
	}
	// Already answered: nothing changes.
	if err := b.store.AnswerJoin(id, "denied", "", ""); err != nil && !errors.Is(err, jobs.ErrNotFound) {
		return err
	}
	return nil
}

// dismiss removes a request from the list. An allowed one nobody picked up takes its unseen worker with it.
func (b *joinBook) dismiss(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.store.DismissJoin(id); errors.Is(err, jobs.ErrNotFound) {
		return errJoinNotFound
	} else {
		return err
	}
}

// joinBody is what a joining worker sends. Strangers on the network reach this, so each field is bounded.
type joinBody struct {
	Name         string          `json:"name"`
	OS           string          `json:"os"`
	Arch         string          `json:"arch"` // the processor type; "" from an older worker
	Hardware     *string         `json:"hardware"`
	Port         int             `json:"port"` // the worker's web port, for the link on the Workers page; 0 when not sent
	Capabilities json.RawMessage `json:"capabilities"`
}

// problem says what is wrong with the body, or "" if nothing.
func (in joinBody) problem() string {
	switch {
	case strings.TrimSpace(in.Name) == "" || len(in.Name) > 80:
		return "The name is needed, at most 80 characters."
	case in.Port < 0 || in.Port > 65535:
		return "The port is a number from 1 to 65535."
	case len(in.OS) > 40:
		return "The operating system is at most 40 characters."
	case len(in.Arch) > 40:
		return "The processor type is at most 40 characters."
	case in.Hardware != nil && len(*in.Hardware) > 200:
		return "The hardware description is at most 200 characters."
	case len(in.Capabilities) > 8<<10:
		return "The capabilities are at most 8 KiB."
	}
	if _, ok := reportedCaps(in.Capabilities); !ok {
		return "The capabilities are an empty object, or a list of the encoder tests."
	}
	return ""
}

// fromLocal reports whether a join comes from the server's own network. A TLS peer from outside passes the
// transport check, so it gets this answer. A proxy on this host forwards the internet's address in these
// headers, and a peer that is not a trusted proxy can send them too, so such a request is refused, not read as
// local.
func fromLocal(w http.ResponseWriter, r *http.Request, t Transport, client netip.Addr) bool {
	forwarded := r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" || r.Header.Get("Forwarded") != ""
	if private(client) && !(forwarded && !trustedPeer(t, r)) {
		return true
	}
	apiError(w, http.StatusForbidden, "not_local", "Join requests come from the server's own network. Pair with a code from the Workers page instead. Nothing was changed.")
	return false
}

// trustedPeer says whether the direct peer is one of the server's trusted proxies.
func trustedPeer(t Transport, r *http.Request) bool {
	peer, _ := peerAddr(r)
	for _, p := range t.TrustedProxies {
		if p.Contains(peer) {
			return true
		}
	}
	return false
}

// tooManyTries answers a locked address, with the wording pairing uses.
func tooManyTries(w http.ResponseWriter, until time.Time) {
	w.Header().Set("Retry-After", strconv.Itoa(int(time.Until(until).Seconds())+1))
	apiError(w, http.StatusTooManyRequests, "too_many_tries", "Too many wrong pairing codes from this address. Wait 15 minutes, then make a new code on the Workers page.")
}

// ask handles POST /api/worker/v1/join. The gate has already checked the transport and the worker's API level.
func (b *joinBook) ask(w http.ResponseWriter, r *http.Request, t Transport, limiter *pairLimiter, now func() time.Time) {
	client := t.Client(r)
	if !fromLocal(w, r, t, client) {
		return
	}
	// An address locked by wrong codes or secrets gets no new request either. Asking itself does not count.
	if locked, until := limiter.locked(client, now()); locked {
		tooManyTries(w, until)
		return
	}
	var in joinBody
	if !readJSON(w, r, &in) {
		return
	}
	if msg := in.problem(); msg != "" {
		apiError(w, http.StatusBadRequest, "invalid_join", msg+" Nothing was changed.")
		return
	}
	v, level := workerVersion(r)
	caps, _ := reportedCaps(in.Capabilities) // problem() has checked it
	req := &joinReq{name: strings.TrimSpace(in.Name), os: in.OS, arch: in.Arch, version: v, hardware: in.Hardware, apiLevel: level,
		capabilities: caps, address: client, port: in.Port}
	id, secret, err := b.add(req, now())
	if errors.Is(err, errJoinFull) {
		apiError(w, http.StatusTooManyRequests, "too_many_joins", "Too many join requests are waiting. Allow or dismiss some on the Workers page.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{"id": id, "expiresAt": req.expiresAt.UTC().Format(time.RFC3339), "secret": secret})
}

// poll handles GET /api/worker/v1/join/{id}, with the secret as the bearer token. Wrong guesses count against
// the same limiter as pairing codes.
func (b *joinBook) poll(w http.ResponseWriter, r *http.Request, t Transport, limiter *pairLimiter, now func() time.Time) {
	client := t.Client(r)
	if !fromLocal(w, r, t, client) {
		return
	}
	if locked, until := limiter.locked(client, now()); locked {
		tooManyTries(w, until)
		return
	}
	state, req, err := b.get(r.PathValue("id"), bearer(r), now())
	if errors.Is(err, errJoinNotFound) {
		limiter.fail(client, now())
		time.Sleep(PairFailDelay) // no lock held: other requests go on
		apiError(w, http.StatusNotFound, "join_request_not_found", "There is no such join request. Nothing was changed.")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	out := map[string]any{"state": state}
	if state == "allowed" {
		// The token is minted now, once: the request is gone after this, so a second poll finds nothing.
		workerID, token, err := b.store.PickUpJoin(req.id)
		if errors.Is(err, jobs.ErrNotFound) {
			apiError(w, http.StatusNotFound, "join_request_not_found", "There is no such join request. Nothing was changed.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		out["workerId"], out["name"], out["token"] = workerID, req.workerName, token
	}
	writeJSON(w, out)
}

// joinViews is the list the Workers page shows: requests still open or expired, newest first.
func (v *workerView) joinViews(now time.Time) []map[string]any {
	names := map[string]bool{}
	if remotes, err := v.store.RemoteWorkers(); err == nil {
		for _, rw := range remotes {
			names[rw.Name] = true
		}
	}
	out := []map[string]any{}
	for _, r := range v.joins.pending(now) {
		state := "waiting"
		if !now.Before(r.expiresAt) {
			state = "expired"
		}
		addr := r.address.WithZone("") // a link-local zone (fe80::1%eth0) would make a URL browsers can't open
		var hardware any
		if r.hardware != nil {
			hardware = *r.hardware
		}
		port := joinPort
		if r.port > 0 {
			port = strconv.Itoa(r.port)
		}
		out = append(out, map[string]any{"id": r.id, "name": r.name, "address": addr.String(), "os": r.os,
			"hardware": hardware, "url": "http://" + net.JoinHostPort(addr.String(), port),
			"askedAt": r.askedAt.UTC().Format(time.RFC3339), "expiresAt": r.expiresAt.UTC().Format(time.RFC3339),
			"state": state, "replacesWorker": names[r.name]})
	}
	return out
}

// v2JoinRequests are the person's answers to a join (signed in, like /api/v2/workers).
func v2JoinRequests(api *http.ServeMux, v *workerView) {
	// answer writes the error for a failed answer, and reports whether the answer went through.
	answer := func(w http.ResponseWriter, err error) bool {
		switch {
		case errors.Is(err, errJoinNotFound):
			apiError(w, http.StatusNotFound, "join_request_not_found", "There is no such join request. Nothing was changed.")
		case errors.Is(err, errJoinExpired):
			apiError(w, http.StatusConflict, "join_request_expired", "This join request expired. Ask the worker to join again. Nothing was changed.")
		case errors.Is(err, errJoinAnswered):
			apiError(w, http.StatusConflict, "join_request_answered", "This join request was already answered. Nothing was changed.")
		case err != nil:
			serverError(w, err)
		default:
			return true
		}
		return false
	}
	api.HandleFunc("POST /api/v2/workers/join-requests/{id}/allow", func(w http.ResponseWriter, r *http.Request) {
		wk, err := v.joins.allow(r.PathValue("id"), time.Now())
		if !answer(w, err) {
			return
		}
		v.joins.changed()
		item, err := v.remote(wk)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"worker": item})
	})
	api.HandleFunc("POST /api/v2/workers/join-requests/{id}/deny", func(w http.ResponseWriter, r *http.Request) {
		if answer(w, v.joins.deny(r.PathValue("id"))) {
			v.joins.changed()
			w.WriteHeader(http.StatusNoContent)
		}
	})
	api.HandleFunc("POST /api/v2/workers/join-requests/{id}/dismiss", func(w http.ResponseWriter, r *http.Request) {
		if answer(w, v.joins.dismiss(r.PathValue("id"))) {
			v.joins.changed()
			w.WriteHeader(http.StatusNoContent)
		}
	})
}
