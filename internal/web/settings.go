// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package web

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/shrink-your-shit/shrink-your-shit/internal/flow"
	"github.com/shrink-your-shit/shrink-your-shit/internal/jobs"
	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
	"github.com/shrink-your-shit/shrink-your-shit/internal/update"
	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// Settings are the global settings (UI-API-CONTRACT.md "Settings and first-run"). This slice stores
// and checks them; using parallelJobs, quietHours, lowPriority, scanSchedule and queueOrder belongs to
// later slices (24/35), keepOriginalsDays to slice 17.
type Settings struct {
	// OldControlLevel and OldPerLibraryFlow are the retired global control level (each flow has its own
	// now). For one release the page before the Flows tab still reads and sets them (ui-shop 04:37Z),
	// so GET shows them and a PATCH stores them; nothing in the engine uses them (a flow's kind is its
	// own), and a rollback finds them.
	OldControlLevel   string `json:"controlLevel,omitempty"`
	OldPerLibraryFlow *bool  `json:"perLibraryFlow,omitempty"`
	FirstRunDone      bool   `json:"firstRunDone"`
	FirstRunStep      int    `json:"firstRunStep"` // 1..5
	// FirstRunLibrary is first run's step 1 (name and folder, kept until a library exists; the folder
	// needn't exist yet); null when there is none (design, 02:41Z).
	FirstRunLibrary   *FirstRunLibrary `json:"firstRunLibrary"`
	KeepOriginalsDays int              `json:"keepOriginalsDays"`
	// LogKeepDays is how long a finished job's log is kept (user review 11-13 item E): 7, 30, 90, or 0 for forever.
	LogKeepDays  int        `json:"logKeepDays"`
	ParallelJobs int        `json:"parallelJobs"`
	QuietHours   QuietHours `json:"quietHours"`
	LowPriority  bool       `json:"lowPriority"`
	ScanSchedule string     `json:"scanSchedule"` // "HH:MM", or "" for none
	QueueOrder   string     `json:"queueOrder"`   // newest|oldest|largest|smallest
	// Languages are the person's languages for "Keep only my languages" (slice 25b): ISO 639-2 codes,
	// stored in one form (rules.CanonLang); two-letter codes and the 639-2/T forms are accepted.
	Languages []string `json:"languages"`
	// ScanEvery is how often libraries are looked at, in seconds, once someone has changed it here (0:
	// not changed: the -scan-every flag is used; design 01:41Z, a saved value wins).
	ScanEvery int `json:"scanEvery"`
	// CheckForUpdates is whether the app asks GitHub once a day whether a newer release exists. On unless the
	// person turned it off (nil is on; an explicit false is off). With it off, nothing is sent anywhere.
	CheckForUpdates *bool `json:"checkForUpdates"`
	// BackupKeep is how many automatic backups (before an update or a migration) are kept, 1 to 50; manual
	// ones stay until deleted (UI-API-CONTRACT-v2.md "Backup").
	BackupKeep int `json:"backupKeep"`
	// WorkflowsMayRunCommands is the Safety switch for a workflow's "Run a command" card (FB2b): off by default,
	// because the program runs as this app's user. Off, such a workflow can't be published and a published one's
	// card reports that it can't run.
	WorkflowsMayRunCommands bool `json:"workflowsMayRunCommands"`
}

// UpdateChecks is whether the app may ask GitHub for a newer release: on unless the person turned it off.
func (s Settings) UpdateChecks() bool { return s.CheckForUpdates == nil || *s.CheckForUpdates }

// QuietHours: both "HH:MM", or both "".
type QuietHours struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// FirstRunLibrary is the library first run's step 1 is making.
type FirstRunLibrary struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

const settingsKey = "settings"

