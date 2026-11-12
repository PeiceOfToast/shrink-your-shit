// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/rules"
)

// A library is a folder the program looks after, with exactly one flow saying what to do with its
// files. A new library is not started: nothing is scanned or queued until it is (StartLibrary).

// Library is one library record.
type Library struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	FlowID  int64  `json:"flowId"`
	Started bool   `json:"started"`
	Created string `json:"createdAt"`
	// LastScan is when the scanner last finished walking it (RFC3339; "" never), LastScanVideos how many
	// videos it found then (slice A's card).
	LastScan       string `json:"lastScan"`
	LastScanVideos int    `json:"lastScanVideos"`
	// OverrideFolder is its own folder for new files with "Next to each library" ("": the default
	// beside it; V5a).
	OverrideFolder string `json:"overrideFolder"`
	// Priority goes to its new and waiting jobs: higher is taken first (V5b; 0 by default).
	Priority int `json:"priority"`
}

// Flow is one flow record. Its behaviour is its template until the graph engine (slice 30).
type Flow struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	Template rules.Template `json:"template"`
	Created  string         `json:"createdAt"`
	Changed  string         `json:"changedAt"`
}

// ErrNotFound: no such record.
var ErrNotFound = errors.New("not found")

// ErrPathTaken: another library already has this folder.
var ErrPathTaken = errors.New("another library already uses this folder")

// ErrLibraryGone: a job was not added because its library was removed or is not started.
var ErrLibraryGone = errors.New("its library was removed or is not started")

