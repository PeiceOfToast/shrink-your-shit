// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/pathmap"
)

// Shared folders (V7c, v7-plan.md "V7c shape"; orch-lead 15:17Z): a remote worker's path mappings and
// their real tests. A test writes a probe file with random content in the server's folder; the worker
// learns of it on its next heartbeat's answer, reads it through its mapping and sends back its SHA-256
// (and, for read_write, writes a probe of its own there, which the server reads). Probes are removed
// after the test, both. A test the worker doesn't answer within PathTestTimeout ends failed, also
// across a restart (its deadline is stored).

// PathTestTimeout is how long a path test waits for the worker.
var PathTestTimeout = 60 * time.Second

type pathTest struct {
	id, worker, server string
	probe, sum         string // the server's probe file and its SHA-256
	readWrite          bool
	wProbe, wContent   string // read_write: where the worker's probe appears on the server, and its content
	read, write        string // the probes as the worker sees them
}

type pathTests struct {
	mu   sync.Mutex
	byID map[string]*pathTest
}

var tests = &pathTests{byID: map[string]*pathTest{}}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// errMappingUnknown: the worker has no mapping of that server folder.
var errMappingUnknown = errors.New("no such mapping")

// start begins a real test of worker's mapping of server.
func (pt *pathTests) start(store *jobs.Store, worker, server string) error {
	all, err := store.PathMappings(worker)
	if err != nil {
		return err
	}
	var m *jobs.PathMapping
	for i := range all {
		if all[i].Server == server {
			m = &all[i]
		}
	}
	if m == nil {
		return errMappingUnknown
	}
	t := &pathTest{id: randomHex(8), worker: worker, server: server, readWrite: m.Mode == "read_write"}
	content := randomHex(32)
	t.probe = filepath.Join(server, ".syp-pathtest-"+t.id)
	if err := os.WriteFile(t.probe, []byte(content), 0o644); err != nil {
		return err
	}
	h := sha256.Sum256([]byte(content))
	t.sum = hex.EncodeToString(h[:])
	t.read, _ = pathmap.Map(t.probe, m.Server, m.Worker)
	if t.readWrite {
		t.wProbe = t.probe + "-w"
		t.wContent = randomHex(32)
		t.write, _ = pathmap.Map(t.wProbe, m.Server, m.Worker)
	}
	if err := store.StartPathTest(worker, server, time.Now().Add(PathTestTimeout)); err != nil {
		os.Remove(t.probe)
		return err
	}
	pt.mu.Lock()
	pt.byID[t.id] = t
	pt.mu.Unlock()
	return nil
}

// pending are worker's tests for its heartbeat's answer.
func (pt *pathTests) pending(worker string) []map[string]any {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	out := []map[string]any{}
	for _, t := range pt.byID {
		if t.worker == worker {
			out = append(out, map[string]any{"id": t.id, "read": t.read, "write": nilIfEmpty(t.write), "writeContent": nilIfEmpty(t.wContent)})
		}
	}
	return out
}

func (pt *pathTests) take(id string) *pathTest {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	t := pt.byID[id]
	delete(pt.byID, id)
	return t
}

// end records a test's result, removes its probes and says so on the live stream.
func (pt *pathTests) end(store *jobs.Store, t *pathTest, ok bool, words string) {
	os.Remove(t.probe)
	if t.wProbe != "" {
		os.Remove(t.wProbe)
	}
	result := "failed"
	if ok {
		result = "passed"
	}
	if changed, _ := store.EndPathTest(t.worker, t.server, result, words, time.Now()); changed {
		store.RecordEvent("worker.updated", map[string]any{"type": "worker.updated", "workerId": t.worker, "status": "paths_tested"})
	}
}

// answer is the worker's report on test id: what it read (SHA-256), whether it wrote its probe, or why
// it couldn't. false: no such test (ended already).
func (pt *pathTests) answer(store *jobs.Store, worker, id, sum string, wrote bool, problem string) bool {
	pt.mu.Lock()
	t := pt.byID[id]
	if t == nil || t.worker != worker {
		pt.mu.Unlock()
		return false
	}
	delete(pt.byID, id)
	pt.mu.Unlock()
	switch {
	case problem != "":
		pt.end(store, t, false, problem)
	case !strings.EqualFold(sum, t.sum):
		pt.end(store, t, false, "The worker read a different file at "+t.read+": the mapping doesn't point to "+t.server+".")
	case t.readWrite && !wrote:
		pt.end(store, t, false, "The worker couldn't write in "+t.write+".")
	case t.readWrite:
		got, err := os.ReadFile(t.wProbe)
		if err != nil || string(got) != t.wContent {
			pt.end(store, t, false, "What the worker wrote at "+t.write+" didn't appear in "+t.server+": the mapping doesn't point to the same folder.")
			return true
		}
		pt.end(store, t, true, "")
	default:
		pt.end(store, t, true, "")
	}
	return true
}

// expire ends the tests whose worker didn't answer in time (also those a restart left pending).
func (pt *pathTests) expire(store *jobs.Store, online func(worker string) bool) {
	overdue, err := store.OverduePathTests(time.Now())
	if err != nil {
		return
	}
	for _, o := range overdue {
		words := "The worker didn't answer within 60 s; it is offline."
		if online(o[0]) {
			words = "The worker didn't answer within 60 s."
		}
		var t *pathTest
		pt.mu.Lock()
		for id, x := range pt.byID {
			if x.worker == o[0] && x.server == o[1] {
				t = x
				delete(pt.byID, id)
			}
		}
		pt.mu.Unlock()
		if t == nil { // a restart: its probe's name is gone with it; the sweep below removes it
			t = &pathTest{worker: o[0], server: o[1]}
			if old, _ := filepath.Glob(filepath.Join(o[1], ".syp-pathtest-*")); len(old) > 0 {
				for _, p := range old {
					os.Remove(p)
				}
			}
		}
		pt.end(store, t, false, words)
	}
}

// mappingView is a mapping as GET /workers shows it.
func mappingView(m jobs.PathMapping) map[string]any {
	var test any
	if m.Test != "" {
		test = map[string]any{"status": m.Test, "testedAt": nilIfEmpty(m.TestedAt), "detail": nilIfEmpty(m.Detail)}
	}
	return map[string]any{"server": m.Server, "worker": m.Worker, "mode": m.Mode, "test": test}
}

// usableMapping is worker's passed mapping covering serverPath (read_write needed: write), as the
// worker's path; "" when there is none.
func usableMapping(store *jobs.Store, worker, serverPath string, write bool) string {
	all, err := store.PathMappings(worker)
	if err != nil {
		return ""
	}
	for _, m := range all {
		if m.Test != "passed" || (write && m.Mode != "read_write") {
			continue
		}
		if p, ok := pathmap.Map(serverPath, m.Server, m.Worker); ok {
			return p
		}
	}
	return ""
}