func defaultSettings() Settings {
	return Settings{OldControlLevel: "basic", FirstRunStep: 1, KeepOriginalsDays: 7, LogKeepDays: 30, ParallelJobs: 1,
		LowPriority: true, ScanSchedule: "02:00", QueueOrder: "newest", Languages: []string{}, BackupKeep: 10}
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// problem says what is wrong with s, as an error code and message ("" if nothing).
func (s Settings) problem() (code, msg string) {
	switch {
	case s.OldControlLevel != "" && !oneOf(s.OldControlLevel, "basic", "standard", "advanced"):
		return "invalid_setting", "controlLevel must be basic, standard or advanced."
	case s.FirstRunStep < 1 || s.FirstRunStep > 5:
		return "invalid_setting", "firstRunStep must be from 1 to 5."
	case badLanguage(s.Languages) != "":
		return "invalid_setting", fmt.Sprintf("%q is not a language code (use codes like eng, fre, ger, or en, fr, de).", badLanguage(s.Languages))
	case s.KeepOriginalsDays < 0 || s.KeepOriginalsDays > 365:
		return "invalid_setting", "Enter a whole number of days, from 0 to 365."
	case !oneOf(fmt.Sprint(s.LogKeepDays), "0", "7", "30", "90"):
		return "invalid_setting", "logKeepDays is 7, 30 or 90 days, or 0 to keep logs forever."
	case s.BackupKeep < 1 || s.BackupKeep > 50:
		return "invalid_setting", "Enter a whole number of backups to keep, from 1 to 50."
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
// CommandRunner is what a workflow's "Run a command" card may run with (FB2b): flow.RunProgram while
// "Workflows may run commands" is on, and nil once it is off, which makes the card refuse with "running
// commands is off". Read per job, so turning the switch off takes effect on the next card.
func CommandRunner(store *jobs.Store) func(program string, args []string, dir string, timeout time.Duration, onLine func(string)) (string, error) {
	if s, err := LoadSettings(store); err == nil && s.WorkflowsMayRunCommands {
		return flow.RunProgram
	}
	return nil
}

// Policy is what the saved settings let a workflow do (FB2b).
func (s Settings) Policy() workflow.Policy {
	return workflow.Policy{CommandsAllowed: s.WorkflowsMayRunCommands}
}

// policyFor reads the live policy; when the settings can't be read, nothing extra is allowed.
func policyFor(store *jobs.Store) workflow.Policy {
	s, err := LoadSettings(store)
	if err != nil {
		return workflow.Policy{}
	}
	return s.Policy()
}

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
		if upgraded { // an upgrade started at Standard before the control-level slice (D13)
			s.FirstRunDone, s.OldControlLevel = true, "standard"
		}
		return s, ss.save(s)
	}
	return s, json.Unmarshal([]byte(v), &s) // fields added later keep their defaults
}

func (ss *settingsStore) save(s Settings) error {
	b, _ := json.Marshal(s)
	return ss.store.SetMeta(settingsKey, string(b))
}

// readOnlySettings are shown by GET and ignored in a PATCH (the page may send back what it got).
var readOnlySettings = map[string]bool{"scanEverySource": true, "scanEveryMin": true, "scanEveryMax": true, "authRequired": true, "trashPath": true, "updateAvailable": true}

func settingsRoutes(api *http.ServeMux, ss *settingsStore, access *Access, eng Engine) {
	view := func(s Settings) map[string]any {
		b, _ := json.Marshal(s)
		var m map[string]any
		json.Unmarshal(b, &m)
		m["authRequired"] = access.Required()
		// retired, shown as stored for one release (ui-shop 04:37Z: the page before the Flows tab reads
		// them); "basic" and false when none is stored, as a fresh install had
		m["controlLevel"], m["perLibraryFlow"] = cmp.Or(s.OldControlLevel, "basic"), s.OldPerLibraryFlow != nil && *s.OldPerLibraryFlow
		m["trashPath"] = "Kept next to each file, in a folder called .syp-trash"
		m["checkForUpdates"] = s.UpdateChecks()
		m["updateAvailable"] = nil // the newer release's version, from the last check, when the checks are on
		if st, err := update.Load(ss.store, eng.Version); err == nil && s.UpdateChecks() && st.Available {
			m["updateAvailable"] = st.Latest
		}
		// how often libraries are looked at, in seconds: the value the scanner runs with now (ui-shop
		// 01:41Z). It is the -scan-every flag; once it can be changed here, a saved value wins (design).
		m["scanEvery"], m["scanEverySource"] = int(eng.ScanEvery.Seconds()), "flag"
		m["scanEveryMin"], m["scanEveryMax"] = scanEveryBounds(eng) // what PATCH accepts (ui-shop 04:11Z)
		if s.ScanEvery > 0 {
			m["scanEvery"], m["scanEverySource"] = s.ScanEvery, "saved"
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
			case k == "controlLevel" || k == "perLibraryFlow": // retired, stored for one release (lead 04:39Z); may be missing when none is stored
				m[k] = v
			case k == "scanEvery": // at least the settle time (a new file is decided on its second look, at least that apart), at most a day
				var n int
				if json.Unmarshal(v, &n) != nil {
					apiError(w, http.StatusBadRequest, "invalid_setting", "scanEvery is a whole number of seconds. Nothing was changed.")
					return
				}
				if s.ScanEvery == 0 && n == int(eng.ScanEvery.Seconds()) {
					continue // the flag's value sent back as it came: not a change, it stays the flag's
				}
				if low, high := scanEveryBounds(eng); n < low || n > high {
					apiError(w, http.StatusBadRequest, "invalid_setting", fmt.Sprintf("Look for new files every %d seconds to 24 hours (86400 seconds): a new file is converted once it has stayed the same for %d seconds. Nothing was changed.", low, low))
					return
				}
				m[k] = v
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
		if next.ScanEvery > 0 && next.ScanEvery != s.ScanEvery && eng.SetScanEvery != nil { // applied now, no restart
			eng.SetScanEvery(time.Duration(next.ScanEvery) * time.Second)
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

// scanEveryBounds are the seconds scanEvery may take: at least the settle time (a new file is decided on
// its second look, at least that apart), at most a day. PATCH checks them, and GET shows them, so the
// page offers only choices that are accepted.
func scanEveryBounds(eng Engine) (low, high int) {
	return max(int(eng.Settle.Seconds()), 1), 86400
}
