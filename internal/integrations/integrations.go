// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package integrations tells Sonarr and Radarr about a file the app replaced, so they notice it at once
// instead of at their next scan. An instance is an address, a write-only API key and path mappings. The
// key is sealed with the share-password cipher and never returned, logged or stored in a job.
package integrations

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// RefreshStep is the job step a rescan leaves: its words are the plain summary, its ok is false for a warning.
const RefreshStep = "media_refresh"

// failingAfter is how many failed calls in a row make an instance "failing" (failingSince).
const failingAfter = 3

// answerTimeout is how long an instance may take to answer before it is reported as not answering.
const answerTimeout = 60 * time.Second

// noNextScan is the tail for a message about an instance that did not answer during a replace.
const noNextScan = "; it will notice the file at its next scan"

// Kind is which app an instance is.
type Kind string

// The kinds this version knows.
const (
	Sonarr   Kind = "sonarr"
	Radarr   Kind = "radarr"
	Plex     Kind = "plex"
	Jellyfin Kind = "jellyfin"
)

type kindInfo struct {
	label    string // the name people read
	noun     string // what the app calls the thing a file belongs to, for messages
	keyWord  string // what the secret is called in messages: "the API key is wrong"
	listPath string
	command  string // the rescan command's name
	idField  string // the command's field for the item's id
	auth     func(key string) (header, value string)
}

// headerKey sends the key in one header, as it is.
func headerKey(name string) func(string) (string, string) {
	return func(key string) (string, string) { return name, key }
}

var kinds = map[Kind]kindInfo{
	Sonarr: {label: "Sonarr", noun: "show", keyWord: "API key", listPath: "/api/v3/series", command: "RescanSeries",
		idField: "seriesId", auth: headerKey("X-Api-Key")},
	Radarr: {label: "Radarr", noun: "movie", keyWord: "API key", listPath: "/api/v3/movie", command: "RescanMovie",
		idField: "movieId", auth: headerKey("X-Api-Key")},
	Plex: {label: "Plex", noun: "library", keyWord: "token", auth: headerKey("X-Plex-Token")},
	Jellyfin: {label: "Jellyfin", noun: "library", keyWord: "API key",
		auth: func(key string) (string, string) { return "Authorization", `MediaBrowser Token="` + key + `"` }},
}

// Invalid is a request the contract refuses, with its code and what to say about it.
type Invalid struct {
	Code    string
	Message string
}

func (e Invalid) Error() string { return e.Code }

// ErrNotFound is an id that is not a saved integration.
var ErrNotFound = errors.New("integration not found")

// Mapping says where one of their folders is here: their path /tv is our /mnt/media/tv.
type Mapping struct {
	TheirPath string `json:"theirPath"`
	OurPath   string `json:"ourPath"`
}

// Result is the last answer an instance gave.
type Result struct {
	OK      bool      `json:"ok"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// Integration is one saved instance as the API shows it: the key is never here, only whether one is saved.
type Integration struct {
	ID           string     `json:"id"`
	Kind         Kind       `json:"kind"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	HasKey       bool       `json:"hasKey"`
	PathMappings []Mapping  `json:"pathMappings"`
	Enabled      bool       `json:"enabled"`
	LastResult   *Result    `json:"lastResult"`
	FailingSince *time.Time `json:"failingSince"`
}

// NewIntegration is what Create takes.
type NewIntegration struct {
	Kind         Kind
	Name         string
	URL          string
	APIKey       string
	PathMappings []Mapping
	Enabled      *bool
}

// Patch is what Update takes: a nil field is left as it is (a nil APIKey keeps the saved key).
type Patch struct {
	Name         *string
	URL          *string
	APIKey       *string
	PathMappings *[]Mapping
	Enabled      *bool
}

// TestIn is a Test: a saved instance (ID) whose fields the given ones override for this Test only, or a
// new one (Kind, URL, APIKey).
type TestIn struct {
	ID           string
	Kind         Kind
	URL          *string
	APIKey       *string
	PathMappings *[]Mapping
}

