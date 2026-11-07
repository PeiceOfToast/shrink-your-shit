// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// Settings are the global settings (UI-API-CONTRACT.md "Settings and first-run"). This slice stores
// and checks them; using parallelJobs, quietHours, lowPriority, scanSchedule and queueOrder belongs to
// later slices (24/35), keepOriginalsDays to slice 17.
type Settings struct {
	// OldControlLevel and OldPerLibraryFlow are the retired global control level (each flow has its own
	// now). Kept as stored for one release, so a rollback finds them, but never shown or used, and a
	// PATCH cannot change them (review e9455d0).
	OldControlLevel   string     `json:"controlLevel,omitempty"`
	OldPerLibraryFlow *bool      `json:"perLibraryFlow,omitempty"`
	FirstRunDone      bool       `json:"firstRunDone"`
	FirstRunStep      int        `json:"firstRunStep"` // 1..5
	KeepOriginalsDays int        `json:"keepOriginalsDays"`
	ParallelJobs      int        `json:"parallelJobs"`
	QuietHours        QuietHours `json:"quietHours"`
	LowPriority       bool       `json:"lowPriority"`
	ScanSchedule      string     `json:"scanSchedule"` // "HH:MM", or "" for none
	QueueOrder        string     `json:"queueOrder"`   // newest|oldest|largest|smallest
	// Languages are the person's languages for "Keep only my languages" (slice 25b): ISO 639-2 codes,
	// stored in one form (rules.CanonLang); two-letter codes and the 639-2/T forms are accepted.
	Languages []string `json:"languages"`
}

// QuietHours: both "HH:MM", or both "".
type QuietHours struct {
	From string `json:"from"`
	To   string `json:"to"`
}

const settingsKey = "settings"

