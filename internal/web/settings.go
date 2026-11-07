// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sync"

	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
)

// Settings are the global settings (UI-API-CONTRACT.md "Settings and first-run"). This slice stores
// and checks them; using parallelJobs, quietHours, lowPriority, scanSchedule and queueOrder belongs to
// later slices (24/35), keepOriginalsDays to slice 17.
type Settings struct {
	ControlLevel      string     `json:"controlLevel"` // basic|standard|advanced; changing it never touches flows
	PerLibraryFlow    bool       `json:"perLibraryFlow"`
	FirstRunDone      bool       `json:"firstRunDone"`
	FirstRunStep      int        `json:"firstRunStep"` // 1..5
	KeepOriginalsDays int        `json:"keepOriginalsDays"`
	ParallelJobs      int        `json:"parallelJobs"`
	QuietHours        QuietHours `json:"quietHours"`
	LowPriority       bool       `json:"lowPriority"`
	ScanSchedule      string     `json:"scanSchedule"` // "HH:MM", or "" for none
	QueueOrder        string     `json:"queueOrder"`   // newest|oldest|largest|smallest
}

// QuietHours: both "HH:MM", or both "".
type QuietHours struct {
	From string `json:"from"`
	To   string `json:"to"`
}

const settingsKey = "settings"

func defaultSettings() Settings {
	return Settings{ControlLevel: "basic", FirstRunStep: 1, KeepOriginalsDays: 7, ParallelJobs: 1,
		LowPriority: true, ScanSchedule: "02:00", QueueOrder: "newest"}
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// problem says what is wrong with s, as an error code and message ("" if nothing).
func (s Settings) problem() (code, msg string) {
	switch {
	case !oneOf(s.ControlLevel, "basic", "standard", "advanced"):
		return "invalid_setting", "controlLevel must be basic, standard or advanced."
	case s.FirstRunStep < 1 || s.FirstRunStep > 5:
		return "invalid_setting", "firstRunStep must be from 1 to 5."
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

// settingsStore keeps the settings in app_meta. The first time (a new database, or one from before
// settings existed), first-run counts as done if a library already exists: an upgrade, or -library.
type settingsStore struct {
	mu    sync.Mutex
	store *jobs.Store
}

func (ss *settingsStore) load() (Settings, error) {
	v, ok, err := ss.store.Meta(settingsKey)
	if err != nil {
		return Settings{}, err
	}
	s := defaultSettings()
	if !ok {
		libs, err := ss.store.Libraries()
		if err != nil {
			return Settings{}, err
		}
		s.FirstRunDone = len(libs) > 0
		return s, ss.save(s)
	}
	return s, json.Unmarshal([]byte(v), &s) // fields added later keep their defaults
}

func (ss *settingsStore) save(s Settings) error {
	b, _ := json.Marshal(s)
	return ss.store.SetMeta(settingsKey, string(b))
}

// readOnlySettings are shown by GET and ignored in a PATCH (the page may send back what it got).
var readOnlySettings = map[string]bool{"authRequired": true, "trashPath": true, "updateAvailable": true}

func settingsRoutes(api *http.ServeMux, ss *settingsStore, access *Access) {
	view := func(s Settings) map[string]any {
		b, _ := json.Marshal(s)
		var m map[string]any
		json.Unmarshal(b, &m)
		m["authRequired"] = access.Required()
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
		if err := ss.save(next); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, view(next))
	})
}