// Folder is one of their root folders after path mapping.
type Folder struct {
	TheirPath string  `json:"theirPath"`
	OurPath   *string `json:"ourPath"`
	InLibrary bool    `json:"inLibrary"`
}

// TestResult is what Test answers.
type TestResult struct {
	OK      bool     `json:"ok"`
	Message string   `json:"message"`
	Version *string  `json:"version"`
	Folders []Folder `json:"folders"`
}

// record is one saved instance as it is kept (in app_meta, as JSON). Sealed and Nonce are the key.
type record struct {
	ID           string     `json:"id"`
	Kind         Kind       `json:"kind"`
	Name         string     `json:"name"`
	URL          string     `json:"url"`
	Sealed       []byte     `json:"sealed"`
	Nonce        []byte     `json:"nonce"`
	Mappings     []Mapping  `json:"mappings"`
	Enabled      bool       `json:"enabled"`
	Last         *Result    `json:"last,omitempty"`
	Streak       int        `json:"streak"`
	StreakSince  *time.Time `json:"streakSince,omitempty"`
	FailingSince *time.Time `json:"failingSince,omitempty"`
}

type fileDoc struct {
	Items []record `json:"items"`
}

const metaKey = "integrations"

// Options are the seams tests use. Nil fields get the defaults: a 60 s client, the clock, no logging.
type Options struct {
	Client *http.Client
	Now    func() time.Time
	Logf   func(format string, args ...any)
}

// Service keeps the instances and tells them about replaced files.
type Service struct {
	st     *jobs.Store
	client *http.Client
	now    func() time.Time
	logf   func(string, ...any)
	mu     sync.Mutex // guards the saved document
	wg     sync.WaitGroup
}

// New makes the service over st.
func New(st *jobs.Store, o Options) *Service {
	s := &Service{st: st, client: o.Client, now: o.Now, logf: o.Logf}
	// The key goes only to the address saved. A redirect is not followed: it would carry the key to another host.
	c := http.Client{Timeout: answerTimeout}
	if o.Client != nil {
		c = *o.Client
	}
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	s.client = &c
	if s.now == nil {
		s.now = time.Now
	}
	if s.logf == nil {
		s.logf = func(string, ...any) {}
	}
	return s
}

func (r record) public() Integration {
	m := r.Mappings
	if m == nil {
		m = []Mapping{}
	}
	return Integration{ID: r.ID, Kind: r.Kind, Name: r.Name, URL: r.URL, HasKey: len(r.Sealed) > 0,
		PathMappings: m, Enabled: r.Enabled, LastResult: r.Last, FailingSince: r.FailingSince}
}

func (s *Service) load() (fileDoc, error) {
	raw, ok, err := s.st.Meta(metaKey)
	if err != nil {
		return fileDoc{}, err
	}
	var f fileDoc
	if ok && raw != "" {
		if err := json.Unmarshal([]byte(raw), &f); err != nil {
			return fileDoc{}, err
		}
	}
	return f, nil
}

func (s *Service) save(f fileDoc) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return s.st.SetMeta(metaKey, string(b))
}

func find(f fileDoc, id string) int {
	for i := range f.Items {
		if f.Items[i].ID == id {
			return i
		}
	}
	return -1
}

// List is every saved instance, in the order they were added.
func (s *Service) List() ([]Integration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return nil, err
	}
	out := []Integration{}
	for _, r := range f.Items {
		out = append(out, r.public())
	}
	return out, nil
}

