// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package notify sends the person news of their conversions to a webhook or a Discord channel: a job failed,
// a file needs them, a worker went offline, a batch finished. It reads the job store's event feed, so a
// notification never sits in a job's path: sending runs in the background, with retries, and a failed send
// fails nothing.
package notify

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Events are the six things a target can be told about.
type Events struct {
	JobFailed       bool `json:"jobFailed"`
	NeedsYou        bool `json:"needsYou"`
	FileFinished    bool `json:"fileFinished"`
	BatchFinished   bool `json:"batchFinished"`
	WorkerOffline   bool `json:"workerOffline"`
	EncoderFellBack bool `json:"encoderFellBack"`
}

// defaultEvents are the contract's defaults.
var defaultEvents = Events{JobFailed: true, NeedsYou: true, FileFinished: false, BatchFinished: true, WorkerOffline: true, EncoderFellBack: false}

// EventsPatch is a partial Events: the fields left out keep their value.
type EventsPatch struct {
	JobFailed       *bool `json:"jobFailed"`
	NeedsYou        *bool `json:"needsYou"`
	FileFinished    *bool `json:"fileFinished"`
	BatchFinished   *bool `json:"batchFinished"`
	WorkerOffline   *bool `json:"workerOffline"`
	EncoderFellBack *bool `json:"encoderFellBack"`
}

func (e Events) with(p *EventsPatch) Events {
	if p == nil {
		return e
	}
	set := func(dst *bool, src *bool) {
		if src != nil {
			*dst = *src
		}
	}
	set(&e.JobFailed, p.JobFailed)
	set(&e.NeedsYou, p.NeedsYou)
	set(&e.FileFinished, p.FileFinished)
	set(&e.BatchFinished, p.BatchFinished)
	set(&e.WorkerOffline, p.WorkerOffline)
	set(&e.EncoderFellBack, p.EncoderFellBack)
	return e
}

// Result is the last real send or Test: what the person is shown.
type Result struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	At      string `json:"at"`
}

// Target is one place the news goes, as the API shows it. Its address is write-only.
type Target struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	URLShown     string  `json:"urlShown"`
	Enabled      bool    `json:"enabled"`
	Events       Events  `json:"events"`
	LastResult   *Result `json:"lastResult"`
	FailingSince *string `json:"failingSince"`
}

// Summary is when the batch summary is sent: off, when the queue drains, or daily or weekly at a time.
type Summary struct {
	Mode string `json:"mode"`
	At   string `json:"at"`
}

// Settings is what GET /api/v2/notifications returns.
type Settings struct {
	Targets  []Target `json:"targets"`
	Summary  Summary  `json:"summary"`
	LinkBase *string  `json:"linkBase"`
}

// record is one target as it is kept (the address sealed).
type record struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Kind         string  `json:"kind"`
	URLShown     string  `json:"urlShown"`
	CT           []byte  `json:"ct"`
	Nonce        []byte  `json:"nonce"`
	Enabled      bool    `json:"enabled"`
	Events       Events  `json:"events"`
	Last         *Result `json:"last,omitempty"`
	Streak       int     `json:"streak"`
	StreakSince  string  `json:"streakSince,omitempty"`
	FailingSince string  `json:"failingSince,omitempty"`
}

// file is the whole notification setup, kept under one app-meta key.
type file struct {
	Targets     []record `json:"targets"`
	Summary     Summary  `json:"summary"`
	LinkBase    string   `json:"linkBase"`
	LastSummary string   `json:"lastSummary,omitempty"` // a summary's window starts here
}

const (
	metaKey     = "notifications"
	eventCursor = "notify.eventSeq"
	stepCursor  = "notify.stepRow"
	// failingAfter is how many failed sends in a row make a target "failing".
	failingAfter = 3
)

// Invalid is a request the API refuses; Code is the contract's error code.
type Invalid struct{ Code, Message string }

func (e Invalid) Error() string { return e.Message }

// ErrNotFound is a target id that is not there.
var ErrNotFound = errors.New("not found")

