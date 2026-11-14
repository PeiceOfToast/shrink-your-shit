// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package remote is this program in worker mode (V7b, slices/v2-engine/v7-plan.md): a worker on another
// computer that pairs with a server and works for it. V7b1: it pairs, keeps its token, and says it is
// there (heartbeats); converting comes with V7b2.
package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/sysload"
)

// APILevel is the worker API level this worker speaks (the server's web.WorkerAPILevel).
const APILevel = 1

// HeartbeatEvery is how often a worker says it is there.
var HeartbeatEvery = 10 * time.Second

// State is what a paired worker keeps in its data folder (worker.json, readable only by its owner).
type State struct {
	Server   string `json:"server"`
	WorkerID string `json:"workerId"`
	Name     string `json:"name"`
	Token    string `json:"token"`
	// ServerName is the name the server shows for itself (the one the person saw when joining). Empty in a state
	// saved before it was kept; then the address stands in for it.
	ServerName string `json:"serverName,omitempty"`
}

// StateFile is the state's file name in the worker's data folder.
const StateFile = "worker.json"

// Load reads the state saved in dir.
func Load(dir string) (State, error) {
	var s State
	b, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(b, &s)
}

// Save writes s to dir (mode 600: the token opens the worker API).
func (s State) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(dir, StateFile), b, 0o600)
}

// Client calls a server's worker API.
type Client struct {
	Server, Token, Version string
	HTTP                   *http.Client
	meter                  sysload.Meter // this computer's load, sent with each heartbeat (V7d)

	holdMu   sync.Mutex
	holdBack string // why it takes no work now ("" takes work), sent with each heartbeat (#174 QA e)
}

// holding sets why this worker takes no work ("" when it does again), saying so in its log once per
// change.
func (c *Client) holding(why string) {
	c.holdMu.Lock()
	defer c.holdMu.Unlock()
	if why == c.holdBack {
		return
	}
	switch {
	case why != "":
		log.Print(why)
	default:
		log.Print("Taking work again: there is room in its work folder.")
	}
	c.holdBack = why
}

func (c *Client) holdingBack() string {
	c.holdMu.Lock()
	defer c.holdMu.Unlock()
	return c.holdBack
}

// APIError is a refusal from the server: its code and words.
type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return e.Message }

func (c *Client) call(ctx context.Context, path string, in, out any) error {
	return c.callWith(ctx, http.MethodPost, path, in, out, c.Token)
}

// callWith calls one worker API path with the given bearer token ("" sends none). in is sent as JSON when
// it is not nil.
func (c *Client) callWith(ctx context.Context, method, path string, in, out any, token string) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimSuffix(c.Server, "/")+"/api/worker/v1/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SYP-Worker", fmt.Sprintf("%s; api=%d", c.Version, APILevel))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	h := c.HTTP
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	res, err := h.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var e struct {
			Error struct{ Code, Message string }
		}
		json.NewDecoder(res.Body).Decode(&e)
		if e.Error.Message == "" {
			e.Error.Message = res.Status
		}
		return &APIError{Status: res.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
	}
	if out != nil {
		if err := json.NewDecoder(res.Body).Decode(out); err != nil {
			return fmt.Errorf("%w: %v", ErrNotWorkerAPI, err)
		}
	}
	return nil
}

// Pair exchanges a pairing code for this worker's token.
func (c *Client) Pair(ctx context.Context, code, name string) (State, error) {
	var out struct{ WorkerID, Name, Token string }
	if err := c.call(ctx, "pair", map[string]any{"code": code, "name": name, "capabilities": map[string]any{}}, &out); err != nil {
		return State{}, err
	}
	return State{Server: c.Server, WorkerID: out.WorkerID, Name: out.Name, Token: out.Token}, nil
}

// JoinEvery is how often a joining worker asks the server whether the person has answered.
var JoinEvery = 2 * time.Second

// ErrJoinDenied and ErrJoinExpired end a join the person did not complete.
var (
	ErrJoinDenied  = errors.New("The server said no.")
	ErrJoinExpired = errors.New("Nobody allowed it within 10 minutes. Run the command again.")
)

// ErrNotWorkerAPI means the server answered with something other than the worker API's JSON: another
// program, or a server of another kind.
var ErrNotWorkerAPI = errors.New("the server answered, but not as a shrink-your-shit server")

// Asked is a join request the server took: its id, the secret the wait polls with, and when the request
// expires (RFC 3339; "" when the server did not say).
type Asked struct {
	ID, Secret, ExpiresAt string
}

// Ask sends the join request and returns at once with the server's answer. port is the web port this
// worker answers on, so the server's Workers page can link to it; 0 sends none (the server assumes 8484).
func (c *Client) Ask(ctx context.Context, name, osName string, port int) (Asked, error) {
	in := map[string]any{"name": name, "os": osName, "capabilities": map[string]any{}}
	if port > 0 {
		in["port"] = port
	}
	var a struct{ ID, Secret, ExpiresAt string }
	if err := c.call(ctx, "join", in, &a); err != nil {
		return Asked{}, err
	}
	return Asked{ID: a.ID, Secret: a.Secret, ExpiresAt: a.ExpiresAt}, nil
}

// joinTrouble is how many polls in a row may fail (the network, the server restarting) before Wait reports
// "unreachable". Wait keeps polling until the request ends either way.
const joinTrouble = 3

// joinGrace lets a request end on the server's clock before this worker gives up on it.
const joinGrace = time.Minute