// Create saves a new instance.
func (s *Service) Create(in NewIntegration) (Integration, error) {
	if _, ok := kinds[in.Kind]; !ok {
		return Integration{}, invalidKind()
	}
	if err := checkURL(in.URL); err != nil {
		return Integration{}, err
	}
	if in.APIKey == "" {
		return Integration{}, missingKey()
	}
	if err := checkMappings(in.PathMappings); err != nil {
		return Integration{}, err
	}
	sealed, nonce, err := jobs.SealSecret(in.APIKey)
	if err != nil {
		return Integration{}, err
	}
	id, err := newID()
	if err != nil {
		return Integration{}, err
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	rec := record{ID: id, Kind: in.Kind, Name: nameOr(in.Name, in.Kind), URL: strings.TrimSpace(in.URL),
		Sealed: sealed, Nonce: nonce, Mappings: cleanMappings(in.PathMappings), Enabled: enabled}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Integration{}, err
	}
	f.Items = append(f.Items, rec)
	if err := s.save(f); err != nil {
		return Integration{}, err
	}
	return rec.public(), nil
}

// Update changes the fields given; a nil field is kept.
func (s *Service) Update(id string, p Patch) (Integration, error) {
	if p.URL != nil {
		if err := checkURL(*p.URL); err != nil {
			return Integration{}, err
		}
	}
	if p.APIKey != nil && *p.APIKey == "" {
		return Integration{}, missingKey()
	}
	if p.PathMappings != nil {
		if err := checkMappings(*p.PathMappings); err != nil {
			return Integration{}, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return Integration{}, err
	}
	i := find(f, id)
	if i < 0 {
		return Integration{}, ErrNotFound
	}
	r := &f.Items[i]
	if p.Name != nil {
		r.Name = nameOr(*p.Name, r.Kind)
	}
	if p.URL != nil {
		r.URL = strings.TrimSpace(*p.URL)
	}
	if p.APIKey != nil {
		sealed, nonce, err := jobs.SealSecret(*p.APIKey)
		if err != nil {
			return Integration{}, err
		}
		r.Sealed, r.Nonce = sealed, nonce
	}
	if p.PathMappings != nil {
		r.Mappings = cleanMappings(*p.PathMappings)
	}
	if p.Enabled != nil {
		r.Enabled = *p.Enabled
	}
	if err := s.save(f); err != nil {
		return Integration{}, err
	}
	return r.public(), nil
}

// Delete removes a saved instance.
func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		return err
	}
	i := find(f, id)
	if i < 0 {
		return ErrNotFound
	}
	f.Items = append(f.Items[:i], f.Items[i+1:]...)
	return s.save(f)
}

// Test asks an instance (saved or new) for its version and root folders. A Test of a saved instance with no
// overrides sets its last result, like a real call does; an override is for this Test only.
func (s *Service) Test(in TestIn) (TestResult, error) {
	var kind Kind
	var base, key string
	var maps []Mapping
	var saved *record
	overridden := false
	if in.ID != "" {
		s.mu.Lock()
		f, err := s.load()
		s.mu.Unlock()
		if err != nil {
			return TestResult{}, err
		}
		i := find(f, in.ID)
		if i < 0 {
			return TestResult{}, ErrNotFound
		}
		r := f.Items[i]
		saved = &r
		kind, base, maps = r.Kind, r.URL, r.Mappings
		k, err := jobs.OpenSecret(r.Sealed, r.Nonce)
		if err != nil {
			return TestResult{}, err
		}
		key = k
		if in.URL != nil {
			base, overridden = *in.URL, true
		}
		if in.APIKey != nil {
			key, overridden = *in.APIKey, true
		}
		if in.PathMappings != nil {
			maps, overridden = *in.PathMappings, true
		}
	} else {
		kind = in.Kind
		if in.URL != nil {
			base = *in.URL
		}
		if in.APIKey != nil {
			key = *in.APIKey
		}
		if in.PathMappings != nil {
			maps = *in.PathMappings
		}
	}
	if _, ok := kinds[kind]; !ok {
		return TestResult{}, invalidKind()
	}
	if err := checkURL(base); err != nil {
		return TestResult{}, err
	}
	if key == "" {
		return TestResult{}, missingKey()
	}
	if err := checkMappings(maps); err != nil {
		return TestResult{}, err
	}
	res, answered := s.probe(kind, base, key, cleanMappings(maps))
	if saved != nil && !overridden {
		s.note(saved.ID, Result{OK: res.OK, Message: res.Message, At: s.now()}, answered)
	}
	return res, nil
}

