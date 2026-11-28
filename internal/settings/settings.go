// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

// Package settings is the app's one settings store: each stored field is a row in app_settings, keyed "group.field"
// and holding its value as JSON. Page 11's General, Processing and logs read and write it through GET and PATCH
// /api/v2/settings; the engine reads the fields it acts on from here (keepOriginalsDays, queueOrder, parallelJobs).
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/PeiceOfToast/shrink-your-shit/internal/store"
)

// Doc is the settings document. Version and UpdateAvailable are read-only: Version is the running build, and
// UpdateAvailable stays nil until the update check exists.
type Doc struct {
	General    General    `json:"general"`
	Appearance Appearance `json:"appearance"`
	Processing Processing `json:"processing"`
	Logs       Logs       `json:"logs"`
	BackupKeep int        `json:"backupKeep"`
}

type General struct {
	ServerName      string   `json:"serverName"`
	Language        string   `json:"language"`
	TimeFormat      string   `json:"timeFormat"`
	DateFormat      string   `json:"dateFormat"`
	CheckForUpdates bool     `json:"checkForUpdates"`
	Version         string   `json:"version"`
	UpdateAvailable any      `json:"updateAvailable"`
	Languages       []string `json:"languages"`
}

type Appearance struct {
	Theme  string  `json:"theme"`
	Accent *string `json:"accent"`
}

type Processing struct {
	ParallelJobs        int    `json:"parallelJobs"`
	GPUJobsSeparate     bool   `json:"gpuJobsSeparate"`
	ScanSchedule        string `json:"scanSchedule"`
	QueueOrder          string `json:"queueOrder"`
	LowPriority         bool   `json:"lowPriority"`
	KeepOriginalsDays   int    `json:"keepOriginalsDays"`
	LowSpaceGB          int    `json:"lowSpaceGB"`
	WorkflowsMayRunCmds bool   `json:"workflowsMayRunCommands"`
	TrashPath           string `json:"trashPath"`
}

type Logs struct {
	JobDays    int `json:"jobDays"`
	SystemDays int `json:"systemDays"`
	RawDays    int `json:"rawDays"`
}

// LogChoices are the values each log setting may take (page 11).
var LogChoices = struct {
	JobDays, SystemDays, RawDays []int
}{
	JobDays:    []int{0, 7, 30, 90},
	SystemDays: []int{0, 7, 14, 30, 90},
	RawDays:    []int{0, 7, 30, 90},
}

// Migration 12 is the settings table; migration 11 is the failure code (#789).
func init() {
	store.Register(store.Migration{Version: 12, Name: "app-settings", Up: createSettings})
}

