// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package notify

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// rx is a fake receiver: it records each JSON body and answers with a status it is told.
type rx struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	bodies []map[string]any
}

func newRx(t *testing.T, status int) *rx {
	t.Helper()
	r := &rx{status: status}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var m map[string]any
		json.NewDecoder(req.Body).Decode(&m)
		r.mu.Lock()
		r.bodies = append(r.bodies, m)
		st := r.status
		r.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *rx) setStatus(n int) { r.mu.Lock(); r.status = n; r.mu.Unlock() }

// events are the "event" field of each message received, in order.
func (r *rx) events() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, m := range r.bodies {
		s, _ := m["event"].(string)
		out = append(out, s)
	}
	return out
}

func (r *rx) count() int { return len(r.events()) }

// sorted returns a sorted copy: messages are sent in parallel, so their arrival order is not fixed.
func sorted(s []string) []string { return slices.Sorted(slices.Values(s)) }

// clock is a settable time, safe for the send goroutines to read.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// logs collects every log line, so a test can look for a secret in them.
type logs struct {
	mu    sync.Mutex
	lines []string
}

func (l *logs) logf(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *logs) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

func fixture(t *testing.T) (*jobs.Store, *Service, *clock, *logs) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SetPaused(false); err != nil { // a new store starts paused; claims need it off
		t.Fatal(err)
	}
	clk := &clock{t: time.Now().UTC().Truncate(time.Second)}
	lg := &logs{}
	// no retries here: each failed send is one attempt, so the streak counts sends
	svc := New(st, Options{Now: clk.now, Logf: lg.logf, Backoff: []time.Duration{}})
	svc.SetSummary(&Summary{Mode: "off", At: "09:00"}, nil) // batch summaries are tested on their own
	return st, svc, clk, lg
}

// start makes the first tick, which sets where event reading begins: events after it are the ones sent.
func start(t *testing.T, svc *Service) {
	t.Helper()
	svc.Tick()
	svc.Idle()
}