// RealPath is a library folder as stored: absolute, cleaned and with links resolved, so the scanner
// (which does not follow a link at the top of a folder tree) walks the real folder, and two names
// for one folder (a link, macOS's /var for /private/var, a Windows short name like RUNNER~1) are
// seen as one. Every comparison of library folders uses it.
func RealPath(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// canonical is RealPath, or the cleaned absolute path when the folder cannot be resolved (it is
// gone, or not reachable just now): stored paths are compared in this form.
func canonical(p string) string {
	if r, err := RealPath(p); err == nil {
		return r
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
}

// FolderClash returns the library whose folder is p, or holds it, or is inside it (comparing real
// paths), except the library with id except; ok is false if none.
func (s *Store) FolderClash(p string, except int64) (l Library, same bool, ok bool, err error) {
	libs, err := s.Libraries()
	if err != nil {
		return Library{}, false, false, err
	}
	p = canonical(p)
	for _, l := range libs {
		if l.ID == except {
			continue
		}
		lp := canonical(l.Path)
		switch {
		case lp == p:
			return l, true, true, nil
		case Within(p, lp) || Within(lp, p):
			return l, false, true, nil
		}
	}
	return Library{}, false, false, nil
}

// Within reports whether p is inside dir (not equal to it).
func Within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

const libCols = `id,name,path,flow_id,started,created,last_scan,last_scan_videos,override_folder,priority`

func scanLibrary(r interface{ Scan(...any) error }) (Library, error) {
	var l Library
	err := r.Scan(&l.ID, &l.Name, &l.Path, &l.FlowID, &l.Started, &l.Created, &l.LastScan, &l.LastScanVideos, &l.OverrideFolder, &l.Priority)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return l, err
}

// Libraries lists every library, oldest first.
func (s *Store) Libraries() ([]Library, error) {
	rows, err := s.db.Query(`SELECT ` + libCols + ` FROM libraries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		l, err := scanLibrary(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// GetLibrary returns one library (ErrNotFound if there is none).
func (s *Store) GetLibrary(id int64) (Library, error) {
	return scanLibrary(s.db.QueryRow(`SELECT `+libCols+` FROM libraries WHERE id=?`, id))
}

// GetFlow returns one flow (ErrNotFound if there is none).
func (s *Store) GetFlow(id int64) (Flow, error) {
	var f Flow
	var t string
	err := s.db.QueryRow(`SELECT id,name,template,created,changed FROM flows WHERE id=?`, id).
		Scan(&f.ID, &f.Name, &t, &f.Created, &f.Changed)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal([]byte(t), &f.Template); err != nil {
		return f, err
	}
	f.Template.Languages = strings.Join(s.Languages(), ",") // the person's, not stored in the flow
	return f, nil
}

// Languages are the person's languages (the program settings' "languages", Settings > General), read
// here so every flow compiled for a file sees the current ones.
func (s *Store) Languages() []string {
	v, ok, err := s.Meta("settings")
	if err != nil || !ok {
		return nil
	}
	var st struct{ Languages []string }
	json.Unmarshal([]byte(v), &st)
	return st.Languages
}

type execer interface {
	Exec(string, ...any) (sql.Result, error)
	QueryRow(string, ...any) *sql.Row
}

// uniqueFlowName is base, or base plus " 2", " 3"... if a flow already has that name.
func uniqueFlowName(q execer, base string) (string, error) {
	name := base
	for n := 2; ; n++ {
		var c int
		if err := q.QueryRow(`SELECT COUNT(*) FROM flows WHERE name=?`, name).Scan(&c); err != nil {
			return "", err
		}
		if c == 0 {
			return name, nil
		}
		name = fmt.Sprintf("%s %d", base, n)
	}
}

func (s *Store) addFlow(q execer, name string, t rules.Template) (int64, error) {
	b, err := json.Marshal(t)
	if err != nil {
		return 0, err
	}
	now := s.stamp()
	r, err := q.Exec(`INSERT INTO flows(name,template,created,changed) VALUES(?,?,?,?)`, name, string(b), now, now)
	if err != nil {
		return 0, err
	}
	return r.LastInsertId()
}

func (s *Store) pathTaken(q execer, path string, except int64) error {
	var c int
	if err := q.QueryRow(`SELECT COUNT(*) FROM libraries WHERE path=? AND id<>?`, path, except).Scan(&c); err != nil {
		return err
	}
	if c > 0 {
		return ErrPathTaken
	}
	return nil
}

// AddLibrary adds a library with its own new flow made from t. The flow is named after the library
// (a clash adds " 2", " 3"...), once; it is never renamed automatically afterwards.
func (s *Store) AddLibrary(name, path string, t rules.Template, started bool) (Library, error) {
	return s.addLibrary(name, path, name, t, 0, started)
}

// AddLibraryOnFlow adds a library that uses the existing flow flowID (ErrNotFound if there is none).
func (s *Store) AddLibraryOnFlow(name, path string, flowID int64, started bool) (Library, error) {
	if err := s.published(s.db, flowID); err != nil {
		return Library{}, err
	}
	if _, err := s.GetFlow(flowID); err != nil {
		return Library{}, err
	}
	return s.addLibrary(name, path, "", rules.Template{}, flowID, started)
}

// addLibrary adds a library on flowID, or (0) on a new flow made from t and named after flowBase.
func (s *Store) addLibrary(name, path, flowBase string, t rules.Template, flowID int64, started bool) (Library, error) {
	path = canonical(path)
	if SystemFolder(path) {
		return Library{}, ErrSystemFolder
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	if err := s.pathTaken(tx, path, 0); err != nil {
		return Library{}, err
	}
	if flowID == 0 {
		flowName, err := uniqueFlowName(tx, flowBase)
		if err != nil {
			return Library{}, err
		}
		if flowID, err = s.addFlow(tx, flowName, t); err != nil {
			return Library{}, err
		}
	}
	r, err := tx.Exec(`INSERT INTO libraries(name,path,flow_id,started,created) VALUES(?,?,?,?,?)`,
		name, path, flowID, started, s.stamp())
	if err != nil {
		return Library{}, err
	}
	id, _ := r.LastInsertId()
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	// first run's step-1 library (name, folder) waits in the settings until a library exists: any way
	// one is made clears it (design, 02:41Z)
	s.db.Exec(`UPDATE app_meta SET value = json_set(value, '$.firstRunLibrary', json('null')) WHERE key = 'settings' AND json_valid(value)`)
	s.libraryChanged(id, false)
	return s.GetLibrary(id)
}

// Upgraded reports whether this database held work before libraries existed (an upgrade), as
// opposed to a fresh install. Decided once, by migration 3.
func (s *Store) Upgraded() (bool, error) {
	v, _, err := s.Meta("install")
	return v == "upgrade", err
}

// EnsureLibraries adds a library for each folder in paths that no library has yet. It never changes
// or removes a library. On an upgrade, each is started with its own flow made from legacy (the
// program's flags), so it converts as before; on the first start after the upgrade those flows are
// named "<folder> - Legacy". On a fresh install each is NOT started and gets a slider flow named after
// it: first-run shows it and nothing is converted before Start (DESIGN/FIRST-RUN.md). A folder inside,
// or around, another library's folder is not added: skipped says why, for a log line (their files
// would belong to two libraries). Jobs made before libraries were records are linked to the library
// whose folder holds them.
func (s *Store) EnsureLibraries(paths []string, legacy rules.Template) (added int, skipped []string, err error) {
	upgraded, err := s.Upgraded()
	if err != nil {
		return 0, nil, err
	}
	var marker int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM app_meta WHERE key='legacy_flows'`).Scan(&marker); err != nil {
		return 0, nil, err
	}
	for _, given := range paths {
		p, err := RealPath(given)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", given, err))
			continue
		}
		if SystemFolder(p) {
			skipped = append(skipped, fmt.Sprintf("%s: %v", given, ErrSystemFolder))
			continue
		}
		libs, err := s.Libraries()
		if err != nil {
			return added, skipped, err
		}
		have, clash := false, ""
		for _, l := range libs {
			lp := canonical(l.Path)
			switch {
			case lp == p || l.Path == given:
				have = true
			case Within(p, lp) || Within(lp, p):
				clash = l.Path
			}
		}
		if have {
			continue
		}
		if clash != "" {
			skipped = append(skipped, fmt.Sprintf("%s: it is inside, or contains, the folder of another library (%s)", given, clash))
			continue
		}
		name := filepath.Base(p)
		flowBase, t, started := name, rules.SliderTemplate(rules.DefaultSliderPosition), false
		if upgraded {
			t, started = legacy, true
			if marker > 0 {
				flowBase = name + " - Legacy"
			}
		}
		if _, err := s.addLibrary(name, p, flowBase, t, 0, started); err != nil {
			return added, skipped, err
		}
		added++
	}
	if marker > 0 {
		if _, err := s.db.Exec(`DELETE FROM app_meta WHERE key='legacy_flows'`); err != nil {
			return added, skipped, err
		}
	}
	return added, skipped, s.linkOldJobs()
}

// linkOldJobs gives jobs made before libraries were records the library whose folder holds the file.
func (s *Store) linkOldJobs() error {
	rows, err := s.db.Query(`SELECT id, path FROM jobs WHERE library_id IS NULL`)
	if err != nil {
		return err
	}
	type old struct {
		id   int64
		path string
	}
	var olds []old
	for rows.Next() {
		var o old
		rows.Scan(&o.id, &o.path)
		olds = append(olds, o)
	}
	rows.Close()
	if len(olds) == 0 {
		return nil
	}
	libs, err := s.Libraries()
	if err != nil {
		return err
	}
	for _, o := range olds {
		real := o.path
		if r, err := RealPath(filepath.Dir(o.path)); err == nil {
			real = filepath.Join(r, filepath.Base(o.path))
		}
		for _, l := range libs {
			if Within(o.path, l.Path) || Within(real, l.Path) {
				if _, err := s.db.Exec(`UPDATE jobs SET library_id=? WHERE id=?`, l.ID, o.id); err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}

// UpdateLibrary changes a library's name and/or folder (nil leaves it as it is).
func (s *Store) UpdateLibrary(id int64, name, path *string) (Library, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	if _, err := scanLibrary(tx.QueryRow(`SELECT `+libCols+` FROM libraries WHERE id=?`, id)); err != nil {
		return Library{}, err
	}
	if name != nil {
		if _, err := tx.Exec(`UPDATE libraries SET name=? WHERE id=?`, *name, id); err != nil {
			return Library{}, err
		}
	}
	if path != nil {
		p := canonical(*path)
		if SystemFolder(p) {
			return Library{}, ErrSystemFolder
		}
		path = &p
		if err := s.pathTaken(tx, *path, id); err != nil {
			return Library{}, err
		}
		if _, err := tx.Exec(`UPDATE libraries SET path=? WHERE id=?`, *path, id); err != nil {
			return Library{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	s.libraryChanged(id, false)
	return s.GetLibrary(id)
}

// StartLibrary marks a library started: from now on it is scanned.
func (s *Store) StartLibrary(id int64) error {
	s.removeMu.Lock()
	defer s.removeMu.Unlock()
	if s.removing[id] {
		return ErrLibraryBusy // being removed (BeginRemove)
	}
	var p string // a library made at a system folder before slice 23 is not started
	if err := s.db.QueryRow(`SELECT path FROM libraries WHERE id=?`, id).Scan(&p); err == nil && SystemFolder(p) {
		return ErrSystemFolder
	}
	r, err := s.db.Exec(`UPDATE libraries SET started=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.libraryChanged(id, false)
	return nil
}

// DeleteLibrary removes the library record and its queued jobs. Media files, its flow and its
// finished jobs stay. While one of its jobs runs it removes nothing and returns ErrLibraryBusy (checked
// in the same transaction, so no claim falls between): see BeginRemove.
func (s *Store) DeleteLibrary(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var busy int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM jobs WHERE library_id=? AND status=?`, id, Running).Scan(&busy); err != nil {
		return err
	}
	if busy > 0 {
		return ErrLibraryBusy
	}
	r, err := tx.Exec(`DELETE FROM libraries WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	gone, err := jobIDs(tx, `SELECT id FROM jobs WHERE library_id=? AND status=?`, id, Queued)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM jobs WHERE library_id=? AND status=?`, id, Queued); err != nil {
		return err
	}
	// their files' "queued" records go too, so a library added again on the folder decides them afresh
	// (ui-shop, #159 QA); every other decision (skipped, stopped, restored) stays
	if _, err := tx.Exec(`DELETE FROM files WHERE substr(decision, 1, length(?)) = ? AND NOT EXISTS (SELECT 1 FROM jobs WHERE jobs.path=files.path)`,
		DecisionQueued, DecisionQueued); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.jobsRemoved(gone...)
	s.libraryChanged(id, true)
	return nil
}

// libraryChanged writes library.changed for the live stream (/api/v2/events, with its seq id, so a
// page that reconnects gets it again): a library was added, paused or resumed, or its name, folder,
// priority, folder for new files or workflow changed; removed: it was deleted (ui-shop 06:24Z).
func (s *Store) libraryChanged(id int64, removed bool) {
	d := map[string]any{"type": "library.changed", "libraryId": strconv.FormatInt(id, 10)}
	if removed {
		d["removed"] = true
	}
	s.RecordEvent("library.changed", d)
}

// TemplateFor returns the template of the job's library's flow, which the worker compiles into the
// job's Plan. A job made before libraries were records is matched to its library by folder.
func (s *Store) TemplateFor(j Job) (rules.Template, error) {
	var flowID int64
	var err error
	if j.LibraryID > 0 {
		err = s.db.QueryRow(`SELECT flow_id FROM libraries WHERE id=?`, j.LibraryID).Scan(&flowID)
	} else {
		err = s.db.QueryRow(`SELECT flow_id FROM libraries WHERE path=?`, j.Library).Scan(&flowID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return rules.Template{}, fmt.Errorf("its library no longer exists: %w", ErrLibraryGone)
	}
	if err != nil {
		return rules.Template{}, err
	}
	f, err := s.GetFlow(flowID)
	if err != nil {
		return rules.Template{}, err
	}
	return f.Template, nil
}

// LibraryCounts are a library's job figures for its card.
type LibraryCounts struct {
	Working, Waiting int
	Failed           int // failed, and kept by a check: the Failed list's (slice A: "3 files failed")
	// FreedBytes: original minus new, summed over done jobs whose original is gone (replaced and not
	// kept); WaitingBytes: the same over done jobs whose original is still kept in .syp-trash
	// (UI-API-CONTRACT.md F1: freed + waiting = all saved).
	FreedBytes, WaitingBytes int64
	CouldFreeBytes           int64 // originals left where they were (output-first), still there (V5c)
	GrewBytes                int64 // new files bigger than their originals: the growth (never in the above)
}

// SetLibraryScanned records a finished scan of library id: when, and how many videos it found.
func (s *Store) SetLibraryScanned(id int64, videos int) error {
	_, err := s.db.Exec(`UPDATE libraries SET last_scan=?, last_scan_videos=? WHERE id=?`, s.stamp(), videos, id)
	return err
}

// Counts returns LibraryCounts by library id.
func (s *Store) Counts() (map[int64]LibraryCounts, error) {
	rows, err := s.db.Query(`SELECT library_id,
  SUM(status=?), SUM(status=?), SUM(status IN (?,?)),
  COALESCE(SUM(`+freedSQL+`),0), COALESCE(SUM(`+waitingSQL+`),0), COALESCE(SUM(`+couldFreeSQL+`),0), COALESCE(SUM(`+grewSQL+`),0)
FROM jobs WHERE library_id IS NOT NULL GROUP BY library_id`, Running, Queued, Failed, Kept)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]LibraryCounts{}
	for rows.Next() {
		var id int64
		var c LibraryCounts
		if err := rows.Scan(&id, &c.Working, &c.Waiting, &c.Failed, &c.FreedBytes, &c.WaitingBytes, &c.CouldFreeBytes, &c.GrewBytes); err != nil {
			return nil, err
		}
		out[id] = c
	}
	return out, rows.Err()
}

// Meta reads a value from app_meta ("" and false if it is not set).
func (s *Store) Meta(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_meta WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetMeta stores a value in app_meta.
func (s *Store) SetMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// ErrNameTaken: another flow already has this name.
var ErrNameTaken = errors.New("another flow already has this name")

// ErrFlowInUse: libraries use this flow, so it cannot be deleted.
type ErrFlowInUse struct{ UsedBy []string }

func (e ErrFlowInUse) Error() string { return "the flow is used by libraries" }

// FlowUse is a flow with the names of the libraries that use it.
type FlowUse struct {
	Flow
	UsedBy []string
}

// Flows lists every flow, oldest first, with who uses it.
func (s *Store) Flows() ([]FlowUse, error) {
	rows, err := s.db.Query(`SELECT id FROM flows WHERE status <> 'draft' ORDER BY id`) // a workflow never published isn't the old pages' to use
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	out := []FlowUse{}
	for _, id := range ids {
		f, err := s.GetFlow(id)
		if err != nil {
			return nil, err
		}
		used, err := s.usedBy(s.db, id)
		if err != nil {
			return nil, err
		}
		out = append(out, FlowUse{Flow: f, UsedBy: used})
	}
	return out, nil
}

func (s *Store) usedBy(q interface {
	Query(string, ...any) (*sql.Rows, error)
}, flowID int64) ([]string, error) {
	rows, err := q.Query(`SELECT name FROM libraries WHERE flow_id=? ORDER BY name`, flowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		names = append(names, n)
	}
	return names, rows.Err()
}

func (s *Store) nameFree(q execer, name string, except int64) error {
	var c int
	if err := q.QueryRow(`SELECT COUNT(*) FROM flows WHERE name=? AND id<>?`, name, except).Scan(&c); err != nil {
		return err
	}
	if c > 0 {
		return ErrNameTaken
	}
	return nil
}

// AddFlow adds a flow named name (ErrNameTaken if one already is).
func (s *Store) AddFlow(name string, t rules.Template) (Flow, error) {
	if err := s.nameFree(s.db, name, 0); err != nil {
		return Flow{}, err
	}
	id, err := s.addFlow(s.db, name, t)
	if err != nil {
		return Flow{}, nameErr(err)
	}
	return s.GetFlow(id)
}

// SetFlowTemplate saves t as flow id's template (the flow page's editor): every library that uses it
// changes, and their files are looked at again.
func (s *Store) SetFlowTemplate(id int64, t rules.Template) (Flow, error) {
	if _, err := s.GetFlow(id); err != nil {
		return Flow{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Flow{}, err
	}
	defer tx.Rollback()
	if err := s.setTemplate(tx, id, t); err != nil {
		return Flow{}, err
	}
	if err := tx.Commit(); err != nil {
		return Flow{}, err
	}
	if _, err := s.EnsureRevision(id); err != nil { // V2: its revision exists before any job queued under it
		return Flow{}, err
	}
	return s.GetFlow(id)
}

// FreeFlowName is base, or "base 2", "base 3"...: the first that no flow is called.
func (s *Store) FreeFlowName(base string) (string, error) { return uniqueFlowName(s.db, base) }

// RenameFlow renames a flow. Nothing renames a flow but this.
func (s *Store) RenameFlow(id int64, name string) (Flow, error) {
	if _, err := s.GetFlow(id); err != nil {
		return Flow{}, err
	}
	if err := s.nameFree(s.db, name, id); err != nil {
		return Flow{}, err
	}
	if _, err := s.db.Exec(`UPDATE flows SET name=?, changed=? WHERE id=?`, name, s.stamp(), id); err != nil {
		return Flow{}, nameErr(err)
	}
	return s.GetFlow(id)
}

// DuplicateFlow copies a flow as "<name> (copy)" (a clash adds " 2", " 3"...), used by no library.
func (s *Store) DuplicateFlow(id int64) (Flow, error) {
	f, err := s.GetFlow(id)
	if err != nil {
		return Flow{}, err
	}
	if err := s.published(s.db, id); err != nil {
		return Flow{}, err
	}
	name, err := uniqueFlowName(s.db, f.Name+" (copy)")
	if err != nil {
		return Flow{}, err
	}
	nid, err := s.addFlow(s.db, name, f.Template)
	if err != nil {
		return Flow{}, nameErr(err)
	}
	return s.GetFlow(nid)
}

// DeleteFlow deletes a flow no library uses (ErrFlowInUse otherwise).
func (s *Store) DeleteFlow(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	used, err := s.usedBy(tx, id)
	if err != nil {
		return err
	}
	if len(used) > 0 {
		return ErrFlowInUse{UsedBy: used}
	}
	r, err := tx.Exec(`DELETE FROM flows WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SetLibraryFlow gives a library another existing flow, in one transaction (so a flow deleted at the
// same moment cannot be left referenced). The library's files are forgotten by the scanner's cache,
// so files skipped under the old flow are looked at again with the new one.
func (s *Store) SetLibraryFlow(libraryID, flowID int64) (Library, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	var c int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM flows WHERE id=?`, flowID).Scan(&c); err != nil {
		return Library{}, err
	}
	if c == 0 {
		return Library{}, ErrNotFound
	}
	if err := s.published(tx, flowID); err != nil { // in the same transaction: never a library on a placeholder
		return Library{}, err
	}
	l, err := scanLibrary(tx.QueryRow(`SELECT `+libCols+` FROM libraries WHERE id=?`, libraryID))
	if err != nil {
		return Library{}, err
	}
	if _, err := tx.Exec(`UPDATE libraries SET flow_id=? WHERE id=?`, flowID, libraryID); err != nil {
		return Library{}, err
	}
	if err := forgetFiles(tx, l.Path); err != nil {
		return Library{}, err
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	s.libraryChanged(libraryID, false)
	return s.GetLibrary(libraryID)
}

// forgetFiles drops the scanner's records of the files under dir, so the next scan probes them again.
func forgetFiles(q execer, dir string) error {
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)
	_, err := q.Exec(`DELETE FROM files WHERE substr(path, 1, length(?)) = ?`, prefix, prefix)
	return err
}

// nameErr turns the unique-name index's refusal into ErrNameTaken.
func nameErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: flows.name") {
		return ErrNameTaken
	}
	return err
}

// ErrFlowShared: the library's flow is used by other libraries too, and the caller did not say whether
// to change it for all of them or to give this library its own copy.
type ErrFlowShared struct{ UsedBy []string }

func (e ErrFlowShared) Error() string { return "the flow is shared with other libraries" }

// SaveSlider sets a library's slider position, in one transaction:
//   - a legacy or custom flow is never overwritten: a new slider flow named after the library is made
//     and assigned, and the old one stays in Flows;
//   - a flow other libraries use too needs choice: "change_both" changes it for all of them,
//     "make_copy" gives this library "<library> (own copy)"; no choice is ErrFlowShared;
//   - otherwise the flow is changed.
//
// The files of every library whose setting changed are forgotten by the scanner's cache, so the next
// scan looks at them again with the new setting.
func (s *Store) SaveSlider(libraryID int64, quality int, choice string) (Library, error) {
	return s.SaveTemplate(libraryID, rules.SliderTemplate(quality), choice)
}

// SaveTemplate saves an editor's setting (the slider's or the Standard selectors') as library
// libraryID's flow, with the same rules as SaveSlider: a flow the editors cannot show (legacy, custom)
// is never overwritten, it gets a new one; a shared flow needs choice.
func (s *Store) SaveTemplate(libraryID int64, t rules.Template, choice string) (Library, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	l, err := scanLibrary(tx.QueryRow(`SELECT `+libCols+` FROM libraries WHERE id=?`, libraryID))
	if err != nil {
		return Library{}, err
	}
	f, err := s.flowIn(tx, l.FlowID)
	if err != nil {
		return Library{}, err
	}
	users, err := s.usedBy(tx, l.FlowID)
	if err != nil {
		return Library{}, err
	}
	switch {
	case f.Template.Kind != rules.KindSlider && f.Template.Kind != rules.KindSelectors:
		err = s.newFlowFor(tx, l, l.Name, t)
	case len(users) > 1 && choice == "change_both":
		err = s.setTemplate(tx, l.FlowID, t)
	case len(users) > 1 && choice == "make_copy":
		err = s.newFlowFor(tx, l, l.Name+" (own copy)", t)
	case len(users) > 1:
		return Library{}, ErrFlowShared{UsedBy: users}
	default:
		err = s.setTemplate(tx, l.FlowID, t)
	}
	if err != nil {
		return Library{}, err
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	s.libraryChanged(libraryID, false)
	return s.GetLibrary(libraryID)
}

// ReplaceCustom gives a library a new slider flow at Balanced, named after it; its old flow stays in
// Flows ("Use a slider setting instead").
func (s *Store) ReplaceCustom(libraryID int64) (Library, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Library{}, err
	}
	defer tx.Rollback()
	l, err := scanLibrary(tx.QueryRow(`SELECT `+libCols+` FROM libraries WHERE id=?`, libraryID))
	if err != nil {
		return Library{}, err
	}
	if err := s.newFlowFor(tx, l, l.Name, rules.SliderTemplate(rules.DefaultSliderPosition)); err != nil {
		return Library{}, err
	}
	if err := tx.Commit(); err != nil {
		return Library{}, err
	}
	s.libraryChanged(libraryID, false)
	return s.GetLibrary(libraryID)
}

func (s *Store) flowIn(q execer, id int64) (Flow, error) {
	var f Flow
	var t string
	err := q.QueryRow(`SELECT id,name,template,created,changed FROM flows WHERE id=?`, id).Scan(&f.ID, &f.Name, &t, &f.Created, &f.Changed)
	if errors.Is(err, sql.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	return f, json.Unmarshal([]byte(t), &f.Template)
}

// newFlowFor makes a flow (named base, made unique) and gives it to library l.
func (s *Store) newFlowFor(tx *sql.Tx, l Library, base string, t rules.Template) error {
	name, err := uniqueFlowName(tx, base)
	if err != nil {
		return err
	}
	id, err := s.addFlow(tx, name, t)
	if err != nil {
		return nameErr(err)
	}
	if _, err := tx.Exec(`UPDATE libraries SET flow_id=? WHERE id=?`, id, l.ID); err != nil {
		return err
	}
	return forgetFiles(tx, l.Path)
}

// setTemplate changes a flow's template and forgets the files of every library that uses it.
func (s *Store) setTemplate(tx *sql.Tx, flowID int64, t rules.Template) error {
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE flows SET template=?, changed=? WHERE id=?`, string(b), s.stamp(), flowID); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT path FROM libraries WHERE flow_id=?`, flowID)
	if err != nil {
		return err
	}
	var paths []string
	for rows.Next() {
		var p string
		rows.Scan(&p)
		paths = append(paths, p)
	}
	rows.Close()
	for _, p := range paths {
		if err := forgetFiles(tx, p); err != nil {
			return err
		}
	}
	return nil
}

// StopLibrary pauses library id (V5b): nothing new is scanned or queued, and its waiting jobs are not
// taken; a running one finishes. StartLibrary resumes it (also the old pages' Start).
func (s *Store) StopLibrary(id int64) error {
	r, err := s.db.Exec(`UPDATE libraries SET started=0 WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.libraryChanged(id, false)
	return nil
}

// SetLibraryPriority sets library id's priority, for its new jobs and its waiting ones (lead 01:04Z).
func (s *Store) SetLibraryPriority(id int64, p int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	r, err := tx.Exec(`UPDATE libraries SET priority=? WHERE id=?`, p, id)
	if err != nil {
		return err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	// not a job the person put first or last (design 03:22Z): its own place wins
	if _, err := tx.Exec(`UPDATE jobs SET priority=? WHERE library_id=? AND status=? AND priority BETWEEN ? AND ?`, p, id, Queued, -MaxLibraryPriority, MaxLibraryPriority); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.libraryChanged(id, false)
	return nil
}