func createSettings(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE app_settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`)
	return err
}

// Options are the values the app supplies: the running version and the trash folder shown read-only.
type Options struct {
	Version   string
	TrashPath string
}

// Defaults is the document a fresh install has.
func Defaults(o Options) Doc {
	return Doc{
		General: General{
			ServerName: "shrink-server", Language: "en", TimeFormat: "24h", DateFormat: "YYYY-MM-DD",
			CheckForUpdates: true, Version: o.Version, Languages: []string{},
		},
		Appearance: Appearance{Theme: "system"},
		Processing: Processing{
			ParallelJobs: 1, GPUJobsSeparate: true, ScanSchedule: "02:00", QueueOrder: "newest", LowPriority: true,
			KeepOriginalsDays: 7, LowSpaceGB: 10, TrashPath: o.TrashPath,
		},
		Logs:       Logs{JobDays: 30, SystemDays: 14, RawDays: 7},
		BackupKeep: 10,
	}
}

// Refusal is a settings change that was not saved. Code is the API code; Field names the setting.
type Refusal struct {
	Code    string
	Field   string
	Message string
}

func (r *Refusal) Error() string { return r.Code + ": " + r.Field + ": " + r.Message }

// Change is one stored field whose value changed: its key and the old and new JSON values.
type Change struct {
	Key string
	Old json.RawMessage
	New json.RawMessage
}

// field is one setting: its key, how a patch sets it, how it is read, and whether it is stored.
type field struct {
	key    string
	set    func(d *Doc, raw json.RawMessage) error
	get    func(d Doc) any
	stored bool
}

func typed[T any](key string, ptr func(d *Doc) *T, stored bool) field {
	return field{
		key: key,
		set: func(d *Doc, raw json.RawMessage) error {
			var v T
			if err := json.Unmarshal(raw, &v); err != nil {
				return err
			}
			*ptr(d) = v
			return nil
		},
		get:    func(d Doc) any { v := *ptr(&d); return v },
		stored: stored,
	}
}

var fields = []field{
	typed("general.serverName", func(d *Doc) *string { return &d.General.ServerName }, true),
	typed("general.language", func(d *Doc) *string { return &d.General.Language }, true),
	typed("general.timeFormat", func(d *Doc) *string { return &d.General.TimeFormat }, true),
	typed("general.dateFormat", func(d *Doc) *string { return &d.General.DateFormat }, true),
	typed("general.checkForUpdates", func(d *Doc) *bool { return &d.General.CheckForUpdates }, true),
	typed("general.languages", func(d *Doc) *[]string { return &d.General.Languages }, true),
	typed("general.version", func(d *Doc) *string { return &d.General.Version }, false),
	typed("general.updateAvailable", func(d *Doc) *any { return &d.General.UpdateAvailable }, false),
	typed("appearance.theme", func(d *Doc) *string { return &d.Appearance.Theme }, true),
	typed("appearance.accent", func(d *Doc) **string { return &d.Appearance.Accent }, true),
	typed("processing.parallelJobs", func(d *Doc) *int { return &d.Processing.ParallelJobs }, true),
	typed("processing.gpuJobsSeparate", func(d *Doc) *bool { return &d.Processing.GPUJobsSeparate }, true),
	typed("processing.scanSchedule", func(d *Doc) *string { return &d.Processing.ScanSchedule }, true),
	typed("processing.queueOrder", func(d *Doc) *string { return &d.Processing.QueueOrder }, true),
	typed("processing.lowPriority", func(d *Doc) *bool { return &d.Processing.LowPriority }, true),
	typed("processing.keepOriginalsDays", func(d *Doc) *int { return &d.Processing.KeepOriginalsDays }, true),
	typed("processing.lowSpaceGB", func(d *Doc) *int { return &d.Processing.LowSpaceGB }, true),
	typed("processing.workflowsMayRunCommands", func(d *Doc) *bool { return &d.Processing.WorkflowsMayRunCmds }, true),
	typed("processing.trashPath", func(d *Doc) *string { return &d.Processing.TrashPath }, false),
	typed("logs.jobDays", func(d *Doc) *int { return &d.Logs.JobDays }, true),
	typed("logs.systemDays", func(d *Doc) *int { return &d.Logs.SystemDays }, true),
	typed("logs.rawDays", func(d *Doc) *int { return &d.Logs.RawDays }, true),
	typed("backupKeep", func(d *Doc) *int { return &d.BackupKeep }, true),
}

// readOnly are the fields a patch may not change (a patch names them and is refused with read_only).
var readOnly = map[string]bool{"general.version": true, "general.updateAvailable": true, "processing.trashPath": true}

// fieldByKey finds a setting by its key.
func fieldByKey(key string) (field, bool) {
	for _, f := range fields {
		if f.key == key {
			return f, true
		}
	}
	return field{}, false
}

// Load reads the document: the stored values over the defaults. A stored value that no longer parses keeps its default.
func Load(ctx context.Context, db *store.DB, o Options) (Doc, error) {
	d := Defaults(o)
	rows, err := db.Read().QueryContext(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return d, fmt.Errorf("read the settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return d, err
		}
		if f, ok := fieldByKey(key); ok && f.stored {
			_ = f.set(&d, json.RawMessage(value))
		}
	}
	return d, rows.Err()
}

// Get reads one stored setting by key, as its typed value. The default is used when no row holds it.
func Get[T any](ctx context.Context, db *store.DB, key string, def T) T {
	var value string
	err := db.Read().QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return def
	}
	var v T
	if json.Unmarshal([]byte(value), &v) != nil {
		return def
	}
	return v
}

// Patch applies a settings change all or nothing. The body is the PATCH shape: groups of changed fields, and
// backupKeep at the top. Every field is checked before anything is saved; the first problem is the refusal. It
// returns the saved document and the changes, one per stored field that changed.
func Patch(ctx context.Context, db *store.DB, o Options, body map[string]json.RawMessage) (Doc, []Change, error) {
	cur, err := Load(ctx, db, o)
	if err != nil {
		return cur, nil, err
	}
	next := cur
	for group, raw := range body {
		if group == "backupKeep" {
			if err := applyField(&next, "backupKeep", raw); err != nil {
				return cur, nil, err
			}
			continue
		}
		if group != "general" && group != "appearance" && group != "processing" && group != "logs" {
			return cur, nil, &Refusal{Code: "unknown_setting", Field: group, Message: "There is no setting called " + group + "."}
		}
		var fieldsIn map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fieldsIn); err != nil {
			return cur, nil, &Refusal{Code: "invalid_setting", Field: group, Message: "Send the " + group + " settings as an object."}
		}
		for name, value := range fieldsIn {
			key := group + "." + name
			if readOnly[key] {
				return cur, nil, &Refusal{Code: "read_only", Field: key, Message: key + " can't be changed here."}
			}
			if err := applyField(&next, key, value); err != nil {
				return cur, nil, err
			}
		}
	}
	if err := validate(next); err != nil {
		return cur, nil, err
	}
	changes, err := diff(cur, next)
	if err != nil {
		return cur, nil, err
	}
	if err := save(ctx, db, changes); err != nil {
		return cur, nil, err
	}
	return next, changes, nil
}

// applyField sets one field from a patch value. An unknown key is unknown_setting; a value of the wrong kind is
// invalid_setting for that field.
func applyField(d *Doc, key string, raw json.RawMessage) error {
	f, ok := fieldByKey(key)
	if !ok {
		return &Refusal{Code: "unknown_setting", Field: key, Message: "There is no setting called " + key + "."}
	}
	// null is a value only where the field is a pointer (the accent colour): a list, a number, a flag or a text would
	// be saved as null and crash the page that reads it (review M1).
	if strings.TrimSpace(string(raw)) == "null" {
		switch reflect.ValueOf(f.get(*d)).Kind() {
		case reflect.Ptr, reflect.Interface, reflect.Invalid:
		default:
			return refuse(key, "Use a value for "+key+", not nothing.")
		}
	}
	if err := f.set(d, raw); err != nil {
		return &Refusal{Code: "invalid_setting", Field: key, Message: "Use a value of the right kind for " + key + "."}
	}
	return nil
}

var (
	hhmm  = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	iso3  = regexp.MustCompile(`^[a-z]{3}$`)
	hexCC = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

func inRange(v, min, max int) bool { return v >= min && v <= max }

func inInts(v int, choices []int) bool {
	for _, c := range choices {
		if c == v {
			return true
		}
	}
	return false
}

func refuse(field, message string) error {
	return &Refusal{Code: "invalid_setting", Field: field, Message: message}
}

// validate checks the whole document with page 11's ranges and choices. It reports the first problem.
func validate(d Doc) error {
	g := d.General
	if g.ServerName == "" {
		return refuse("general.serverName", "Give the server a name.")
	}
	for _, l := range g.Languages {
		if !iso3.MatchString(l) {
			return refuse("general.languages", "Use three-letter codes like eng, ger, fra.")
		}
	}
	if g.Language != "en" {
		return refuse("general.language", "Pick a language the app offers.")
	}
	if g.TimeFormat != "24h" && g.TimeFormat != "12h" {
		return refuse("general.timeFormat", "Pick 24-hour or 12-hour time.")
	}
	switch g.DateFormat {
	case "YYYY-MM-DD", "DD.MM.YYYY", "MM/DD/YYYY":
	default:
		return refuse("general.dateFormat", "Pick a date format from the list.")
	}
	a := d.Appearance
	switch a.Theme {
	case "system", "light", "dark":
	default:
		return refuse("appearance.theme", "Pick Match system, Dark or Light.")
	}
	if a.Accent != nil && !hexCC.MatchString(*a.Accent) {
		return refuse("appearance.accent", "Use a colour like #3b82f6.")
	}
	p := d.Processing
	if !inRange(p.ParallelJobs, 1, 8) {
		return refuse("processing.parallelJobs", "Pick from 1 to 8 files at once.")
	}
	if p.ScanSchedule != "" && !hhmm.MatchString(p.ScanSchedule) {
		return refuse("processing.scanSchedule", "Use a time like 02:00, or leave it empty for no daily scan.")
	}
	switch p.QueueOrder {
	case "newest", "oldest", "largest", "smallest":
	default:
		return refuse("processing.queueOrder", "Pick newest, oldest, largest or smallest.")
	}
	if !inRange(p.KeepOriginalsDays, 0, 365) {
		return refuse("processing.keepOriginalsDays", "Keep originals from 0 to 365 days.")
	}
	if !inRange(p.LowSpaceGB, 1, 1000) {
		return refuse("processing.lowSpaceGB", "Pick a limit from 1 to 1000 GB.")
	}
	l := d.Logs
	if !inInts(l.JobDays, LogChoices.JobDays) {
		return refuse("logs.jobDays", "Pick 7, 30, 90 days or forever.")
	}
	if !inInts(l.SystemDays, LogChoices.SystemDays) {
		return refuse("logs.systemDays", "Pick a number of days from the list.")
	}
	if !inInts(l.RawDays, LogChoices.RawDays) {
		return refuse("logs.rawDays", "Pick 7, 30, 90 days or forever.")
	}
	// 0 means forever, so it is longer than any number of days.
	if l.JobDays != 0 && (l.RawDays == 0 || l.RawDays > l.JobDays) {
		return refuse("logs.rawDays", "Raw tool output can't be kept longer than the job log it belongs to.")
	}
	if !inRange(d.BackupKeep, 1, 50) {
		return refuse("backupKeep", "Keep from 1 to 50 backups.")
	}
	return nil
}

// stored lists the stored fields of a document as key → JSON value.
func stored(d Doc) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for _, f := range fields {
		if !f.stored {
			continue
		}
		b, err := json.Marshal(f.get(d))
		if err != nil {
			return nil, err
		}
		out[f.key] = b
	}
	return out, nil
}

// diff is one change per stored field whose JSON value differs.
func diff(before, after Doc) ([]Change, error) {
	a, err := stored(before)
	if err != nil {
		return nil, err
	}
	b, err := stored(after)
	if err != nil {
		return nil, err
	}
	var out []Change
	for _, f := range fields {
		if !f.stored {
			continue
		}
		if string(a[f.key]) != string(b[f.key]) {
			out = append(out, Change{Key: f.key, Old: a[f.key], New: b[f.key]})
		}
	}
	return out, nil
}

// save writes the changed fields in one transaction, so a failed write saves none of them.
func save(ctx context.Context, db *store.DB, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	return db.Tx(ctx, func(tx *sql.Tx) error {
		for _, c := range changes {
			if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value) VALUES (?, ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value`, c.Key, string(c.New)); err != nil {
				return fmt.Errorf("save %s: %w", c.Key, err)
			}
		}
		return nil
	})
}

// AsRefusal returns the refusal in err, if err is one.
func AsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// Languages is general.languages: the person's own languages. "My languages" in a flow is a token that a run
// resolves to these when the run starts, so a change applies to the next run. Empty until the person sets some.
func Languages(ctx context.Context, db *store.DB) []string {
	return Get(ctx, db, "general.languages", []string{})
}
