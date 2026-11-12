// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// The remote workers' API (V7b1, slices/v2-engine/v7-plan.md "V7b design"), under /api/worker/v1/.
// It takes only a worker's own token (the person's token and session are refused there, and a worker
// token opens nothing else); every call names its worker, lease calls also their lease (409 once it
// ended: fencing). Plain HTTP only from this network (Q1 a).

// WorkerAPILevel is the remote worker API's level; a worker that sends another is told to update.
const WorkerAPILevel = 1

// workerHeader is the header a worker sends: "<version>; api=<level>".
const workerHeader = "X-SYP-Worker"

// Transport decides whether a request came over a connection a worker may use (Q1 a, orch-lead
// 13:28Z): TLS on this server, or plain HTTP only from a private address. X-Forwarded-Proto and
// X-Forwarded-For are believed only from a direct peer listed in TrustedProxies (empty by default):
// anyone else can write them.
type Transport struct {
	TrustedProxies []netip.Prefix
}

// ParseTrustedProxies reads a comma-separated list of addresses or CIDR ranges ("" for none).
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if p, err := netip.ParsePrefix(f); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, errors.New(f + " is not an address or a range")
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

func peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	return a.Unmap(), err == nil
}

// private: loopback, RFC 1918, link-local, or unique local (fc00::/7).
func private(a netip.Addr) bool {
	return a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast()
}

// Allowed reports whether r may reach the worker API (or pairing).
func (t Transport) Allowed(r *http.Request) bool {
	peer, ok := peerAddr(r)
	if !ok {
		return false
	}
	trusted := false
	for _, p := range t.TrustedProxies {
		if p.Contains(peer) {
			trusted = true
		}
	}
	if !trusted {
		return r.TLS != nil || private(peer)
	}
	// a trusted proxy: its word on the client's connection and address
	if strings.EqualFold(strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]), "https") {
		return true
	}
	if f := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); f != "" {
		a, err := netip.ParseAddr(f)
		return err == nil && private(a.Unmap())
	}
	return r.TLS != nil || private(peer)
}

const httpsWords = "Use https:// for a worker outside your network (this server's own HTTPS, or a proxy listed in its trusted proxies)."

// workerVersion reads the worker header: its version and API level.
func workerVersion(r *http.Request) (string, int) {
	v, rest, _ := strings.Cut(r.Header.Get(workerHeader), ";")
	level := 0
	if _, n, ok := strings.Cut(rest, "api="); ok {
		level, _ = strconv.Atoi(strings.TrimSpace(n))
	}
	return strings.TrimSpace(v), level
}