// probe asks the instance for its version and then its root folders. answered says whether both calls worked.
func (s *Service) probe(kind Kind, base, key string, maps []Mapping) (TestResult, bool) {
	ki := kinds[kind]
	if kind == Plex {
		return s.probePlex(ki, base, key, maps)
	}
	if kind == Jellyfin {
		return s.probeJellyfin(ki, base, key, maps)
	}
	out := TestResult{Folders: []Folder{}}
	st, body, err := s.do(ki, "GET", base, key, "/api/v3/system/status", nil)
	if err != nil {
		out.Message = unreached(ki, base, err, "")
		return out, false
	}
	if !ok2(st) {
		out.Message = answer(ki, st)
		return out, false
	}
	var sys struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(body, &sys)
	if sys.Version != "" {
		v := sys.Version
		out.Version = &v
	}
	st, body, err = s.do(ki, "GET", base, key, "/api/v3/rootfolder", nil)
	if err != nil {
		out.Message = unreached(ki, base, err, "")
		return out, false
	}
	if !ok2(st) {
		out.Message = answer(ki, st)
		return out, false
	}
	var roots []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &roots); err != nil {
		out.Message = ki.label + " sent something the app doesn't understand."
		return out, true
	}
	paths := make([]string, 0, len(roots))
	for _, rf := range roots {
		paths = append(paths, rf.Path)
	}
	out.Folders = s.mapFolders(paths, maps)
	out.OK = true
	if out.Version != nil {
		out.Message = fmt.Sprintf("Connected to %s %s.", ki.label, *out.Version)
	} else {
		out.Message = "Connected to " + ki.label + "."
	}
	return out, true
}

// mapFolders turns their folder paths into Folder rows: mapped by maps, or kept when the path exists here.
func (s *Service) mapFolders(paths []string, maps []Mapping) []Folder {
	out := []Folder{}
	libs, _ := s.st.Libraries()
	for _, p := range paths {
		f := Folder{TheirPath: p}
		if ours, ok := toOurs(p, maps); ok {
			f.OurPath = &ours
		} else if _, err := os.Stat(p); err == nil {
			f.OurPath = &p
		}
		if f.OurPath != nil {
			for _, l := range libs {
				if _, ok := under(*f.OurPath, l.Path); ok {
					f.InLibrary = true
				}
			}
		}
		out = append(out, f)
	}
	return out
}

// Tell tells every enabled instance that the file at finalPath was replaced; from is the file it replaced
// (the job's source). from is empty when the original was kept (Beside and Folder modes), so the new file is
// only a new file. It runs in the background, so a slow or dead instance never holds the worker, and a
// warning never fails the job.
func (s *Service) Tell(jobID int64, from, finalPath string) {
	// Beside and Folder modes keep the original: it is still there, so it was not replaced and is not reported
	// as deleted. The new file is then a new file.
	if from != "" && from != finalPath {
		if _, err := os.Stat(from); err == nil {
			from = ""
		}
	}
	s.mu.Lock()
	f, err := s.load()
	s.mu.Unlock()
	if err != nil {
		s.logf("integrations: could not read the saved instances: %v", err)
		return
	}
	for _, r := range f.Items {
		if !r.Enabled {
			continue
		}
		rec := r
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.tellOne(jobID, rec, from, finalPath)
		}()
	}
}

// Wait returns once the background tells started so far have finished (tests use it).
func (s *Service) Wait() { s.wg.Wait() }