// Options are the seams: the HTTP client, the offline threshold, the clock and a log line.
type Options struct {
	Client       *http.Client
	OfflineAfter time.Duration
	Now          func() time.Time
	Logf         func(format string, args ...any)
	Backoff      []time.Duration // the waits before each retry of a failed send
	// FailureText is the sentence the Jobs page shows for a failed job, by job id ("" when it has none). A failure
	// sends its first line; failedWords is the fallback.
	FailureText func(jobID int64) string
}

// Service keeps the setup and sends the news.
type Service struct {
	st   *jobs.Store
	opt  Options
	mu   sync.Mutex
	sem  chan struct{}
	wg   sync.WaitGroup
	prev map[string]bool // workers online at the last tick (Tick only)
	seen bool            // the first tick only records who is online
}

// New makes a Service over the job store.
func New(st *jobs.Store, o Options) *Service {
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if o.OfflineAfter == 0 {
		o.OfflineAfter = 30 * time.Second
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.Backoff == nil {
		o.Backoff = []time.Duration{5 * time.Second, 30 * time.Second, 2 * time.Minute}
	}
	return &Service{st: st, opt: o, sem: make(chan struct{}, 4), prev: map[string]bool{}}
}

// Run ticks until ctx ends. Sends run beside it, never in its way.
func (s *Service) Run(stop <-chan struct{}) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.Tick()
		}
	}
}

// Idle waits until every send in flight has finished (tests use it; the app never needs it).
func (s *Service) Idle() { s.wg.Wait() }

// Tick reads what happened since the last tick and sends what is due. The first tick starts from now, so
// history from before the notifier existed is not sent.
func (s *Service) Tick() {
	s.events()
	s.fallbacks()
	s.workers()
	s.summary()
}

func (s *Service) cursor(key string, latest func() (int64, error)) (int64, error) {
	v, ok, err := s.st.Meta(key)
	if err != nil {
		return 0, err
	}
	if !ok {
		n, err := latest()
		if err != nil {
			return 0, err
		}
		return n, s.st.SetMeta(key, fmt.Sprint(n))
	}
	var n int64
	fmt.Sscan(v, &n)
	return n, nil
}

func (s *Service) setCursor(key string, n int64) { s.st.SetMeta(key, fmt.Sprint(n)) }

func (s *Service) events() {
	cur, err := s.cursor(eventCursor, s.st.LatestEventSeq)
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	evs, err := s.st.EventsAfter(cur, 500)
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	for _, e := range evs {
		cur = e.Seq
		if e.Kind != "job" {
			continue
		}
		name := "A file"
		if j, err := s.st.Get(e.JobID); err == nil {
			name = jobs.LastName(j.Path)
		}
		switch e.To {
		case "failed":
			s.notifyAll("jobFailed", "/jobs?tab=failed", withoutFolders(fmt.Sprintf("Failed: %s. %s", name, s.failureSentence(e))), "job.failed")
		case "kept":
			s.notifyAll("needsYou", "/jobs?tab=completed", fmt.Sprintf("Kept the original of %s: a safety check refused the new file.", name), "job.kept")
		case "done":
			s.notifyAll("fileFinished", "/jobs?tab=completed", fmt.Sprintf("Converted: %s.", name), "job.done")
		}
	}
	s.setCursor(eventCursor, cur)
}

// pathWord is a run of text with a separator in it: a candidate path.
var pathWord = regexp.MustCompile(`[^\s'"]*[/\\][^\s'"]*`)

// withoutFolders keeps a failure notification to file names. Each path in it becomes its last name, so no folder
// leaves the server: webhooks go to outside services. The Jobs page keeps its own words.
func withoutFolders(text string) string {
	return pathWord.ReplaceAllStringFunc(text, func(w string) string {
		if !namesAPath(w) {
			return w
		}
		return jobs.LastName(w)
	})
}