// workerAPI is the /api/worker/v1/ handler.
func workerAPI(store *jobs.Store, t Transport, version string) http.Handler {
	mux := http.NewServeMux()
	now := time.Now
	// gate: the transport, the version, and (but for pairing) the worker's token
	gate := func(w http.ResponseWriter, r *http.Request, pairing bool) (jobs.RemoteWorker, bool) {
		w.Header().Set("X-SYP-API", "1")
		if !t.Allowed(r) {
			apiError(w, http.StatusForbidden, "https_required", httpsWords)
			return jobs.RemoteWorker{}, false
		}
		if _, level := workerVersion(r); level != WorkerAPILevel {
			writeErrorWith(w, http.StatusUpgradeRequired, "update_worker", "Update this worker to "+version+".", map[string]any{"version": version})
			return jobs.RemoteWorker{}, false
		}
		if mt := r.Header.Get("Content-Type"); r.Method != http.MethodGet && !strings.HasPrefix(mt, "application/json") {
			apiError(w, http.StatusUnsupportedMediaType, "invalid_json", "Send JSON (Content-Type: application/json).")
			return jobs.RemoteWorker{}, false
		}
		if pairing {
			return jobs.RemoteWorker{}, true
		}
		wk, err := store.WorkerByToken(bearer(r))
		if err != nil {
			apiError(w, http.StatusForbidden, "worker_token", "This worker isn't paired with this server (or was removed). Pair it again from the Workers page.")
			return wk, false
		}
		v, level := workerVersion(r)
		store.SeenWorker(wk.ID, v, level, now())
		return wk, true
	}
	mux.HandleFunc("POST /api/worker/v1/pair", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := gate(w, r, true); !ok {
			return
		}
		var in struct {
			Code, Name   string
			Capabilities json.RawMessage
		}
		if !readJSON(w, r, &in) {
			return
		}
		v, level := workerVersion(r)
		caps := string(in.Capabilities)
		wk, token, err := store.Pair(in.Code, in.Name, v, level, caps, now())
		if errors.Is(err, jobs.ErrPairingInvalid) {
			apiError(w, http.StatusForbidden, "pairing_invalid", "This pairing code isn't valid (it works once, for 15 minutes). Make a new one on the Workers page.")
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"workerId": wk.ID, "name": wk.Name, "token": token})
	})
	mux.HandleFunc("POST /api/worker/v1/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		wk, ok := gate(w, r, false)
		if !ok {
			return
		}
		var in struct{ FreeSlots int }
		if !readJSON(w, r, &in) {
			return
		}
		writeJSON(w, map[string]any{"workerId": wk.ID, "paused": store.Paused()})
	})
	// a lease: at most one job (the plan comes with V7b2); none: {lease: null}
	mux.HandleFunc("POST /api/worker/v1/lease", func(w http.ResponseWriter, r *http.Request) {
		wk, ok := gate(w, r, false)
		if !ok {
			return
		}
		var in struct{ Slots int }
		if !readJSON(w, r, &in) {
			return
		}
		if in.Slots < 1 {
			writeJSON(w, map[string]any{"lease": nil})
			return
		}
		j, err := store.Claim(wk.ID)
		if errors.Is(err, jobs.ErrNoJob) {
			writeJSON(w, map[string]any{"lease": nil})
			return
		}
		if err != nil {
			serverError(w, err)
			return
		}
		lease, _ := store.LeaseOf(j.ID)
		_, expires, _ := store.LeaseHeld(lease, wk.ID)
		writeJSON(w, map[string]any{"lease": map[string]any{"id": lease, "expiresAt": expires,
			"job": map[string]any{"id": strconv.FormatInt(j.ID, 10), "name": filepath.Base(j.Path), "bytes": j.SizeBefore, "durationSec": j.DurationSec}}})
	})
	leaseOf := func(w http.ResponseWriter, r *http.Request) (jobs.RemoteWorker, jobs.Job, string, bool) {
		wk, ok := gate(w, r, false)
		if !ok {
			return wk, jobs.Job{}, "", false
		}
		lease := r.PathValue("lease")
		j, _, held := store.LeaseHeld(lease, wk.ID)
		if !held {
			apiError(w, http.StatusConflict, "lease_gone", "This job was given back to the queue (its lease ended). Ask for new work.")
			return wk, j, lease, false
		}
		return wk, j, lease, true
	}
	mux.HandleFunc("POST /api/worker/v1/leases/{lease}/progress", func(w http.ResponseWriter, r *http.Request) {
		_, j, lease, ok := leaseOf(w, r)
		if !ok {
			return
		}
		var in struct {
			Percent float64
			Stage   string
		}
		if !readJSON(w, r, &in) {
			return
		}
		if held, err := store.RenewLease(j.ID, lease); err != nil || !held {
			apiError(w, http.StatusConflict, "lease_gone", "This job was given back to the queue (its lease ended). Ask for new work.")
			return
		}
		if in.Percent > 0 {
			store.Progress(j.ID, in.Percent)
		}
		_, expires, _ := store.LeaseHeld(lease, j.Worker)
		writeJSON(w, map[string]any{"ok": true, "expiresAt": expires})
	})
	mux.HandleFunc("POST /api/worker/v1/leases/{lease}/release", func(w http.ResponseWriter, r *http.Request) {
		_, j, _, ok := leaseOf(w, r)
		if !ok {
			return
		}
		var in struct{}
		if !readJSON(w, r, &in) {
			return
		}
		if err := store.Release(j.ID); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	})
	mux.HandleFunc("/api/worker/v1/", func(w http.ResponseWriter, r *http.Request) {
		apiError(w, http.StatusNotFound, "not_found", "There is no such worker call.")
	})
	return mux
}