func (s *Service) tellOne(jobID int64, r record, from, finalPath string) {
	ki := kinds[r.Kind]
	key, err := jobs.OpenSecret(r.Sealed, r.Nonce)
	if err != nil {
		summary := ki.label + "'s key can't be read; enter it again in Settings."
		s.step(jobID, false, summary)
		return
	}
	var summary string
	var ok, answered, owns bool
	switch r.Kind {
	case Plex:
		summary, ok, answered = s.tellPlex(ki, r.Name, r.URL, key, toTheirs(finalPath, r.Mappings))
		owns = true
	case Jellyfin:
		summary, ok, answered = s.tellJellyfin(ki, r.Name, r.URL, key, toTheirs(from, r.Mappings), toTheirs(finalPath, r.Mappings))
		owns = true
	default:
		summary, ok, answered, owns = s.rescan(ki, r.URL, key, toTheirs(finalPath, r.Mappings))
	}
	if owns { // an instance without this file's show or movie (a Radarr told about an episode) leaves no step or log line
		s.step(jobID, ok, summary)
		s.logf("%s: %s", ki.label, summary)
	}
	s.note(r.ID, Result{OK: ok, Message: summary, At: s.now()}, answered)
}

func (s *Service) step(jobID int64, ok bool, summary string) {
	if err := s.st.AddStep(jobID, RefreshStep, ok, summary); err != nil {
		s.logf("integrations: could not record the rescan step: %v", err)
	}
}

// rescan finds the item (series or movie) the file belongs to and asks the instance to rescan it. owns is
// false only when the instance answered and has no item holding the file: the file is not its business.
func (s *Service) rescan(ki kindInfo, base, key, file string) (summary string, ok, answered, owns bool) {
	st, body, err := s.do(ki, "GET", base, key, ki.listPath, nil)
	if err != nil {
		return unreached(ki, base, err, noNextScan), false, false, true
	}
	if !ok2(st) {
		return answer(ki, st), false, false, true
	}
	var items []struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(body, &items); err != nil {
		return ki.label + " sent something the app doesn't understand.", false, false, true
	}
	best, bestLen := -1, -1
	for i, it := range items {
		if _, ok := under(file, it.Path); ok && len(norm(it.Path)) > bestLen {
			best, bestLen = i, len(norm(it.Path))
		}
	}
	if best < 0 {
		return fmt.Sprintf("%s doesn't have this file's %s.", ki.label, ki.noun), true, true, false
	}
	it := items[best]
	st, _, err = s.do(ki, "POST", base, key, "/api/v3/command", map[string]any{"name": ki.command, ki.idField: it.ID})
	if err != nil {
		return unreached(ki, base, err, noNextScan), false, false, true
	}
	if !ok2(st) {
		return answer(ki, st), false, false, true
	}
	title := it.Title
	if title == "" {
		title = "it"
	}
	return fmt.Sprintf("%s rescanned %s.", ki.label, title), true, true, true
}

// note records an answer: it sets the last result, counts failures in a row and sets failingSince at the
// third (to the first of them); a call that worked clears the count.
func (s *Service) note(id string, res Result, answered bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := s.load()
	if err != nil {
		s.logf("integrations: could not record a result: %v", err)
		return
	}
	i := find(f, id)
	if i < 0 {
		return
	}
	r := &f.Items[i]
	r.Last = &res
	if answered {
		r.Streak, r.StreakSince, r.FailingSince = 0, nil, nil
	} else {
		r.Streak++
		if r.StreakSince == nil {
			t := res.At
			r.StreakSince = &t
		}
		if r.Streak >= failingAfter && r.FailingSince == nil {
			r.FailingSince = r.StreakSince
		}
	}
	if err := s.save(f); err != nil {
		s.logf("integrations: could not record a result: %v", err)
	}
}

// do sends one request with the key in the header the kind uses. The error may name the address; the key never
// appears in it.
func (s *Service) do(ki kindInfo, method, base, key, p string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, strings.TrimSuffix(strings.TrimSpace(base), "/")+p, rd)
	if err != nil {
		return 0, nil, err
	}
	h, v := ki.auth(key)
	req.Header.Set(h, v)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, b, nil
}