func enqueue(t *testing.T, st *jobs.Store, path string) int64 {
	t.Helper()
	id, err := st.Enqueue(jobs.Job{Path: path, Library: "lib", Reason: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// claim takes the next queued job, so the test can finish it.
func claim(t *testing.T, st *jobs.Store) int64 {
	t.Helper()
	j, err := st.Claim("local")
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}

func mustCreate(t *testing.T, svc *Service, in NewTarget) Target {
	t.Helper()
	tg, err := svc.Create(in)
	if err != nil {
		t.Fatal(err)
	}
	return tg
}

func TestEachEventFiresAndItsToggleTurnsItOff(t *testing.T) {
	st, svc, _, _ := fixture(t)
	start(t, svc)
	r := newRx(t, 200)
	tg := mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/tok"})

	// jobFailed (default on)
	id := enqueue(t, st, "/media/a.mkv")
	claim(t, st)
	if err := st.Fail(id, "x", "disk full"); err != nil {
		t.Fatal(err)
	}
	// needsYou (default on): a kept original
	id = enqueue(t, st, "/media/b.mkv")
	claim(t, st)
	if err := st.Keep(id, "ask", "Ask before replacing"); err != nil {
		t.Fatal(err)
	}
	// fileFinished (default off): a converted file
	id = enqueue(t, st, "/media/c.mkv")
	claim(t, st)
	if err := st.Complete(id, "/media/c.mp4", 100); err != nil {
		t.Fatal(err)
	}
	// encoderFellBack (default off)
	id = enqueue(t, st, "/media/d.mkv")
	claim(t, st)
	if err := st.AddStep(id, jobs.EncoderFallbackStep, true, "the graphics card gave up; the processor did it"); err != nil {
		t.Fatal(err)
	}
	svc.Tick()
	svc.Idle()
	if got := r.events(); fmt.Sprint(sorted(got)) != "[job.failed job.kept]" {
		t.Fatalf("defaults: got %v, want [job.failed job.kept]", got)
	}

	// turn jobFailed off: the next failure is silent
	off := false
	if _, err := svc.Update(tg.ID, TargetPatch{Events: &EventsPatch{JobFailed: &off}}); err != nil {
		t.Fatal(err)
	}
	id = enqueue(t, st, "/media/e.mkv")
	claim(t, st)
	st.Fail(id, "x", "again")
	svc.Tick()
	svc.Idle()
	if n := r.count(); n != 2 {
		t.Fatalf("after jobFailed off: %d messages, want still 2", n)
	}

	// turn fileFinished and encoderFellBack on: both now arrive
	on := true
	if _, err := svc.Update(tg.ID, TargetPatch{Events: &EventsPatch{FileFinished: &on, EncoderFellBack: &on}}); err != nil {
		t.Fatal(err)
	}
	id = enqueue(t, st, "/media/f.mkv")
	claim(t, st)
	st.Complete(id, "/media/f.mp4", 50)
	id = enqueue(t, st, "/media/g.mkv")
	claim(t, st)
	st.AddStep(id, jobs.EncoderFallbackStep, true, "fell back")
	svc.Tick()
	svc.Idle()
	if got := r.events(); fmt.Sprint(sorted(got[2:])) != "[encoder.fell job.done]" {
		t.Fatalf("after turning on: got %v, want the last two [job.done encoder.fell]", got[2:])
	}
}

func TestWorkerOfflineFires(t *testing.T) {
	st, svc, clk, _ := fixture(t)
	r := newRx(t, 200)
	mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/tok"})
	base := clk.now()
	code, _, err := st.NewPairing(base)
	if err != nil {
		t.Fatal(err)
	}
	w, _, err := st.Pair(code, "Den", "1.0", "linux", "amd64", 1, "{}", base)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SeenWorker(w.ID, "1.0", 1, base); err != nil {
		t.Fatal(err)
	}
	svc.Tick() // online at the baseline
	svc.Idle()
	clk.set(base.Add(2 * time.Minute)) // not heard from for two minutes
	svc.Tick()
	svc.Idle()
	if got := r.events(); fmt.Sprint(got) != "[worker.offline]" {
		t.Fatalf("worker went offline: got %v, want [worker.offline]", got)
	}
}

// The secret: never in GET, never in the store's plain text, never in a log, never in lastResult.
func TestSecretNeverShows(t *testing.T) {
	st, svc, _, lg := fixture(t)
	start(t, svc)
	const secret = "SECRETTOKEN4417"
	r := newRx(t, 500) // a failing send, so lastResult and the log are both written
	tg := mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/" + secret})
	if !strings.HasSuffix(tg.URLShown, "/…") || strings.Contains(tg.URLShown, secret) {
		t.Fatalf("urlShown %q should hide the path", tg.URLShown)
	}
	id := enqueue(t, st, "/media/a.mkv")
	claim(t, st)
	st.Fail(id, "x", "boom")
	svc.Tick()
	svc.Idle()

	s, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), secret) {
		t.Errorf("GET shows the secret: %s", b)
	}
	if s.Targets[0].LastResult == nil || s.Targets[0].LastResult.OK {
		t.Fatalf("lastResult should record the failed send: %+v", s.Targets[0].LastResult)
	}
	if v, _, _ := st.Meta(metaKey); strings.Contains(v, secret) {
		t.Errorf("the stored setup holds the secret in plain text")
	}
	if strings.Contains(lg.all(), secret) {
		t.Errorf("a log line shows the secret: %s", lg.all())
	}
	if lg.all() == "" {
		t.Errorf("expected a log line for the failed send")
	}
}

// Three failed sends in a row set failingSince to the first of them; a success clears it.
func TestFailingSinceAfterThreeFailures(t *testing.T) {
	st, svc, clk, _ := fixture(t)
	start(t, svc)
	r := newRx(t, 500)
	tg := mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/tok"})
	first := clk.now()
	for i := 0; i < 3; i++ {
		clk.set(first.Add(time.Duration(i) * time.Minute))
		id := enqueue(t, st, fmt.Sprintf("/media/%d.mkv", i))
		claim(t, st)
		st.Fail(id, "x", "boom")
		svc.Tick()
		svc.Idle()
		s, _ := svc.Get()
		fs := s.Targets[0].FailingSince
		if i < 2 && fs != nil {
			t.Fatalf("after %d failures failingSince = %v, want none", i+1, *fs)
		}
		if i == 2 && (fs == nil || *fs != first.Format(time.RFC3339)) {
			t.Fatalf("after 3 failures failingSince = %v, want the first failure %s", fs, first.Format(time.RFC3339))
		}
	}
	r.setStatus(200)
	if _, err := svc.Test(tg.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	s, _ := svc.Get()
	if s.Targets[0].FailingSince != nil {
		t.Errorf("a success should clear failingSince, got %v", *s.Targets[0].FailingSince)
	}
}

// A send to an address that cannot be reached never fails the job, which stays done.
func TestSendFailureLeavesTheJobDone(t *testing.T) {
	st, svc, _, lg := fixture(t)
	start(t, svc)
	dead := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	dead.Close() // nothing listens there now
	on := true   // fileFinished is off by default; this test needs the send to happen
	mustCreate(t, svc, NewTarget{Name: "gone", Kind: "webhook", URL: dead.URL + "/hook/tok", Events: &EventsPatch{FileFinished: &on}})
	id := enqueue(t, st, "/media/a.mkv")
	claim(t, st)
	if err := st.Complete(id, "/media/a.mp4", 10); err != nil {
		t.Fatal(err)
	}
	svc.Tick()
	svc.Idle()
	j, err := st.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != jobs.Done {
		t.Fatalf("job status %q after a failed send, want done", j.Status)
	}
	if !strings.Contains(lg.all(), "Could not reach") {
		t.Fatalf("the send was never attempted; log: %q", lg.all())
	}
}

func TestTestForms(t *testing.T) {
	_, svc, _, _ := fixture(t)
	r := newRx(t, 204)
	// kind and url, before anything is saved
	res, err := svc.Test("", "webhook", r.srv.URL+"/hook/tok")
	if err != nil || !res.OK || res.Message != "Sent. The webhook answered 204." {
		t.Fatalf("kind+url: %+v, %v", res, err)
	}
	// a saved target, with no overrides: its lastResult is set
	tg := mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/tok"})
	if _, err := svc.Test(tg.ID, "", ""); err != nil {
		t.Fatal(err)
	}
	s, _ := svc.Get()
	if s.Targets[0].LastResult == nil || !s.Targets[0].LastResult.OK {
		t.Fatalf("a test of the saved target should set lastResult: %+v", s.Targets[0].LastResult)
	}
	// a saved target with an edited url: tested, but lastResult is not touched
	before := *s.Targets[0].LastResult
	d := newRx(t, 401)
	res, err = svc.Test(tg.ID, "", d.srv.URL+"/hook/other")
	if err != nil || res.OK {
		t.Fatalf("edited url: %+v, %v", res, err)
	}
	s, _ = svc.Get()
	if *s.Targets[0].LastResult != before {
		t.Errorf("an override test changed lastResult: %+v", s.Targets[0].LastResult)
	}
	// Discord's answer for a wrong webhook
	dc := newRx(t, 401)
	res, _ = svc.Test("", "discord", dc.srv.URL+"/api/webhooks/1/x")
	if res.Message != "Discord answered 401: the webhook URL is wrong or was deleted." {
		t.Errorf("discord 401 message: %q", res.Message)
	}
	if got := dc.bodies; len(got) != 1 {
		t.Fatalf("discord test should post once")
	} else if _, ok := got[0]["content"]; !ok {
		t.Errorf("discord takes a content field, got %v", got[0])
	}
	// errors
	if _, err := svc.Test("", "webhook", "ftp://x"); !isCode(err, "invalid_url") {
		t.Errorf("bad url: %v", err)
	}
	if _, err := svc.Test("", "email", r.srv.URL); !isCode(err, "invalid_kind") {
		t.Errorf("bad kind: %v", err)
	}
	if _, err := svc.Test("nope", "", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}

func isCode(err error, code string) bool {
	var inv Invalid
	return errors.As(err, &inv) && inv.Code == code
}

func TestSummaryModes(t *testing.T) {
	day := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, time.UTC) }
	mon := 23 // Monday 2026-11-23
	some := jobs.Batch{Converted: 2, Failed: 1}
	none := jobs.Batch{}
	cases := []struct {
		name          string
		sum           Summary
		now, last     time.Time
		b             jobs.Batch
		send, advance bool
	}{
		{"off never", Summary{Mode: "off", At: "09:00"}, day(19, 12, 0), day(18, 0, 0), some, false, false},
		{"drained sends when empty", Summary{Mode: "drained"}, day(19, 12, 0), day(19, 11, 0), some, true, true},
		{"drained waits while busy", Summary{Mode: "drained"}, day(19, 12, 0), day(19, 11, 0), jobs.Batch{Converted: 1, Active: 3}, false, false},
		{"drained with nothing finished", Summary{Mode: "drained"}, day(19, 12, 0), day(19, 11, 0), none, false, false},
		{"daily before the time", Summary{Mode: "daily", At: "09:00"}, day(19, 8, 59), day(18, 9, 0), some, false, false},
		{"daily at the time", Summary{Mode: "daily", At: "09:00"}, day(19, 9, 0), day(18, 9, 0), some, true, true},
		{"daily already sent today", Summary{Mode: "daily", At: "09:00"}, day(19, 10, 0), day(19, 9, 0), some, false, false},
		{"daily with nothing: window moves on", Summary{Mode: "daily", At: "09:00"}, day(19, 10, 0), day(18, 9, 0), none, false, true},
		{"weekly on a Monday", Summary{Mode: "weekly", At: "09:00"}, day(mon, 9, 30), day(19, 9, 0), some, true, true},
		{"weekly not on a Monday", Summary{Mode: "weekly", At: "09:00"}, day(24, 9, 30), day(19, 9, 0), some, false, false},
	}
	for _, c := range cases {
		send, advance := summaryDue(c.sum, c.now, c.last, c.b)
		if send != c.send || advance != c.advance {
			t.Errorf("%s: send %v advance %v, want %v %v", c.name, send, advance, c.send, c.advance)
		}
	}
}

// The batch line has the four numbers in the contract's order, with GB at 1e9 bytes.
func TestSummaryText(t *testing.T) {
	got := summaryText(jobs.Batch{Converted: 4, Saved: 2_500_000_000, Waiting: 1, Failed: 2})
	want := "4 converted, 2.5 GB smaller, 1 waiting to be freed, 2 failed."
	if got != want {
		t.Fatalf("summary text %q, want %q", got, want)
	}
	if got := summaryText(jobs.Batch{Converted: 2, Saved: -20_000_000}); got != "2 converted, 0.0 GB smaller, 0 waiting to be freed, 0 failed." {
		t.Fatalf("a batch that grew: %q", got)
	}
}

// Through a real tick: a drained summary arrives once the queue empties, and names the batch.
func TestDrainedSummarySendsTheBatch(t *testing.T) {
	st, svc, _, _ := fixture(t)
	svc.SetSummary(&Summary{Mode: "drained", At: "09:00"}, nil)
	r := newRx(t, 200)
	mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/tok"})
	svc.Tick() // sets the window start and the event cursor
	svc.Idle()
	id := enqueue(t, st, "/media/a.mkv")
	claim(t, st)
	if err := st.Complete(id, "/media/a.mp4", 10); err != nil {
		t.Fatal(err)
	}
	svc.Tick()
	svc.Idle()
	var texts []string
	r.mu.Lock()
	for _, m := range r.bodies {
		if e, _ := m["event"].(string); e == "batch.finished" {
			s, _ := m["text"].(string)
			texts = append(texts, s)
		}
	}
	r.mu.Unlock()
	if len(texts) != 1 || !strings.HasPrefix(texts[0], "1 converted, ") {
		t.Fatalf("batch messages %q, want one that starts \"1 converted, \"", texts)
	}
}

// An address that can't be reached is named by its host only: the client's own error text holds the
// whole URL, token included, and lastResult is served by GET (review P2-1 on #330).
func TestUnreachableNamesTheHostOnly(t *testing.T) {
	_, svc, _, lg := fixture(t)
	const secret = "SECRETTOKEN9921"
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := "http://" + ln.Addr().String() + "/hook/" + secret
	ln.Close() // nothing listens there now
	tg := mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: addr})
	res, err := svc.Test(tg.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || res.Message != "Could not reach "+ln.Addr().String()+"." {
		t.Errorf("unreachable test: %+v", res)
	}
	s, err := svc.Get()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), secret) || !strings.Contains(string(b), "Could not reach "+ln.Addr().String()+".") {
		t.Errorf("GET should hold the host-only message and no secret: %s", b)
	}
	if strings.Contains(lg.all(), secret) {
		t.Errorf("a log line shows the secret: %s", lg.all())
	}
}