// Wait polls until the person answers the request and returns the same State Pair returns. Allowed, denied
// and expired end it; a failed poll the server can't answer for now (the network, a 5xx, a 429) does not.
// report, if not nil, hears "unreachable" after joinTrouble failed polls in a row and "waiting" when the
// server answers again. ctx ends the wait with ctx's error.
func (c *Client) Wait(ctx context.Context, a Asked, report func(state string)) (State, error) {
	var deadline time.Time
	if t, err := time.Parse(time.RFC3339, a.ExpiresAt); err == nil {
		deadline = t.Add(joinGrace)
	}
	fails, unreachable := 0, false
	for {
		if !deadline.IsZero() && !time.Now().Before(deadline) {
			return State{}, ErrJoinExpired
		}
		var out struct{ State, WorkerID, Name, Token string }
		err := c.callWith(ctx, http.MethodGet, "join/"+a.ID, nil, &out, a.Secret)
		if ctx.Err() != nil {
			return State{}, ctx.Err()
		}
		switch {
		case err != nil && transientPoll(err):
			fails++
			if fails >= joinTrouble && !unreachable {
				unreachable = true
				if report != nil {
					report("unreachable")
				}
			}
		case err != nil:
			var e *APIError
			if errors.As(err, &e) && e.Code == "join_request_not_found" {
				return State{}, errors.New("The server no longer has this join request (it restarted or dropped it). Run the command again.")
			}
			return State{}, err
		default:
			fails = 0
			if unreachable {
				unreachable = false
				if report != nil {
					report("waiting")
				}
			}
			switch out.State {
			case "allowed":
				return State{Server: c.Server, WorkerID: out.WorkerID, Name: out.Name, Token: out.Token}, nil
			case "denied":
				return State{}, ErrJoinDenied
			case "expired":
				return State{}, ErrJoinExpired
			}
		}
		select {
		case <-ctx.Done():
			return State{}, ctx.Err()
		case <-time.After(JoinEvery):
		}
	}
}

// transientPoll says whether a failed poll may succeed on the next try: the network failed, or the server
// is busy or restarting. A refusal that will not change (a wrong secret, a request that is gone) is not.
func transientPoll(err error) bool {
	var e *APIError
	if errors.As(err, &e) {
		return e.Status >= 500 || e.Status == http.StatusTooManyRequests
	}
	return !errors.Is(err, ErrNotWorkerAPI)
}

// Join asks the server to let this worker join, then waits until the person allows it there (the same State
// Pair returns), denies it, or the request expires. ctx ends the wait.
func (c *Client) Join(ctx context.Context, name, osName string) (State, error) {
	a, err := c.Ask(ctx, name, osName, 0)
	if err != nil {
		return State{}, err
	}
	return c.Wait(ctx, a, nil)
}

// Heartbeat says this worker is there, with its free slots.
func (c *Client) Heartbeat(ctx context.Context, free int) error {
	l := c.meter.Sample()
	var out struct{ PathTests []PathTest }
	var hold any
	if why := c.holdingBack(); why != "" {
		hold = map[string]string{"code": "low_disk", "message": why}
	}
	if err := c.call(ctx, "heartbeat", map[string]any{"freeSlots": free, "cpuPercent": l.CPUPercent, "memUsedBytes": l.MemUsedBytes,
		"memTotalBytes": l.MemTotalBytes, "holdingBack": hold}, &out); err != nil {
		return err
	}
	for _, t := range out.PathTests { // V7c: its shared folders' tests ride the heartbeat's answer
		c.runPathTest(ctx, t)
	}
	return nil
}

// Run sends heartbeats until ctx ends. It stops early, with the server's words, when the server says
// this worker is no longer paired or must be updated; other errors (the server restarting, the network)
// are logged and retried.
func (c *Client) Run(ctx context.Context) error {
	t := time.NewTicker(HeartbeatEvery)
	defer t.Stop()
	said := ""
	for {
		err := c.Heartbeat(ctx, 1)
		var ae *APIError
		switch {
		case errors.As(err, &ae) && (ae.Code == "worker_token" || ae.Code == "update_worker" || ae.Code == "https_required"):
			return err
		case err != nil && ctx.Err() == nil:
			if err.Error() != said {
				log.Printf("can't reach %s: %v (trying again)", c.Server, err)
			}
			said = err.Error()
		case err == nil && said != "":
			log.Printf("reached %s again", c.Server)
			said = ""
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}

// PathTest is a shared folder's real test (V7c): read the server's probe at Read through the mapping;
// for read_write also write WriteContent at Write. Nothing else is ever written.
type PathTest struct {
	ID, Read     string
	Write        *string
	WriteContent *string
}

func (c *Client) runPathTest(ctx context.Context, t PathTest) {
	res := map[string]any{}
	b, err := os.ReadFile(t.Read)
	switch {
	case err != nil:
		res["problem"] = "This worker can't open " + t.Read + ": " + err.Error()
	default:
		h := sha256.Sum256(b)
		res["sha256"] = hex.EncodeToString(h[:])
		if t.Write != nil && t.WriteContent != nil {
			if err := os.WriteFile(*t.Write, []byte(*t.WriteContent), 0o644); err != nil {
				res["problem"] = "This worker can't write in " + filepath.Dir(*t.Write) + ": " + err.Error()
			} else {
				res["wrote"] = true
			}
		}
	}
	if err := c.call(ctx, "paths/"+t.ID+"/result", res, nil); err != nil {
		log.Printf("path test %s: %v", t.ID, err)
	}
}