func defaultSettings() Settings {
	return Settings{FirstRunStep: 1, KeepOriginalsDays: 7, ParallelJobs: 1,
		LowPriority: true, ScanSchedule: "02:00", QueueOrder: "newest", Languages: []string{}}
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// problem says what is wrong with s, as an error code and message ("" if nothing).
func (s Settings) problem() (code, msg string) {
	switch {
	case s.FirstRunStep < 1 || s.FirstRunStep > 5:
		return "invalid_setting", "firstRunStep must be from 1 to 5."
	case badLanguage(s.Languages) != "":
		return "invalid_setting", fmt.Sprintf("%q is not a language code (use codes like eng, fre, ger, or en, fr, de).", badLanguage(s.Languages))
	case s.KeepOriginalsDays < 0 || s.KeepOriginalsDays > 365:
		return "invalid_setting", "Enter a whole number of days, from 0 to 365."
	case s.ParallelJobs < 1 || s.ParallelJobs > 8:
		return "invalid_setting", "Enter a whole number from 1 to 8."
	case (s.QuietHours.From == "") != (s.QuietHours.To == ""):
		return "invalid_setting", "Set both a start and an end for quiet hours, or leave both empty."
	case s.QuietHours.From != "" && (!hhmm.MatchString(s.QuietHours.From) || !hhmm.MatchString(s.QuietHours.To)):
		return "invalid_setting", "Quiet hours are times like 22:00."
	case s.ScanSchedule != "" && !hhmm.MatchString(s.ScanSchedule):
		return "invalid_setting", "The scan time is a time like 02:00."
	case !oneOf(s.QueueOrder, "newest", "oldest", "largest", "smallest"):
		return "invalid_setting", "queueOrder must be newest, oldest, largest or smallest."
	}
	return "", ""
}

func oneOf(v string, set ...string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}

// settingsStore keeps the settings in app_meta. When they are first made: a fresh install has
// first-run to do (even with libraries from -library or Docker's /media, which first-run shows); an
// upgrade has it done and starts at Standard (DESIGN/FIRST-RUN.md).
type settingsStore struct {
	mu    sync.Mutex
	store *jobs.Store
}

// LoadSettings reads the settings (and makes them, the first time), for the program outside the web
// page: the worker reads keepOriginalsDays from it.
func LoadSettings(store *jobs.Store) (Settings, error) {
	ss := &settingsStore{store: store}
	return ss.load()
}

func (ss *settingsStore) load() (Settings, error) {
	v, ok, err := ss.store.Meta(settingsKey)
	if err != nil {
		return Settings{}, err
	}
	s := defaultSettings()
	if !ok {
		upgraded, err := ss.store.Upgraded()
		if err != nil {
			return Settings{}, err
		}
		if upgraded {
			s.FirstRunDone = true
		}
		return s, ss.save(s)
	}
	return s, json.Unmarshal([]byte(v), &s) // fields added later keep their defaults
}

func (ss *settingsStore) save(s Settings) error {
	b, _ := json.Marshal(s)
	return ss.store.SetMeta(settingsKey, string(b))
}

// readOnlySettings are shown by GET and ignored in a PATCH (the page may send back what it got). The
// control level is each flow's own since the control-level slice: controlLevel and perLibraryFlow are
// no longer shown, and a PATCH that sends them is accepted and ignored for one release.
var readOnlySettings = map[string]bool{"authRequired": true, "trashPath": true, "updateAvailable": true,
	"controlLevel": true, "perLibraryFlow": true}

func settingsRoutes(api *http.ServeMux, ss *settingsStore, access *Access) {
	view := func(s Settings) map[string]any {
		b, _ := json.Marshal(s)
		var m map[string]any
		json.Unmarshal(b, &m)
		m["authRequired"] = access.Required()
		delete(m, "controlLevel") // retired: kept in storage for one release, never shown
		delete(m, "perLibraryFlow")
		m["trashPath"] = "Kept next to each file, in a folder called .syp-trash"
		m["updateAvailable"] = nil
		m["integrations"] = map[string]any{ // integrations come in a later slice; empty, so the page can show them
			"sonarr": map[string]any{"url": "", "hasKey": false}, "radarr": map[string]any{"url": "", "hasKey": false},
			"plex": map[string]any{"url": "", "hasKey": false}, "jellyfin": map[string]any{"url": "", "hasKey": false},
			"notifications": map[string]any{"webhookUrl": "", "onDone": false, "onFailed": true, "onNeedsAttention": true},
		}
		return m
	}
	api.HandleFunc("GET /api/settings", func(w http.ResponseWriter, r *http.Request) {
		ss.mu.Lock()
		defer ss.mu.Unlock()
		s, err := ss.load()
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, view(s))
	})
	// PATCH takes any subset of the fields; the result must be valid as a whole, or nothing is saved.
	api.HandleFunc("PATCH /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var patch map[string]json.RawMessage
		if !readJSON(w, r, &patch) {
			return
		}
		ss.mu.Lock()
		defer ss.mu.Unlock()
		s, err := ss.load()
		if err != nil {
			serverError(w, err)
			return
		}
		cur, _ := json.Marshal(s)
		var m map[string]json.RawMessage
		json.Unmarshal(cur, &m)
		for k, v := range patch {
			switch {
			case readOnlySettings[k]:
				continue
			case k == "integrations":
				apiError(w, http.StatusBadRequest, "not_available", "Integrations can't be set up yet; they come in a later version. Nothing was changed.")
				return
			case k == "quietHours":
				var q struct{ From, To *string } // a partial quietHours keeps the other end
				if err := json.Unmarshal(v, &q); err != nil {
					apiError(w, http.StatusBadRequest, "invalid_setting", "quietHours must be {\"from\":\"HH:MM\",\"to\":\"HH:MM\"}.")
					return
				}
				if q.From != nil {
					s.QuietHours.From = *q.From
				}
				if q.To != nil {
					s.QuietHours.To = *q.To
				}
				m[k], _ = json.Marshal(s.QuietHours)
			default:
				if _, known := m[k]; !known {
					apiError(w, http.StatusBadRequest, "unknown_setting", "There is no setting called "+k+". Nothing was changed.")
					return
				}
				m[k] = v
			}
		}
		b, _ := json.Marshal(m)
		next := defaultSettings()
		if err := json.Unmarshal(b, &next); err != nil {
			apiError(w, http.StatusBadRequest, "invalid_setting", "A setting has the wrong type of value. Nothing was changed.")
			return
		}
		if code, msg := next.problem(); code != "" {
			apiError(w, http.StatusBadRequest, code, msg+" Nothing was changed.")
			return
		}
		next.Languages = canonLanguages(next.Languages)
		if err := ss.save(next); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, view(next))
	})
}

// badLanguage is the first entry of langs that is not a language code ("" if they all are).
func badLanguage(langs []string) string {
	for _, l := range langs {
		if rules.CanonLang(l) == "" {
			return l
		}
	}
	return ""
}

// canonLanguages stores the languages in one form, each once, in the order given.
func canonLanguages(langs []string) []string {
	out, seen := []string{}, map[string]bool{}
	for _, l := range langs {
		if c := rules.CanonLang(l); c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}
