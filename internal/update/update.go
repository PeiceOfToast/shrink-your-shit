// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package update tells the person when a newer release is on GitHub. It only reads: it never downloads
// or replaces anything (the update itself is a later part).
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ReleaseURL is GitHub's latest release of this program. Tests replace it.
var ReleaseURL = "https://api.github.com/repos/PeiceOfToast/shrink-your-shit/releases/latest"

// cacheKey is the app_meta key the last check is kept under.
const cacheKey = "updateCheck"

// mu serialises checks, so two requests at once make one real request.
var mu sync.Mutex

// Meta is the part of the store the cached check uses (jobs.Store has it).
type Meta interface {
	Meta(key string) (string, bool, error)
	SetMeta(key, value string) error
}

// record is the cached check: the last good release, when the last real request was made, and its error.
type record struct {
	CheckedAt   time.Time `json:"checkedAt"` // zero: never checked
	Latest      string    `json:"latest"`    // "" when no release is known
	Notes       string    `json:"notes"`
	URL         string    `json:"url"`
	PublishedAt string    `json:"publishedAt"`
	Error       string    `json:"error"`
}

// Status is the update notice as the API shows it. Available is false unless Latest is newer than Current.
type Status struct {
	Current, Latest, Notes, URL, PublishedAt, Error string
	CheckedAt                                       time.Time
	Available                                       bool
}

// Load is the cached notice (no request is made).
func Load(store Meta, current string) (Status, error) {
	rec, err := loadRecord(store)
	if err != nil {
		return Status{}, err
	}
	return statusOf(rec, current), nil
}

func loadRecord(store Meta) (record, error) {
	var rec record
	s, ok, err := store.Meta(cacheKey)
	if err != nil || !ok {
		return rec, err
	}
	if err := json.Unmarshal([]byte(s), &rec); err != nil {
		return record{}, nil // a damaged record is the same as none: the next check replaces it
	}
	return rec, nil
}

func statusOf(rec record, current string) Status {
	return Status{Current: current, Latest: rec.Latest, Notes: rec.Notes, URL: rec.URL, PublishedAt: rec.PublishedAt,
		Error: rec.Error, CheckedAt: rec.CheckedAt, Available: newer(rec.Latest, current)}
}

// Checker asks GitHub for the latest release and keeps the answer.
type Checker struct {
	Store   Meta
	Current string       // the running version: "dev" never has an update
	URL     string       // ReleaseURL when empty
	Client  *http.Client // a 10 s timeout when nil
	Now     func() time.Time
	Logf    func(format string, args ...any) // log.Printf when nil
}

// Check returns the notice, asking GitHub only when the last real request was at least minAge ago. A
// failure keeps the last good release and records the error; one line is logged when the error changes.
func (c *Checker) Check(minAge time.Duration) (Status, error) {
	mu.Lock()
	defer mu.Unlock()
	rec, err := loadRecord(c.Store)
	if err != nil {
		return Status{}, err
	}
	now := c.now()
	if !rec.CheckedAt.IsZero() && now.Sub(rec.CheckedAt) < minAge {
		return statusOf(rec, c.Current), nil
	}
	before := rec.Error
	rel, err := c.fetch()
	rec.CheckedAt = now
	if err != nil {
		rec.Error = err.Error()
		if rec.Error != before {
			c.logf("update check: %s", rec.Error)
		}
	} else {
		rec.Error = ""
		rec.Latest, rec.Notes, rec.URL, rec.PublishedAt = rel.Latest, rel.Notes, rel.URL, rel.PublishedAt
	}
	b, _ := json.Marshal(rec)
	if err := c.Store.SetMeta(cacheKey, string(b)); err != nil {
		return Status{}, err
	}
	return statusOf(rec, c.Current), nil
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Checker) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// release is the part of GitHub's answer the notice keeps.
type release struct {
	Latest, Notes, URL, PublishedAt string
}

func (c *Checker) fetch() (release, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	url := c.URL
	if url == "" {
		url = ReleaseURL
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return release{}, err
	}
	// only this program's name and version identify the request
	req.Header.Set("User-Agent", "shrink-your-shit/"+c.Current)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return release{}, fmt.Errorf("could not reach GitHub: %v", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return release{}, fmt.Errorf("GitHub is limiting requests (HTTP %d); the check will try again later", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return release{}, fmt.Errorf("GitHub answered HTTP %d", resp.StatusCode)
	}
	var in struct {
		TagName     string `json:"tag_name"`
		Body        string `json:"body"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&in); err != nil {
		return release{}, fmt.Errorf("GitHub's answer could not be read: %v", err)
	}
	// drafts and pre-releases are not updates
	if in.Draft || in.Prerelease || strings.Contains(in.TagName, "-") {
		return release{}, nil
	}
	return release{Latest: strings.TrimPrefix(in.TagName, "v"), Notes: in.Body, URL: in.HTMLURL, PublishedAt: in.PublishedAt}, nil
}

// firstWait and everyWait pace Run; tests shorten them.
var firstWait, everyWait = time.Minute, time.Hour

// Run checks once a minute after start and then every hour, while on() says checks are on. A check is
// made only when the last one is a day old, so GitHub is asked at most once a day. It returns when ctx ends.
func Run(ctx context.Context, c *Checker, on func() bool) {
	wait := firstWait
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = everyWait
		if on() {
			c.Check(24 * time.Hour)
		}
	}
}

// newer reports whether latest is a later release than current. Either is a plain x.y.z (a "v" is allowed
// on latest); "dev" or anything else is never older, so it never has an update.
func newer(latest, current string) bool {
	if latest == "" {
		return false
	}
	l, ok := parse(latest)
	if !ok {
		return false
	}
	c, ok := parse(current)
	if !ok {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// parse reads x.y.z (with an optional leading "v").
func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Env is what install type detection looks at; tests replace it.
type Env struct {
	Getenv func(string) string
	Exists func(path string) bool
	Exe    func() string // the running program's path ("" if unknown)
}

// DetectEnv reads the real environment.
func DetectEnv() Env {
	return Env{
		Getenv: os.Getenv,
		Exists: func(p string) bool { _, err := os.Stat(p); return err == nil },
		Exe: func() string {
			p, err := os.Executable()
			if err != nil {
				return ""
			}
			return p
		},
	}
}

// InstallType is how this program was installed, which says what to do about an update: "docker" (a
// container), "script" (install.sh put it in its place), "windows", or "source" (built by hand).
func InstallType(current, goos string, env Env) string {
	if env.Exists("/.dockerenv") || env.Getenv("SYP_INSTALL") == "docker" {
		return "docker"
	}
	if env.Getenv("SYP_INSTALL") == "script" {
		return "script"
	}
	// installs from before the marker (v0.1.1) are told apart by where the program is
	exe := env.Exe()
	if exe == "/usr/local/bin/shrink-your-shit" {
		return "script"
	}
	// install.sh paths are always /-separated, so path.Join, not filepath.Join (which uses \ on Windows)
	if home := env.Getenv("HOME"); home != "" && exe == path.Join(home, ".local", "bin", "shrink-your-shit") {
		return "script"
	}
	if goos == "windows" && current != "dev" {
		return "windows"
	}
	return "source"
}