func ok2(st int) bool { return st >= 200 && st < 300 }

// answer is what an instance that answered with a bad status is said to have done.
func answer(ki kindInfo, st int) string {
	if st == http.StatusUnauthorized {
		return fmt.Sprintf("%s answered 401: the %s is wrong.", ki.label, ki.keyWord)
	}
	if st >= 300 && st < 400 {
		return ki.label + " answered with a redirect to another address; check the address."
	}
	return fmt.Sprintf("%s answered %d.", ki.label, st)
}

// unreached says an instance did not answer, naming only its host (never the address or the key).
func unreached(ki kindInfo, base string, err error, tail string) string {
	if timedOut(err) {
		return fmt.Sprintf("%s didn't answer in %d s%s.", ki.label, int(answerTimeout/time.Second), tail)
	}
	u, _ := url.Parse(strings.TrimSpace(base))
	host := "the server"
	if u != nil && u.Host != "" {
		host = u.Host
	}
	return fmt.Sprintf("Could not reach %s%s.", host, tail)
}

func timedOut(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded)
}

var absPath = regexp.MustCompile(`^(/|[A-Za-z]:[\\/]|\\\\)`)

func checkURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Invalid{Code: "invalid_url", Message: "The address must start with http:// or https:// and name a server."}
	}
	return nil
}

func checkMappings(ms []Mapping) error {
	for _, m := range ms {
		if !absPath.MatchString(strings.TrimSpace(m.TheirPath)) || !absPath.MatchString(strings.TrimSpace(m.OurPath)) {
			return Invalid{Code: "invalid_mapping", Message: "Path mappings need absolute paths on both sides."}
		}
	}
	return nil
}

func cleanMappings(ms []Mapping) []Mapping {
	out := []Mapping{}
	for _, m := range ms {
		out = append(out, Mapping{TheirPath: strings.TrimSpace(m.TheirPath), OurPath: strings.TrimSpace(m.OurPath)})
	}
	return out
}

func invalidKind() error {
	return Invalid{Code: "invalid_kind", Message: "The kind is sonarr, radarr, plex or jellyfin."}
}

func missingKey() error {
	return Invalid{Code: "missing_key", Message: "The API key is needed."}
}

func nameOr(name string, k Kind) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	return kinds[k].label
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// norm makes a path comparable across the two systems: forward slashes, no trailing slash.
func norm(p string) string {
	return strings.TrimSuffix(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"), "/")
}

// under says whether p is root or inside it, and what follows root (starting with "/" or empty).
func under(p, root string) (string, bool) {
	r, pp := norm(root), norm(p)
	if pp == r {
		return "", true
	}
	if strings.HasPrefix(pp, r+"/") {
		return strings.TrimPrefix(pp, r), true
	}
	return "", false
}

// toTheirs maps a file of ours to the path the instance uses (the longest matching mapping); no mapping
// means the same path.
func toTheirs(file string, maps []Mapping) string {
	best, bestLen := -1, -1
	var rest string
	for i, m := range maps {
		if r, ok := under(file, m.OurPath); ok && len(norm(m.OurPath)) > bestLen {
			best, bestLen, rest = i, len(norm(m.OurPath)), r
		}
	}
	if best < 0 {
		return file
	}
	return norm(maps[best].TheirPath) + rest
}

// toOurs maps one of their paths to ours; ok is false when no mapping applies.
func toOurs(theirs string, maps []Mapping) (string, bool) {
	best, bestLen := -1, -1
	var rest string
	for i, m := range maps {
		if r, ok := under(theirs, m.TheirPath); ok && len(norm(m.TheirPath)) > bestLen {
			best, bestLen, rest = i, len(norm(m.TheirPath)), r
		}
	}
	if best < 0 {
		return "", false
	}
	return norm(maps[best].OurPath) + rest, true
}