// A failure's message is fixed words for its code: never the code, never the stored words (they can hold a path).
func TestFailureTextIsWordsNotCodeOrPath(t *testing.T) {
	st, svc, _, _ := fixture(t)
	start(t, svc)
	r := newRx(t, 200)
	mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: r.srv.URL + "/hook/tok"})
	id := enqueue(t, st, "/media/a.mkv")
	claim(t, st)
	// stored words that name a folder (as worker.go's notOurs and again.go do) never reach the target
	if err := st.Fail(id, jobs.ReasonCannotWrite, "The file at /home/someone/private/a.mkv is not the one that was queued."); err != nil {
		t.Fatal(err)
	}
	svc.Tick()
	svc.Idle()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.bodies) != 1 || r.bodies[0]["text"] != "Failed: a.mkv. Not allowed to write in its folder." {
		t.Fatalf("failure message: %v", r.bodies)
	}
}

// A server whose certificate the machine can't check (the image once had no CA bundle) is not
// "unreachable": the words say the secure connection failed, still naming the host only.
func TestCertificateFailureSaysSo(t *testing.T) {
	_, svc, _, _ := fixture(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "https://")
	tg := mustCreate(t, svc, NewTarget{Name: "hook", Kind: "webhook", URL: srv.URL + "/hook/SECRETTOKEN9921"})
	res, err := svc.Test(tg.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "Could not make a secure connection to " + host + " (certificate check failed)."
	if res.OK || res.Message != want {
		t.Errorf("result %+v, want message %q", res, want)
	}
}