// namesAPath is true for a word that is a path: it starts with a separator or a drive (C:\), or has two separators.
// "and/or" is not one.
func namesAPath(w string) bool {
	t := strings.TrimRight(w, `/\`)
	if len(t) == 0 {
		return false
	}
	if t[0] == '/' || t[0] == '\\' || (len(t) >= 3 && t[1] == ':' && (t[2] == '\\' || t[2] == '/')) {
		return true
	}
	return strings.Count(t, "/")+strings.Count(t, `\`) >= 2
}

// failureSentence is the first line of the Jobs sentence for a failed job, or failedWords for its code when there is none.
func (s *Service) failureSentence(e jobs.Event) string {
	if s.opt.FailureText != nil {
		if line, _, _ := strings.Cut(strings.TrimSpace(s.opt.FailureText(e.JobID)), "\n"); line != "" {
			return line
		}
	}
	return failedWords(e.Reason)
}

func (s *Service) fallbacks() {
	cur, err := s.cursor(stepCursor, s.st.LatestStepRow)
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	rows, err := s.st.FallbacksAfter(cur, 500)
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	for _, f := range rows {
		cur = f.Row
		name := "A file"
		if j, err := s.st.Get(f.JobID); err == nil {
			name = filepath.Base(j.Path)
		}
		s.notifyAll("encoderFellBack", "/jobs?tab=completed", fmt.Sprintf("%s: %s", name, f.Words), "encoder.fell")
	}
	s.setCursor(stepCursor, cur)
}

// workers tells of a paired worker that was online at the last tick and is not heard from now.
func (s *Service) workers() {
	rws, err := s.st.RemoteWorkers()
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	now := s.opt.Now()
	online := map[string]bool{}
	for _, rw := range rws {
		seen, _ := time.Parse(time.RFC3339, rw.LastSeen)
		online[rw.ID] = !seen.IsZero() && now.Sub(seen) <= s.opt.OfflineAfter
	}
	if s.seen {
		for _, rw := range rws {
			if s.prev[rw.ID] && !online[rw.ID] {
				s.notifyAll("workerOffline", "/workers", fmt.Sprintf("Worker %s went offline.", rw.Name), "worker.offline")
			}
		}
	}
	s.prev, s.seen = online, true
}

// summary sends the batch summary when its mode says it is due.
func (s *Service) summary() {
	s.mu.Lock()
	f, err := s.load()
	s.mu.Unlock()
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	now := s.opt.Now()
	if f.Summary.Mode == "off" {
		return
	}
	if f.LastSummary == "" {
		s.mu.Lock()
		f.LastSummary = now.UTC().Format(time.RFC3339)
		s.save(f)
		s.mu.Unlock()
		return
	}
	last, _ := time.Parse(time.RFC3339, f.LastSummary)
	b, err := s.st.BatchSince(f.LastSummary)
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	send, advance := summaryDue(f.Summary, now, last, b)
	if !advance {
		return
	}
	if send {
		s.notifyAll("batchFinished", "/jobs", summaryText(b), "batch.finished")
	}
	s.mu.Lock()
	f.LastSummary = now.UTC().Format(time.RFC3339)
	s.save(f)
	s.mu.Unlock()
}

// summaryDue says whether a batch summary is sent now (send) and whether the window moves on (advance). drained
// waits for an empty queue; daily and weekly (Mondays) wait for the first check at or after the time of day.
// A window with nothing finished in it is still closed, so the next summary counts from now.
func summaryDue(sum Summary, now, last time.Time, b jobs.Batch) (send, advance bool) {
	at, _ := time.Parse("15:04", sum.At)
	todayAt := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
	found := b.Converted+b.Failed > 0
	switch sum.Mode {
	case "drained":
		send = b.Active == 0 && found
		return send, send
	case "daily":
		advance = !now.Before(todayAt) && last.Before(todayAt)
	case "weekly":
		advance = now.Weekday() == time.Monday && !now.Before(todayAt) && last.Before(todayAt)
	}
	return advance && found, advance
}

// failedWords says why a job failed in fixed words from its code. It is the fallback when Options.FailureText is
// not set or says nothing; when it is set, the Jobs sentence is sent instead (it can name the output folder, as Jobs does).
func failedWords(code string) string {
	switch code {
	case jobs.ReasonDiskFull:
		return "The disk filled up."
	case jobs.ReasonCannotRead:
		return "Not allowed to read the file."
	case jobs.ReasonCannotWrite:
		return "Not allowed to write in its folder."
	case jobs.ReasonStalled:
		return "The conversion stopped making progress."
	case jobs.ReasonLowDiskSpace:
		return "Not enough free space next to the file."
	case jobs.ReasonWorkerLost:
		return "Its worker stopped answering."
	}
	return "The conversion stopped with an error."
}

// summaryText is the contract's batch line: "N converted, X GB smaller, Y waiting to be freed, Z failed."
func summaryText(b jobs.Batch) string {
	return fmt.Sprintf("%d converted, %.1f GB smaller, %d waiting to be freed, %d failed.",
		b.Converted, float64(max(b.Saved, 0))/1e9, b.Waiting, b.Failed) // a batch that grew reads 0.0, not -0.0
}

// notifyAll queues msg for every enabled target that wants this kind of news.
func (s *Service) notifyAll(flag, path, text, event string) {
	s.mu.Lock()
	f, err := s.load()
	s.mu.Unlock()
	if err != nil {
		s.opt.Logf("notify: %v", err)
		return
	}
	link := ""
	if f.LinkBase != "" {
		link = f.LinkBase + path
	}
	for _, r := range f.Targets {
		if r.Enabled && wants(r.Events, flag) {
			s.deliver(r.ID, message{Event: event, Text: text, Link: link})
		}
	}
}

func wants(e Events, flag string) bool {
	switch flag {
	case "jobFailed":
		return e.JobFailed
	case "needsYou":
		return e.NeedsYou
	case "fileFinished":
		return e.FileFinished
	case "batchFinished":
		return e.BatchFinished
	case "workerOffline":
		return e.WorkerOffline
	case "encoderFellBack":
		return e.EncoderFellBack
	}
	return false
}

type message struct{ Event, Text, Link string }

// deliver sends m to target id in the background, retrying with backoff. The target is read again for each
// attempt, so an edit or a delete is seen; a failed send never reaches a job.
func (s *Service) deliver(id string, m message) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.sem <- struct{}{}
		defer func() { <-s.sem }()
		for attempt := 0; attempt <= len(s.opt.Backoff); attempt++ {
			if attempt > 0 {
				time.Sleep(s.opt.Backoff[attempt-1])
			}
			s.mu.Lock()
			f, err := s.load()
			var rec *record
			for i := range f.Targets {
				if f.Targets[i].ID == id {
					rec = &f.Targets[i]
				}
			}
			s.mu.Unlock()
			if err != nil || rec == nil || !rec.Enabled {
				return
			}
			addr, err := jobs.DecryptSecret(rec.CT, rec.Nonce)
			var res Result
			if err != nil || addr == "" {
				res = Result{OK: false, Message: "The saved address could not be read; edit the target and save it again."}
			} else {
				res = s.post(rec.Kind, addr, m)
			}
			res.At = s.opt.Now().UTC().Format(time.RFC3339)
			s.record(id, res)
			if res.OK {
				return
			}
			s.opt.Logf("notify: %s", res.Message)
		}
	}()
}

// record stores a real send's result; failingSince follows the streak of failed sends.
func (s *Service) record(id string, res Result) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return
	}
	for i := range f.Targets {
		r := &f.Targets[i]
		if r.ID != id {
			continue
		}
		r.Last = &res
		if res.OK {
			r.Streak, r.StreakSince, r.FailingSince = 0, "", ""
		} else {
			if r.Streak == 0 {
				r.StreakSince = res.At
			}
			r.Streak++
			if r.Streak >= failingAfter {
				r.FailingSince = r.StreakSince
			}
		}
	}
	s.save(f)
}

// message to the target's kind: Discord takes {"content"}; a webhook takes {"event","text","link"}.
func (s *Service) post(kind, addr string, m message) Result {
	host := ""
	if u, err := url.Parse(addr); err == nil {
		host = u.Host // host and port; never the path, query or user, which can hold the secret
	}
	var body []byte
	if kind == "discord" {
		body, _ = json.Marshal(map[string]string{"content": m.Text + linkLine(m.Link)})
	} else {
		body, _ = json.Marshal(map[string]string{"event": m.Event, "text": m.Text, "link": m.Link})
	}
	req, err := http.NewRequest(http.MethodPost, addr, bytes.NewReader(body))
	if err != nil {
		return Result{OK: false, Message: fmt.Sprintf("Could not send to %s.", host)}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "shrink-your-shit")
	resp, err := s.opt.Client.Do(req)
	if err != nil {
		// the client's error text carries the whole address, so only the host and port are named
		return Result{OK: false, Message: fmt.Sprintf("Could not reach %s.", host)}
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	return answer(kind, resp.StatusCode)
}

func linkLine(link string) string {
	if link == "" {
		return ""
	}
	return "\n" + link
}

// answer is the one plain sentence the person sees for a send's HTTP status.
func answer(kind string, code int) Result {
	who := "The webhook"
	if kind == "discord" {
		who = "Discord"
	}
	ok := code >= 200 && code < 300
	switch {
	case ok:
		return Result{OK: true, Message: fmt.Sprintf("Sent. %s answered %d.", who, code)}
	case kind == "discord" && (code == 401 || code == 404):
		return Result{OK: false, Message: fmt.Sprintf("Discord answered %d: the webhook URL is wrong or was deleted.", code)}
	default:
		return Result{OK: false, Message: fmt.Sprintf("%s answered %d.", who, code)}
	}
}

// validURL: http or https, with a host.
func validURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Invalid{"invalid_url", "The address must start with http:// or https:// and name a host."}
	}
	return nil
}

// shown is the address with everything after the host hidden: "https://discord.com/…".
func shown(raw string) string {
	u, _ := url.Parse(raw)
	return u.Scheme + "://" + u.Host + "/…"
}

func validKind(k string) error {
	if k != "webhook" && k != "discord" {
		return Invalid{"invalid_kind", `The kind must be "webhook" or "discord".`}
	}
	return nil
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Service) load() (file, error) {
	f := file{Summary: Summary{Mode: "drained", At: "09:00"}}
	v, ok, err := s.st.Meta(metaKey)
	if err != nil {
		return f, err
	}
	if ok {
		if err := json.Unmarshal([]byte(v), &f); err != nil {
			return f, err
		}
	}
	return f, nil
}

func (s *Service) save(f file) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return s.st.SetMeta(metaKey, string(b))
}

func view(r record) Target {
	t := Target{ID: r.ID, Name: r.Name, Kind: r.Kind, URLShown: r.URLShown, Enabled: r.Enabled, Events: r.Events, LastResult: r.Last}
	if r.FailingSince != "" {
		fs := r.FailingSince
		t.FailingSince = &fs
	}
	return t
}

// Get is the setup as the API shows it.
func (s *Service) Get() (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Settings{}, err
	}
	out := Settings{Targets: []Target{}, Summary: f.Summary}
	for _, r := range f.Targets {
		out.Targets = append(out.Targets, view(r))
	}
	if f.LinkBase != "" {
		lb := f.LinkBase
		out.LinkBase = &lb
	}
	return out, nil
}

// SetSummary changes the summary mode and time, and the link base (nil leaves it; "" clears it).
func (s *Service) SetSummary(sum *Summary, linkBase *string) (Settings, error) {
	s.mu.Lock()
	f, err := s.load()
	if err != nil {
		s.mu.Unlock()
		return Settings{}, err
	}
	if sum != nil {
		switch sum.Mode {
		case "off", "drained", "daily", "weekly":
		default:
			s.mu.Unlock()
			return Settings{}, Invalid{"invalid", `The summary mode must be off, drained, daily or weekly.`}
		}
		if !hhmm.MatchString(sum.At) {
			s.mu.Unlock()
			return Settings{}, Invalid{"invalid", `The summary time must be HH:MM.`}
		}
		f.Summary = *sum
	}
	if linkBase != nil && *linkBase != "" {
		u, err := url.Parse(*linkBase)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			s.mu.Unlock()
			return Settings{}, Invalid{"invalid", "The link base must be an http or https address."}
		}
	}
	if linkBase != nil {
		f.LinkBase = strings.TrimRight(*linkBase, "/")
	}
	if err := s.save(f); err != nil {
		s.mu.Unlock()
		return Settings{}, err
	}
	s.mu.Unlock()
	return s.Get()
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// NewTarget is what POST /targets sends.
type NewTarget struct {
	Name    string
	Kind    string
	URL     string
	Enabled *bool
	Events  *EventsPatch
}

// Create adds a target.
func (s *Service) Create(in NewTarget) (Target, error) {
	if err := validKind(in.Kind); err != nil {
		return Target{}, err
	}
	if err := validURL(in.URL); err != nil {
		return Target{}, err
	}
	ct, nonce, err := jobs.EncryptSecret(in.URL)
	if err != nil {
		return Target{}, err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = shown(in.URL)
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	r := record{ID: newID(), Name: name, Kind: in.Kind, URLShown: shown(in.URL), CT: ct, Nonce: nonce,
		Enabled: enabled, Events: defaultEvents.with(in.Events)}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Target{}, err
	}
	f.Targets = append(f.Targets, r)
	if err := s.save(f); err != nil {
		return Target{}, err
	}
	return view(r), nil
}

// TargetPatch is what PATCH /targets/{id} sends: any subset; a nil field is left as it is.
type TargetPatch struct {
	Name    *string
	URL     *string
	Enabled *bool
	Events  *EventsPatch
}

// Update changes a target.
func (s *Service) Update(id string, p TargetPatch) (Target, error) {
	if p.URL != nil {
		if err := validURL(*p.URL); err != nil {
			return Target{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Target{}, err
	}
	for i := range f.Targets {
		r := &f.Targets[i]
		if r.ID != id {
			continue
		}
		if p.Name != nil && strings.TrimSpace(*p.Name) != "" {
			r.Name = strings.TrimSpace(*p.Name)
		}
		if p.Enabled != nil {
			r.Enabled = *p.Enabled
		}
		r.Events = r.Events.with(p.Events)
		if p.URL != nil {
			ct, nonce, err := jobs.EncryptSecret(*p.URL)
			if err != nil {
				return Target{}, err
			}
			r.CT, r.Nonce, r.URLShown = ct, nonce, shown(*p.URL)
		}
		if err := s.save(f); err != nil {
			return Target{}, err
		}
		return view(*r), nil
	}
	return Target{}, ErrNotFound
}

// Delete removes a target.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	for i := range f.Targets {
		if f.Targets[i].ID == id {
			f.Targets = append(f.Targets[:i], f.Targets[i+1:]...)
			return s.save(f)
		}
	}
	return ErrNotFound
}

// Test sends one test message now. With id: the saved target, and url (if given) replaces its address for
// this test only; a test of the saved address also sets its last result. Without id: kind and url, before
// anything is saved.
func (s *Service) Test(id, kind, rawURL string) (Result, error) {
	m := message{Event: "test", Text: "Test from shrink-your-shit: notifications are working."}
	if id == "" {
		if err := validKind(kind); err != nil {
			return Result{}, err
		}
		if err := validURL(rawURL); err != nil {
			return Result{}, err
		}
		return s.post(kind, rawURL, m), nil
	}
	s.mu.Lock()
	f, err := s.load()
	s.mu.Unlock()
	if err != nil {
		return Result{}, err
	}
	var rec *record
	for i := range f.Targets {
		if f.Targets[i].ID == id {
			rec = &f.Targets[i]
		}
	}
	if rec == nil {
		return Result{}, ErrNotFound
	}
	addr := rawURL
	if rawURL != "" {
		if err := validURL(rawURL); err != nil {
			return Result{}, err
		}
	} else {
		if addr, err = jobs.DecryptSecret(rec.CT, rec.Nonce); err != nil {
			return Result{}, err
		}
	}
	res := s.post(rec.Kind, addr, m)
	res.At = s.opt.Now().UTC().Format(time.RFC3339)
	if rawURL == "" {
		s.record(id, res)
	}
	return res, nil
}
